package eval

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"
)

// SyntheticValidator generates perturbed variants of an input spreadsheet and
// runs the agent's code against them to verify generalization. This replaces
// the golden-answer-based verification that previously leaked expected values.
type SyntheticValidator struct {
	pythonPath string
}

func NewSyntheticValidator(pythonPath string) *SyntheticValidator {
	if pythonPath == "" {
		pythonPath = "python3"
	}
	return &SyntheticValidator{pythonPath: pythonPath}
}

// VariantResult holds the outcome of running code against one synthetic variant.
type VariantResult struct {
	VariantType string `json:"variant_type"`
	Passed      bool   `json:"passed"`
	Error       string `json:"error,omitempty"`
}

// ValidationResult holds the aggregate outcome of synthetic validation.
type ValidationResult struct {
	Passed      bool            `json:"passed"`
	PassCount   int             `json:"pass_count"`
	TotalCount  int             `json:"total_count"`
	FailDetails []VariantResult `json:"fail_details,omitempty"`
}

// Validate generates synthetic variants and runs the given code against each.
// The code is executed in a fresh Python process per variant.
func (sv *SyntheticValidator) Validate(ctx context.Context, inputFile, code, answerPosition, workDir string) (*ValidationResult, error) {
	variants, err := sv.generateVariants(inputFile, workDir)
	if err != nil {
		return nil, fmt.Errorf("generate variants: %w", err)
	}

	result := &ValidationResult{TotalCount: len(variants)}

	for _, v := range variants {
		vr := sv.runOnVariant(ctx, v.path, v.variantType, code, answerPosition, workDir)
		if vr.Passed {
			result.PassCount++
		} else {
			result.FailDetails = append(result.FailDetails, vr)
		}
		os.Remove(v.path)
	}

	result.Passed = result.PassCount == result.TotalCount
	return result, nil
}

type variant struct {
	path        string
	variantType string
}

func (sv *SyntheticValidator) generateVariants(inputFile, workDir string) ([]variant, error) {
	var variants []variant

	vp, err := generateValuePerturbation(inputFile, workDir)
	if err == nil && vp != "" {
		variants = append(variants, variant{path: vp, variantType: "value_perturbation"})
	}

	ss, err := generateStructuralShift(inputFile, workDir)
	if err == nil && ss != "" {
		variants = append(variants, variant{path: ss, variantType: "structural_shift"})
	}

	ds, err := generateDataScaling(inputFile, workDir)
	if err == nil && ds != "" {
		variants = append(variants, variant{path: ds, variantType: "data_scaling"})
	}

	if len(variants) == 0 {
		return nil, fmt.Errorf("no variants could be generated")
	}
	return variants, nil
}

// generateValuePerturbation creates a copy with numeric values randomly perturbed by +-10%.
func generateValuePerturbation(inputFile, workDir string) (string, error) {
	outPath := filepath.Join(workDir, "variant_value_perturb.xlsx")
	if err := copyFileForVariant(inputFile, outPath); err != nil {
		return "", err
	}

	f, err := excelize.OpenFile(outPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	for _, sheetName := range f.GetSheetList() {
		rows, err := f.GetRows(sheetName)
		if err != nil {
			continue
		}
		for rowIdx, row := range rows {
			if rowIdx == 0 {
				continue // skip header
			}
			for colIdx, val := range row {
				if val == "" {
					continue
				}
				if numVal, ok := parseFloatLoose(val); ok {
					factor := 1.0 + (rand.Float64()*0.2 - 0.1) // +-10%
					newVal := numVal * factor
					cellRef, _ := excelize.CoordinatesToCellName(colIdx+1, rowIdx+1)
					f.SetCellValue(sheetName, cellRef, newVal)
				}
			}
		}
	}

	if err := f.SaveAs(outPath); err != nil {
		return "", err
	}
	return outPath, nil
}

// generateStructuralShift inserts 1-2 empty rows at the top to shift data down.
func generateStructuralShift(inputFile, workDir string) (string, error) {
	outPath := filepath.Join(workDir, "variant_struct_shift.xlsx")
	if err := copyFileForVariant(inputFile, outPath); err != nil {
		return "", err
	}

	f, err := excelize.OpenFile(outPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	shiftRows := rand.Intn(2) + 1 // 1 or 2 rows

	for _, sheetName := range f.GetSheetList() {
		if err := f.InsertRows(sheetName, 1, shiftRows); err != nil {
			continue
		}
	}

	if err := f.SaveAs(outPath); err != nil {
		return "", err
	}
	return outPath, nil
}

// generateDataScaling duplicates some data rows to change the total row count.
func generateDataScaling(inputFile, workDir string) (string, error) {
	outPath := filepath.Join(workDir, "variant_data_scale.xlsx")
	if err := copyFileForVariant(inputFile, outPath); err != nil {
		return "", err
	}

	f, err := excelize.OpenFile(outPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	for _, sheetName := range f.GetSheetList() {
		rows, err := f.GetRows(sheetName)
		if err != nil || len(rows) < 3 {
			continue
		}
		// Duplicate the last data row
		lastRow := rows[len(rows)-1]
		newRowIdx := len(rows) + 1
		for colIdx, val := range lastRow {
			cellRef, _ := excelize.CoordinatesToCellName(colIdx+1, newRowIdx)
			if numVal, ok := parseFloatLoose(val); ok {
				factor := 1.0 + (rand.Float64()*0.2 - 0.1)
				f.SetCellValue(sheetName, cellRef, numVal*factor)
			} else {
				f.SetCellValue(sheetName, cellRef, val)
			}
		}
	}

	if err := f.SaveAs(outPath); err != nil {
		return "", err
	}
	return outPath, nil
}

func (sv *SyntheticValidator) runOnVariant(ctx context.Context, variantPath, variantType, code, answerPosition, workDir string) VariantResult {
	// Rewrite the code to use the variant file path instead of the original input
	variantCode := rewriteCodeForVariant(code, variantPath)

	tmpDir, err := os.MkdirTemp("", "synval_*")
	if err != nil {
		return VariantResult{VariantType: variantType, Passed: false, Error: err.Error()}
	}
	defer os.RemoveAll(tmpDir)

	// Copy variant into tmpDir
	base := filepath.Base(variantPath)
	tmpVariant := filepath.Join(tmpDir, base)
	if err := copyFileForVariant(variantPath, tmpVariant); err != nil {
		return VariantResult{VariantType: variantType, Passed: false, Error: err.Error()}
	}

	scriptPath := filepath.Join(tmpDir, "run.py")
	if err := os.WriteFile(scriptPath, []byte(variantCode), 0644); err != nil {
		return VariantResult{VariantType: variantType, Passed: false, Error: err.Error()}
	}

	cmd := exec.CommandContext(ctx, sv.pythonPath, scriptPath)
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		errMsg := fmt.Sprintf("exit error: %v\noutput: %s", err, truncateString(string(output), 500))
		return VariantResult{VariantType: variantType, Passed: false, Error: errMsg}
	}

	return VariantResult{VariantType: variantType, Passed: true}
}

func rewriteCodeForVariant(code, variantPath string) string {
	// The agent code references input_file variable. We prepend an override.
	return fmt.Sprintf("input_file = %q\n%s", variantPath, code)
}

func parseFloatLoose(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", "")
	s = strings.TrimPrefix(s, "$")
	s = strings.TrimPrefix(s, "¥")
	s = strings.TrimPrefix(s, "€")
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err == nil
}

func copyFileForVariant(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "... [truncated]"
}
