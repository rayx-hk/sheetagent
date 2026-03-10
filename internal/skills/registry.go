// Package skills implements a dynamic skill registry for v1.0.0 productization.
// Skills are reusable capability plugins that can be hot-loaded and injected
// into the agent's prompt and tool set based on task classification.
//
// Each skill encapsulates:
//   - A system prompt fragment (domain expertise)
//   - Optional tool definitions (specialized functions)
//   - Pre/post processing hooks
//   - Metadata for matching (trigger keywords, instruction types)
package skills

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Skill represents a registered capability plugin.
type Skill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Version     string   `json:"version"`
	Priority    int      `json:"priority"` // higher = preferred when multiple match
	Enabled     bool     `json:"enabled"`

	// Matching criteria
	TriggerKeywords  []string `json:"trigger_keywords"`
	InstructionTypes []string `json:"instruction_types"`
	SheetPatterns    []string `json:"sheet_patterns"` // e.g. "merged_cells", "pivot_table"

	// Prompt injection
	SystemPromptFragment string `json:"system_prompt_fragment"`
	ExampleCode          string `json:"example_code"`

	// Stats
	UsageCount   int     `json:"usage_count"`
	SuccessRate  float64 `json:"success_rate"`
	AvgScoreGain float64 `json:"avg_score_gain"`
}

// SkillMatch holds a skill and its relevance score.
type SkillMatch struct {
	Skill Skill
	Score float64
}

// Registry manages the skill database.
type Registry struct {
	mu       sync.RWMutex
	skills   map[string]*Skill
	dataDir  string
}

// NewRegistry creates a registry, loading skills from the given directory.
func NewRegistry(dataDir string) (*Registry, error) {
	r := &Registry{
		skills:  make(map[string]*Skill),
		dataDir: dataDir,
	}
	if err := r.loadAll(); err != nil {
		slog.Warn("skill registry: could not load existing skills", "error", err)
	}
	return r, nil
}

// Register adds or updates a skill.
func (r *Registry) Register(skill Skill) {
	r.mu.Lock()
	defer r.mu.Unlock()
	skill.Enabled = true
	r.skills[skill.ID] = &skill
}

// Match finds skills relevant to the given task context.
func (r *Registry) Match(instruction, instructionType string, sheetFeatures []string) []SkillMatch {
	r.mu.RLock()
	defer r.mu.RUnlock()

	lower := strings.ToLower(instruction)
	typeLower := strings.ToLower(instructionType)

	var matches []SkillMatch
	for _, skill := range r.skills {
		if !skill.Enabled {
			continue
		}

		score := 0.0

		// Keyword matching
		for _, kw := range skill.TriggerKeywords {
			if strings.Contains(lower, strings.ToLower(kw)) {
				score += 0.3
			}
		}

		// Instruction type matching
		for _, it := range skill.InstructionTypes {
			if strings.EqualFold(it, instructionType) || strings.Contains(typeLower, strings.ToLower(it)) {
				score += 0.5
			}
		}

		// Sheet pattern matching
		for _, sp := range skill.SheetPatterns {
			for _, sf := range sheetFeatures {
				if strings.EqualFold(sp, sf) {
					score += 0.2
				}
			}
		}

		// Success rate bonus
		if skill.UsageCount > 5 {
			score += skill.SuccessRate * 0.2
		}

		if score > 0 {
			matches = append(matches, SkillMatch{Skill: *skill, Score: score})
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].Skill.Priority > matches[j].Skill.Priority
	})

	return matches
}

// RecordOutcome updates skill stats after a task attempt.
func (r *Registry) RecordOutcome(skillID string, success bool, scoreGain float64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	skill, ok := r.skills[skillID]
	if !ok {
		return
	}
	skill.UsageCount++
	// Exponential moving average for success rate
	alpha := 0.1
	successVal := 0.0
	if success {
		successVal = 1.0
	}
	skill.SuccessRate = skill.SuccessRate*(1-alpha) + successVal*alpha
	skill.AvgScoreGain = skill.AvgScoreGain*(1-alpha) + scoreGain*alpha
}

