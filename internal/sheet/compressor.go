package sheet

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

type CompressLevel int

const (
	CompressNone CompressLevel = iota
	CompressLight
	CompressAggressive
)

type CompressOptions struct {
	Level              CompressLevel
	SampleRows         int // Module 1: data rows per region
	IndexMinFreq       int // Module 2: min frequency to index
	IndexMinLen        int // Module 2: min string length to index
	AggregateThreshold int // Module 3: min rows before aggregating
}

var (
	LightOptions = CompressOptions{
		Level: CompressLight, SampleRows: 3, IndexMinFreq: 5, IndexMinLen: 8, AggregateThreshold: 20,
	}
	AggressiveOptions = CompressOptions{
		Level: CompressAggressive, SampleRows: 1, IndexMinFreq: 3, IndexMinLen: 5, AggregateThreshold: 10,
	}
)

type Compressor struct{}

func NewCompressor() *Compressor {
	return &Compressor{}
}

func (c *Compressor) CompressFull(ws *SheetData, opts CompressOptions) string {
	if ws == nil {
		return ""
	}

	cellsCount := ws.MaxRow * ws.MaxCol
	if cellsCount < 500 && opts.Level != CompressNone {
		opts = CompressOptions{Level: CompressNone} // Force no compression for very small sheets
	}

	var b strings.Builder

	// === Header: Sheet info ===
	activeRange := fmt.Sprintf("A1:%s%d", getColName(ws.MaxCol), ws.MaxRow)
	fmt.Fprintf(&b, "Sheet=%q rows=%d cols=%d range=%s\n",
		ws.Name, ws.MaxRow, ws.MaxCol, activeRange)

	headers := extractHeaders(*ws)
	if len(headers) > 0 {
		fmt.Fprintf(&b, "Headers: %s\n\n", strings.Join(headers, " | "))
	}

	if opts.Level == CompressNone {
		// Just dump the data
		for r := 1; r <= ws.MaxRow; r++ {
			rowValues := getRowValues(ws, r)
			hasData := false
			for _, v := range rowValues {
				if v != "" {
					hasData = true
					break
				}
			}
			if hasData {
				fmt.Fprintf(&b, "  r%d: %s\n", r, strings.Join(rowValues, " | "))
			}
		}
		return b.String()
	}

	// === Module 2: Build inverse index ===
	invIdx := buildInverseIndex(ws.Cells, headers, opts.IndexMinLen, opts.IndexMinFreq)
	if dict := invIdx.Dictionary(); dict != "" {
		fmt.Fprintf(&b, "%s\n\n", dict)
	}

	// === Module 3: Column profiles ===
	b.WriteString("Column profiles:\n")
	headerRow := 1 // Assume row 1 for header simplify
	for i, header := range headers {
		profile := profileColumn(ws, i+1, headerRow)
		profile.Name = header
		fmt.Fprintf(&b, "  %s (%s): %s\n", header, getColName(i+1), profile.Encode())
	}
	b.WriteString("\n")

	// === Module 1: Anchor rows with sampled data ===
	if ws.MaxRow <= opts.AggregateThreshold {
		b.WriteString("Data (all rows):\n")
		for row := headerRow + 1; row <= ws.MaxRow; row++ {
			rowData := getRowValues(ws, row)
			translated := make([]string, len(rowData))
			for i, v := range rowData {
				translated[i] = invIdx.Translate(v)
			}
			fmt.Fprintf(&b, "  r%d: %s\n", row, strings.Join(translated, " | "))
		}
	} else {
		b.WriteString("Structure:\n")
		anchors := detectAnchorRows(ws, opts.SampleRows)
		for _, anchor := range anchors {
			translated := make([]string, len(anchor.Data))
			for i, v := range anchor.Data {
				translated[i] = invIdx.Translate(v)
			}
			fmt.Fprintf(&b, "  [%s row=%d] %s\n",
				anchorTypeName(anchor.Type), anchor.RowNum,
				strings.Join(translated, " | "))
		}
	}

	return b.String()
}

// -------------------------------------------------------------
// Module 1: Structural Anchor
// -------------------------------------------------------------

type AnchorType int

const (
	AnchorHeader AnchorType = iota
	AnchorSubtotal
	AnchorSectionBreak
	AnchorDataSample
	AnchorEmpty
)

