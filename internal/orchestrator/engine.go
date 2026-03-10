// engine.go implements the top-level Engine with lazy MCTS escalation:
//
//	Input → AdaptiveSOP.Decide() → PromptAdditions injected
//	      → Phase 1: Single linear attempt (succeeds ~60%)
//	      → Phase 2: MCTS only if linear failed AND SOP flagged hard
//	      → Phase 3: Near-miss repair if close to passing
//
// This "linear-first" strategy avoids the full MCTS cost for easy/medium tasks
// while still using MCTS for genuinely hard tasks that need it.
package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	eimodel "github.com/cloudwego/eino/components/model"

	"github.com/rayx-hk/sheetagent/internal/agent"
	"github.com/rayx-hk/sheetagent/internal/eval"
	"github.com/rayx-hk/sheetagent/internal/executor"
	"github.com/rayx-hk/sheetagent/internal/memory"
	"github.com/rayx-hk/sheetagent/internal/model"
	"github.com/rayx-hk/sheetagent/internal/rag"
	"github.com/rayx-hk/sheetagent/internal/skills"
)

// EngineConfig controls the Engine's behavior.
type EngineConfig struct {
	MaxRetry      int
	REPLExecutor  *executor.REPLExecutor
	PythonPath    string
	ReviewModel   eimodel.ToolCallingChatModel
	UseDualEngine bool
	UseMCTS       bool // globally enable/disable MCTS routing
	MCTSConfig    MCTSConfig
}

// Engine is the top-level orchestration layer. It uses AdaptiveSOP to decide
// per-task strategy and routes execution to Orchestrator or MCTSRunner.
type Engine struct {
	cfg          EngineConfig
	sop          *AdaptiveSOP
	orchestrator *Orchestrator
	mctsRunner   *MCTSRunner
	builder      *PromptBuilder
	failureStore *memory.FailureStore
}

// NewEngine creates the Engine with all components wired together.
// Pass nil for optional components (skillReg, fewShot, failStore) for graceful degradation.
func NewEngine(
	cfg EngineConfig,
	codeAct adk.Agent,
	judge *eval.OJJudge,
	builder *PromptBuilder,
	skillReg *skills.Registry,
	fewShot *rag.FewShotStore,
	failStore *memory.FailureStore,
) *Engine {
	e := &Engine{
		cfg:     cfg,
		builder: builder,
		sop:     NewAdaptiveSOP(skillReg, fewShot, failStore),
		failureStore: failStore,
	}

	// Linear retry orchestrator (always available)
	e.orchestrator = NewOrchestrator(OrchestratorConfig{
		MaxRetry:      cfg.MaxRetry,
		REPLExecutor:  cfg.REPLExecutor,
		PythonPath:    cfg.PythonPath,
		UseDualEngine: cfg.UseDualEngine,
		ReviewModel:   cfg.ReviewModel,
	}, codeAct, judge, builder)

	// MCTS runner (created only if globally enabled)
	if cfg.UseMCTS {
		mctsAgents := []adk.Agent{codeAct}
		mctsCfg := cfg.MCTSConfig
		if mctsCfg.DefaultN == 0 {
			mctsCfg = DefaultMCTSConfig()
		}
		e.mctsRunner = NewMCTSRunner(
			mctsCfg,
			mctsAgents,
			judge,
			builder,
			cfg.REPLExecutor,
			cfg.PythonPath,
			cfg.UseDualEngine,
		)
	}

	return e
}

