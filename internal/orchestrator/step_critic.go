package orchestrator

import (
	"regexp"
	"strings"
)

// StepCritic evaluates agent-generated code for generalization risk.
// v0.5.x uses a rule-based engine (0 tokens, millisecond latency).
// Future versions may add a lightweight LLM evaluation layer.
type StepCritic struct{}

func NewStepCritic() *StepCritic {
	return &StepCritic{}
}

// CriticResult holds the per-candidate scoring output.
type CriticResult struct {
	Score       float64  `json:"score"`       // 0.0 (terrible) to 1.0 (excellent)
	Penalties   []string `json:"penalties"`   // reasons for deductions
	Bonuses     []string `json:"bonuses"`     // reasons for bonus points
	ShouldPrune bool     `json:"should_prune"` // score below PruneThreshold
}

var (
	hardcodedIlocRe      = regexp.MustCompile(`\.iloc\s*\[\s*[:,\d]+\s*\]`)
	hardcodedRangeRe     = regexp.MustCompile(`range\s*\(\s*\d+\s*,\s*\d+\s*\)`)
	hardcodedSheetRe     = regexp.MustCompile(`wb\s*\[\s*["']Sheet\d+["']\s*\]`)
	hardcodedColIdxRe    = regexp.MustCompile(`column\s*=\s*\d+`)
	hardcodedRowIdxRe    = regexp.MustCompile(`row\s*=\s*\d+`)
	dynamicMaxRowRe      = regexp.MustCompile(`ws\.max_row|\.max_row`)
	dynamicMinRowRe      = regexp.MustCompile(`ws\.min_row|\.min_row`)
	dynamicFindRe        = regexp.MustCompile(`find_header|find_col|headers\.index`)
	dynamicSheetnameRe   = regexp.MustCompile(`wb\.sheetnames|sheetnames\[`)
	changelogRe          = regexp.MustCompile(`CHANGE_LOG\s*=`)
	resultMarkerRe       = regexp.MustCompile(`===RESULT===`)
	// New: detect best practices
	verificationRe       = regexp.MustCompile(`VERIFIED:|load_workbook.*data_only|Post-Write`)
	confidenceRe         = regexp.MustCompile(`===CONFIDENCE===`)
	pandasVerifyRe       = regexp.MustCompile(`PANDAS_VERIFY_CODE_START`)
	toExcelRe            = regexp.MustCompile(`\.to_excel\s*\(`)
	hardcodedValueRe     = regexp.MustCompile(`\.value\s*=\s*\d+(\.\d+)?$`)
	assertRe             = regexp.MustCompile(`assert\s+.*column|assert\s+.*row|assert\s+.*range`)
	sortReverseMissingRe = regexp.MustCompile(`\.sort\s*\(`)
	mergedCellCheckRe    = regexp.MustCompile(`merged_cells|merge`)
)

// Evaluate scores a candidate code snippet for generalization risk.
func (sc *StepCritic) Evaluate(code, answerPosition string) CriticResult {
	result := CriticResult{Score: 1.0}

	// Penalties for hardcoding
	if hardcodedIlocRe.MatchString(code) {
		result.Score -= 0.25
		result.Penalties = append(result.Penalties, "uses .iloc with hardcoded indices")
	}

	hardcodedRanges := hardcodedRangeRe.FindAllString(code, -1)
	dangerousRanges := 0
	for _, r := range hardcodedRanges {
		if !strings.Contains(r, "min_row") && !strings.Contains(r, "max_row") &&
			!strings.Contains(r, "len(") && !strings.Contains(r, "shape") {
			dangerousRanges++
		}
	}
	if dangerousRanges > 0 {
		result.Score -= 0.15 * float64(dangerousRanges)
		result.Penalties = append(result.Penalties, "hardcoded range() with literal numbers")
	}

	if hardcodedSheetRe.MatchString(code) {
		result.Score -= 0.15
		result.Penalties = append(result.Penalties, "hardcoded sheet name like wb[\"Sheet1\"]")
	}

	// Count hardcoded row= and column= assignments
	hardcodedColCount := len(hardcodedColIdxRe.FindAllString(code, -1))
	hardcodedRowCount := len(hardcodedRowIdxRe.FindAllString(code, -1))
	if hardcodedColCount > 3 {
		result.Score -= 0.10
		result.Penalties = append(result.Penalties, "many hardcoded column= indices")
	}
	if hardcodedRowCount > 5 {
		result.Score -= 0.10
		result.Penalties = append(result.Penalties, "many hardcoded row= indices")
	}

	// Bonuses for dynamic references
	if dynamicMaxRowRe.MatchString(code) {
		result.Score += 0.05
		result.Bonuses = append(result.Bonuses, "uses ws.max_row for dynamic boundaries")
	}
	if dynamicMinRowRe.MatchString(code) {
		result.Score += 0.05
		result.Bonuses = append(result.Bonuses, "uses ws.min_row for dynamic start")
	}
	if dynamicFindRe.MatchString(code) {
		result.Score += 0.10
		result.Bonuses = append(result.Bonuses, "uses dynamic header/column lookup")
	}
	if dynamicSheetnameRe.MatchString(code) {
		result.Score += 0.05
		result.Bonuses = append(result.Bonuses, "uses wb.sheetnames for dynamic sheet selection")
	}

	// Dangerous patterns
	if toExcelRe.MatchString(code) {
		result.Score -= 0.30
		result.Penalties = append(result.Penalties, "uses pandas .to_excel() which destroys formatting")
	}

	// Required elements
	if !changelogRe.MatchString(code) {
		result.Score -= 0.20
		result.Penalties = append(result.Penalties, "missing CHANGE_LOG declaration")
	}
	if !resultMarkerRe.MatchString(code) {
		result.Score -= 0.10
		result.Penalties = append(result.Penalties, "missing ===RESULT=== marker")
	}

	// Additional bonuses for best practices
	if verificationRe.MatchString(code) {
		result.Score += 0.08
		result.Bonuses = append(result.Bonuses, "includes post-write verification (M12)")
	}
	if confidenceRe.MatchString(code) {
		result.Score += 0.03
		result.Bonuses = append(result.Bonuses, "outputs confidence score (M11)")
	}
	if pandasVerifyRe.MatchString(code) {
		result.Score += 0.08
		result.Bonuses = append(result.Bonuses, "includes Pandas dual-engine verification")
	}
	if assertRe.MatchString(code) {
		result.Score += 0.05
		result.Bonuses = append(result.Bonuses, "uses coordinate assertions (M7)")
	}
	if mergedCellCheckRe.MatchString(code) {
		result.Score += 0.05
		result.Bonuses = append(result.Bonuses, "checks for merged cells")
	}

	// Clamp
	if result.Score < 0 {
		result.Score = 0
	}
	if result.Score > 1 {
		result.Score = 1
	}

	result.ShouldPrune = result.Score < 0.3
	return result
}
