package eval

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/rayx-hk/sheetagent/internal/executor"
	"github.com/rayx-hk/sheetagent/internal/sheet"
)

// SelfChecker provides blind feedback on agent output quality without access
// to the golden answer file. It ensures no answer leakage while providing
// actionable feedback through formula-aware cell inspection and tiered hints.
type SelfChecker struct {
	parser      *sheet.ExcelizeParser
	validator   *SyntheticValidator
	formulaEval *executor.FormulaEvaluator
}

func NewSelfChecker(pythonPath string) *SelfChecker {
	return &SelfChecker{
		parser:      sheet.NewParser(),
		validator:   NewSyntheticValidator(pythonPath),
		formulaEval: executor.NewFormulaEvaluator(pythonPath),
	}
}

// SelfCheckResult contains feedback derived solely from inspecting the agent's
// output and running synthetic variants -- never from the golden answer.
type SelfCheckResult struct {
	Passed         bool            `json:"passed"`
	EmptyCells     []string        `json:"empty_cells,omitempty"`
	FormulaCells   []string        `json:"formula_cells,omitempty"`
	ErrorCells     []string        `json:"error_cells,omitempty"` // cells with #VALUE!, #REF!, etc.
	ValueCells     int             `json:"value_cells"`
	TypeIssues     []string        `json:"type_issues,omitempty"`
	SyntheticFails []VariantResult `json:"synthetic_fails,omitempty"`
	ExecutionError string          `json:"execution_error,omitempty"`
	AgentVerified  bool            `json:"agent_verified"`
	Summary        string          `json:"summary"`

	// Structural analysis (方案 C): type/magnitude info without leaking values
	NumericCells      int      `json:"numeric_cells"`
	TextCells         int      `json:"text_cells"`
	DateCells         int      `json:"date_cells"`
	FormulaEvalOK     bool     `json:"formula_eval_ok"`
	ZeroFormulaCells  []string `json:"zero_formula_cells,omitempty"`
}

// CheckOpts controls SelfChecker behavior per-attempt for tiered feedback.
type CheckOpts struct {
	Attempt          int  // current retry attempt (0-based)
	RunSynthetic     bool // whether to run synthetic variants (expensive)
	AgentSelfVerify  string // Agent's own M12 verification output, if any
}

// Check examines the agent's output file without comparing to golden answer.
//
// Key fix: ForceCalculate is called when formulas are detected, so formula-based
// solutions are not falsely reported as "empty cells".
func (sc *SelfChecker) Check(ctx context.Context, outputFile, answerPosition, code, inputFile, workDir string) (*SelfCheckResult, error) {
	return sc.CheckWithOpts(ctx, outputFile, answerPosition, code, inputFile, workDir, CheckOpts{})
}

