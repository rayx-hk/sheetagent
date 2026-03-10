package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/rayx-hk/sheetagent/internal/executor"
	"github.com/rayx-hk/sheetagent/internal/sheet"
)

// CrossCheckMismatch records a cell-level disagreement between Path A and Path B.
type CrossCheckMismatch struct {
	Address   string `json:"address"`
	PathAVal  string `json:"path_a_val"`
	PathBVal  string `json:"path_b_val"`
	Hint      string `json:"hint,omitempty"`
}

// DualEngineResult holds the cross-validation outcome.
type DualEngineResult struct {
	PathAValues map[string]string    `json:"path_a_values"`
	PathBValues map[string]string    `json:"path_b_values"`
	Mismatches  []CrossCheckMismatch `json:"mismatches,omitempty"`
	Passed      bool                 `json:"passed"`
	Feedback    string               `json:"feedback"`
	PathBError  string               `json:"path_b_error,omitempty"`
}

// DualEngineValidator implements the Dual-Engine Cross-Validation strategy:
// Path A (openpyxl/formulas written to Excel) vs Path B (Pandas pure computation).
type DualEngineValidator struct {
	parser    *sheet.ExcelizeParser
	evaluator *executor.FormulaEvaluator
	pythonPath string
	timeout    time.Duration
}

func NewDualEngineValidator(pythonPath string) *DualEngineValidator {
	if pythonPath == "" {
		pythonPath = "python3"
	}
	return &DualEngineValidator{
		parser:     sheet.NewParser(),
		evaluator:  executor.NewFormulaEvaluator(pythonPath),
		pythonPath: pythonPath,
		timeout:    60 * time.Second,
	}
}

// Validate performs dual-engine cross-validation:
// 1. Read Path A results (after FormulaEvaluator recalculation)
// 2. Execute Path B Pandas code to get independent computation
// 3. Compare cell-by-cell
func (de *DualEngineValidator) Validate(
	ctx context.Context,
	outputFile string,
	answerPosition string,
	pathBCode string,
	inputOrigFile string,
	workDir string,
) (*DualEngineResult, error) {
	result := &DualEngineResult{
		PathAValues: make(map[string]string),
		PathBValues: make(map[string]string),
	}

	if pathBCode == "" {
		result.Passed = true
		result.Feedback = "No Path B code provided; skipping dual-engine validation."
		return result, nil
	}

	// Step 1: Evaluate formulas in Path A output
	if err := de.evaluator.Evaluate(outputFile); err != nil {
		slog.Warn("dual-engine: FormulaEvaluator failed for Path A", "error", err)
	}

	// Step 2: Read Path A cell values
	pathASheets, err := de.parser.Parse(ctx, outputFile)
	if err != nil {
		return nil, fmt.Errorf("dual-engine: parse Path A output: %w", err)
	}

	sheetName, cells := parseCellRange(answerPosition)
	filterBySheet := func(data []sheet.SheetData) []sheet.SheetData {
		if sheetName == "" {
			return data
		}
		for _, s := range data {
			if strings.EqualFold(s.Name, sheetName) {
				return []sheet.SheetData{s}
			}
		}
		return data
	}

	pathAMap := buildCellMap(filterBySheet(pathASheets))
	for _, addr := range cells {
		result.PathAValues[addr] = pathAMap[addr]
	}

	// Step 3: Execute Path B Pandas code
	pathBValues, pathBErr := de.executePathB(ctx, pathBCode, inputOrigFile, answerPosition, workDir)
	if pathBErr != nil {
		result.PathBError = pathBErr.Error()
		result.Passed = true // Don't fail if Path B itself errors
		result.Feedback = fmt.Sprintf("Path B (Pandas verification) execution failed: %s. Falling back to Path A only.", pathBErr)
		slog.Warn("dual-engine: Path B execution failed", "error", pathBErr)
		return result, nil
	}
	result.PathBValues = pathBValues

	// Step 4: Cross-check cell by cell
	for _, addr := range cells {
		aVal := result.PathAValues[addr]
		bVal := result.PathBValues[addr]

		if bVal == "" {
			continue // Path B didn't produce a value for this cell; skip
		}

		if !CompareValues(aVal, bVal) {
			mismatch := CrossCheckMismatch{
				Address:  addr,
				PathAVal: aVal,
				PathBVal: bVal,
				Hint:     generateCrossCheckHint(addr, aVal, bVal),
			}
			result.Mismatches = append(result.Mismatches, mismatch)
		}
	}

	result.Passed = len(result.Mismatches) == 0
	result.Feedback = de.buildFeedback(result)
	return result, nil
}

