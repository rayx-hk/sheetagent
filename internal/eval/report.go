package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// FailureCategory classifies why a task failed.
type FailureCategory string

const (
	FailCategoryHallucinatedTool FailureCategory = "hallucinated_tool" // LLM called non-existent tool
	FailCategoryAPIError         FailureCategory = "api_error"         // 403/500 from LLM proxy
	FailCategoryValueMismatch    FailureCategory = "value_mismatch"    // output cells don't match answer
	FailCategoryPythonError      FailureCategory = "python_error"      // Python execution failed
	FailCategoryJudgeError       FailureCategory = "judge_error"       // evaluation error
	FailCategoryAgentError       FailureCategory = "agent_error"       // other agent errors
	FailCategoryUnknown          FailureCategory = "unknown"
)

// ClassifyFailure infers a FailureCategory from an error message string.
func ClassifyFailure(errMsg string) FailureCategory {
	if errMsg == "" {
		return FailCategoryValueMismatch
	}
	s := strings.ToLower(errMsg)
	switch {
	case strings.Contains(s, "not found in toolsnode"):
		return FailCategoryHallucinatedTool
	case strings.Contains(s, "400 bad request") || strings.Contains(s, "403 forbidden") ||
		strings.Contains(s, "500 internal") || strings.Contains(s, "rate limit"):
		return FailCategoryAPIError
	case strings.Contains(s, "cells matched") || strings.Contains(s, "mismatch"):
		return FailCategoryValueMismatch
	case strings.Contains(s, "traceback") || strings.Contains(s, "exit status"):
		return FailCategoryPythonError
	case strings.Contains(s, "judge error"):
		return FailCategoryJudgeError
	case strings.Contains(s, "agent error") || strings.Contains(s, "no response from agent"):
		return FailCategoryAgentError
	default:
		return FailCategoryValueMismatch
	}
}

type BenchReport struct {
	Timestamp  time.Time                  `json:"timestamp"`
	Dataset    string                     `json:"dataset"`
	Total      int                        `json:"total"`
	Pass       int                        `json:"pass"`
	Fail       int                        `json:"fail"`
	Error      int                        `json:"error"`
	PassRate   float64                    `json:"pass_rate"`
	Duration   time.Duration              `json:"duration"`
	ByType     map[string]TypeStat        `json:"by_type,omitempty"`
	ByCategory map[string]int             `json:"by_category,omitempty"`
	Failures   []FailureDetail            `json:"failures,omitempty"`
}

type TypeStat struct {
	Total    int     `json:"total"`
	Pass     int     `json:"pass"`
	PassRate float64 `json:"pass_rate"`
}

type FailureDetail struct {
	TaskID          string          `json:"task_id"`
	InstructionType string          `json:"instruction_type,omitempty"`
	Category        FailureCategory `json:"category"`
	AttemptCount    int             `json:"attempt_count"`
	Reason          string          `json:"reason"`
	Instruction     string          `json:"instruction,omitempty"`
}

func (r *BenchReport) CalcPassRate() {
	if r.Total > 0 {
		r.PassRate = float64(r.Pass) / float64(r.Total)
	}
	for k, s := range r.ByType {
		if s.Total > 0 {
			s.PassRate = float64(s.Pass) / float64(s.Total)
			r.ByType[k] = s
		}
	}
}

func (r *BenchReport) AddResult(taskID, taskType string, pass bool, errMsg string) {
	r.AddDetailedResult(FailureDetail{
		TaskID:          taskID,
		InstructionType: taskType,
		Category:        ClassifyFailure(errMsg),
		Reason:          errMsg,
	}, pass)
}