// CheckWithOpts is the full-featured check with tiered feedback control.
func (sc *SelfChecker) CheckWithOpts(ctx context.Context, outputFile, answerPosition, code, inputFile, workDir string, opts CheckOpts) (*SelfCheckResult, error) {
	result := &SelfCheckResult{Passed: true}

	// Phase 0: Parse Agent's own M12 self-verification if provided
	if opts.AgentSelfVerify != "" {
		result.AgentVerified = parseAgentVerification(opts.AgentSelfVerify)
	}

	// Phase 1: Detect formulas in target cells BEFORE any evaluation.
	// This lets us distinguish "formula not evaluated" from "truly empty".
	formulaCells := sc.detectFormulasInTarget(ctx, outputFile, answerPosition)
	result.FormulaCells = formulaCells

	// Phase 1b: If formulas exist, force recalculation so that
	// formula results become visible to subsequent reads.
	formulaEvalOK := true
	if len(formulaCells) > 0 {
		slog.Info("self-checker: formulas detected in target, running FormulaEvaluator",
			"formula_count", len(formulaCells), "file", outputFile)
		if err := sc.formulaEval.Evaluate(outputFile); err != nil {
			formulaEvalOK = false
			slog.Warn("self-checker: FormulaEvaluator failed — formula cells may appear empty",
				"error", err)
		}
	}

	// Phase 2: Check that answer_position cells are non-empty (post-recalculation).
	emptyCells, typeIssues, errorCells, zeroCells, valueCells, err := sc.checkTargetCells(ctx, outputFile, answerPosition)
	if err != nil {
		result.ExecutionError = fmt.Sprintf("failed to read output file: %v", err)
		result.Passed = false
		result.Summary = result.ExecutionError
		return result, nil
	}

	// When FormulaEval failed, formula cells will read as empty even though the
	// agent wrote a valid formula. Exclude them from the empty-cell list.
	if !formulaEvalOK && len(formulaCells) > 0 {
		formulaSet := make(map[string]bool, len(formulaCells))
		for _, fc := range formulaCells {
			formulaSet[strings.ToUpper(fc)] = true
		}
		filtered := emptyCells[:0]
		for _, ec := range emptyCells {
			if !formulaSet[strings.ToUpper(ec)] {
				filtered = append(filtered, ec)
			}
		}
		excluded := len(emptyCells) - len(filtered)
		if excluded > 0 {
			slog.Info("self-checker: excluded formula cells from empty check",
				"excluded", excluded, "remaining_empty", len(filtered))
		}
		emptyCells = filtered
	}

	// Phase 2b: Detect formula cells that evaluated to zero — a strong signal
	// that the formula approach failed (e.g., array formulas unsupported by
	// excelize/Excel, or logically incorrect formulas).
	if len(formulaCells) > 0 {
		formulaSet := make(map[string]bool, len(formulaCells))
		for _, fc := range formulaCells {
			formulaSet[strings.ToUpper(fc)] = true
		}
		var zeroFormulas []string
		for _, zc := range zeroCells {
			if formulaSet[strings.ToUpper(zc)] {
				zeroFormulas = append(zeroFormulas, zc)
			}
		}
		// Also count formula cells that are empty as "zero formulas"
		for _, ec := range emptyCells {
			if formulaSet[strings.ToUpper(ec)] {
				zeroFormulas = append(zeroFormulas, ec)
			}
		}
		if len(zeroFormulas) > 0 {
			slog.Warn("self-checker: formula cells evaluated to zero/empty",
				"zero_formula_cells", len(zeroFormulas), "total_formulas", len(formulaCells))
			result.ZeroFormulaCells = zeroFormulas
			// If ALL formula cells are zero/empty, the formula approach almost certainly failed
			if len(zeroFormulas) == len(formulaCells) {
				result.Passed = false
			}
		}
	}

	result.EmptyCells = emptyCells
	result.ErrorCells = errorCells
	result.TypeIssues = typeIssues
	result.ValueCells = valueCells
	result.FormulaEvalOK = formulaEvalOK

	// Structural analysis: classify non-empty cells by type (stored but NOT
	// fed back to agent — the verbose feedback was causing context bloat).
	numericCount, textCount, dateCount := sc.classifyCellTypes(ctx, outputFile, answerPosition)
	result.NumericCells = numericCount
	result.TextCells = textCount
	result.DateCells = dateCount

	// Tolerate a small fraction of empty cells — some tasks intentionally have
	// blank cells within the answer range (e.g., optional fields, N/A entries).
	// Only fail if >5% of target cells are empty or ALL cells are empty.
	totalCells := len(emptyCells) + valueCells
	if len(emptyCells) > 0 && totalCells > 0 {
		emptyRatio := float64(len(emptyCells)) / float64(totalCells)
		if valueCells == 0 || emptyRatio > 0.05 {
			result.Passed = false
		}
	}

	// Phase 3: Run synthetic validation ONLY when explicitly requested.
	// Removed from default retry loop to avoid false negatives that confuse the agent.
	if opts.RunSynthetic && code != "" && inputFile != "" {
		valResult, err := sc.validator.Validate(ctx, inputFile, code, answerPosition, workDir)
		if err != nil {
			result.SyntheticFails = []VariantResult{{
				VariantType: "generator_error",
				Error:       err.Error(),
			}}
		} else if !valResult.Passed {
			result.Passed = false
			result.SyntheticFails = valResult.FailDetails
		}
	}

	result.Summary = sc.buildSummary(result, opts.Attempt)
	return result, nil
}

