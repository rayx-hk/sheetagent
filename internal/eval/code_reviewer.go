package eval

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	eimodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/rayx-hk/sheetagent/internal/sheet"
)

// CodeReviewer uses an LLM to review agent code for logical errors without
// access to the golden answer. This is pure code review, not answer leakage.
type CodeReviewer struct {
	model  eimodel.ToolCallingChatModel
	parser *sheet.ExcelizeParser
}

type ReviewResult struct {
	HasIssues bool
	Issues    []string
	Feedback  string
}

func NewCodeReviewer(m eimodel.ToolCallingChatModel) *CodeReviewer {
	if m == nil {
		return nil
	}
	return &CodeReviewer{
		model:  m,
		parser: sheet.NewParser(),
	}
}

const reviewSystemPrompt = `You are a code reviewer for spreadsheet manipulation tasks.
You review Python/openpyxl code that modifies Excel files.

You will receive:
1. The original task instruction
2. The Python code written by an agent
3. A sample of output cell values from the target region (may include formula errors like #REF!, #VALUE!)

## Review Checklist (ordered by frequency of causing failures):

### A. Near-Miss Patterns (tasks scoring 90%+ but not 100%)
- NUMBER PRECISION: Is the output rounding/truncating correctly?
  * "Round to 2 decimal places" → check ROUND() or round() usage
  * Percentages: should output be 0.15 or 15%? Check if instruction says "as percentage"
  * Currency: does output include/exclude currency symbols as required?
  * Large numbers: check for thousands separator handling (1,000 vs 1000)
- DATE FORMAT: Does the output date format match what's expected?
  * "MM/DD/YYYY" vs "DD/MM/YYYY" vs "YYYY-MM-DD" — check instruction wording
  * Date vs datetime: should time portion be included?
- STRING FORMATTING:
  * Leading/trailing spaces: is strip() applied where needed?
  * Case sensitivity: should output be UPPER, lower, or Title Case?
  * "Remove last N chars" vs "Keep first N chars" — verify slicing direction

### B. Logic Errors
- Sort direction (ascending vs descending) — check instruction wording EXACTLY
- Aggregation scope: does the SUM/AVERAGE/COUNT cover ALL relevant rows? Off-by-one?
- Filter criteria: does the WHERE/IF condition match instruction EXACTLY?
  * "greater than" vs "greater than or equal to"
  * "contains" vs "equals" vs "starts with"
- VLOOKUP/INDEX-MATCH: is approximate_match set correctly (TRUE vs FALSE)?
- Conditional logic: are nested conditions in correct order?

### C. Structural Errors
- Empty cells in target: code wrote to wrong row/column offset
- Data type coercion: string "123" vs number 123
- Header row inclusion/exclusion: is the header counted as data?
- Multi-sheet: did code write to the correct sheet?
- Formula #REF!/#NAME?/#VALUE!: formula has syntax or reference bugs

### D. Edge Cases
- Division by zero: is there protection (IFERROR, try/except)?
- Missing/NA values in source data: how are they handled?
- Duplicate values: does the logic handle ties correctly?
- Empty source ranges: what happens if no data matches the filter?

RULES:
- You do NOT have access to the expected answer. This is blind code review.
- Only flag issues you are CONFIDENT about from the instruction and code logic.
- Be specific: state which operation is wrong and WHY, with the exact code line or formula.
- Pay special attention to near-miss patterns (section A) — these cause the most failures.
- If the code looks correct, respond with exactly: NO_ISSUES_FOUND

Output format when issues exist:
ISSUES:
1. <specific issue description>
2. <specific issue description>`

// Review checks agent code for logical errors using LLM-based code review.
// It never sees the golden answer — only instruction, code, and output sample.
func (cr *CodeReviewer) Review(ctx context.Context, instruction, code, outputSample, answerPosition string) (*ReviewResult, error) {
	if cr == nil || cr.model == nil || code == "" {
		return &ReviewResult{HasIssues: false}, nil
	}

	userPrompt := fmt.Sprintf("Task Instruction:\n%s\n\nAnswer Position: %s\n\nPython Code:\n```python\n%s\n```\n\nOutput Sample (target region values):\n%s\n\nReview this code for logical errors against the instruction.",
		instruction, answerPosition, truncateCode(code, 3000), outputSample)

	messages := []*schema.Message{
		schema.SystemMessage(reviewSystemPrompt),
		schema.UserMessage(userPrompt),
	}

	resp, err := cr.model.Generate(ctx, messages)
	if err != nil {
		slog.Warn("code-reviewer: LLM call failed", "error", err)
		return &ReviewResult{HasIssues: false, Feedback: "reviewer unavailable: " + err.Error()}, nil
	}

	return parseReviewResponse(resp.Content), nil
}