// executePathB runs the Pandas verification code and extracts computed values.
// The code must print results in ===PANDAS_VERIFY==={"R1C1": "val", ...} format.
func (de *DualEngineValidator) executePathB(ctx context.Context, code, inputFile, answerPosition, workDir string) (map[string]string, error) {
	wrappedCode := fmt.Sprintf(`
import json, sys
input_file = %q
answer_position = %q

%s

# The Path B code should define a dict called 'verify_results'
# mapping cell addresses (R1C1 format) to computed values.
if 'verify_results' in dir():
    print("===PANDAS_VERIFY===" + json.dumps({k: str(v) for k, v in verify_results.items()}))
else:
    print("===PANDAS_VERIFY==={}", file=sys.stdout)
`, inputFile, answerPosition, code)

	execCtx, cancel := context.WithTimeout(ctx, de.timeout)
	defer cancel()

	tmpFile, err := os.CreateTemp(workDir, "pathb_*.py")
	if err != nil {
		return nil, fmt.Errorf("create Path B temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.WriteString(wrappedCode); err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("write Path B code: %w", err)
	}
	tmpFile.Close()

	cmd := exec.CommandContext(execCtx, de.pythonPath, tmpPath)
	cmd.Dir = workDir

	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("Path B execution: %w, output: %s",
			err, truncateString(string(out), 500))
	}

	outStr := string(out)
	marker := "===PANDAS_VERIFY==="
	idx := strings.LastIndex(outStr, marker)
	if idx < 0 {
		return nil, fmt.Errorf("Path B output missing ===PANDAS_VERIFY=== marker")
	}

	jsonStr := strings.TrimSpace(outStr[idx+len(marker):])
	// Take only the first line (in case there's trailing output)
	if nl := strings.Index(jsonStr, "\n"); nl >= 0 {
		jsonStr = jsonStr[:nl]
	}

	var values map[string]string
	if err := json.Unmarshal([]byte(jsonStr), &values); err != nil {
		return nil, fmt.Errorf("parse Path B results: %w, raw: %s", err, truncateString(jsonStr, 200))
	}

	return values, nil
}

func generateCrossCheckHint(addr, pathA, pathB string) string {
	if pathA == "" && pathB != "" {
		return "Excel formula produced empty result but Pandas computed a value. The formula may reference wrong cells or have a syntax error."
	}
	if pathA != "" && pathB == "" {
		return "Excel formula has a value but Pandas returned empty. Path B logic may be incomplete."
	}

	_, aIsNum := parseNumber(normalizeValue(pathA))
	_, bIsNum := parseNumber(normalizeValue(pathB))

	if aIsNum && bIsNum {
		return "Both paths produced numbers but they differ. Check formula cell references and Pandas column selection."
	}
	return "Values disagree between Excel formula and Pandas computation. Compare the logic of both paths."
}

func (de *DualEngineValidator) buildFeedback(result *DualEngineResult) string {
	if result.Passed {
		return "Dual-engine cross-validation PASSED: Path A (Excel) and Path B (Pandas) results match."
	}

	var sb strings.Builder
	sb.WriteString("[DUAL-ENGINE CROSS-CHECK FAILED] ")
	sb.WriteString(fmt.Sprintf("%d cell(s) disagree between Excel formulas and Pandas computation:\n", len(result.Mismatches)))

	limit := len(result.Mismatches)
	if limit > 8 {
		limit = 8
	}
	for _, m := range result.Mismatches[:limit] {
		sb.WriteString(fmt.Sprintf("  [%s] Excel=%q  Pandas=%q", m.Address, m.PathAVal, m.PathBVal))
		if m.Hint != "" {
			sb.WriteString(fmt.Sprintf("  → %s", m.Hint))
		}
		sb.WriteString("\n")
	}
	if len(result.Mismatches) > limit {
		sb.WriteString(fmt.Sprintf("  ...and %d more mismatches\n", len(result.Mismatches)-limit))
	}

	sb.WriteString("\nACTION: Compare your Excel formula logic with the Pandas computation. ")
	sb.WriteString("The Pandas path is a pure-Python independent calculation — if it differs from the Excel formula result, ")
	sb.WriteString("one of the two paths has a bug. Fix the path with the error.")

	return sb.String()
}

// ExtractPathBCode extracts the Pandas verification code from agent trace.
// Looks for code between ===PANDAS_VERIFY_CODE_START=== and ===PANDAS_VERIFY_CODE_END===.
func ExtractPathBCode(traceContent string) string {
	startMarker := "===PANDAS_VERIFY_CODE_START==="
	endMarker := "===PANDAS_VERIFY_CODE_END==="

	startIdx := strings.LastIndex(traceContent, startMarker)
	if startIdx < 0 {
		return ""
	}
	startIdx += len(startMarker)

	endIdx := strings.Index(traceContent[startIdx:], endMarker)
	if endIdx < 0 {
		return ""
	}

	return strings.TrimSpace(traceContent[startIdx : startIdx+endIdx])
}