// detectFormulasInTarget checks if any cells in answer_position contain formulas.
// Uses a raw excelize read (not the parser) to directly inspect formula presence.
func (sc *SelfChecker) detectFormulasInTarget(ctx context.Context, outputFile, answerPosition string) []string {
	sheets, err := sc.parser.Parse(ctx, outputFile)
	if err != nil {
		return nil
	}

	sheetName, targetCells := parseCellRange(answerPosition)

	targetSet := make(map[string]bool, len(targetCells))
	for _, c := range targetCells {
		targetSet[strings.ToUpper(c)] = true
	}

	var formulaCells []string
	for _, sd := range sheets {
		if sheetName != "" && !strings.EqualFold(sd.Name, sheetName) {
			continue
		}
		for _, f := range sd.Formulas {
			addr := cellAddr(f.Row, f.Col)
			if targetSet[strings.ToUpper(addr)] {
				formulaCells = append(formulaCells, addr)
			}
		}
	}
	return formulaCells
}

// checkTargetCells reads the output file and reports which cells in
// answer_position are empty and which have suspicious types.
// Returns: empty cells, type issues, error cells, zero-value cells, count of non-empty value cells.
func (sc *SelfChecker) checkTargetCells(ctx context.Context, outputFile, answerPosition string) (emptyCells []string, typeIssues []string, errorCells []string, zeroCells []string, valueCells int, err error) {
	sheets, err := sc.parser.Parse(ctx, outputFile)
	if err != nil {
		return nil, nil, nil, nil, 0, err
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

	cellMap := buildCellMap(filterBySheet(sheets))

	for _, addr := range cells {
		val, exists := cellMap[addr]
		trimmed := strings.TrimSpace(val)
		if !exists || trimmed == "" {
			emptyCells = append(emptyCells, addr)
		} else if isExcelizerErrorValue(trimmed) {
			errorCells = append(errorCells, addr+"="+truncateString(trimmed, 40))
			valueCells++
		} else {
			if isZeroValue(trimmed) {
				zeroCells = append(zeroCells, addr)
			}
			valueCells++
		}
	}

	return emptyCells, typeIssues, errorCells, zeroCells, valueCells, nil
}

// isZeroValue checks if a cell value represents zero (0, 0.0, 0.00, etc.).
func isZeroValue(s string) bool {
	s = strings.TrimSpace(s)
	if s == "0" {
		return true
	}
	cleaned := strings.TrimRight(s, "0")
	cleaned = strings.TrimRight(cleaned, ".")
	return cleaned == "" || cleaned == "0"
}

// buildSummary creates a human-readable feedback string with tiered detail
// based on the attempt number. Never contains expected values.
func (sc *SelfChecker) buildSummary(result *SelfCheckResult, attempt int) string {
	if result.Passed {
		return "Self-check passed: all target cells written successfully."
	}

	var parts []string

	if result.ExecutionError != "" {
		parts = append(parts, fmt.Sprintf("[EXECUTION ERROR] %s", result.ExecutionError))
	}

	if len(result.EmptyCells) > 0 {
		shown := result.EmptyCells
		if len(shown) > 10 {
			shown = shown[:10]
		}

		// Tiered feedback: more detail on later attempts
		switch {
		case attempt <= 0:
			// First attempt feedback: concise
			parts = append(parts, fmt.Sprintf(
				"[EMPTY CELLS] %d of %d target cells are empty. "+
					"Run Post-Write Verification (M12) to confirm your code wrote to the correct cells.",
				len(result.EmptyCells), len(result.EmptyCells)+result.ValueCells))

		case attempt == 1:
			// Second attempt: more actionable detail
			parts = append(parts, fmt.Sprintf(
				"[EMPTY CELLS] %d of %d target cells are still empty after retry. Addresses: %s. "+
					"Common causes: (1) Code wrote to wrong sheet or wrong cells. "+
					"(2) Formulas were written but not computed — try writing computed values instead of formulas. "+
					"(3) The CHANGE_LOG range doesn't match answer_position. "+
					"Verify by reading back the cells after wb.save().",
				len(result.EmptyCells), len(result.EmptyCells)+result.ValueCells,
				strings.Join(shown, ", ")))

		default:
			// Third+ attempt: switch strategy advice
			parts = append(parts, fmt.Sprintf(
				"[EMPTY CELLS] %d target cells remain empty after %d attempts. Addresses: %s. "+
					"STRATEGY CHANGE REQUIRED: "+
					"(1) Do NOT use Excel formulas — compute values in Python and write them directly. "+
					"(2) Use openpyxl to read existing data, compute the answer in Python, and ws.cell().value = result. "+
					"(3) After wb.save(), immediately re-open and verify: wb2 = openpyxl.load_workbook(input_file); "+
					"print([wb2[sheet].cell(row=r, column=c).value for each target cell]).",
				len(result.EmptyCells), attempt+1,
				strings.Join(shown, ", ")))
		}
	}

	if len(result.ErrorCells) > 0 {
		shown := result.ErrorCells
		if len(shown) > 5 {
			shown = shown[:5]
		}
		allError := len(result.ErrorCells) == result.ValueCells && len(result.EmptyCells) == 0
		if allError {
			// ALL cells are errors → likely a formula bug, not expected errors
			parts = append(parts, fmt.Sprintf(
				"[FORMULA ERRORS] ALL %d target cells contain error values: %s. "+
					"This likely indicates a formula bug (wrong range, bad syntax). "+
					"Fix the formula or fall back to Python computation.",
				len(result.ErrorCells), strings.Join(shown, ", ")))
		} else {
			// Mix of errors and values → errors may be expected (e.g., VLOOKUP no match = #N/A)
			parts = append(parts, fmt.Sprintf(
				"[INFO] %d of %d target cells contain formula error values: %s. "+
					"These MAY be expected (e.g., VLOOKUP returning #N/A for non-matching rows). "+
					"Only fix if ALL cells error — partial errors in lookup tasks are normal.",
				len(result.ErrorCells), len(result.ErrorCells)+len(result.EmptyCells)+result.ValueCells-len(result.ErrorCells),
				strings.Join(shown, ", ")))
		}
	}

	if len(result.ZeroFormulaCells) > 0 {
		shown := result.ZeroFormulaCells
		if len(shown) > 10 {
			shown = shown[:10]
		}
		allFormulasBad := len(result.ZeroFormulaCells) == len(result.FormulaCells)
		if allFormulasBad {
			parts = append(parts, fmt.Sprintf(
				"[FORMULA ZERO VALUES] ALL %d formula cells evaluated to 0 or empty. Cells: %s. "+
					"This strongly indicates your Excel formulas are not working correctly. "+
					"Common causes: (1) Array formulas (INDEX+MATCH with multi-cell ranges) are not supported by the evaluation engine. "+
					"(2) The formula logic is incorrect. "+
					"MANDATORY: SWITCH TO PYTHON — read the data with openpyxl/pandas, compute the answer in Python, "+
					"and write computed values directly with ws.cell().value = result. Do NOT use Excel formulas.",
				len(result.ZeroFormulaCells), strings.Join(shown, ", ")))
		} else {
			parts = append(parts, fmt.Sprintf(
				"[FORMULA ZERO VALUES] %d of %d formula cells evaluated to 0/empty: %s. "+
					"Some formulas may be incorrect or use unsupported functions. "+
					"Consider switching affected cells to Python-computed values.",
				len(result.ZeroFormulaCells), len(result.FormulaCells),
				strings.Join(shown, ", ")))
		}
	} else if len(result.FormulaCells) > 0 && len(result.EmptyCells) > 0 {
		if !result.FormulaEvalOK {
			parts = append(parts, fmt.Sprintf(
				"[FORMULA EVAL ERRORS] %d formulas could not be evaluated by the formula engine. "+
					"This usually means unsupported functions (COUNTIFS, TEXTJOIN, etc.). "+
					"SWITCH TO PYTHON: compute the values in Python and write them with ws.cell().value = computed_value.",
				len(result.FormulaCells)))
		} else {
			parts = append(parts, fmt.Sprintf(
				"[FORMULA INFO] %d target cells contain Excel formulas. "+
					"If these formulas produced empty results, the formula may be incorrect or referencing wrong ranges. "+
					"Consider computing the value in Python instead.",
				len(result.FormulaCells)))
		}
	}

	if len(result.TypeIssues) > 0 {
		parts = append(parts, fmt.Sprintf("[TYPE ISSUES] %s", strings.Join(result.TypeIssues, "; ")))
	}

	for _, sf := range result.SyntheticFails {
		parts = append(parts, fmt.Sprintf(
			"[GENERALIZATION FAILURE] Code failed on synthetic %s variant: %s. "+
				"Use dynamic references (ws.max_row, find_header(), etc.).",
			sf.VariantType, truncateString(sf.Error, 300)))
	}

	return strings.Join(parts, "\n\n")
}

// isExcelizerErrorValue detects Excel error values or excelize internal error strings
// that appear in cell values (e.g. "#VALUE!", "YEAR requires exactly 1 argument").
func isExcelizerErrorValue(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '#' {
		return true
	}
	lower := strings.ToLower(s)
	return strings.Contains(lower, "requires exactly") ||
		strings.Contains(lower, "requires ") && strings.Contains(lower, "argument") ||
		strings.Contains(lower, "formula not valid") ||
		strings.Contains(lower, "col_num out of range") ||
		strings.Contains(lower, "calc panic") ||
		strings.Contains(lower, "invalid reference")
}

// classifyCellTypes analyzes the types of non-empty cells in the target region.
// Returns counts of numeric, text, and date-like cells.
func (sc *SelfChecker) classifyCellTypes(ctx context.Context, outputFile, answerPosition string) (numeric, text, date int) {
	sheets, err := sc.parser.Parse(ctx, outputFile)
	if err != nil {
		return 0, 0, 0
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

	cellMap := buildCellMap(filterBySheet(sheets))

	for _, addr := range cells {
		val := cellMap[addr]
		trimmed := strings.TrimSpace(val)
		if trimmed == "" || isExcelizerErrorValue(trimmed) {
			continue
		}
		if looksNumeric(trimmed) {
			numeric++
		} else if looksDate(trimmed) {
			date++
		} else {
			text++
		}
	}
	return
}

// looksNumeric checks if a string looks like a number (with optional formatting).
func looksNumeric(s string) bool {
	cleaned := strings.ReplaceAll(strings.ReplaceAll(s, ",", ""), " ", "")
	cleaned = strings.TrimPrefix(cleaned, "$")
	cleaned = strings.TrimPrefix(cleaned, "£")
	cleaned = strings.TrimPrefix(cleaned, "€")
	cleaned = strings.TrimSuffix(cleaned, "%")
	cleaned = strings.TrimPrefix(cleaned, "-")
	if cleaned == "" {
		return false
	}
	dotCount := 0
	for _, c := range cleaned {
		if c == '.' {
			dotCount++
			if dotCount > 1 {
				return false
			}
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// looksDate checks if a string looks like a date value.
func looksDate(s string) bool {
	datePatterns := []string{"/", "-"}
	hasDelim := false
	for _, p := range datePatterns {
		if strings.Contains(s, p) {
			hasDelim = true
			break
		}
	}
	if !hasDelim {
		return false
	}
	digitCount := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			digitCount++
		}
	}
	return digitCount >= 4 && digitCount <= 14 && len(s) <= 25
}

// parseAgentVerification checks if the agent's own M12 output indicates success.
func parseAgentVerification(output string) bool {
	return strings.Contains(output, "VERIFIED:") && !strings.Contains(output, "WARNING:")
}

// cellAddr converts (row, col) 1-based to "A1" style address.
func cellAddr(row, col int) string {
	colName := ""
	c := col
	for c > 0 {
		c--
		colName = string(rune('A'+c%26)) + colName
		c /= 26
	}
	return fmt.Sprintf("%s%d", colName, row)
}

// BuildBlindRetryHints generates retry feedback from a SelfCheckResult.
func BuildBlindRetryHints(scr *SelfCheckResult) string {
	if scr == nil || scr.Passed {
		return ""
	}
	return "\n\n" + scr.Summary
}
