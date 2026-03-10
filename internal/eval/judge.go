package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/rayx-hk/sheetagent/internal/executor"
	"github.com/rayx-hk/sheetagent/internal/sheet"
)

type JudgeResult struct {
	Pass       bool           `json:"pass"`
	Score      float64        `json:"score"`
	Total      int            `json:"total"`
	Matched    int            `json:"matched"`
	Mismatches []CellMismatch `json:"mismatches,omitempty"`
	Detail     string         `json:"detail"`
}

func (jr *JudgeResult) MismatchSummary(maxItems int) string {
	if len(jr.Mismatches) == 0 {
		return ""
	}
	limit := len(jr.Mismatches)
	if limit > maxItems {
		limit = maxItems
	}
	items := jr.Mismatches[:limit]
	b, err := json.Marshal(items)
	if err != nil {
		return jr.Detail
	}
	s := string(b)
	if limit < len(jr.Mismatches) {
		s += fmt.Sprintf(" ...and %d more", len(jr.Mismatches)-limit)
	}
	return s
}

type Judge interface {
	Evaluate(ctx context.Context, outputFile, answerFile, answerPosition string) (*JudgeResult, error)
}

type OJJudge struct {
	parser      *sheet.ExcelizeParser
	formulaEval *executor.FormulaEvaluator
}

func NewOJJudge(pythonPath string) *OJJudge {
	return &OJJudge{
		parser:      sheet.NewParser(),
		formulaEval: executor.NewFormulaEvaluator(pythonPath),
	}
}

func (j *OJJudge) Evaluate(ctx context.Context, outputFile, answerFile, answerPosition string) (*JudgeResult, error) {
	slog.Info("evaluating", "output", outputFile, "answer", answerFile, "position", answerPosition)

	// Recalculate formulas in the agent's output so cached values are readable by excelize.
	if err := j.formulaEval.Evaluate(outputFile); err != nil {
		slog.Warn("FormulaEvaluator failed on output, continuing", "err", err)
	}

	// Also recalculate the golden answer file — some benchmarks ship formulas in
	// the answer file, and excelize's built-in formula engine has known gaps
	// (YEAR, MID, TEXTJOIN, etc.). Running MS Excel / LibreOffice first ensures
	// we compare against correctly evaluated values.
	if err := j.formulaEval.Evaluate(answerFile); err != nil {
		slog.Warn("FormulaEvaluator failed on answer file, continuing", "err", err)
	}

	result := CompareFiles(ctx, j.parser, outputFile, answerFile, answerPosition)

	jr := &JudgeResult{
		Total:      result.Total,
		Matched:    result.Matched,
		Mismatches: result.Mismatches,
		Pass:       result.Match,
	}

	if result.Total > 0 {
		jr.Score = float64(result.Matched) / float64(result.Total)
	}

	if !result.Match {
		var b strings.Builder
		fmt.Fprintf(&b, "%d/%d cells matched (score=%.1f%%)", result.Matched, result.Total, jr.Score*100)
		limit := len(result.Mismatches)
		if limit > 5 {
			limit = 5
		}
		for i := 0; i < limit; i++ {
			m := result.Mismatches[i]
			fmt.Fprintf(&b, "\n  [%s] expected=%q got=%q", m.Address, m.Expected, m.Actual)
		}
		if len(result.Mismatches) > 5 {
			fmt.Fprintf(&b, "\n  ...and %d more mismatches", len(result.Mismatches)-5)
		}
		jr.Detail = b.String()
	} else {
		jr.Detail = fmt.Sprintf("all %d cells matched", result.Total)
	}

	return jr, nil
}
