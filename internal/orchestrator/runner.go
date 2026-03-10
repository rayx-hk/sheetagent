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
	eimodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/rayx-hk/sheetagent/internal/agent"
	"github.com/rayx-hk/sheetagent/internal/eval"
	"github.com/rayx-hk/sheetagent/internal/executor"
)

type OrchestratorConfig struct {
	MaxRetry       int
	REPLExecutor   *executor.REPLExecutor
	PythonPath     string
	UseDualEngine  bool
	ReviewModel    eimodel.ToolCallingChatModel // optional: LLM for code review
}

type Orchestrator struct {
	cfg             OrchestratorConfig
	codeAct         adk.Agent
	judge           *eval.OJJudge
	selfChecker     *eval.SelfChecker
	codeReviewer    *eval.CodeReviewer
	dualEngine      *eval.DualEngineValidator
	formulaStrategy *FormulaStrategy
	builder         *PromptBuilder
}

func NewOrchestrator(cfg OrchestratorConfig, codeAct adk.Agent, judge *eval.OJJudge, builder *PromptBuilder) *Orchestrator {
	o := &Orchestrator{
		cfg:             cfg,
		codeAct:         codeAct,
		judge:           judge,
		selfChecker:     eval.NewSelfChecker(cfg.PythonPath),
		codeReviewer:    eval.NewCodeReviewer(cfg.ReviewModel),
		formulaStrategy: NewFormulaStrategy(),
		builder:         builder,
	}
	if cfg.UseDualEngine {
		o.dualEngine = eval.NewDualEngineValidator(cfg.PythonPath)
	}
	return o
}

// LowConfidenceThreshold: below this, the agent is considered fundamentally confused.
const LowConfidenceThreshold = 0.3

// MaxTransientRetries caps extra retries for transient API errors (500, timeout)
// so they don't consume the normal retry budget.
const MaxTransientRetries = 3

type TaskResult struct {
	Success    bool
	Error      string
	Attempt    int
	Code       string
	AgentTrace []*schema.Message
	Confidence float64 // 0.0-1.0 from agent, -1 if not parsed

	TokensIn  int64
	TokensOut int64
	LLMCalls  int

	// Phase-level durations for performance profiling.
	PhaseTimings map[string]float64 // phase name → seconds
}

// Run executes the agent with formula-aware self-checking in the retry loop.
// The golden answerFile is ONLY used for final scoring after the loop exits,
// never leaked to the agent as feedback.
func (o *Orchestrator) Run(ctx context.Context, input agent.CodeActInput, answerFile string) TaskResult {
	return o.RunWithMaxRetry(ctx, input, answerFile, o.cfg.MaxRetry)
}