func (r *BenchReport) AddDetailedResult(detail FailureDetail, pass bool) {
	r.Total++
	if pass {
		r.Pass++
	} else {
		if detail.Category == FailCategoryAPIError || detail.Category == FailCategoryAgentError {
			r.Error++
		} else {
			r.Fail++
		}
		r.Failures = append(r.Failures, detail)
	}

	if r.ByType == nil {
		r.ByType = make(map[string]TypeStat)
	}
	s := r.ByType[detail.InstructionType]
	s.Total++
	if pass {
		s.Pass++
	}
	r.ByType[detail.InstructionType] = s

	if !pass {
		if r.ByCategory == nil {
			r.ByCategory = make(map[string]int)
		}
		r.ByCategory[string(detail.Category)]++
	}
}

func (r *BenchReport) WriteJSON(path string) error {
	r.CalcPassRate()
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (r *BenchReport) WriteMarkdown(path string) error {
	r.CalcPassRate()
	var b strings.Builder

	fmt.Fprintf(&b, "# Benchmark Report\n\n")
	fmt.Fprintf(&b, "- **Dataset**: %s\n", r.Dataset)
	fmt.Fprintf(&b, "- **Timestamp**: %s\n", r.Timestamp.Format(time.RFC3339))
	fmt.Fprintf(&b, "- **Duration**: %s\n", r.Duration.Round(time.Second))
	fmt.Fprintf(&b, "- **Pass@1**: %.2f%% (%d/%d)\n\n", r.PassRate*100, r.Pass, r.Total)

	fmt.Fprintf(&b, "## Summary\n\n")
	fmt.Fprintf(&b, "| Metric | Count |\n")
	fmt.Fprintf(&b, "|--------|-------|\n")
	fmt.Fprintf(&b, "| Total  | %d    |\n", r.Total)
	fmt.Fprintf(&b, "| Pass   | %d    |\n", r.Pass)
	fmt.Fprintf(&b, "| Fail   | %d    |\n", r.Fail)
	fmt.Fprintf(&b, "| Error  | %d    |\n\n", r.Error)

	if len(r.ByType) > 0 {
		fmt.Fprintf(&b, "## By Type\n\n")
		fmt.Fprintf(&b, "| Type | Total | Pass | Rate |\n")
		fmt.Fprintf(&b, "|------|-------|------|------|\n")
		for t, s := range r.ByType {
			fmt.Fprintf(&b, "| %s | %d | %d | %.1f%% |\n", t, s.Total, s.Pass, s.PassRate*100)
		}
		b.WriteString("\n")
	}

	if len(r.ByCategory) > 0 {
		fmt.Fprintf(&b, "## Failure Categories\n\n")
		fmt.Fprintf(&b, "| Category | Count |\n")
		fmt.Fprintf(&b, "|----------|-------|\n")
		cats := make([]string, 0, len(r.ByCategory))
		for c := range r.ByCategory {
			cats = append(cats, c)
		}
		sort.Slice(cats, func(i, j int) bool { return r.ByCategory[cats[i]] > r.ByCategory[cats[j]] })
		for _, c := range cats {
			fmt.Fprintf(&b, "| %s | %d |\n", c, r.ByCategory[c])
		}
		b.WriteString("\n")
	}

	if len(r.Failures) > 0 {
		limit := len(r.Failures)
		if limit > 50 {
			limit = 50
		}
		fmt.Fprintf(&b, "## Failures (showing %d/%d)\n\n", limit, len(r.Failures))
		fmt.Fprintf(&b, "| Task | Type | Category | Attempts | Reason |\n")
		fmt.Fprintf(&b, "|------|------|----------|----------|--------|\n")
		for i := 0; i < limit; i++ {
			f := r.Failures[i]
			reason := f.Reason
			if len(reason) > 120 {
				reason = reason[:120] + "..."
			}
			reason = strings.ReplaceAll(reason, "|", "\\|")
			fmt.Fprintf(&b, "| %s | %s | %s | %d | %s |\n",
				f.TaskID, f.InstructionType, f.Category, f.AttemptCount, reason)
		}
	}

	return os.WriteFile(path, []byte(b.String()), 0644)
}