func anchorTypeName(t AnchorType) string {
	switch t {
	case AnchorHeader:
		return "HEADER"
	case AnchorSubtotal:
		return "SUBTOTAL"
	case AnchorSectionBreak:
		return "SECTION"
	case AnchorDataSample:
		return "DATA"
	default:
		return "EMPTY"
	}
}

type AnchorRow struct {
	RowNum int
	Type   AnchorType
	Data   []string
}

func detectAnchorRows(ws *SheetData, sampleN int) []AnchorRow {
	var anchors []AnchorRow
	sampleCount := 0

	for rowIdx := 1; rowIdx <= ws.MaxRow; rowIdx++ {
		rowCells := getCellsInRow(ws, rowIdx)
		if len(rowCells) == 0 {
			continue
		}

		vals := toValues(rowCells)

		switch {
		case isHeaderRow(rowCells):
			anchors = append(anchors, AnchorRow{RowNum: rowIdx, Type: AnchorHeader, Data: vals})
			sampleCount = 0
		case isSubtotalRow(rowCells):
			anchors = append(anchors, AnchorRow{RowNum: rowIdx, Type: AnchorSubtotal, Data: vals})
			sampleCount = 0
		case isEmptyRow(rowCells):
			// skip
		default:
			if sampleCount < sampleN {
				anchors = append(anchors, AnchorRow{RowNum: rowIdx, Type: AnchorDataSample, Data: vals})
				sampleCount++
			}
		}
	}
	return anchors
}

func getCellsInRow(ws *SheetData, r int) []CellValue {
	var res []CellValue
	for _, c := range ws.Cells {
		if c.Row == r {
			res = append(res, c)
		}
	}
	return res
}

func getRowValues(ws *SheetData, r int) []string {
	cells := getCellsInRow(ws, r)
	if len(cells) == 0 {
		return []string{}
	}
	maxC := 0
	for _, c := range cells {
		if c.Col > maxC {
			maxC = c.Col
		}
	}
	res := make([]string, maxC)
	for _, c := range cells {
		res[c.Col-1] = c.Value
	}
	return res
}

func toValues(cells []CellValue) []string {
	if len(cells) == 0 {
		return []string{}
	}
	maxC := 0
	for _, c := range cells {
		if c.Col > maxC {
			maxC = c.Col
		}
	}
	res := make([]string, maxC)
	for _, c := range cells {
		res[c.Col-1] = c.Value
	}
	return res
}

func isHeaderRow(cells []CellValue) bool {
	total := len(cells)
	if total == 0 {
		return false
	}
	textCount := 0
	for _, c := range cells {
		if c.Type == "string" && len(strings.TrimSpace(c.Value)) > 0 {
			textCount++
		}
	}
	return float64(textCount)/float64(total) >= 0.7
}

func isSubtotalRow(cells []CellValue) bool {
	for _, c := range cells {
		if c.Type == "formula" {
			upper := strings.ToUpper(c.Value)
			if strings.Contains(upper, "SUM") || strings.Contains(upper, "SUBTOTAL") ||
				strings.Contains(upper, "AVERAGE") || strings.Contains(upper, "TOTAL") {
				return true
			}
		}
	}
	return false
}

func isEmptyRow(cells []CellValue) bool {
	for _, c := range cells {
		if strings.TrimSpace(c.Value) != "" {
			return false
		}
	}
	return true
}

// -------------------------------------------------------------
// Module 2: Inverse Index Translation
// -------------------------------------------------------------

type InverseIndex struct {
	IndexToValue map[int]string
	ValueToIndex map[string]int
	nextIdx      int
}

func buildInverseIndex(cells []CellValue, headers []string, minLen, minFreq int) *InverseIndex {
	idx := &InverseIndex{
		IndexToValue: make(map[int]string),
		ValueToIndex: make(map[string]int),
	}

	freq := make(map[string]int)
	headerSet := make(map[string]bool)
	for _, h := range headers {
		headerSet[strings.TrimSpace(strings.ToLower(h))] = true
	}

	for _, c := range cells {
		if c.Type == "string" && len(c.Value) >= minLen {
			normalized := strings.TrimSpace(c.Value)
			if !headerSet[strings.ToLower(normalized)] {
				freq[normalized]++
			}
		}
	}

	for val, count := range freq {
		if count >= minFreq {
			idx.ValueToIndex[val] = idx.nextIdx
			idx.IndexToValue[idx.nextIdx] = val
			idx.nextIdx++
		}
	}
	return idx
}