// RunWithMaxRetry is like Run but allows the caller (Engine) to override MaxRetry
// based on per-task SOP decisions (e.g. easy=2, medium=3, hard=5).
func (o *Orchestrator) RunWithMaxRetry(ctx context.Context, input agent.CodeActInput, answerFile string, maxRetry int) TaskResult {
	if maxRetry <= 0 {
		maxRetry = o.cfg.MaxRetry
	}

	var lastErr string
	var lastCode string
	var lastConfidence float64 = -1
	var fullTrace []*schema.Message
	var selfCheckPassed bool
	var passedAtAttempt int = -1 // tracks which attempt self-check first passed
	transientRetries := 0

	for attempt := 0; attempt <= maxRetry; attempt++ {
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
			errStr := err.Error()
			if isTransientAPIError(errStr) && transientRetries < MaxTransientRetries {
				transientRetries++
				attempt-- // don't consume retry budget for transient errors
				slog.Warn("transient API error, retrying without budget cost",
					"attempt", attempt, "transient_retry", transientRetries, "error", errStr)
				continue
			}
			lastErr = fmt.Sprintf("agent error: %v", err)
			slog.Warn("agent error", "attempt", attempt, "error", err)
			continue
		}

		code := extractCode(msg, toolOutputs, trace)
		lastCode = code
		confidence := extractConfidence(trace)
		lastConfidence = confidence
		if confidence >= 0 {
			slog.Info("agent confidence", "confidence", fmt.Sprintf("%.2f", confidence), "attempt", attempt)
		}

		// Extract Agent's own M12 self-verification output from trace
		agentVerifyOutput := extractAgentVerification(trace)

		// Self-check with tiered options:
		// - ForceCalculate is called inside when formulas are detected
		// - SyntheticValidator is NOT run during retries (only final verification)
		// - Attempt number controls feedback verbosity
		scr, err := o.selfChecker.CheckWithOpts(ctx, input.InputFile, input.AnswerPosition,
			code, input.InputFile+".orig", input.WorkDir, eval.CheckOpts{
				Attempt:         attempt,
				RunSynthetic:    false, // never run synthetic in retry loop
				AgentSelfVerify: agentVerifyOutput,
			})
		if err != nil {
			lastErr = fmt.Sprintf("self-check error: %v", err)
			slog.Warn("self-check error", "attempt", attempt, "error", err)
			continue
		}

		if scr.Passed {
			passedAtAttempt = attempt
			// Dual-engine cross-validation for formula-type tasks
			if o.dualEngine != nil && o.isFormulaTask(input) {
				pathBCode := eval.ExtractPathBCode(extractAllTraceContent(trace))
				if pathBCode != "" {
					der, dualErr := o.dualEngine.Validate(ctx, input.InputFile, input.AnswerPosition,
						pathBCode, input.InputFile+".orig", input.WorkDir)
					if dualErr != nil {
						slog.Warn("dual-engine validation error", "error", dualErr)
					} else if !der.Passed {
						slog.Info("dual-engine cross-check FAILED", "attempt", attempt,
							"mismatches", len(der.Mismatches))
						lastErr = der.Feedback
						continue
					} else {
						slog.Info("dual-engine cross-check PASSED", "attempt", attempt)
					}
				}
			}

			// Code reviewer: catch logic errors self-checker can't detect.
			// Run when retries remain so the agent can fix reviewer-identified issues.
			if o.codeReviewer != nil && attempt < o.cfg.MaxRetry {
				outputSample := o.codeReviewer.ReadOutputSample(ctx, input.InputFile, input.AnswerPosition, 20)
				rr, reviewErr := o.codeReviewer.Review(ctx, input.Instruction, code, outputSample, input.AnswerPosition)
				if reviewErr != nil {
					slog.Warn("code-reviewer error", "error", reviewErr)
				} else if rr.HasIssues {
					slog.Info("code-reviewer found issues, retrying", "attempt", attempt,
						"issues", len(rr.Issues))
					lastErr = rr.Feedback
					continue
				}
				slog.Info("code-reviewer: no issues found", "attempt", attempt)
			}

			selfCheckPassed = true
			slog.Info("self-check passed", "attempt", attempt,
				"formulas", len(scr.FormulaCells), "values", scr.ValueCells,
				"agent_verified", scr.AgentVerified,
				"confidence", fmt.Sprintf("%.2f", confidence))
			break
		}

		// Self-check failed: build tiered feedback.
		// On attempt 2+, supplement with code reviewer insight if available.
		feedback := scr.Summary

		// Formula-Fallback: when formula errors dominate, nudge toward Python
		if len(scr.ErrorCells) > 0 && !scr.FormulaEvalOK && len(scr.FormulaCells) > 0 {
			feedback += "\n\n[FORMULA FALLBACK] Formula evaluator failed. Switch to Python: compute values and write them directly."
		}

		if o.codeReviewer != nil && attempt >= 1 && code != "" {
			outputSample := o.codeReviewer.ReadOutputSample(ctx, input.InputFile, input.AnswerPosition, 15)
			rr, reviewErr := o.codeReviewer.Review(ctx, input.Instruction, code, outputSample, input.AnswerPosition)
			if reviewErr != nil {
				slog.Warn("code-reviewer error on failed self-check", "error", reviewErr)
			} else if rr.HasIssues {
				slog.Info("code-reviewer supplemented self-check feedback",
					"attempt", attempt, "issues", len(rr.Issues))
				feedback += "\n\n" + rr.Feedback
			}
		}
		lastErr = feedback
		slog.Info("self-check failed", "attempt", attempt,
			"empty_cells", len(scr.EmptyCells),
			"formula_cells", len(scr.FormulaCells),
			"value_cells", scr.ValueCells,
			"agent_verified", scr.AgentVerified,
			"confidence", fmt.Sprintf("%.2f", confidence))
	}

	// Final scoring: OJ Judge with golden answer (only for reporting, never fed back).
	// Always run regardless of self-check result or code extraction — the agent may
	// have written correct values via tool calls even when extractCode returns empty.
	actualAttempt := maxRetry + 1
	if passedAtAttempt >= 0 {
		actualAttempt = passedAtAttempt + 1
	}
	finalResult := TaskResult{
		Attempt:    actualAttempt,
		Code:       lastCode,
		AgentTrace: fullTrace,
		Confidence: lastConfidence,
	}

	jr, err := o.judge.Evaluate(ctx, input.InputFile, answerFile, input.AnswerPosition)
	if err != nil {
		finalResult.Error = fmt.Sprintf("final judge error: %v", err)
		return finalResult
	}
	if jr.Pass {
		finalResult.Success = true
		finalResult.Error = jr.Detail
		return finalResult
	}
	finalResult.Error = jr.Detail
	if !selfCheckPassed && lastErr != "" {
		finalResult.Error += "\n\n[Self-Check] " + lastErr
	}

	return finalResult
}

