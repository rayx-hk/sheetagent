package orchestrator

import (
	"fmt"
	"strings"
)

// FormulaStrategy implements the Formula-First approach: for calculation-type
// tasks, the agent is instructed to write native Excel formulas instead of
// computing values in Python. This guarantees generalization because Excel
// formulas are inherently dynamic (they reference live cell ranges).
//
// Coverage target: 100% of calculate/sum/average/count/lookup tasks use formulas.
type FormulaStrategy struct{}

func NewFormulaStrategy() *FormulaStrategy {
	return &FormulaStrategy{}
}

// TaskClassification identifies how the task should be handled.
type TaskClassification string

const (
	ClassFormula TaskClassification = "formula" // must use Excel formula
	ClassPython  TaskClassification = "python"  // free-form Python/openpyxl
	ClassHybrid  TaskClassification = "hybrid"  // formula + Python post-processing
)

// ClassifyTask determines whether the task should use formulas or Python.
func (fs *FormulaStrategy) ClassifyTask(instructionType, instruction string) TaskClassification {
	lower := strings.ToLower(instruction)
	typeLower := strings.ToLower(instructionType)

	// Strong formula signals
	formulaKeywords := []string{
		"sum", "average", "count", "countif", "sumif", "averageif",
		"vlookup", "hlookup", "index", "match", "lookup",
		"max", "min", "median", "mode",
		"concatenate", "concat", "textjoin",
		"if(", "ifs(", "iferror",
		"round", "roundup", "rounddown",
		"calculate the total", "compute the sum",
		"find the average", "count the number",
	}
	for _, kw := range formulaKeywords {
		if strings.Contains(lower, kw) || strings.Contains(typeLower, kw) {
			return ClassFormula
		}
	}

	// Calculation-type instruction types
	calcTypes := []string{"calculate", "compute", "formula", "aggregate", "total"}
	for _, ct := range calcTypes {
		if strings.Contains(typeLower, ct) {
			return ClassFormula
		}
	}

	// Strong Python signals (structural manipulation, not calculation)
	pythonKeywords := []string{
		"delete", "remove", "sort", "reorder", "rearrange",
		"copy", "move", "merge", "split", "insert row",
		"format", "highlight", "color", "bold", "italic",
		"chart", "graph", "pivot",
	}
	for _, kw := range pythonKeywords {
		if strings.Contains(lower, kw) {
			return ClassPython
		}
	}

	// Hybrid: extraction + calculation
	if (strings.Contains(lower, "find") || strings.Contains(lower, "extract")) &&
		(strings.Contains(lower, "calculate") || strings.Contains(lower, "sum")) {
		return ClassHybrid
	}

	return ClassPython
}

// FormulaPromptInjection returns additional prompt text when Formula-First applies.
func (fs *FormulaStrategy) FormulaPromptInjection(classification TaskClassification, answerPosition string) string {
	switch classification {
	case ClassFormula:
		return fmt.Sprintf(`
=== FORMULA-FIRST MANDATORY DIRECTIVE ===
This task is classified as FORMULA-type. You MUST write native Excel formulas.

RULES:
1. NEVER compute values in Python and write them as constants.
2. ALWAYS use ws.cell(row=r, column=c).value = "=FORMULA(...)" to write formulas.
3. Use dynamic references: entire column ranges (e.g. A:A), INDIRECT(), OFFSET().
4. For VLOOKUP/INDEX-MATCH: reference the full data table, not hardcoded subsets.
5. If the formula would produce #N/A or #VALUE!, that is CORRECT — write it anyway.
   The benchmark expects the formula, not a Python-computed workaround.
6. Target answer position: %s

PREFERRED FORMULA PATTERNS:
- Total/Sum: =SUM(B2:B100) or =SUMIF(A:A,"criteria",B:B)
- Average:   =AVERAGE(B2:B100) or =AVERAGEIF(A:A,"criteria",B:B)
- Count:     =COUNTIF(A:A,"criteria") or =COUNTA(A2:A100)
- Lookup:    =INDEX(B:B,MATCH("key",A:A,0)) or =VLOOKUP("key",A:C,2,FALSE)
- Conditional: =IF(A2>100,"High","Low")
=== END FORMULA-FIRST DIRECTIVE ===
`, answerPosition)

	case ClassHybrid:
		return fmt.Sprintf(`
=== HYBRID STRATEGY DIRECTIVE ===
This task requires both data extraction and calculation.

APPROACH:
1. Use Python/openpyxl to locate and extract relevant data ranges.
2. For the final answer cells at %s, prefer Excel formulas over Python constants.
3. If a formula is impractical (e.g. complex multi-step logic), compute in Python
   but verify the result with a sanity check.
=== END HYBRID STRATEGY ===
`, answerPosition)

	default:
		return ""
	}
}

// EnhanceInput augments the CodeActInput with formula strategy if applicable.
func (fs *FormulaStrategy) EnhanceInput(instruction, instructionType, answerPosition string) string {
	classification := fs.ClassifyTask(instructionType, instruction)
	return fs.FormulaPromptInjection(classification, answerPosition)
}
