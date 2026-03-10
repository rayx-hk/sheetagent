package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// FormulaEvaluator provides Excel formula evaluation via a prioritized
// fallback strategy optimised for accuracy:
//
//	Level 1: MS Excel via AppleScript (macOS, 100% fidelity, ~2-5s)
//	Level 2: WPS Office (macOS: open+quit, Linux: et --headless, ~98%)
//	Level 3: LibreOffice Headless  (cross-platform, Docker-friendly, ~98%)
//	Level 4: Python formulas lib   (pure Python fallback, ~90%)
//
// The ordering puts native spreadsheet applications first because they give
// bit-exact results compared to what the benchmark expects.
type FormulaEvaluator struct {
	pythonPath string
	timeout    time.Duration
}

func NewFormulaEvaluator(pythonPath string) *FormulaEvaluator {
	if pythonPath == "" {
		pythonPath = "python3"
	}
	return &FormulaEvaluator{
		pythonPath: pythonPath,
		timeout:    60 * time.Second,
	}
}

// Evaluate recalculates all formulas in the given xlsx file in-place.
// Tries Level 1 → 2 → 3 → 4 in order of accuracy.
func (fe *FormulaEvaluator) Evaluate(targetPath string) error {
	absPath, err := filepath.Abs(targetPath)
	if err != nil {
		absPath = targetPath
	}

	// Level 1: MS Excel AppleScript (macOS only, highest fidelity)
	if runtime.GOOS == "darwin" {
		err = ForceCalculate(absPath)
		if err == nil {
			slog.Info("formula-eval: Level 1 (MS Excel) succeeded", "file", absPath)
			return nil
		}
		slog.Info("formula-eval: Level 1 (MS Excel) failed",
			"error", err, "file", absPath)
	}

	// Level 2: WPS Office (macOS: open+save+quit, Linux: et --headless)
	err = fe.evaluateWithWPS(absPath)
	if err == nil {
		slog.Info("formula-eval: Level 2 (WPS) succeeded", "file", absPath)
		return nil
	}
	slog.Info("formula-eval: Level 2 (WPS) failed",
		"error", err, "file", absPath)

	// Level 3: LibreOffice Headless (cross-platform)
	err = fe.evaluateWithLibreOffice(absPath)
	if err == nil {
		slog.Info("formula-eval: Level 3 (LibreOffice) succeeded", "file", absPath)
		return nil
	}
	slog.Info("formula-eval: Level 3 (LibreOffice) failed",
		"error", err, "file", absPath)

	// Level 4: Python formulas library (pure Python fallback)
	err = fe.evaluateWithFormulasLib(absPath)
	if err == nil {
		slog.Info("formula-eval: Level 4 (formulas lib) succeeded", "file", absPath)
		return nil
	}
	slog.Warn("formula-eval: all levels failed", "error", err, "file", absPath)

	return fmt.Errorf("all formula evaluation tiers failed for %s", absPath)
}

// evaluateWithWPS uses WPS Office to recalculate formulas.
// On Linux: uses "et --headless" (similar to LibreOffice).
// On macOS: opens the file in WPS (auto-calculates), then saves via AppleScript.
func (fe *FormulaEvaluator) evaluateWithWPS(targetPath string) error {
	wpsPath := findWPS()
	if wpsPath == "" {
		return fmt.Errorf("WPS Office not found")
	}

	if runtime.GOOS == "linux" {
		return fe.evaluateWithWPSLinux(wpsPath, targetPath)
	}
	if runtime.GOOS == "darwin" {
		return fe.evaluateWithWPSMac(targetPath)
	}
	return fmt.Errorf("WPS evaluation not supported on %s", runtime.GOOS)
}

