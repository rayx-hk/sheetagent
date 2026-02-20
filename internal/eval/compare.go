package eval

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rayx-hk/dataagent/internal/sheet"
)

func CompareValues(expected, actual string) bool {
	expected = strings.TrimSpace(expected)
	actual = strings.TrimSpace(actual)
	if strings.EqualFold(expected, actual) {
		return true
	}

	ne := normalizeValue(expected)
	na := normalizeValue(actual)
	if strings.EqualFold(ne, na) {
		return true
	}

	if (strings.Contains(ne, ",") && strings.Contains(na, ",")) || (strings.Contains(ne, ";") && strings.Contains(na, ";")) {
		if compareLists(ne, na) {
			return true
		}
	}

	ef, eOk := parseNumber(ne)
	af, aOk := parseNumber(na)
	if eOk && aOk {
		if math.Abs(ef-af) < 1e-4 {
			return true
		}
		if ef != 0 && math.Abs((ef-af)/ef) < 1e-4 {
			return true
		}
	}

	if et, ok := parseDate(expected); ok {
		if at, ok := parseDate(actual); ok {
			return et.Year() == at.Year() && et.Month() == at.Month() && et.Day() == at.Day()
		}
	}

	if eb, ok := parseBool(expected); ok {
		if ab, ok := parseBool(actual); ok {
			return eb == ab
		}
	}

	return false
}

func normalizeValue(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\u00a0", " ")
	s = strings.ReplaceAll(s, "\u200b", "")
	s = strings.TrimPrefix(s, "$")
	s = strings.TrimPrefix(s, "¥")
	s = strings.TrimPrefix(s, "€")
	s = strings.TrimPrefix(s, "£")
	s = strings.TrimSpace(s)
	return s
}

func compareLists(expected, actual string) bool {
	sep := ","
	if strings.Contains(expected, ";") {
		sep = ";"
	}

	eParts := strings.Split(expected, sep)
	aParts := strings.Split(actual, sep)

	if len(eParts) != len(aParts) {
		return false
	}

	var eClean, aClean []string
	for _, p := range eParts {
		eClean = append(eClean, normalizeValue(p))
	}
	for _, p := range aParts {
		aClean = append(aClean, normalizeValue(p))
	}

	sort.Strings(eClean)
	sort.Strings(aClean)

	for i := range eClean {
		if !strings.EqualFold(eClean[i], aClean[i]) {
			return false
		}
	}
	return true
}

var commaNumberRe = regexp.MustCompile(`^-?[\d,]+\.?\d*$`)
var accountingRe = regexp.MustCompile(`^\(([\d,]+\.?\d*)\)$`)

func parseNumber(s string) (float64, bool) {
	if pct := strings.TrimSuffix(s, "%"); pct != s {
		if v, err := strconv.ParseFloat(strings.ReplaceAll(pct, ",", ""), 64); err == nil {
			return v / 100.0, true
		}
	}

	if m := accountingRe.FindStringSubmatch(s); len(m) > 1 {
		s = "-" + m[1]
	}

	if commaNumberRe.MatchString(s) {
		s = strings.ReplaceAll(s, ",", "")
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return v, true
	}
	return 0, false
}

var dateFormats = []string{
	"2006-01-02",
	"2006/01/02",
	"01/02/2006",
	"1/2/2006",
	"01-02-2006",
	"1-2-2006",
	"Jan 2, 2006",
	"January 2, 2006",
	"2 Jan 2006",
	"02-Jan-2006",
	"02-Jan-06",
	"2006-01-02 15:04:05",
	"01/02/2006 15:04:05",
	"2006-01-02T15:04:05",
	"20060102",
	time.DateOnly,
	"15:04:05",
	"3:04:05 PM",
	"03:04 PM",
	"15:04",
}

func parseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range dateFormats {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && f > 25000 && f < 60000 {
		days := int(f)
		base := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
		return base.AddDate(0, 0, days), true
	}
	return time.Time{}, false
}

func parseBool(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "1":
		return true, true
	case "false", "no", "0":
		return false, true
	}
	return false, false
}

type CompareResult struct {
	Match      bool              `json:"match"`
	Total      int               `json:"total"`
	Matched    int               `json:"matched"`
	Mismatches []CellMismatch    `json:"mismatches,omitempty"`
}