// Run executes a single task through the Engine pipeline:
// 1. SOP decides strategy (difficulty, formula type, prompt additions)
// 2. PromptAdditions injected into input
// 3. Always try linear first (1 attempt) — succeeds ~60% of the time at near-zero marginal cost
// 4. Only escalate to MCTS if linear fails AND SOP decided MCTS is warranted
// 5. Near-miss self-repair as final fallback
func (e *Engine) Run(ctx context.Context, input agent.CodeActInput, answerFile string) TaskResult {
	timings := make(map[string]float64)

	// Inject per-task token stats into context for tracking.
	ctx, taskTokens := model.WithTaskStats(ctx)

	sopStart := time.Now()
	decision := e.sop.Decide(
		input.Instruction,
		input.InstructionType,
		input.AnswerPosition,
		nil, // sheet features (extracted at parse time)
	)
	timings["sop"] = time.Since(sopStart).Seconds()

	if decision.PromptAdditions != "" {
		input.PromptAdditions = decision.PromptAdditions
	}

	// Phase 1: Always try a single linear attempt first. Most tasks (~60%)
	// succeed on the first try, avoiding the full MCTS cost entirely.
	slog.Info("engine: linear first-attempt",
		"difficulty", decision.Difficulty,
		"classification", decision.Classification)
	p1Start := time.Now()
	result := e.orchestrator.RunWithMaxRetry(ctx, input, answerFile, 1)
	timings["phase1_linear"] = time.Since(p1Start).Seconds()

	// Phase 2: If first attempt failed and SOP wants MCTS, escalate with
	// the first-attempt error as prior knowledge for better candidates.
	if !result.Success && ctx.Err() == nil && decision.UseMCTS && e.mctsRunner != nil {
		slog.Info("engine: escalating to MCTS after linear failure",
			"difficulty", decision.Difficulty,
			"candidates", decision.MCTSCandidates,
			"first_error", truncateStr(result.Error, 200))
		p2Start := time.Now()
		mctsInput := input
		mctsInput.PreviousError = result.Error
		mctsInput.Attempt = result.Attempt
		result = e.mctsRunner.Run(ctx, mctsInput, answerFile, decision.MCTSCandidates)
		timings["phase2_mcts"] = time.Since(p2Start).Seconds()
	} else if !result.Success && ctx.Err() == nil && !decision.UseMCTS {
		// Non-MCTS path: use remaining linear retries
		if decision.MaxRetry > 1 {
			slog.Info("engine: continuing linear retries",
				"remaining_retries", decision.MaxRetry-1)
			p2Start := time.Now()
			retryInput := input
			retryInput.PreviousError = result.Error
			retryInput.Attempt = 1
			result = e.orchestrator.RunWithMaxRetry(ctx, retryInput, answerFile, decision.MaxRetry-1)
			timings["phase2_retry"] = time.Since(p2Start).Seconds()
		}
	}

	// Phase 3: Near-miss self-repair
	if !result.Success && ctx.Err() == nil {
		p3Start := time.Now()
		result = e.tryNearMissRepair(ctx, input, answerFile, result)
		timings["phase3_repair"] = time.Since(p3Start).Seconds()
	}

	// Attach phase timings and token stats to result.
	result.PhaseTimings = timings
	if taskTokens != nil {
		result.TokensIn = taskTokens.PromptTokens
		result.TokensOut = taskTokens.CompletionTokens
		result.LLMCalls = int(taskTokens.Calls)
	}

	// Record failure for future warnings
	if !result.Success && e.failureStore != nil {
		e.failureStore.Record(memory.FailureRecord{
			InstructionType: input.InstructionType,
			Instruction:     input.Instruction,
			AnswerPosition:  input.AnswerPosition,
			ErrorSummary:    result.Error,
			CodeSnippet:     extractKeyLines(result.Code, 300),
			AttemptCount:    result.Attempt,
		})
	}

	return result
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// nearMissScoreThreshold: minimum score to trigger self-repair attempt.
const nearMissScoreThreshold = 0.5

// tryNearMissRepair evaluates the current output, and if score >= 50%,
// gives the agent one extra shot with blind feedback (mismatch count +
// cell addresses, but NO expected values).
func (e *Engine) tryNearMissRepair(ctx context.Context, input agent.CodeActInput, answerFile string, prevResult TaskResult) TaskResult {
	judge := eval.NewOJJudge(e.cfg.PythonPath)
	jr, err := judge.Evaluate(ctx, input.InputFile, answerFile, input.AnswerPosition)
	if err != nil || jr.Pass {
		if jr != nil && jr.Pass {
			prevResult.Success = true
		}
		return prevResult
	}

	if jr.Score < nearMissScoreThreshold {
		return prevResult
	}

	slog.Info("near-miss detected, attempting self-repair",
		"score", jr.Score, "matched", jr.Matched, "total", jr.Total,
		"mismatches", len(jr.Mismatches))

	feedback := buildNearMissFeedback(jr)
	repairInput := input
	repairInput.Attempt = prevResult.Attempt + 1
	repairInput.PreviousError = feedback

	if err := recopyFile(input.InputFile); err != nil {
		slog.Warn("recopy for near-miss repair failed", "error", err)
	}

	repairResult := e.orchestrator.RunWithMaxRetry(ctx, repairInput, answerFile, 1)
	if repairResult.Success {
		slog.Info("near-miss self-repair SUCCEEDED", "prev_score", jr.Score)
		return repairResult
	}

	jr2, err2 := judge.Evaluate(ctx, input.InputFile, answerFile, input.AnswerPosition)
	if err2 == nil && jr2.Score > jr.Score {
		slog.Info("near-miss repair improved score", "before", jr.Score, "after", jr2.Score)
		repairResult.Error = jr2.Detail
		return repairResult
	}

	return prevResult
}

// buildNearMissFeedback creates blind feedback for near-miss tasks:
// includes mismatch cell addresses and actual values, but NOT expected values.
func buildNearMissFeedback(jr *eval.JudgeResult) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(
		"[NEAR-MISS REPAIR] Your code matched %d/%d cells (%.0f%%). "+
			"Only %d cells need fixing. Focus on these:\n\n",
		jr.Matched, jr.Total, jr.Score*100, len(jr.Mismatches)))

	limit := len(jr.Mismatches)
	if limit > 15 {
		limit = 15
	}
	for i := 0; i < limit; i++ {
		m := jr.Mismatches[i]
		if m.Actual == "" {
			sb.WriteString(fmt.Sprintf("  - %s: EMPTY (your code did not write to this cell)\n", m.Address))
		} else {
			sb.WriteString(fmt.Sprintf("  - %s: your value=%q (WRONG — re-examine logic)\n", m.Address, m.Actual))
		}
	}
	if len(jr.Mismatches) > limit {
		sb.WriteString(fmt.Sprintf("  ...and %d more\n", len(jr.Mismatches)-limit))
	}

	sb.WriteString("\nDo NOT change cells that are already correct. Only fix the listed cells.")
	return sb.String()
}

// GetFailureStore returns the failure store for persistence at shutdown.
func (e *Engine) GetFailureStore() *memory.FailureStore {
	return e.failureStore
}

func extractKeyLines(code string, maxLen int) string {
	if len(code) <= maxLen {
		return code
	}
	return code[:maxLen]
}