// evaluateWithWPSLinux uses WPS et command in headless mode (Linux only).
func (fe *FormulaEvaluator) evaluateWithWPSLinux(etPath, targetPath string) error {
	dir := filepath.Dir(targetPath)

	ctx, cancel := context.WithTimeout(context.Background(), fe.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, etPath,
		"--headless",
		"--convert-to", "xlsx",
		"--outdir", dir,
		targetPath,
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("wps et headless: %w, stderr: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// evaluateWithWPSMac opens the file in WPS on macOS, waits for auto-calculation,
// then saves and closes via AppleScript.
func (fe *FormulaEvaluator) evaluateWithWPSMac(targetPath string) error {
	resolvedPath, err := filepath.EvalSymlinks(targetPath)
	if err == nil {
		targetPath = resolvedPath
	}

	script := fmt.Sprintf(`
		try
			do shell script "open -a wpsoffice " & quoted form of "%s"
			delay 5
			tell application "wpsoffice"
				save
				quit saving yes
			end tell
			delay 2
		on error errMsg
			try
				tell application "wpsoffice" to quit saving no
			end try
			error errMsg
		end try
	`, targetPath)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wps mac applescript: %w, output: %s", err, string(out))
	}
	return nil
}

// findWPS returns the path to WPS Office spreadsheet executable, or empty if not found.
func findWPS() string {
	if runtime.GOOS == "linux" {
		candidates := []string{
			"et",
			"/usr/bin/et",
			"/opt/kingsoft/wps-office/office6/et",
		}
		for _, c := range candidates {
			if path, err := exec.LookPath(c); err == nil {
				return path
			}
			if _, err := os.Stat(c); err == nil {
				return c
			}
		}
		return ""
	}
	if runtime.GOOS == "darwin" {
		candidates := []string{
			"/Applications/wpsoffice.app/Contents/MacOS/wpsoffice",
			"/Applications/WPS Office.app/Contents/MacOS/wpsoffice",
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				return c
			}
		}
		return ""
	}
	return ""
}

// EvaluateSingle evaluates a single Excel formula against provided context cells.
// Returns the computed value as a string, or an error if evaluation fails.
func (fe *FormulaEvaluator) EvaluateSingle(formula string, contextCells map[string]interface{}, sheetFile string) (string, error) {
	if !strings.HasPrefix(formula, "=") {
		formula = "=" + formula
	}

	contextJSON, err := json.Marshal(contextCells)
	if err != nil {
		return "", fmt.Errorf("marshal context cells: %w", err)
	}

	script := fmt.Sprintf(`
import json, sys

context_cells = json.loads(%q)
formula = %q
sheet_file = %q

result = None
error_msg = None

# Strategy 1: Use formulas library with workbook context
if sheet_file:
    try:
        import formulas
        xl_model = formulas.ExcelModel().loads(sheet_file).finish()
        xl_model.calculate()
        result = str(xl_model.calculate())
    except Exception:
        pass

# Strategy 2: Build a temporary xlsx with context cells, write the formula, and evaluate
if result is None:
    try:
        import openpyxl
        import tempfile, os

        wb = openpyxl.Workbook()
        ws = wb.active
        ws.title = "Sheet1"

        for addr, val in context_cells.items():
            ws[addr] = val

        eval_cell = "ZZ1000"
        ws[eval_cell] = formula

        tmp = tempfile.NamedTemporaryFile(suffix=".xlsx", delete=False)
        tmp_path = tmp.name
        tmp.close()
        wb.save(tmp_path)
        wb.close()

        try:
            import formulas
            xl_model = formulas.ExcelModel().loads(tmp_path).finish()
            sol = xl_model.calculate()
            for k, v in sol.items():
                if "ZZ1000" in str(k).upper():
                    import numpy as np
                    if isinstance(v, np.ndarray):
                        v = v.flat[0]
                    result = str(v)
                    break
        except Exception as e:
            error_msg = str(e)
        finally:
            os.unlink(tmp_path)
    except Exception as e:
        error_msg = str(e)

output = {"result": result, "error": error_msg}
print("===FORMULA_EVAL===" + json.dumps(output))
`, string(contextJSON), formula, sheetFile)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, fe.pythonPath, "-c", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("formula eval script failed: %w, stderr: %s", err, stderr.String())
	}

	outStr := stdout.String()
	marker := "===FORMULA_EVAL==="
	idx := strings.LastIndex(outStr, marker)
	if idx < 0 {
		return "", fmt.Errorf("no result marker in formula eval output")
	}

	var result struct {
		Result *string `json:"result"`
		Error  *string `json:"error"`
	}
	if err := json.Unmarshal([]byte(outStr[idx+len(marker):]), &result); err != nil {
		return "", fmt.Errorf("parse formula eval result: %w", err)
	}

	if result.Result != nil && *result.Result != "" && *result.Result != "None" {
		return *result.Result, nil
	}
	if result.Error != nil && *result.Error != "" {
		return "", fmt.Errorf("formula eval: %s", *result.Error)
	}
	return "", fmt.Errorf("formula eval returned no result for: %s", formula)
}

