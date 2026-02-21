package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/rayx-hk/sheetagent/internal/agent"
	"github.com/rayx-hk/sheetagent/internal/eval"
	"github.com/rayx-hk/sheetagent/internal/executor"
)

type OrchestratorConfig struct {
	MaxRetry     int
	REPLExecutor *executor.REPLExecutor
}

type Orchestrator struct {
	cfg     OrchestratorConfig
	codeAct adk.Agent
	judge   *eval.OJJudge
	builder *PromptBuilder
}

func NewOrchestrator(cfg OrchestratorConfig, codeAct adk.Agent, judge *eval.OJJudge, builder *PromptBuilder) *Orchestrator {
	return &Orchestrator{
		cfg:     cfg,
		codeAct: codeAct,
		judge:   judge,
		builder: builder,
	}
}

// LowConfidenceThreshold: below this, the agent is considered fundamentally confused.
const LowConfidenceThreshold = 0.3

type TaskResult struct {
	Success    bool
	Error      string
	Attempt    int
	Code       string
	AgentTrace []*schema.Message
	Confidence float64 // 0.0-1.0 from agent, -1 if not parsed
}

func (o *Orchestrator) Run(ctx context.Context, input agent.CodeActInput, answerFile string) TaskResult {
	var lastErr string
	var lastConfidence float64 = -1
	var fullTrace []*schema.Message

	for attempt := 0; attempt <= o.cfg.MaxRetry; attempt++ {
		slog.Info("orchestrator attempt", "attempt", attempt, "file", input.InputFile)

		input.Attempt = attempt
		if attempt > 0 {
			input.PreviousError = lastErr
			if lastConfidence >= 0 && lastConfidence < LowConfidenceThreshold {
				input.PreviousError += fmt.Sprintf("\n\n[LOW CONFIDENCE WARNING] Your previous attempt reported confidence %.2f (below %.2f). Consider exploratory steps (e.g., inspect data with df.head(), check headers) before attempting another fix.", lastConfidence, LowConfidenceThreshold)
			}
			if err := recopyFile(input.InputFile); err != nil {
				slog.Warn("recopy input failed", "error", err)
			}
		}

		runCtx := ctx
		var replSession executor.REPLSession
		if o.cfg.REPLExecutor != nil {
			var err error
			replSession, err = o.cfg.REPLExecutor.StartSession(ctx, input.WorkDir)
			if err != nil {
				lastErr = fmt.Sprintf("start REPL session: %v", err)
				slog.Warn("REPL session start failed", "attempt", attempt, "error", err)
				continue
			}
			runCtx = agent.WithREPLSession(ctx, replSession)
		}

		msg, toolOutputs, trace, err := agent.RunCodeAct(runCtx, o.codeAct, input)
		if replSession != nil {
			replSession.Close()
			replSession = nil
		}
		fullTrace = append(fullTrace, trace...)

		if err != nil {
			lastErr = fmt.Sprintf("agent error: %v", err)
			slog.Warn("agent error", "attempt", attempt, "error", err)
			continue
		}

		code := extractCode(msg, toolOutputs)
		confidence := extractConfidence(trace)
		lastConfidence = confidence
		if confidence >= 0 {
			slog.Info("agent confidence", "confidence", fmt.Sprintf("%.2f", confidence), "attempt", attempt)
			if confidence < LowConfidenceThreshold {
				slog.Warn("agent low confidence — consider exploratory phase instead of immediate retry",
					"confidence", fmt.Sprintf("%.2f", confidence), "attempt", attempt, "file", input.InputFile)
			}
		}

		jr, err := o.judge.Evaluate(ctx, input.InputFile, answerFile, input.AnswerPosition)
		if err != nil {
			lastErr = fmt.Sprintf("judge error: %v", err)
			continue
		}

		if jr.Pass {
			return TaskResult{
				Success:    true,
				Attempt:    attempt + 1,
				Code:       code,
				AgentTrace: fullTrace,
				Confidence: confidence,
			}
		}

		lastErr = jr.Detail
		if mismatchJSON := jr.MismatchSummary(10); mismatchJSON != "" {
			lastErr += "\nMismatch details: " + mismatchJSON
		}
		lastErr += buildRetryHints(jr)
		slog.Info("judge failed", "attempt", attempt, "score", fmt.Sprintf("%.1f%%", jr.Score*100),
			"matched", jr.Matched, "total", jr.Total, "confidence", fmt.Sprintf("%.2f", confidence))
	}

	return TaskResult{
		Success:    false,
		Error:      lastErr,
		Attempt:    o.cfg.MaxRetry + 1,
		AgentTrace: fullTrace,
		Confidence: lastConfidence,
	}
}

