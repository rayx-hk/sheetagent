package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/rayx-hk/sheetagent/internal/agent"
	"github.com/rayx-hk/sheetagent/internal/eval"
	"github.com/rayx-hk/sheetagent/internal/executor"
)

// MCTSConfig controls the MCTS search behavior.
type MCTSConfig struct {
	DefaultN       int     // default candidate paths for normal tasks
	HardTaskN      int     // candidate paths for hard tasks
	Temperature    float64 // sampling temperature (set via model config)
	MaxDepth       int     // max retry rounds (tree depth)
	PruneThreshold float64 // StepCritic score below this triggers pruning
}

// DefaultMCTSConfig returns sensible defaults for MCTS.
func DefaultMCTSConfig() MCTSConfig {
	return MCTSConfig{
		DefaultN:       2,
		HardTaskN:      2,
		Temperature:    0.7,
		MaxDepth:       2,
		PruneThreshold: 0.3,
	}
}

// MCTSCandidate holds one code generation path and its evaluation.
type MCTSCandidate struct {
	Code            string
	CriticScore     float64
	SelfCheck       *eval.SelfCheckResult
	DualEngineCheck *eval.DualEngineResult
	Confidence      float64
	Model           string // which model generated this
	Pruned          bool
	Trace           []*schema.Message
}

// MCTSResult is the output of an MCTS search.
type MCTSResult struct {
	BestCandidate *MCTSCandidate
	AllCandidates []MCTSCandidate
	TotalGenerated int
	TotalPruned    int
	TotalPassed    int
	Depth          int // how many MCTS rounds were needed
}

// MCTSRunner implements the MCTS + PRM search tree for code generation.
type MCTSRunner struct {
	config      MCTSConfig
	agents      []adk.Agent // multiple agents for model diversity
	critic      *StepCritic
	selfChecker *eval.SelfChecker
	dualEngine  *eval.DualEngineValidator
	judge       *eval.OJJudge
	builder     *PromptBuilder
	replExec    *executor.REPLExecutor
	formulaStrat *FormulaStrategy
}

// NewMCTSRunner creates an MCTS runner with one or more agents.
func NewMCTSRunner(
	config MCTSConfig,
	agents []adk.Agent,
	judge *eval.OJJudge,
	builder *PromptBuilder,
	replExec *executor.REPLExecutor,
	pythonPath string,
	useDualEngine bool,
) *MCTSRunner {
	r := &MCTSRunner{
		config:       config,
		agents:       agents,
		critic:       NewStepCritic(),
		selfChecker:  eval.NewSelfChecker(pythonPath),
		judge:        judge,
		builder:      builder,
		replExec:     replExec,
		formulaStrat: NewFormulaStrategy(),
	}
	if useDualEngine {
		r.dualEngine = eval.NewDualEngineValidator(pythonPath)
	}
	return r
}

