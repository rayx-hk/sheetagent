package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/rayx-hk/dataagent/internal/agent"
	"github.com/rayx-hk/dataagent/internal/eval"
)

type OrchestratorConfig struct {
	MaxRetry int
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

type TaskResult struct {
	Success    bool
	Error      string
	Attempt    int
	Code       string
	AgentTrace []*schema.Message
}

func (o *Orchestrator) Run(ctx context.Context, input agent.CodeActInput, answerFile string) TaskResult {
	var lastErr string
	var fullTrace []*schema.Message

	for attempt := 0; attempt <= o.cfg.MaxRetry; attempt++ {
		slog.Info("orchestrator attempt", "attempt", attempt, "file", input.InputFile)

		input.Attempt = attempt
		if attempt > 0 {
			input.PreviousError = lastErr
			if err := recopyFile(input.InputFile); err != nil {
				slog.Warn("recopy input failed", "error", err)
			}
		}

		msg, toolOutputs, trace, err := agent.RunCodeAct(ctx, o.codeAct, input)
		fullTrace = append(fullTrace, trace...)
		
		if err != nil {
			lastErr = fmt.Sprintf("agent error: %v", err)
			slog.Warn("agent error", "attempt", attempt, "error", err)
			continue
		}

		code := extractCode(msg, toolOutputs)

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
			}
		}

		lastErr = jr.Detail
		if mismatchJSON := jr.MismatchSummary(10); mismatchJSON != "" {
			lastErr += "\nMismatch details: " + mismatchJSON
		}
		slog.Info("judge failed", "attempt", attempt, "score", fmt.Sprintf("%.1f%%", jr.Score*100),
			"matched", jr.Matched, "total", jr.Total)
	}

	return TaskResult{
		Success:    false,
		Error:      lastErr,
		Attempt:    o.cfg.MaxRetry + 1,
		AgentTrace: fullTrace,
	}
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
