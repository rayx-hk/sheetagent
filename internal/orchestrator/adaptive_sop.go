// adaptive_sop.go implements the v1.0.0 Adaptive SOP (Standard Operating
// Procedure) that dynamically selects the optimal execution strategy per task.
//
// Instead of a one-size-fits-all pipeline, the SOP analyzes the task and
// assembles a custom pipeline from available components:
//   - Formula-First vs Python vs Hybrid
//   - MCTS (hard tasks) vs Linear retry (easy tasks)
//   - Skills injection based on task type
//   - Failure pattern warnings
//   - RAG few-shot examples
//
// This is the "brain" that orchestrates all v0.4.x/v0.5.x modules.
package orchestrator

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/rayx-hk/sheetagent/internal/memory"
	"github.com/rayx-hk/sheetagent/internal/rag"
	"github.com/rayx-hk/sheetagent/internal/skills"
)

// TaskDifficulty estimates how hard a task is, driving strategy selection.
type TaskDifficulty string

const (
	DifficultyEasy   TaskDifficulty = "easy"
	DifficultyMedium TaskDifficulty = "medium"
	DifficultyHard   TaskDifficulty = "hard"
)

// SOPDecision captures the chosen strategy for a task.
type SOPDecision struct {
	Difficulty       TaskDifficulty
	Classification   TaskClassification
	UseMCTS          bool
	MCTSCandidates   int
	MaxRetry         int
	MatchedSkills    []skills.SkillMatch
	FewShotExamples  []rag.SolvedExample
	FailureWarnings  []memory.FailureRecord
	PromptAdditions  string // combined prompt injection
}

// AdaptiveSOP makes per-task strategy decisions.
type AdaptiveSOP struct {
	formulaStrategy *FormulaStrategy
	skillRegistry   *skills.Registry
	fewShotStore    *rag.FewShotStore
	failureStore    *memory.FailureStore
}

// NewAdaptiveSOP creates the adaptive SOP engine.
// Any component can be nil (graceful degradation).
func NewAdaptiveSOP(
	skillReg *skills.Registry,
	fewShot *rag.FewShotStore,
	failStore *memory.FailureStore,
) *AdaptiveSOP {
	return &AdaptiveSOP{
		formulaStrategy: NewFormulaStrategy(),
		skillRegistry:   skillReg,
		fewShotStore:    fewShot,
		failureStore:    failStore,
	}
}

// Decide analyzes the task and produces a SOPDecision.
func (sop *AdaptiveSOP) Decide(instruction, instructionType, answerPosition string, sheetFeatures []string) SOPDecision {
	d := SOPDecision{}

	// 1. Classify task type (formula vs python)
	d.Classification = sop.formulaStrategy.ClassifyTask(instructionType, instruction)

	// 2. Estimate difficulty
	d.Difficulty = sop.estimateDifficulty(instruction, instructionType, sheetFeatures)

	// 3. MCTS decision based on difficulty
	switch d.Difficulty {
	case DifficultyEasy:
		d.UseMCTS = false
		d.MaxRetry = 2
		d.MCTSCandidates = 0
	case DifficultyMedium:
		d.UseMCTS = false
		d.MCTSCandidates = 0
		d.MaxRetry = 3
	case DifficultyHard:
		d.UseMCTS = true
		d.MCTSCandidates = 2
		d.MaxRetry = 4
	}

	// 4. Match skills
	if sop.skillRegistry != nil {
		d.MatchedSkills = sop.skillRegistry.Match(instruction, instructionType, sheetFeatures)
	}

	// 5. Retrieve few-shot examples
	if sop.fewShotStore != nil {
		d.FewShotExamples = sop.fewShotStore.Retrieve(instruction, instructionType, 2)
	}

	// 6. Retrieve failure warnings
	if sop.failureStore != nil {
		d.FailureWarnings = sop.failureStore.RetrieveWarnings(instructionType, instruction, sheetFeatures)
	}

	// 7. Build combined prompt additions
	d.PromptAdditions = sop.buildPromptAdditions(d, answerPosition)

	slog.Info("sop decision",
		"difficulty", d.Difficulty,
		"classification", d.Classification,
		"use_mcts", d.UseMCTS,
		"mcts_n", d.MCTSCandidates,
		"skills", len(d.MatchedSkills),
		"examples", len(d.FewShotExamples),
		"warnings", len(d.FailureWarnings),
	)

	return d
}

func (sop *AdaptiveSOP) estimateDifficulty(instruction, instructionType string, sheetFeatures []string) TaskDifficulty {
	score := 0

	lower := strings.ToLower(instruction)
	typeLower := strings.ToLower(instructionType)

	// Instruction complexity signals
	if len(instruction) > 300 {
		score += 2
	} else if len(instruction) > 150 {
		score += 1
	}

	// Multi-step tasks
	multiStepKeywords := []string{"then", "after that", "next", "finally", "first", "second", "step"}
	for _, kw := range multiStepKeywords {
		if strings.Contains(lower, kw) {
			score++
			break
		}
	}

	// Complex operations
	complexKeywords := []string{"pivot", "vlookup", "index match", "conditional format",
		"across sheets", "multiple sheets", "chart", "macro"}
	for _, kw := range complexKeywords {
		if strings.Contains(lower, kw) || strings.Contains(typeLower, kw) {
			score += 2
		}
	}

	// Sheet structure complexity
	complexFeatures := []string{"merged_cells", "multi_header", "sparse_layout", "multi_sheet"}
	for _, cf := range complexFeatures {
		for _, sf := range sheetFeatures {
			if strings.EqualFold(cf, sf) {
				score++
			}
		}
	}

	// Prior failure rate for this type
	if sop.failureStore != nil {
		warnings := sop.failureStore.RetrieveWarnings(instructionType, instruction, sheetFeatures)
		if len(warnings) >= 2 {
			score += 2
		} else if len(warnings) >= 1 {
			score++
		}
	}

	switch {
	case score >= 5:
		return DifficultyHard
	case score >= 2:
		return DifficultyMedium
	default:
		return DifficultyEasy
	}
}

func (sop *AdaptiveSOP) buildPromptAdditions(d SOPDecision, answerPosition string) string {
	var sb strings.Builder

	// Formula strategy injection
	formulaText := sop.formulaStrategy.FormulaPromptInjection(d.Classification, answerPosition)
	if formulaText != "" {
		sb.WriteString(formulaText)
	}

	// Skills injection (max 2)
	if len(d.MatchedSkills) > 0 {
		sb.WriteString(skills.FormatPromptInjection(d.MatchedSkills, 2))
	}

	// Few-shot examples
	if len(d.FewShotExamples) > 0 {
		sb.WriteString(rag.FormatFewShot(d.FewShotExamples))
	}

	// Failure warnings
	if len(d.FailureWarnings) > 0 {
		sb.WriteString(memory.FormatWarnings(d.FailureWarnings))
	}

	// Difficulty-specific guidance
	switch d.Difficulty {
	case DifficultyHard:
		sb.WriteString(fmt.Sprintf(`
=== HARD TASK ADVISORY ===
This task is classified as HARD (multiple complexity signals detected).
Strategy: MCTS search with %d candidates.
- Take extra care with dynamic references.
- Verify sheet structure before writing.
- Use find_header() for ALL column lookups.
- Add post-write verification.
=== END ADVISORY ===
`, d.MCTSCandidates))
	case DifficultyMedium:
		sb.WriteString("\n[NOTE] Medium difficulty task — using targeted search with verification.\n")
	}

	return sb.String()
}