// Run executes the MCTS search: parallel generation, PRM pruning,
// triple verification, and majority voting.
func (m *MCTSRunner) Run(ctx context.Context, input agent.CodeActInput, answerFile string, n int) TaskResult {
	if n <= 0 {
		n = m.config.DefaultN
	}

	const maxEmptyRetries = 2

	var bestResult TaskResult
	var allCandidates []MCTSCandidate
	emptyRetries := 0

	for depth := 0; depth < m.config.MaxDepth; depth++ {
		slog.Info("mcts round", "depth", depth, "n", n)

		candidates := m.generateCandidates(ctx, input, n)
		allCandidates = append(allCandidates, candidates...)

		allEmpty := true
		for _, c := range candidates {
			if strings.TrimSpace(c.Code) != "" {
				allEmpty = false
				break
			}
		}
		if allEmpty {
			emptyRetries++
			if emptyRetries > maxEmptyRetries {
				slog.Error("mcts: exceeded max empty retries, aborting",
					"retries", emptyRetries, "depth", depth)
				return TaskResult{Error: fmt.Sprintf("all candidates empty after %d retries (API may be down)", emptyRetries)}
			}
			wait := time.Duration(15*(depth+1)) * time.Second
			slog.Warn("mcts: all candidates returned empty (API likely down), cooling off",
				"depth", depth, "wait", wait, "empty_retry", emptyRetries, "max", maxEmptyRetries)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return TaskResult{Error: "context cancelled during cooldown"}
			}
			n = maxOf(2, n/2)
			continue
		}
		emptyRetries = 0

		// PRM pruning
		var survived []MCTSCandidate
		pruned := 0
		for i := range candidates {
			cr := m.critic.Evaluate(candidates[i].Code, input.AnswerPosition)
			candidates[i].CriticScore = cr.Score
			if cr.ShouldPrune {
				candidates[i].Pruned = true
				pruned++
				slog.Info("mcts pruned candidate", "depth", depth, "score", fmt.Sprintf("%.2f", cr.Score),
					"penalties", strings.Join(cr.Penalties, "; "))
				continue
			}
			survived = append(survived, candidates[i])
		}

		if len(survived) == 0 {
			slog.Warn("mcts all candidates pruned, retrying with relaxed threshold", "depth", depth)
			// Fallback: take the best-scored pruned candidate
			sort.Slice(candidates, func(i, j int) bool {
				return candidates[i].CriticScore > candidates[j].CriticScore
			})
			survived = candidates[:minOf(3, len(candidates))]
		}

		// Triple verification (blind) + optional dual-engine cross-check
		var passed []MCTSCandidate
		isFormulaTask := m.formulaStrat.ClassifyTask(input.InstructionType, input.Instruction) == ClassFormula ||
			m.formulaStrat.ClassifyTask(input.InstructionType, input.Instruction) == ClassHybrid

		for i := range survived {
			scr, err := m.selfChecker.Check(ctx, input.InputFile, input.AnswerPosition,
				survived[i].Code, input.InputFile+".orig", input.WorkDir)
			if err != nil {
				slog.Warn("mcts self-check error", "error", err)
				continue
			}
			survived[i].SelfCheck = scr
			if !scr.Passed {
				continue
			}

			// Dual-engine cross-check for formula tasks
			if m.dualEngine != nil && isFormulaTask {
				traceContent := extractAllTraceContent(survived[i].Trace)
				pathBCode := eval.ExtractPathBCode(traceContent)
				if pathBCode != "" {
					der, dualErr := m.dualEngine.Validate(ctx, input.InputFile, input.AnswerPosition,
						pathBCode, input.InputFile+".orig", input.WorkDir)
					if dualErr == nil {
						survived[i].DualEngineCheck = der
						if !der.Passed {
							// Penalize score but don't discard — dual-engine disagreement is a signal
							survived[i].CriticScore *= 0.5
							slog.Info("mcts dual-engine failed for candidate", "idx", i, "mismatches", len(der.Mismatches))
							continue
						}
						// Bonus for dual-engine agreement
						survived[i].CriticScore *= 1.2
					}
				}
			}

			passed = append(passed, survived[i])
		}

		slog.Info("mcts verification", "depth", depth, "generated", len(candidates),
			"pruned", pruned, "survived", len(survived), "passed", len(passed))

		if len(passed) > 0 {
			// Majority voting: select the candidate via critic score
			best := selectBestCandidate(passed)

			// Final OJ Judge scoring
			jr, err := m.judge.Evaluate(ctx, input.InputFile, answerFile, input.AnswerPosition)
			if err != nil {
				bestResult.Error = fmt.Sprintf("final judge error: %v", err)
				return bestResult
			}

			return TaskResult{
				Success:    jr.Pass,
				Attempt:    depth + 1,
				Code:       best.Code,
				AgentTrace: best.Trace,
				Confidence: best.Confidence,
				Error:      jr.Detail,
			}
		}

		// No candidates passed: pick the best failed candidate for feedback.
		// Inject strategy diversity for the next round.
		if len(survived) > 0 {
			best := selectBestCandidate(survived)
			if best.SelfCheck != nil {
				input.PreviousError = best.SelfCheck.Summary
			}

			// Strategy diversity: on depth 1+, nudge toward a different approach
			if depth >= 1 {
				input.PreviousError += "\n\n[STRATEGY SWITCH] Try a fundamentally different approach (e.g., formulas→Python or vice versa)."
			}

			if err := recopyFile(input.InputFile); err != nil {
				slog.Warn("recopy failed", "error", err)
			}
			input.Attempt = depth + 1

			// Reduce candidate count for retry rounds to save tokens
			n = maxOf(2, n/2)
		}
	}

	// Exhausted all depths: final OJ scoring on whatever is in the file
	jr, err := m.judge.Evaluate(ctx, input.InputFile, answerFile, input.AnswerPosition)
	if err != nil {
		bestResult.Error = fmt.Sprintf("final judge error: %v", err)
		return bestResult
	}

	return TaskResult{
		Success:    jr.Pass,
		Attempt:    m.config.MaxDepth,
		Error:      jr.Detail,
		Confidence: -1,
	}
}

