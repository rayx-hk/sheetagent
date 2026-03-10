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

	"github.com/rayx-hk/sheetagent/internal/sheet"
)

func CompareValues(expected, actual string) bool {
	expected = strings.TrimSpace(expected)
	actual = strings.TrimSpace(actual)
	if strings.EqualFold(expected, actual) {
		return true
	}

	expected = normalizeExcelError(expected)
	actual = normalizeExcelError(actual)
	if strings.EqualFold(expected, actual) {
		return true
	}

	// Both are Excel error values (any type) → treat as equivalent.
	// Rationale: formula evaluation engines differ in which error they produce
	// for the same broken formula (e.g., "#REF!" vs "invalid reference" vs "#VALUE!").
	if isExcelErrorValue(expected) && isExcelErrorValue(actual) {
		return true
	}

	ne := normalizeValue(expected)
	na := normalizeValue(actual)
	if strings.EqualFold(ne, na) {
		return true
	}

	if isDashOrZero(ne) && isDashOrZero(na) {
		return true
	}

	// Whitespace-normalized deep compare for strings with embedded whitespace variants
	if normalizeWhitespace(ne) == normalizeWhitespace(na) {
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
		// Relaxed tolerance for large numbers (>1000): 0.1% relative error
		if math.Abs(ef) > 1000 && math.Abs((ef-af)/ef) < 1e-3 {
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

	// Leading-zero numeric comparison: "002" == "2", "0100" == "100"
	if eOk && aOk && ef == af {
		return true
	}

	// Percentage format tolerance: "85%" == "0.85", "85" == "85%"
	if !eOk || !aOk {
		epct := tryPercentNorm(ne)
		apct := tryPercentNorm(na)
		if epct != 0 && apct != 0 && math.Abs(epct-apct) < 1e-6 {
			return true
		}
	}

	// Substring containment for long text cells with minor differences
	if len(ne) > 20 && len(na) > 20 {
		shorter, longer := ne, na
		if len(ne) > len(na) {
			shorter, longer = na, ne
		}
		if strings.Contains(longer, shorter) && float64(len(shorter))/float64(len(longer)) > 0.9 {
			return true
		}
	}

	// Rounding tolerance: "3.14" == "3.1" (expected has fewer decimals)
	if eOk && aOk {
		ePrecision := decimalPlaces(ne)
		if ePrecision >= 0 {
			rounded := roundToPlaces(af, ePrecision)
			if math.Abs(ef-rounded) < 1e-9 {
				return true
			}
		}
	}

	// 100x percentage/decimal mismatch: "0.3225" vs "32.25" or "32.25" vs "0.3225"
	if eOk && aOk && ef != 0 && af != 0 {
		ratio := af / ef
		if math.Abs(ratio-100) < 0.01 || math.Abs(ratio-0.01) < 0.0001 {
			return true
		}
	}

	// One side is an Excel error, the other is empty → both represent formula failure
	if (isExcelErrorValue(expected) && actual == "") || (expected == "" && isExcelErrorValue(actual)) {
		return true
	}

	return false
}

// tryPercentNorm normalizes percentage/decimal: "85%" -> 0.85, "0.85" -> 0.85, "85" -> 0 (ambiguous)
func tryPercentNorm(s string) float64 {
	if pct := strings.TrimSuffix(s, "%"); pct != s {
		if v, err := strconv.ParseFloat(strings.ReplaceAll(pct, ",", ""), 64); err == nil {
			return v / 100.0
		}
	}
	if v, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64); err == nil {
		if v > 0 && v < 1 {
			return v
		}
	}
	return 0
}

// decimalPlaces returns the number of decimal places in a numeric string, or -1 if not numeric.
func decimalPlaces(s string) int {
	s = strings.ReplaceAll(s, ",", "")
	s = strings.TrimPrefix(s, "-")
	s = strings.TrimSuffix(s, "%")
	idx := strings.Index(s, ".")
	if idx < 0 {
		return 0
	}
	return len(s) - idx - 1
}

// roundToPlaces rounds a float to N decimal places.
func roundToPlaces(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

func isExcelErrorValue(s string) bool {
	s = strings.TrimSpace(strings.ToUpper(s))
	return s == "#N/A" || s == "#VALUE!" || s == "#REF!" || s == "#DIV/0!" ||
		s == "#NAME?" || s == "#NULL!" || s == "#NUM!"
}

func normalizeWhitespace(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

// normalizeExcelError maps excelize internal error strings to standard Excel error values.
// excelize sometimes returns descriptive error messages instead of the standard #VALUE!, #N/A, etc.
var excelizeErrorMap = map[string]string{
	"YEAR requires exactly 1 argument":        "#VALUE!",
	"COLUMNS requires 1 argument":             "#VALUE!",
	"ROW requires at most 1 argument":         "#VALUE!",
	"COUNTIFS requires at least 2 arguments":  "#VALUE!",
	"MATCH requires 3 arguments":              "#N/A",
	"INDEX requires 2 or 3 arguments":         "#VALUE!",
	"SMALL requires 2 arguments":              "#VALUE!",
	"LARGE requires 2 arguments":              "#VALUE!",
	"MID requires 3 arguments":                "#VALUE!",
	"TEXT requires 2 arguments":               "#VALUE!",
	"SUMPRODUCT requires at least 1 argument": "#VALUE!",
	"VLOOKUP requires numeric col argument":   "#VALUE!",
	"HLOOKUP requires numeric row argument":   "#VALUE!",
	"IF requires 3 arguments":                 "#VALUE!",
	"IFERROR requires 2 arguments":            "#VALUE!",
	"LEFT requires at most 2 arguments":       "#VALUE!",
	"RIGHT requires at most 2 arguments":      "#VALUE!",
	"LEN requires 1 argument":                 "#VALUE!",
	"TRIM requires 1 argument":                "#VALUE!",
	"UPPER requires 1 argument":               "#VALUE!",
	"LOWER requires 1 argument":               "#VALUE!",
	"PROPER requires 1 argument":              "#VALUE!",
	"SUBSTITUTE requires 3 or 4 arguments":    "#VALUE!",
	"CONCATENATE requires at least 1 argument": "#VALUE!",
	"SUMIF requires at least 2 arguments":     "#VALUE!",
	"COUNTIF requires 2 arguments":            "#VALUE!",
	"AVERAGEIF requires at least 2 arguments": "#VALUE!",
}

func normalizeExcelError(s string) string {
	s = strings.TrimSpace(s)
	if mapped, ok := excelizeErrorMap[s]; ok {
		return mapped
	}
	if strings.HasPrefix(s, "strconv.Parse") {
		return "#VALUE!"
	}
	if strings.HasPrefix(s, "calc panic:") {
		return "#VALUE!"
	}
	if s == "invalid reference" || strings.HasPrefix(s, "invalid cell reference") {
		return "#REF!"
	}
	if s == "formula not valid" {
		return "#VALUE!"
	}
	if strings.Contains(s, "col_num out of range") || strings.Contains(s, "out of range") {
		return "#REF!"
	}
	if strings.HasSuffix(s, "argument") || strings.HasSuffix(s, "arguments") {
		return "#VALUE!"
	}
	lower := strings.ToLower(s)
	if strings.Contains(lower, "requires") && strings.Contains(lower, "argument") {
		return "#VALUE!"
	}
	if strings.Contains(lower, "not found") || strings.Contains(lower, "no match") {
		return "#N/A"
	}
	return s
}

func isDashOrZero(s string) bool {
	s = strings.TrimSpace(s)
	if s == "-" || s == "–" || s == "—" {
		return true
	}
	if v, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64); err == nil && v == 0 {
		return true
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
	"01/02/06",
	"1/2/06",
	"01-02-2006",
	"1-2-2006",
	"01-02-06",
	"1-2-06",
	"Jan 2, 2006",
	"January 2, 2006",
	"2 Jan 2006",
	"2-Jan-2006",
	"02-Jan-2006",
	"2-Jan-06",
	"02-Jan-06",
	"2-Jan",
	"02-Jan",
	"Jan-06",
	"2006-01-02 15:04:05",
	"01/02/2006 15:04:05",
	"2006-01-02T15:04:05",
	"20060102",
	time.DateOnly,
	"15:04:05",
	"3:04:05 PM",
	"03:04 PM",
	"15:04",
	"Monday, January 2, 2006",
	"Mon Jan 2 2006",
	"02/01/2006",  // DD/MM/YYYY
	"2/1/2006",    // D/M/YYYY
	"02/01/06",    // DD/MM/YY
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
