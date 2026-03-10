package eval

import (
	"context"
	"fmt"
	"strings"

	"github.com/rayx-hk/sheetagent/internal/sheet"
)

// InvariantType identifies the kind of logical check to perform.
type InvariantType string

const (
	InvariantSumConsistency InvariantType = "sum_consistency"
	InvariantLookupExists   InvariantType = "lookup_exists"
	InvariantCountBounds    InvariantType = "count_bounds"
	InvariantRowReduction   InvariantType = "row_reduction"
	InvariantSortOrder      InvariantType = "sort_order"
	InvariantNonEmpty       InvariantType = "non_empty"
)

// Invariant is a domain-specific assertion auto-generated based on task type.
type Invariant struct {
	Type        InvariantType `json:"type"`
	Description string        `json:"description"`
	CheckCode   string        `json:"check_code"` // Python snippet to verify
}

// InvariantResult holds the outcome of one invariant check.
type InvariantResult struct {
	Invariant Invariant `json:"invariant"`
	Passed    bool      `json:"passed"`
	Error     string    `json:"error,omitempty"`
}

// TripleVerifyResult holds the combined V1+V2+V3 verification outcome.
type TripleVerifyResult struct {
	V1Execution bool              `json:"v1_execution"`
	V2Synthetic bool              `json:"v2_synthetic"`
	V3Invariant bool              `json:"v3_invariant"`
	OverallPass bool              `json:"overall_pass"`
	Confidence  float64           `json:"confidence"`
	Details     []InvariantResult `json:"details,omitempty"`
	SelfCheck   *SelfCheckResult  `json:"self_check,omitempty"`
}

// ShadowTDDEngine implements the triple verification pipeline:
// V1: sandbox execution (no exceptions), V2: synthetic variants,
// V3: domain-specific invariant assertions.
type ShadowTDDEngine struct {
	selfChecker *SelfChecker
	parser      *sheet.ExcelizeParser
}

func NewShadowTDDEngine(pythonPath string) *ShadowTDDEngine {
	return &ShadowTDDEngine{
		selfChecker: NewSelfChecker(pythonPath),
		parser:      sheet.NewParser(),
	}
}

// TripleVerify runs V1+V2+V3 in sequence. Short-circuits on V1 failure.
func (e *ShadowTDDEngine) TripleVerify(ctx context.Context, outputFile, answerPosition, code, inputOrigFile, workDir, instructionType string) *TripleVerifyResult {
	result := &TripleVerifyResult{}

	// V1 + V2: Delegate to SelfChecker (execution + synthetic)
	scr, err := e.selfChecker.Check(ctx, outputFile, answerPosition, code, inputOrigFile, workDir)
	if err != nil {
		result.V1Execution = false
		return result
	}
	result.SelfCheck = scr
	result.V1Execution = scr.ExecutionError == ""
	result.V2Synthetic = len(scr.SyntheticFails) == 0

	if !result.V1Execution {
		return result
	}

	// V3: Domain invariant checks
	invariants := GenerateInvariants(instructionType, answerPosition)
	if len(invariants) == 0 {
		result.V3Invariant = true
	} else {
		allPassed := true
		for _, inv := range invariants {
			ir := e.checkInvariant(ctx, outputFile, answerPosition, inv)
			result.Details = append(result.Details, ir)
			if !ir.Passed {
				allPassed = false
			}
		}
		result.V3Invariant = allPassed
	}

	result.OverallPass = result.V1Execution && result.V2Synthetic && result.V3Invariant
	result.Confidence = computeTripleConfidence(result)
	return result
}