// extractAgentVerification finds the Agent's M12 Post-Write Verification output
// from the trace messages. Looks for "VERIFIED:" or "WARNING:" patterns in tool outputs.
func extractAgentVerification(trace []*schema.Message) string {
	for i := len(trace) - 1; i >= 0; i-- {
		m := trace[i]
		if m == nil || m.Role != schema.Tool {
			continue
		}
		content := m.Content
		if strings.Contains(content, "VERIFIED:") || strings.Contains(content, "WARNING:") {
			if strings.Contains(content, "target cell") || strings.Contains(content, "empty") {
				return content
			}
		}
	}
	return ""
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

func extractCode(msg *schema.Message, toolOutputs []string, trace []*schema.Message) string {
	re := regexp.MustCompile("(?s)```python\\s*\\n(.+?)\\n```")

	// 1. Check last assistant message for ```python blocks
	if msg != nil {
		if m := re.FindStringSubmatch(msg.Content); len(m) >= 2 {
			return m[1]
		}
	}

	// 2. Check tool outputs for JSON with "code" field
	for _, out := range toolOutputs {
		var execResult struct {
			Stdout string `json:"stdout"`
			Code   string `json:"code"`
		}
		if err := json.Unmarshal([]byte(out), &execResult); err == nil && execResult.Code != "" {
			return execResult.Code
		}
	}

	// 3. Extract from tool_call arguments in the last assistant message
	// (handles cases where the last message IS a python_runner call)
	if msg != nil {
		for _, tc := range msg.ToolCalls {
			if tc.Function.Name == "python_runner" && tc.Function.Arguments != "" {
				var args struct {
					Code string `json:"code"`
				}
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err == nil && args.Code != "" {
					return args.Code
				}
			}
		}
	}

	// 4. Walk the trace in reverse to find the last python_runner tool call.
	// This is the primary path for claude-type models where the agent uses
	// ExitTool as the final message and never echoes code in a text block.
	for i := len(trace) - 1; i >= 0; i-- {
		m := trace[i]
		if m.Role != schema.Assistant {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.Function.Name == "python_runner" && tc.Function.Arguments != "" {
				var args struct {
					Code string `json:"code"`
				}
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err == nil && args.Code != "" {
					return args.Code
				}
			}
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

// isFormulaTask checks if the current task is formula-type via FormulaStrategy.
func (o *Orchestrator) isFormulaTask(input agent.CodeActInput) bool {
	cls := o.formulaStrategy.ClassifyTask(input.InstructionType, input.Instruction)
	return cls == ClassFormula || cls == ClassHybrid
}

// extractAllTraceContent concatenates all assistant message content from a trace.
func extractAllTraceContent(trace []*schema.Message) string {
	var sb strings.Builder
	for _, m := range trace {
		if m == nil {
			continue
		}
		sb.WriteString(m.Content)
		sb.WriteString("\n")
	}
	return sb.String()
}

// isTransientAPIError detects temporary API failures (500, 502, 503, 429, timeouts)
// that should be retried without consuming the agent retry budget.
func isTransientAPIError(errStr string) bool {
	transientPatterns := []string{
		"500 Internal Server Error",
		"502 Bad Gateway",
		"503 Service Unavailable",
		"429 Too Many Requests",
		"connection reset",
		"context deadline exceeded",
		"EOF",
		"broken pipe",
		"stream error",
		"TLS handshake timeout",
	}
	lower := strings.ToLower(errStr)
	for _, p := range transientPatterns {
		if strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
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