// FormatPromptInjection creates the prompt text for matched skills.
func FormatPromptInjection(matches []SkillMatch, maxSkills int) string {
	if len(matches) == 0 {
		return ""
	}
	if maxSkills > len(matches) {
		maxSkills = len(matches)
	}

	var sb strings.Builder
	sb.WriteString("\n=== ACTIVATED SKILLS ===\n")
	for i := 0; i < maxSkills; i++ {
		s := matches[i].Skill
		sb.WriteString(fmt.Sprintf("\n[Skill: %s] (relevance: %.0f%%)\n", s.Name, matches[i].Score*100))
		if s.SystemPromptFragment != "" {
			sb.WriteString(s.SystemPromptFragment)
			sb.WriteString("\n")
		}
		if s.ExampleCode != "" {
			sb.WriteString(fmt.Sprintf("Reference pattern:\n```python\n%s\n```\n", s.ExampleCode))
		}
	}
	sb.WriteString("=== END SKILLS ===\n")
	return sb.String()
}

// Save persists all skills to the data directory.
func (r *Registry) Save() error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if err := os.MkdirAll(r.dataDir, 0755); err != nil {
		return err
	}

	for id, skill := range r.skills {
		data, err := json.MarshalIndent(skill, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal skill %s: %w", id, err)
		}
		path := filepath.Join(r.dataDir, id+".json")
		if err := os.WriteFile(path, data, 0644); err != nil {
			return fmt.Errorf("write skill %s: %w", id, err)
		}
	}
	return nil
}

func (r *Registry) loadAll() error {
	entries, err := os.ReadDir(r.dataDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(r.dataDir, entry.Name()))
		if err != nil {
			continue
		}
		var skill Skill
		if err := json.Unmarshal(data, &skill); err != nil {
			continue
		}
		r.skills[skill.ID] = &skill
	}
	return nil
}

// BuiltinSkills returns the default set of skills for SpreadsheetBench.
func BuiltinSkills() []Skill {
	return []Skill{
		{
			ID:          "formula-writer",
			Name:        "Excel Formula Writer",
			Description: "Expert at writing native Excel formulas for calculation tasks",
			Version:     "1.0",
			Priority:    10,
			Enabled:     true,
			TriggerKeywords:  []string{"sum", "average", "count", "vlookup", "formula", "calculate", "total"},
			InstructionTypes: []string{"calculate", "aggregate", "formula", "lookup"},
			SystemPromptFragment: "You are an Excel formula expert. For this task, prioritize writing native Excel formulas (=SUM, =VLOOKUP, =INDEX/MATCH) over Python computation. Formulas are inherently dynamic and robust.",
		},
		{
			ID:          "data-cleaner",
			Name:        "Data Cleaning Specialist",
			Description: "Handles messy data: inconsistent formats, missing values, duplicates",
			Version:     "1.0",
			Priority:    8,
			Enabled:     true,
			TriggerKeywords:  []string{"clean", "fix", "correct", "standardize", "normalize", "duplicate", "missing"},
			InstructionTypes: []string{"clean", "transform", "fix"},
			SystemPromptFragment: "Data cleaning task detected. Steps: 1) Scan for inconsistencies, 2) Handle missing values, 3) Normalize formats, 4) Verify no data loss.",
		},
		{
			ID:          "layout-navigator",
			Name:        "Complex Layout Navigator",
			Description: "Handles merged cells, multi-header tables, non-standard layouts",
			Version:     "1.0",
			Priority:    9,
			Enabled:     true,
			TriggerKeywords:  []string{"merged", "multi-level", "header", "nested", "complex"},
			SheetPatterns:    []string{"merged_cells", "multi_header", "sparse_layout"},
			SystemPromptFragment: "Complex layout detected. Use openpyxl's merged_cells attribute to detect merged regions. Never assume a simple header-row-data structure. Scan dynamically.",
		},
		{
			ID:          "conditional-logic",
			Name:        "Conditional Processing Expert",
			Description: "IF/THEN logic, conditional formatting, filtered operations",
			Version:     "1.0",
			Priority:    7,
			Enabled:     true,
			TriggerKeywords:  []string{"if", "condition", "when", "based on", "depending", "criteria"},
			InstructionTypes: []string{"conditional", "filter", "classify"},
			SystemPromptFragment: "Conditional task. Use IF/COUNTIF/SUMIF formulas when writing to cells. For complex multi-condition logic, consider nested IFs or IFS formula.",
		},
	}
}