func (idx *InverseIndex) Translate(value string) string {
	if i, ok := idx.ValueToIndex[strings.TrimSpace(value)]; ok {
		return fmt.Sprintf("@%d", i)
	}
	return value
}

func (idx *InverseIndex) Dictionary() string {
	if len(idx.IndexToValue) == 0 {
		return ""
	}
	var parts []string
	for i := 0; i < idx.nextIdx; i++ {
		if val, ok := idx.IndexToValue[i]; ok {
			parts = append(parts, fmt.Sprintf("@%d=%q", i, val))
		}
	}
	return "INDEX: " + strings.Join(parts, ", ")
}

// -------------------------------------------------------------
// Module 3: Data-Format-Aware Aggregation
// -------------------------------------------------------------

type ColumnProfile struct {
	Letter    string
	Name      string // header value
	DataType  string // number | date | string | formula | empty
	Count     int    // non-empty cell count
	NullCount int
	// For numbers:
	Min, Max, Mean, Sum float64
	Precision           int // max decimal places observed
	// For dates:
	DateMin, DateMax string
	DateFormat       string
	// For strings:
	UniqueValues []string
	Cardinality  int
	// For formulas:
	FormulaExample string
}

func profileColumn(ws *SheetData, colIdx int, headerRow int) ColumnProfile {
	var values []CellValue
	for _, cell := range ws.Cells {
		if cell.Col == colIdx && cell.Row > headerRow {
			values = append(values, cell)
		}
	}

	p := ColumnProfile{Count: len(values)}
	if len(values) == 0 {
		p.DataType = "empty"
		p.NullCount = ws.MaxRow - headerRow
		return p
	}

	typeCount := map[string]int{}
	for _, v := range values {
		typeCount[v.Type]++
	}
	dominant := dominantType(typeCount)
	p.DataType = dominant

	switch dominant {
	case "number":
		p.Min, p.Max, p.Mean, p.Sum, p.Precision = computeNumberStats(values)
	case "date": // we don't have explicit date type detection yet, but assume
		p.DateMin, p.DateMax, p.DateFormat = computeDateRange(values)
	case "string":
		p.UniqueValues, p.Cardinality = computeStringStats(values)
	case "formula":
		p.FormulaExample = findFirstFormula(values)
	}

	return p
}

func computeNumberStats(values []CellValue) (min, max, mean, sum float64, precision int) {
	count := 0
	min = math.MaxFloat64
	max = -math.MaxFloat64

	for _, v := range values {
		if v.Type == "number" {
			f, err := strconv.ParseFloat(v.Value, 64)
			if err == nil {
				if f < min {
					min = f
				}
				if f > max {
					max = f
				}
				sum += f
				count++
				parts := strings.Split(v.Value, ".")
				if len(parts) > 1 && len(parts[1]) > precision {
					precision = len(parts[1])
				}
			}
		}
	}
	if count > 0 {
		mean = sum / float64(count)
	} else {
		min = 0
		max = 0
	}
	return
}

func computeDateRange(values []CellValue) (min, max, format string) {
	// A naive implementation since real dates might be floats in xlsx
	for _, v := range values {
		if v.Value != "" {
			if min == "" || v.Value < min {
				min = v.Value
			}
			if max == "" || v.Value > max {
				max = v.Value
			}
		}
	}
	return min, max, "YYYY-MM-DD"
}

func computeStringStats(values []CellValue) ([]string, int) {
	set := make(map[string]bool)
	for _, v := range values {
		if v.Type == "string" && v.Value != "" {
			set[v.Value] = true
		}
	}
	var unique []string
	for k := range set {
		if len(unique) < 10 {
			unique = append(unique, k)
		}
	}
	return unique, len(set)
}

func findFirstFormula(values []CellValue) string {
	for _, v := range values {
		if v.Type == "formula" {
			return v.Value
		}
	}
	return ""
}

