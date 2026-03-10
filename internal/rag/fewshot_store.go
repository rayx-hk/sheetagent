// Package rag implements a lightweight few-shot example store for the
// RAG Few-Shot injection strategy. It indexes successfully solved tasks
// by instruction type and structural fingerprint, then retrieves the
// most relevant examples to inject into the agent prompt.
//
// IMPORTANT — Benchmark Integrity:
// RAG MUST NOT be used during benchmark evaluation. Solutions accumulated
// in the store are indirectly validated by golden answers (OJ Judge AC
// verdict), so injecting them back into the same benchmark constitutes
// test-set contamination. The store enforces this via a mode flag: when
// created with NewBenchLockedStore(), all mutating and retrieval operations
// return empty results or errors.
//
// RAG is a product-mode feature only. Enable it via config "rag.enabled: true"
// and use NewFewShotStore() in the serving/product binary — never in bench.
//
// v0.5.x: file-based JSON store with TF-IDF similarity.
// v1.0.0: upgrade to embedding-based vector store (e.g. Qdrant, Chroma).
package rag

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
)

// ErrBenchMode is returned when RAG operations are attempted in bench mode.
var ErrBenchMode = errors.New("rag: operation rejected — RAG is disabled in benchmark mode to prevent test-set contamination")

// SolvedExample represents one successfully solved benchmark task.
type SolvedExample struct {
	TaskID          string   `json:"task_id"`
	InstructionType string   `json:"instruction_type"`
	Instruction     string   `json:"instruction"`
	Code            string   `json:"code"`
	AnswerPosition  string   `json:"answer_position"`
	SheetStructure  string   `json:"sheet_structure"` // fingerprint: "cols=5,rows=100,sheets=2"
	Tags            []string `json:"tags"`            // auto-extracted keywords
	Score           float64  `json:"score"`           // OJ score when solved
}

// FewShotStore manages the example database.
type FewShotStore struct {
	mu       sync.RWMutex
	examples []SolvedExample
	filePath string

	// TF-IDF: term -> document frequency
	docFreq   map[string]int
	totalDocs int

	// benchLocked prevents all RAG operations when true.
	// Set by NewBenchLockedStore() for benchmark integrity.
	benchLocked bool
}

// NewFewShotStore creates or loads a store from the given JSON file.
// Use this ONLY in production/serving mode, never in bench.
func NewFewShotStore(filePath string) (*FewShotStore, error) {
	store := &FewShotStore{
		filePath: filePath,
		docFreq:  make(map[string]int),
	}
	if err := store.load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load store: %w", err)
	}
	store.rebuildIndex()
	return store, nil
}

// NewBenchLockedStore returns a no-op store that rejects all operations.
// Use this in bench mode so that any accidental RAG call-sites are safe.
func NewBenchLockedStore() *FewShotStore {
	return &FewShotStore{
		docFreq:     make(map[string]int),
		benchLocked: true,
	}
}

// IsBenchLocked reports whether this store is in bench-locked mode.
func (s *FewShotStore) IsBenchLocked() bool {
	return s.benchLocked
}

// Add records a solved example. Returns ErrBenchMode if bench-locked.
func (s *FewShotStore) Add(ex SolvedExample) error {
	if s.benchLocked {
		return ErrBenchMode
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if ex.Tags == nil {
		ex.Tags = extractTags(ex.Instruction, ex.InstructionType)
	}

	// Dedup by task ID
	for i, e := range s.examples {
		if e.TaskID == ex.TaskID {
			s.examples[i] = ex
			s.rebuildIndex()
			return nil
		}
	}
	s.examples = append(s.examples, ex)
	s.rebuildIndex()
	return nil
}

// Retrieve finds the top-k most similar examples for the given query.
// Returns nil silently if bench-locked (no examples should leak in bench mode).
func (s *FewShotStore) Retrieve(instruction, instructionType string, k int) []SolvedExample {
	if s.benchLocked {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.examples) == 0 || k <= 0 {
		return nil
	}

	queryTerms := tokenize(instruction + " " + instructionType)
	queryTFIDF := s.computeTFIDF(queryTerms)

	type scored struct {
		example SolvedExample
		score   float64
	}
	var results []scored

	for _, ex := range s.examples {
		docTerms := tokenize(ex.Instruction + " " + ex.InstructionType + " " + strings.Join(ex.Tags, " "))
		docTFIDF := s.computeTFIDF(docTerms)
		sim := cosineSimilarity(queryTFIDF, docTFIDF)

		// Bonus for same instruction type
		if strings.EqualFold(ex.InstructionType, instructionType) {
			sim += 0.3
		}

		results = append(results, scored{example: ex, score: sim})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	if k > len(results) {
		k = len(results)
	}

	out := make([]SolvedExample, k)
	for i := 0; i < k; i++ {
		out[i] = results[i].example
	}
	return out
}

// FormatFewShot formats retrieved examples as prompt injection text.
func FormatFewShot(examples []SolvedExample) string {
	if len(examples) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n=== SIMILAR SOLVED EXAMPLES (for reference) ===\n")
	for i, ex := range examples {
		sb.WriteString(fmt.Sprintf("\n--- Example %d (type: %s, score: %.1f%%) ---\n", i+1, ex.InstructionType, ex.Score*100))
		sb.WriteString(fmt.Sprintf("Task: %s\n", truncate(ex.Instruction, 200)))
		sb.WriteString(fmt.Sprintf("Answer Position: %s\n", ex.AnswerPosition))

		// Only include key code snippets, not the full code
		codeSnippet := extractKeySnippet(ex.Code, 500)
		sb.WriteString(fmt.Sprintf("Approach:\n```python\n%s\n```\n", codeSnippet))
	}
	sb.WriteString("\n=== END EXAMPLES ===\n")
	sb.WriteString("NOTE: Use these as reference patterns only. Adapt column names, ranges, and logic to the CURRENT task.\n")

	return sb.String()
}

// Save persists the store to disk. Returns ErrBenchMode if bench-locked.
func (s *FewShotStore) Save() error {
	if s.benchLocked {
		return ErrBenchMode
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := json.MarshalIndent(s.examples, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return os.WriteFile(s.filePath, data, 0644)
}

// Size returns the number of stored examples.
func (s *FewShotStore) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.examples)
}

// ---------------------------------------------------------------------------
// Internal
// ---------------------------------------------------------------------------

func (s *FewShotStore) load() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &s.examples)
}