// generateCandidates concurrently generates N code candidates using
// available agents (for model diversity).
func (m *MCTSRunner) generateCandidates(ctx context.Context, input agent.CodeActInput, n int) []MCTSCandidate {
	candidates := make([]MCTSCandidate, n)
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			agentIdx := idx % len(m.agents)
			ag := m.agents[agentIdx]

			runCtx := ctx
			var replSession executor.REPLSession
			if m.replExec != nil {
				var err error
				replSession, err = m.replExec.StartSession(ctx, input.WorkDir)
				if err != nil {
					candidates[idx] = MCTSCandidate{Code: "", Model: fmt.Sprintf("agent_%d", agentIdx)}
					return
				}
				runCtx = agent.WithREPLSession(ctx, replSession)
			}

			msg, toolOutputs, trace, err := agent.RunCodeAct(runCtx, ag, input)
			if replSession != nil {
				replSession.Close()
			}

			if err != nil {
				// Retry once for transient API errors within the goroutine
				if isTransientAPIError(err.Error()) {
					slog.Warn("mcts: transient API error in candidate, retrying once",
						"idx", idx, "error", err)
					if m.replExec != nil {
						rs2, err2 := m.replExec.StartSession(ctx, input.WorkDir)
						if err2 == nil {
							msg2, to2, tr2, err2 := agent.RunCodeAct(
								agent.WithREPLSession(ctx, rs2), ag, input)
							rs2.Close()
							if err2 == nil {
								msg = msg2
								toolOutputs = to2
								trace = tr2
								err = nil
							}
						}
					}
				}
				if err != nil {
					candidates[idx] = MCTSCandidate{
						Code:  "",
						Model: fmt.Sprintf("agent_%d", agentIdx),
						Trace: trace,
					}
					return
				}
			}

			code := extractCode(msg, toolOutputs, trace)
			confidence := extractConfidence(trace)

			candidates[idx] = MCTSCandidate{
				Code:       code,
				Confidence: confidence,
				Model:      fmt.Sprintf("agent_%d", agentIdx),
				Trace:      trace,
			}
		}(i)
	}

	wg.Wait()

	// Filter empty candidates
	var valid []MCTSCandidate
	for _, c := range candidates {
		if c.Code != "" {
			valid = append(valid, c)
		}
	}
	return valid
}

// selectBestCandidate picks the candidate with the highest critic score,
// breaking ties by confidence.
func selectBestCandidate(candidates []MCTSCandidate) MCTSCandidate {
	if len(candidates) == 0 {
		return MCTSCandidate{}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].CriticScore != candidates[j].CriticScore {
			return candidates[i].CriticScore > candidates[j].CriticScore
		}
		return candidates[i].Confidence > candidates[j].Confidence
	})
	return candidates[0]
}

func minOf(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxOf(a, b int) int {
	if a > b {
		return a
	}
	return b
}