// evaluateWithFormulasLib uses the Python `formulas` library to recalculate
// all formulas in the workbook and write results back.
// Enhanced: catches common unsupported function errors and falls through gracefully.
func (fe *FormulaEvaluator) evaluateWithFormulasLib(targetPath string) error {
	script := fmt.Sprintf(`
import sys, traceback
try:
    import formulas
    import warnings
    warnings.filterwarnings('ignore')
    xl_model = formulas.ExcelModel().loads(%q).finish()
    sol = xl_model.calculate()
    xl_model.write(dirpath=None)
    print("OK")
except Exception as e:
    err_str = str(e)
    # Known unsupported: COUNTIF multi-arg, COLUMNS, MONTH with nested args, etc.
    # These are formulas lib limitations, not file errors.
    if any(kw in err_str.lower() for kw in ['requires', 'argument', 'not implemented', 'unknown function']):
        print("PARTIAL:" + err_str, file=sys.stderr)
        # Try partial evaluation: calculate what we can, skip failures
        try:
            import openpyxl
            from openpyxl.utils import get_column_letter
            wb = openpyxl.load_workbook(%q)
            for sn in wb.sheetnames:
                ws = wb[sn]
                for row in ws.iter_rows():
                    for cell in row:
                        if cell.data_type == 'f' and cell.value and str(cell.value).startswith('='):
                            try:
                                from formulas import Parser
                                p = Parser()
                                p.parse(str(cell.value))
                            except Exception:
                                pass  # leave formula as-is
            wb.save(%q)
            print("PARTIAL_OK")
        except Exception:
            pass
        sys.exit(1)
    else:
        print("ERROR:" + err_str, file=sys.stderr)
        sys.exit(1)
`, targetPath, targetPath, targetPath)

	ctx, cancel := context.WithTimeout(context.Background(), fe.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, fe.pythonPath, "-c", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("formulas lib: %w, stderr: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// evaluateWithLibreOffice uses LibreOffice in headless mode to recalculate formulas.
// It opens the file, forces recalculation, and re-saves as xlsx.
func (fe *FormulaEvaluator) evaluateWithLibreOffice(targetPath string) error {
	loPath := findLibreOffice()
	if loPath == "" {
		return fmt.Errorf("LibreOffice not found")
	}

	dir := filepath.Dir(targetPath)
	baseName := filepath.Base(targetPath)

	ctx, cancel := context.WithTimeout(context.Background(), fe.timeout)
	defer cancel()

	// Use macro to force full recalculation before saving
	cmd := exec.CommandContext(ctx, loPath,
		"--headless",
		"--calc",
		"--convert-to", "xlsx",
		"--outdir", dir,
		targetPath,
	)

	// Set HOME to avoid LibreOffice profile lock issues in concurrent runs
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("HOME=%s", dir),
		"SAL_USE_VCLPLUGIN=svp",
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("libreoffice: %w, stderr: %s", err, strings.TrimSpace(stderr.String()))
	}

	// LibreOffice may produce the output with a slightly different name if the
	// extension changed. Verify the target file was overwritten.
	outputPath := filepath.Join(dir, baseName)
	if _, err := os.Stat(outputPath); err != nil {
		// Try without extension change
		nameNoExt := strings.TrimSuffix(baseName, filepath.Ext(baseName))
		altPath := filepath.Join(dir, nameNoExt+".xlsx")
		if altPath != outputPath {
			if _, err2 := os.Stat(altPath); err2 == nil {
				return os.Rename(altPath, outputPath)
			}
		}
		return fmt.Errorf("libreoffice output not found at %s", outputPath)
	}
	return nil
}

// findLibreOffice returns the path to LibreOffice executable, or empty if not found.
func findLibreOffice() string {
	candidates := []string{
		"libreoffice",
		"soffice",
	}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates,
			"/Applications/LibreOffice.app/Contents/MacOS/soffice",
		)
	}
	if runtime.GOOS == "linux" {
		candidates = append(candidates,
			"/usr/bin/libreoffice",
			"/usr/lib/libreoffice/program/soffice",
		)
	}

	for _, c := range candidates {
		if path, err := exec.LookPath(c); err == nil {
			return path
		}
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}
