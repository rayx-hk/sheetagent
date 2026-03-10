// Package memory implements the Failure Memory store for v1.0.0 productization.
// It records failed task attempts with root-cause analysis, then retrieves
// relevant failure patterns during future tasks to prevent repeated mistakes.
//
// The key insight: if a task failed because of merged cells in row 5, and a
// new task has a similar structure, the agent should be warned proactively.
package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// FailureCategory classifies the root cause of a failure.
type FailureCategory string

const (
	CatHardcoding     FailureCategory = "hardcoding"      // hardcoded row/col indices
	CatMergedCells    FailureCategory = "merged_cells"     // didn't handle merged regions
	CatFormulaError   FailureCategory = "formula_error"    // formula produced #VALUE!, #REF!
	CatEmptyResult    FailureCategory = "empty_result"     // target cells left empty
	CatPrecision      FailureCategory = "precision"        // numeric precision mismatch
	CatWrongRange     FailureCategory = "wrong_range"      // wrote to wrong cells
	CatTypeError      FailureCategory = "type_error"       // string vs number mismatch
	CatExecution      FailureCategory = "execution_error"  // Python exception
	CatTimeout        FailureCategory = "timeout"          // execution timeout
	CatLayoutMismatch FailureCategory = "layout_mismatch"  // misunderstood sheet layout
	CatUnknown        FailureCategory = "unknown"
)

// FailureRecord stores one failed attempt with analysis.
type FailureRecord struct {
	TaskID          string          `json:"task_id"`
	InstructionType string          `json:"instruction_type"`
	Instruction     string          `json:"instruction"`
	AnswerPosition  string          `json:"answer_position"`
	Category        FailureCategory `json:"category"`
	ErrorSummary    string          `json:"error_summary"`
	CodeSnippet     string          `json:"code_snippet"`   // the failing code (key lines)
	FixHint         string          `json:"fix_hint"`       // what should have been done
	SheetFeatures   []string        `json:"sheet_features"` // structural features
	Timestamp       time.Time       `json:"timestamp"`
	AttemptCount    int             `json:"attempt_count"`
}

// FailureStore manages the failure database.
type FailureStore struct {
	mu       sync.RWMutex
	records  []FailureRecord
	filePath string
}

// NewFailureStore creates or loads a failure store.
func NewFailureStore(filePath string) (*FailureStore, error) {
	store := &FailureStore{
		filePath: filePath,
	}
	if err := store.load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load failure store: %w", err)
	}
	return store, nil
}

// Record adds a failure to the store.
func (s *FailureStore) Record(record FailureRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record.Timestamp = time.Now()
	if record.Category == "" {
		record.Category = ClassifyFailure(record.ErrorSummary, record.CodeSnippet)
	}
	s.records = append(s.records, record)

	// Cap store size
	const maxRecords = 5000
	if len(s.records) > maxRecords {
		s.records = s.records[len(s.records)-maxRecords:]
	}
}

// RetrieveWarnings finds failure patterns relevant to the given task.
func (s *FailureStore) RetrieveWarnings(instructionType string, instruction string, sheetFeatures []string) []FailureRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	lower := strings.ToLower(instruction)
	typeLower := strings.ToLower(instructionType)

	type scored struct {
		record FailureRecord
		score  float64
	}
	var results []scored

	for _, r := range s.records {
		score := 0.0

		// Same instruction type
		if strings.EqualFold(r.InstructionType, instructionType) {
			score += 0.4
		}

		// Shared sheet features
		for _, sf := range sheetFeatures {
			for _, rf := range r.SheetFeatures {
				if strings.EqualFold(sf, rf) {
					score += 0.2
				}
			}
		}

		// Keyword overlap
		rWords := strings.Fields(strings.ToLower(r.Instruction))
		for _, w := range rWords {
			if len(w) > 3 && (strings.Contains(lower, w) || strings.Contains(typeLower, w)) {
				score += 0.05
			}
		}

		if score > 0.3 {
			results = append(results, scored{record: r, score: score})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	// Return top 3
	maxResults := 3
	if maxResults > len(results) {
		maxResults = len(results)
	}
	out := make([]FailureRecord, maxResults)
	for i := 0; i < maxResults; i++ {
		out[i] = results[i].record
	}
	return out
}

// FormatWarnings creates prompt text warning the agent about known failure patterns.
func FormatWarnings(failures []FailureRecord) string {
	if len(failures) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n=== KNOWN FAILURE PATTERNS (avoid these mistakes) ===\n")
	for i, f := range failures {
		sb.WriteString(fmt.Sprintf("\n[Warning %d] Category: %s\n", i+1, f.Category))
		sb.WriteString(fmt.Sprintf("Similar task failed with: %s\n", truncateStr(f.ErrorSummary, 150)))
		if f.FixHint != "" {
			sb.WriteString(fmt.Sprintf("Recommended fix: %s\n", f.FixHint))
		}
	}
	sb.WriteString("\n=== END WARNINGS ===\n")
	return sb.String()
}

// ClassifyFailure auto-detects the failure category from error text and code.
func ClassifyFailure(errorSummary, code string) FailureCategory {
	lower := strings.ToLower(errorSummary)
	codeLower := strings.ToLower(code)

	switch {
	case strings.Contains(lower, "empty") || strings.Contains(lower, "none"):
		return CatEmptyResult
	case strings.Contains(lower, "#value!") || strings.Contains(lower, "#ref!") || strings.Contains(lower, "#n/a"):
		return CatFormulaError
	case strings.Contains(lower, "precision") || strings.Contains(lower, "decimal"):
		return CatPrecision
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out"):
		return CatTimeout
	case strings.Contains(lower, "exception") || strings.Contains(lower, "error") || strings.Contains(lower, "traceback"):
		return CatExecution
	case strings.Contains(lower, "type") || strings.Contains(lower, "string") && strings.Contains(lower, "number"):
		return CatTypeError
	case strings.Contains(lower, "merge") || strings.Contains(lower, "merged"):
		return CatMergedCells
	case strings.Contains(codeLower, "iloc") || strings.Contains(codeLower, "row=") && strings.Contains(codeLower, "column="):
		return CatHardcoding
	default:
		return CatUnknown
	}
}

// GetStats returns aggregate failure statistics.
func (s *FailureStore) GetStats() map[FailureCategory]int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := make(map[FailureCategory]int)
	for _, r := range s.records {
		stats[r.Category]++
	}
	return stats
}

// Save persists the store to disk.
func (s *FailureStore) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := json.MarshalIndent(s.records, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return os.WriteFile(s.filePath, data, 0644)
}

// Size returns the number of stored failure records.
func (s *FailureStore) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.records)
}

func (s *FailureStore) load() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &s.records)
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