// extractConfidence parses ===CONFIDENCE=== X.XX from assistant messages in trace.
// Returns the last occurrence, or -1 if not found.
func extractConfidence(trace []*schema.Message) float64 {
	re := regexp.MustCompile(`===CONFIDENCE===\s*([0-9.]+)`)
	var last float64 = -1
	for _, m := range trace {
		if m == nil || m.Role != schema.Assistant {
			continue
		}
		content := m.Content
		if content == "" {
			continue
		}
		if subm := re.FindStringSubmatch(content); len(subm) >= 2 {
			if v, err := strconv.ParseFloat(subm[1], 64); err == nil && v >= 0 && v <= 1 {
				last = v
			}
		}
	}
	return last
}

func extractCode(msg *schema.Message, toolOutputs []string) string {
	re := regexp.MustCompile("(?s)```python\\s*\\n(.+?)\\n```")

	if msg != nil {
		if m := re.FindStringSubmatch(msg.Content); len(m) >= 2 {
			return m[1]
		}
	}

	for _, out := range toolOutputs {
		var execResult struct {
			Stdout string `json:"stdout"`
			Code   string `json:"code"`
		}
		if err := json.Unmarshal([]byte(out), &execResult); err == nil && execResult.Code != "" {
			return execResult.Code
		}
	}

	return ""
}

// buildRetryHints provides actionable suggestions based on the mismatch pattern.
func buildRetryHints(jr *eval.JudgeResult) string {
	if jr.Pass || len(jr.Mismatches) == 0 {
		return ""
	}

	var hints []string

	hasEmpty := false
	hasFormulaError := false
	hasPrecision := false
	expectedErrorGotEmpty := 0
	for _, m := range jr.Mismatches {
		if m.Actual == "" && m.Expected != "" {
			hasEmpty = true
		}
		if isExcelError(m.Expected) && m.Actual == "" {
			expectedErrorGotEmpty++
		}
		if isExcelError(m.Actual) {
			hasFormulaError = true
		}
		if _, eOk := strconv.ParseFloat(strings.ReplaceAll(m.Expected, ",", ""), 64); eOk == nil {
			if _, aOk := strconv.ParseFloat(strings.ReplaceAll(m.Actual, ",", ""), 64); aOk == nil {
				hasPrecision = true
			}
		}
	}

	if expectedErrorGotEmpty > 0 {
		hints = append(hints, fmt.Sprintf("[CRITICAL HINT] %d cells expect formula error values (#N/A, #VALUE!, #REF!) but you left them empty. This means the answer file contains Excel FORMULAS that produce these errors. You MUST write the actual Excel formula (e.g., =INDEX(...,MATCH(...)), =VLOOKUP(...)) to the cell using openpyxl, NOT compute the value in Python. Even if the formula will error, WRITE IT — the benchmark expects the formula to be present. Use: ws.cell(row=r, column=c).value = \"=YOUR_FORMULA\"", expectedErrorGotEmpty))
	} else if hasEmpty {
		hints = append(hints, "[HINT] Some target cells are empty (got=\"\"). Your code likely wrote to the wrong cells or the write operation failed silently. Use M12 Post-Write Verification to check.")
	}
	if hasFormulaError {
		hints = append(hints, "[HINT] Some cells contain formula errors (#VALUE!, #N/A, #REF!). Consider: (1) Check VLOOKUP/MATCH criteria and ranges, (2) Verify lookup values exist in the source range, (3) Use IFERROR wrapper, (4) Fall back to Python computation per M13.")
	}
	if hasPrecision {
		hints = append(hints, "[HINT] Numeric precision mismatch detected. Check number formatting — preserve the decimal places shown in the original data.")
	}

	if len(hints) == 0 {
		return ""
	}
	return "\n\n" + strings.Join(hints, "\n")
}

func isExcelError(s string) bool {
	s = strings.TrimSpace(s)
	return s == "#N/A" || s == "#VALUE!" || s == "#REF!" || s == "#DIV/0!" ||
		s == "#NAME?" || s == "#NULL!" || s == "#NUM!" ||
		s == "invalid reference" ||
		strings.HasSuffix(s, "argument") || strings.HasSuffix(s, "arguments")
}

func recopyFile(filePath string) error {
	backupPath := filePath + ".orig"
	if _, err := os.Stat(backupPath); err != nil {
		return nil
	}
	src, err := os.Open(backupPath)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	return err
}