type CellMismatch struct {
	Address  string `json:"address"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

func CompareFiles(_ context.Context, parser *sheet.ExcelizeParser, outputPath, answerPath, answerPosition string) *CompareResult {
	ctx := context.Background()
	outputData, err := parser.Parse(ctx, outputPath)
	if err != nil {
		return &CompareResult{Match: false, Mismatches: []CellMismatch{{Address: "error", Expected: "parse output", Actual: err.Error()}}}
	}
	answerData, err := parser.Parse(ctx, answerPath)
	if err != nil {
		return &CompareResult{Match: false, Mismatches: []CellMismatch{{Address: "error", Expected: "parse answer", Actual: err.Error()}}}
	}

	sheetName, cells := parseCellRange(answerPosition)
	if len(cells) == 0 {
		return compareAllCells(outputData, answerData)
	}

	filterBySheet := func(data []sheet.SheetData) []sheet.SheetData {
		if sheetName == "" {
			return data
		}
		for _, s := range data {
			if strings.EqualFold(s.Name, sheetName) {
				return []sheet.SheetData{s}
			}
		}
		return data
	}

	result := &CompareResult{}
	outputMap := buildCellMap(filterBySheet(outputData))
	answerMap := buildCellMap(filterBySheet(answerData))

	for _, addr := range cells {
		result.Total++
		expected := answerMap[addr]
		actual := outputMap[addr]
		if CompareValues(expected, actual) {
			result.Matched++
		} else {
			result.Mismatches = append(result.Mismatches, CellMismatch{
				Address:  addr,
				Expected: expected,
				Actual:   actual,
			})
		}
	}
	result.Match = len(result.Mismatches) == 0
	return result
}

func compareAllCells(output, answer []sheet.SheetData) *CompareResult {
	outputMap := buildCellMap(output)
	answerMap := buildCellMap(answer)

	result := &CompareResult{}
	for addr, expected := range answerMap {
		result.Total++
		actual := outputMap[addr]
		if CompareValues(expected, actual) {
			result.Matched++
		} else {
			result.Mismatches = append(result.Mismatches, CellMismatch{
				Address:  addr,
				Expected: expected,
				Actual:   actual,
			})
		}
	}
	result.Match = len(result.Mismatches) == 0
	return result
}

func buildCellMap(sheets []sheet.SheetData) map[string]string {
	m := make(map[string]string)
	for _, s := range sheets {
		for _, c := range s.Cells {
			addr := fmt.Sprintf("%s!R%dC%d", s.Name, c.Row, c.Col)
			m[addr] = c.Value
		}
	}
	if len(sheets) == 1 {
		for _, c := range sheets[0].Cells {
			addr := fmt.Sprintf("R%dC%d", c.Row, c.Col)
			m[addr] = c.Value
		}
	}
	return m
}

// parseCellRange supports formats: "A1:B5", "'Sheet1'!A1:B5", "Sheet1!A1:B5"
func parseCellRange(pos string) (sheetName string, cells []string) {
	if pos == "" {
		return "", nil
	}

	pos = strings.TrimSpace(pos)

	if idx := strings.LastIndex(pos, "!"); idx >= 0 {
		sheetName = strings.Trim(pos[:idx], "'")
		pos = pos[idx+1:]
	}

	parts := strings.Split(pos, ":")
	if len(parts) != 2 {
		return sheetName, []string{pos}
	}

	startCol, startRow := splitCellRef(parts[0])
	endCol, endRow := splitCellRef(parts[1])

	if startCol == 0 || startRow == 0 || endCol == 0 || endRow == 0 {
		return sheetName, []string{pos}
	}

	for r := startRow; r <= endRow; r++ {
		for c := startCol; c <= endCol; c++ {
			cells = append(cells, fmt.Sprintf("R%dC%d", r, c))
		}
	}
	return sheetName, cells
}

func splitCellRef(ref string) (col, row int) {
	ref = strings.TrimSpace(ref)
	colStr := ""
	rowStr := ""
	for _, ch := range ref {
		if ch >= 'A' && ch <= 'Z' {
			colStr += string(ch)
		} else if ch >= '0' && ch <= '9' {
			rowStr += string(ch)
		}
	}
	if colStr == "" || rowStr == "" {
		return 0, 0
	}

	col = 0
	for _, ch := range colStr {
		col = col*26 + int(ch-'A') + 1
	}
	fmt.Sscanf(rowStr, "%d", &row)
	return col, row
}