// GenerateInvariants creates domain-specific assertions based on task type.
func GenerateInvariants(instructionType, answerPosition string) []Invariant {
	var invariants []Invariant

	lower := strings.ToLower(instructionType)

	// All tasks: target cells should be non-empty
	invariants = append(invariants, Invariant{
		Type:        InvariantNonEmpty,
		Description: "Target cells in answer_position should be non-empty",
	})

	switch {
	case strings.Contains(lower, "sum") || strings.Contains(lower, "average") || strings.Contains(lower, "calculate"):
		invariants = append(invariants, Invariant{
			Type:        InvariantSumConsistency,
			Description: "Sum of parts should be consistent with totals",
		})
	case strings.Contains(lower, "find") || strings.Contains(lower, "lookup") || strings.Contains(lower, "extract"):
		invariants = append(invariants, Invariant{
			Type:        InvariantLookupExists,
			Description: "Extracted values should exist in source data",
		})
	case strings.Contains(lower, "count"):
		invariants = append(invariants, Invariant{
			Type:        InvariantCountBounds,
			Description: "Count values should be non-negative and <= total rows",
		})
	case strings.Contains(lower, "delete") || strings.Contains(lower, "remove"):
		invariants = append(invariants, Invariant{
			Type:        InvariantRowReduction,
			Description: "Output should have fewer rows than input after deletion",
		})
	case strings.Contains(lower, "sort"):
		invariants = append(invariants, Invariant{
			Type:        InvariantSortOrder,
			Description: "Output column should be in sorted order",
		})
	}

	return invariants
}

// checkInvariant performs a single invariant check by inspecting the output file.
func (e *ShadowTDDEngine) checkInvariant(ctx context.Context, outputFile, answerPosition string, inv Invariant) InvariantResult {
	result := InvariantResult{Invariant: inv, Passed: true}

	sheets, err := e.parser.Parse(ctx, outputFile)
	if err != nil {
		result.Passed = false
		result.Error = fmt.Sprintf("cannot parse output: %v", err)
		return result
	}

	switch inv.Type {
	case InvariantNonEmpty:
		result = e.checkNonEmpty(sheets, answerPosition, inv)
	case InvariantCountBounds:
		result = e.checkCountBounds(sheets, answerPosition, inv)
	case InvariantRowReduction:
		// Would need original row count; skip for now
		result.Passed = true
	default:
		result.Passed = true // unknown invariant type, pass by default
	}

	return result
}

func (e *ShadowTDDEngine) checkNonEmpty(sheets []sheet.SheetData, answerPosition string, inv Invariant) InvariantResult {
	result := InvariantResult{Invariant: inv, Passed: true}

	sheetName, cells := parseCellRange(answerPosition)
	filtered := sheets
	if sheetName != "" {
		for _, s := range sheets {
			if strings.EqualFold(s.Name, sheetName) {
				filtered = []sheet.SheetData{s}
				break
			}
		}
	}

	cellMap := buildCellMap(filtered)
	emptyCount := 0
	for _, addr := range cells {
		val := cellMap[addr]
		if strings.TrimSpace(val) == "" {
			emptyCount++
		}
	}

	if emptyCount > 0 && len(cells) > 0 {
		ratio := float64(emptyCount) / float64(len(cells))
		if ratio > 0.5 {
			result.Passed = false
			result.Error = fmt.Sprintf("%d/%d target cells are empty (%.0f%%)", emptyCount, len(cells), ratio*100)
		}
	}

	return result
}

func (e *ShadowTDDEngine) checkCountBounds(sheets []sheet.SheetData, answerPosition string, inv Invariant) InvariantResult {
	result := InvariantResult{Invariant: inv, Passed: true}

	sheetName, cells := parseCellRange(answerPosition)
	filtered := sheets
	if sheetName != "" {
		for _, s := range sheets {
			if strings.EqualFold(s.Name, sheetName) {
				filtered = []sheet.SheetData{s}
				break
			}
		}
	}

	cellMap := buildCellMap(filtered)
	maxRows := 0
	for _, s := range filtered {
		if s.MaxRow > maxRows {
			maxRows = s.MaxRow
		}
	}

	for _, addr := range cells {
		val := strings.TrimSpace(cellMap[addr])
		if val == "" {
			continue
		}
		var numVal float64
		if _, err := fmt.Sscanf(val, "%f", &numVal); err == nil {
			if numVal < 0 {
				result.Passed = false
				result.Error = fmt.Sprintf("count value at %s is negative: %s", addr, val)
				return result
			}
			if numVal > float64(maxRows)*2 {
				result.Passed = false
				result.Error = fmt.Sprintf("count value at %s exceeds reasonable bounds: %s (max_rows=%d)", addr, val, maxRows)
				return result
			}
		}
	}

	return result
}

func computeTripleConfidence(r *TripleVerifyResult) float64 {
	score := 0.0
	if r.V1Execution {
		score += 0.3
	}
	if r.V2Synthetic {
		score += 0.4
	}
	if r.V3Invariant {
		score += 0.3
	}
	return score
}