// ReadOutputSample reads the first N cell values and any formula content from
// the answer_position region to provide context for the code reviewer.
func (cr *CodeReviewer) ReadOutputSample(ctx context.Context, outputFile, answerPosition string, maxCells int) string {
	if cr == nil {
		return ""
	}
	sheets, err := cr.parser.Parse(ctx, outputFile)
	if err != nil {
		return "(could not read output file)"
	}

	sheetName, cells := parseCellRange(answerPosition)
	filteredSheets := sheets
	if sheetName != "" {
		for _, s := range sheets {
			if strings.EqualFold(s.Name, sheetName) {
				filteredSheets = []sheet.SheetData{s}
				break
			}
		}
	}

	cellMap := buildCellMap(filteredSheets)

	// Build formula map for additional context
	formulaMap := make(map[string]string)
	for _, sd := range filteredSheets {
		for _, f := range sd.Formulas {
			addr := cellAddr(f.Row, f.Col)
			formulaMap[strings.ToUpper(addr)] = f.Value
		}
	}

	var sb strings.Builder
	shown := 0
	emptyCount := 0
	errorCount := 0
	for _, addr := range cells {
		if shown >= maxCells {
			sb.WriteString(fmt.Sprintf("... (%d more cells)\n", len(cells)-shown))
			break
		}
		val := cellMap[addr]
		formula := formulaMap[strings.ToUpper(addr)]

		if val == "" {
			emptyCount++
			if formula != "" {
				sb.WriteString(fmt.Sprintf("  %s = (empty) [formula: =%s]\n", addr, truncateString(formula, 60)))
			} else {
				sb.WriteString(fmt.Sprintf("  %s = (empty)\n", addr))
			}
		} else if strings.HasPrefix(strings.TrimSpace(val), "#") || strings.Contains(val, "requires") {
			errorCount++
			if formula != "" {
				sb.WriteString(fmt.Sprintf("  %s = %s [formula: =%s]\n", addr, val, truncateString(formula, 60)))
			} else {
				sb.WriteString(fmt.Sprintf("  %s = %s\n", addr, truncateString(val, 80)))
			}
		} else {
			sb.WriteString(fmt.Sprintf("  %s = %s\n", addr, truncateString(val, 80)))
		}
		shown++
	}

	if emptyCount > 0 || errorCount > 0 {
		sb.WriteString(fmt.Sprintf("\nSummary: %d empty, %d error values, %d total cells\n",
			emptyCount, errorCount, len(cells)))
	}
	return sb.String()
}

func parseReviewResponse(content string) *ReviewResult {
	content = strings.TrimSpace(content)

	if strings.Contains(content, "NO_ISSUES_FOUND") {
		return &ReviewResult{HasIssues: false}
	}

	result := &ReviewResult{HasIssues: true}

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "ISSUES:") {
			continue
		}
		result.Issues = append(result.Issues, line)
	}

	if len(result.Issues) == 0 {
		return &ReviewResult{HasIssues: false}
	}

	var sb strings.Builder
	sb.WriteString("[CODE REVIEW] Potential issues found in your code:\n")
	limit := len(result.Issues)
	if limit > 5 {
		limit = 5
	}
	for _, issue := range result.Issues[:limit] {
		sb.WriteString(fmt.Sprintf("  %s\n", issue))
	}
	if len(result.Issues) > limit {
		sb.WriteString(fmt.Sprintf("  ...and %d more\n", len(result.Issues)-limit))
	}
	sb.WriteString("\nFix these issues and regenerate the COMPLETE corrected code.")
	result.Feedback = sb.String()

	return result
}

func truncateCode(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n# ... (truncated)"
}