func (p ColumnProfile) Encode() string {
	switch p.DataType {
	case "number":
		if p.Precision == 0 {
			return fmt.Sprintf("NUM[%d..%d avg=%.0f sum=%.0f n=%d]",
				int(p.Min), int(p.Max), p.Mean, p.Sum, p.Count)
		}
		return fmt.Sprintf("NUM[%.2f..%.2f avg=%.2f n=%d]",
			p.Min, p.Max, p.Mean, p.Count)
	case "date":
		return fmt.Sprintf("DATE[%s..%s n=%d]", p.DateMin, p.DateMax, p.Count)
	case "string":
		if p.Cardinality <= 10 {
			return fmt.Sprintf("CAT[%s n=%d]", strings.Join(p.UniqueValues, "|"), p.Count)
		}
		return fmt.Sprintf("STR[cardinality=%d n=%d]", p.Cardinality, p.Count)
	case "formula":
		return fmt.Sprintf("FORMULA[e.g. %s n=%d]", p.FormulaExample, p.Count)
	default:
		return fmt.Sprintf("EMPTY[n=%d]", p.NullCount)
	}
}

// extractHeaders extracts headers from the first row of SheetData
func extractHeaders(ws SheetData) []string {
	headerMap := make(map[int]string)
	maxCol := 0
	for _, cell := range ws.Cells {
		if cell.Row == 1 && cell.Value != "" {
			headerMap[cell.Col] = cell.Value
			if cell.Col > maxCol {
				maxCol = cell.Col
			}
		}
	}
	if len(headerMap) == 0 {
		return nil
	}
	headers := make([]string, 0, maxCol)
	for i := 1; i <= maxCol; i++ {
		headers = append(headers, headerMap[i])
	}
	return headers
}


func getColName(col int) string {
	name, _ := excelize.ColumnNumberToName(col)
	return name
}

func parseRange(answerPos string) (startCol, startRow, endCol, endRow int) {
	// e.g. "B3:D14" or "Sheet2!B3:D14"
	ap := answerPos
	if idx := strings.Index(ap, "!"); idx != -1 {
		ap = ap[idx+1:]
	}
	ap = strings.ReplaceAll(ap, "$", "")
	parts := strings.Split(ap, ":")
	if len(parts) == 1 {
		parts = append(parts, parts[0])
	}
	startColName, startRowStr := splitCellName(parts[0])
	endColName, endRowStr := splitCellName(parts[1])

	startCol, _ = excelize.ColumnNameToNumber(startColName)
	endCol, _ = excelize.ColumnNameToNumber(endColName)
	startRow, _ = strconv.Atoi(startRowStr)
	endRow, _ = strconv.Atoi(endRowStr)
	return
}

func splitCellName(cell string) (string, string) {
	re := regexp.MustCompile(`([a-zA-Z]+)(\d+)`)
	matches := re.FindStringSubmatch(cell)
	if len(matches) == 3 {
		return matches[1], matches[2]
	}
	return "", ""
}

// Extract answer region exactly
func (c *Compressor) CompressAnswerRegion(ws *SheetData, answerPos string) string {
	if ws == nil || answerPos == "" {
		return ""
	}
	startCol, startRow, endCol, endRow := parseRange(answerPos)

	var b strings.Builder
	fmt.Fprintf(&b, "=== Answer Region: %s ===\n", answerPos)

	fmt.Fprintf(&b, "Current content:\n")
	for row := startRow; row <= endRow; row++ {
		for col := startCol; col <= endCol; col++ {
			val := getCellValue(ws, row, col)
			addr, _ := excelize.CoordinatesToCellName(col, row)
			fmt.Fprintf(&b, "  %s: %q\n", addr, val)
		}
	}

	headerRow := 1 // approx
	// Context above
	b.WriteString("Context above:\n")
	for row := max(headerRow, startRow-5); row < startRow; row++ {
		fmt.Fprintf(&b, "  row%d: %s\n", row, strings.Join(getRowValues(ws, row), " | "))
	}

	return b.String()
}

func getCellValue(ws *SheetData, r, c int) string {
	for _, cell := range ws.Cells {
		if cell.Row == r && cell.Col == c {
			return cell.Value
		}
	}
	return ""
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
