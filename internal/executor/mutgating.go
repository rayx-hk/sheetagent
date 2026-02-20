package executor

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// CellRange represents a rectangular cell range (minCol, minRow) to (maxCol, maxRow).
// All coordinates are 1-based.
type CellRange struct {
	Sheet  string // empty means any sheet
	MinCol int
	MinRow int
	MaxCol int
	MaxRow int
}

// changeLogEntry represents one entry in CHANGE_LOG.changes.
type changeLogEntry struct {
	Sheet  string `json:"sheet"`
	Range  string `json:"range"`
	Action string `json:"action"`
}

// changeLogStruct represents the CHANGE_LOG format from the prompt.
type changeLogStruct struct {
	Changes []changeLogEntry `json:"changes"`
}

var (
	rangeRegex     = regexp.MustCompile(`^([A-Za-z]+)(\d+):([A-Za-z]+)(\d+)$`)
	singleCellRe   = regexp.MustCompile(`^([A-Za-z]+)(\d+)$`)
	sheetRangeRe   = regexp.MustCompile(`^'?([^!']+)'?!\s*(.+)$`)
)

func colLettersToNum(s string) int {
	s = strings.ToUpper(strings.TrimSpace(s))
	n := 0
	for _, c := range s {
		if c >= 'A' && c <= 'Z' {
			n = n*26 + int(c-'A') + 1
		}
	}
	return n
}

// parseRange parses "B3:B14" or "B3" into CellRange. Returns nil if unparseable.
func parseRange(r string) *CellRange {
	r = strings.TrimSpace(strings.ReplaceAll(r, "$", ""))
	if m := rangeRegex.FindStringSubmatch(r); len(m) == 5 {
		minCol := colLettersToNum(m[1])
		minRow := parseInt(m[2])
		maxCol := colLettersToNum(m[3])
		maxRow := parseInt(m[4])
		if minRow > 0 && maxRow > 0 && minCol > 0 && maxCol > 0 {
			return normalizeRect(minCol, minRow, maxCol, maxRow)
		}
	}
	if m := singleCellRe.FindStringSubmatch(r); len(m) == 3 {
		col := colLettersToNum(m[1])
		row := parseInt(m[2])
		if row > 0 && col > 0 {
			return &CellRange{MinCol: col, MinRow: row, MaxCol: col, MaxRow: row}
		}
	}
	return nil
}

func parseInt(s string) int {
	var n int
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}

func normalizeRect(c1, r1, c2, r2 int) *CellRange {
	minC, maxC := c1, c2
	if minC > maxC {
		minC, maxC = maxC, minC
	}
	minR, maxR := r1, r2
	if minR > maxR {
		minR, maxR = maxR, minR
	}
	return &CellRange{MinCol: minC, MinRow: minR, MaxCol: maxC, MaxRow: maxR}
}

// parseAnswerPosition parses answer_position into one or more CellRanges.
// Supports: "B3:B14", "Sheet1!B3:B14", "B3", "A1:A10,C1:C10".
func parseAnswerPosition(ap string) []*CellRange {
	ap = strings.TrimSpace(ap)
	if ap == "" {
		return nil
	}
	var sheet string
	if m := sheetRangeRe.FindStringSubmatch(ap); len(m) == 3 {
		sheet = strings.Trim(m[1], "'")
		ap = strings.TrimSpace(m[2])
	}
	var out []*CellRange
	for _, part := range strings.Split(ap, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		r := parseRange(part)
		if r != nil {
			r.Sheet = sheet
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Intersects returns true if a and b overlap.
func (a *CellRange) Intersects(b *CellRange) bool {
	if a == nil || b == nil {
		return false
	}
	if a.Sheet != "" && b.Sheet != "" && !strings.EqualFold(a.Sheet, b.Sheet) {
		return false
	}
	return a.MinCol <= b.MaxCol && a.MaxCol >= b.MinCol &&
		a.MinRow <= b.MaxRow && a.MaxRow >= b.MinRow
}

// ValidateMutationGating checks that at least one change in changeLog intersects
// with answerPosition. Returns an error message for the LLM if validation fails.
func ValidateMutationGating(changeLog interface{}, answerPosition string) (errMsg string) {
	if answerPosition == "" {
		return ""
	}
	targetRanges := parseAnswerPosition(answerPosition)
	if len(targetRanges) == 0 {
		return ""
	}

	changes := extractChanges(changeLog)
	if len(changes) == 0 {
		return fmt.Sprintf("Mutation Error: No CHANGE_LOG changes found. The target answer_position is %s. Your code must declare CHANGE_LOG with changes that include this range.", answerPosition)
	}

	var modifiedRanges []string
	for _, c := range changes {
		rng := c.Range
		if rng == "" {
			continue
		}
		modifiedRanges = append(modifiedRanges, rng)
		cr := parseRange(rng)
		if cr == nil {
			continue
		}
		cr.Sheet = c.Sheet
		for _, tr := range targetRanges {
			if cr.Intersects(tr) {
				return ""
			}
		}
	}

	return fmt.Sprintf("Mutation Error: You modified %s, but the target answer_position is %s. Please correct your coordinates so that your changes overlap with the target range.", formatRanges(modifiedRanges), answerPosition)
}

func extractChanges(cl interface{}) []changeLogEntry {
	if cl == nil {
		return nil
	}
	switch v := cl.(type) {
	case map[string]interface{}:
		if x, ok := v["changes"]; ok {
			return extractChangesList(x)
		}
		return nil
	default:
		var s changeLogStruct
		b, _ := json.Marshal(cl)
		if json.Unmarshal(b, &s) == nil {
			return s.Changes
		}
		return nil
	}
}

func extractChangesList(x interface{}) []changeLogEntry {
	var out []changeLogEntry
	switch v := x.(type) {
	case []interface{}:
		for _, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				e := changeLogEntry{}
				if s, ok := m["sheet"].(string); ok {
					e.Sheet = s
				}
				if s, ok := m["range"].(string); ok {
					e.Range = s
				}
				if s, ok := m["action"].(string); ok {
					e.Action = s
				}
				out = append(out, e)
			}
		}
	}
	return out
}

func formatRanges(ranges []string) string {
	if len(ranges) == 0 {
		return "(none)"
	}
	if len(ranges) == 1 {
		return ranges[0]
	}
	return strings.Join(ranges, ", ")
}