func (s *FewShotStore) rebuildIndex() {
	s.docFreq = make(map[string]int)
	s.totalDocs = len(s.examples)

	for _, ex := range s.examples {
		terms := tokenize(ex.Instruction + " " + ex.InstructionType + " " + strings.Join(ex.Tags, " "))
		seen := make(map[string]bool)
		for _, t := range terms {
			if !seen[t] {
				s.docFreq[t]++
				seen[t] = true
			}
		}
	}
}

func (s *FewShotStore) computeTFIDF(terms []string) map[string]float64 {
	tf := make(map[string]int)
	for _, t := range terms {
		tf[t]++
	}

	tfidf := make(map[string]float64)
	for term, count := range tf {
		termFreq := float64(count) / float64(len(terms))
		df := s.docFreq[term]
		if df == 0 {
			df = 1
		}
		idf := math.Log(float64(s.totalDocs+1) / float64(df+1))
		tfidf[term] = termFreq * idf
	}
	return tfidf
}

func tokenize(text string) []string {
	text = strings.ToLower(text)
	// Split on non-alphanumeric
	var tokens []string
	var current strings.Builder
	for _, r := range text {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			current.WriteRune(r)
		} else {
			if current.Len() > 2 {
				tokens = append(tokens, current.String())
			}
			current.Reset()
		}
	}
	if current.Len() > 2 {
		tokens = append(tokens, current.String())
	}

	// Remove stopwords
	stopwords := map[string]bool{
		"the": true, "and": true, "for": true, "that": true,
		"with": true, "this": true, "from": true, "are": true,
		"was": true, "have": true, "has": true, "not": true,
		"but": true, "all": true, "can": true, "which": true,
	}
	var filtered []string
	for _, t := range tokens {
		if !stopwords[t] {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

func cosineSimilarity(a, b map[string]float64) float64 {
	dot := 0.0
	normA := 0.0
	normB := 0.0

	for k, v := range a {
		normA += v * v
		if bv, ok := b[k]; ok {
			dot += v * bv
		}
	}
	for _, v := range b {
		normB += v * v
	}

	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func extractTags(instruction, instructionType string) []string {
	lower := strings.ToLower(instruction + " " + instructionType)
	tagPatterns := []struct {
		keyword string
		tag     string
	}{
		{"sum", "aggregation"}, {"average", "aggregation"}, {"count", "aggregation"},
		{"vlookup", "lookup"}, {"index", "lookup"}, {"match", "lookup"},
		{"sort", "sorting"}, {"filter", "filtering"}, {"delete", "deletion"},
		{"format", "formatting"}, {"highlight", "formatting"},
		{"concatenate", "text"}, {"concat", "text"},
		{"date", "datetime"}, {"time", "datetime"},
		{"chart", "visualization"}, {"graph", "visualization"},
		{"pivot", "pivot"}, {"merge", "merge"},
		{"conditional", "conditional"}, {"if ", "conditional"},
	}

	seen := make(map[string]bool)
	var tags []string
	for _, p := range tagPatterns {
		if strings.Contains(lower, p.keyword) && !seen[p.tag] {
			tags = append(tags, p.tag)
			seen[p.tag] = true
		}
	}
	return tags
}

func extractKeySnippet(code string, maxLen int) string {
	if len(code) <= maxLen {
		return code
	}
	lines := strings.Split(code, "\n")
	var keyLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Keep imports, key function calls, formula writes, result markers
		if strings.HasPrefix(trimmed, "import") ||
			strings.HasPrefix(trimmed, "from") ||
			strings.Contains(trimmed, "find_header") ||
			strings.Contains(trimmed, ".value =") ||
			strings.Contains(trimmed, "CHANGE_LOG") ||
			strings.Contains(trimmed, "===RESULT===") ||
			strings.Contains(trimmed, "FORMULA") ||
			strings.Contains(trimmed, "def ") {
			keyLines = append(keyLines, line)
		}
	}
	result := strings.Join(keyLines, "\n")
	if len(result) > maxLen {
		return result[:maxLen] + "\n# ... (truncated)"
	}
	return result
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
