package sheet

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"
)

// GridEncoding is the SpreadsheetLLM-inspired coordinate-compressed JSON
// representation of a sheet. It replaces Markdown with structured metadata
// that explicitly models boundaries, column types, merged cells, and anchors.
type GridEncoding struct {
	Sheet      string              `json:"sheet"`
	Rows       int                 `json:"rows"`
	Cols       int                 `json:"cols"`
	Boundaries GridBoundaries      `json:"boundaries"`
	Headers    []GridHeader        `json:"headers"`
	MergedCells []GridMergedCell   `json:"merged_cells,omitempty"`
	SampleRows []GridSampleRow     `json:"sample_rows,omitempty"`
	ColumnStats []GridColumnStat   `json:"column_stats,omitempty"`
}

type GridBoundaries struct {
	HeaderRow int `json:"header_row"`
	DataStart int `json:"data_start"`
	DataEnd   int `json:"data_end"`
}

type GridHeader struct {
	Col    int    `json:"col"`
	Letter string `json:"letter"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Sample string `json:"sample,omitempty"`
}

type GridMergedCell struct {
	Range string `json:"range"`
	Value string `json:"value"`
}

type GridSampleRow struct {
	Row    int      `json:"row"`
	Values []string `json:"values"`
}

type GridColumnStat struct {
	Col         int      `json:"col"`
	Letter      string   `json:"letter"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Count       int      `json:"count"`
	Min         *float64 `json:"min,omitempty"`
	Max         *float64 `json:"max,omitempty"`
	Cardinality int      `json:"cardinality,omitempty"`
	Categories  []string `json:"categories,omitempty"`
	DateRange   string   `json:"date_range,omitempty"`
}

// CompressGridAware produces the SpreadsheetLLM-inspired JSON representation.
func (c *Compressor) CompressGridAware(ws *SheetData, opts CompressOptions) string {
	if ws == nil {
		return ""
	}

	enc := GridEncoding{
		Sheet: ws.Name,
		Rows:  ws.MaxRow,
		Cols:  ws.MaxCol,
	}

	// Detect boundaries
	headerRow := detectHeaderRow(ws)
	enc.Boundaries = GridBoundaries{
		HeaderRow: headerRow,
		DataStart: headerRow + 1,
		DataEnd:   ws.MaxRow,
	}

	// Extract headers
	headers := extractHeaders(*ws)
	for i, h := range headers {
		colIdx := i + 1
		colLetter, _ := excelize.ColumnNumberToName(colIdx)
		sample := ""
		for _, cell := range ws.Cells {
			if cell.Col == colIdx && cell.Row > headerRow && cell.Value != "" {
				sample = cell.Value
				break
			}
		}
		profile := profileColumn(ws, colIdx, headerRow)
		enc.Headers = append(enc.Headers, GridHeader{
			Col:    colIdx,
			Letter: colLetter,
			Name:   h,
			Type:   profile.DataType,
			Sample: sample,
		})
	}

	// Merged cells (read from formulas if available, otherwise skip)
	// Note: merged cell info isn't in SheetData directly, so we skip for now
	// and rely on the file-level parser to provide this if needed.

	// Sample rows
	sampleCount := opts.SampleRows
	if sampleCount <= 0 {
		sampleCount = 3
	}
	added := 0
	for r := enc.Boundaries.DataStart; r <= enc.Boundaries.DataEnd && added < sampleCount; r++ {
		vals := getRowValues(ws, r)
		hasContent := false
		for _, v := range vals {
			if v != "" {
				hasContent = true
				break
			}
		}
		if hasContent {
			enc.SampleRows = append(enc.SampleRows, GridSampleRow{Row: r, Values: vals})
			added++
		}
	}

	// Column stats
	for i, h := range headers {
		colIdx := i + 1
		colLetter, _ := excelize.ColumnNumberToName(colIdx)
		profile := profileColumn(ws, colIdx, headerRow)

		stat := GridColumnStat{
			Col:    colIdx,
			Letter: colLetter,
			Name:   h,
			Type:   profile.DataType,
			Count:  profile.Count,
		}
		switch profile.DataType {
		case "number":
			stat.Min = &profile.Min
			stat.Max = &profile.Max
		case "string":
			stat.Cardinality = profile.Cardinality
			if profile.Cardinality <= 10 {
				stat.Categories = profile.UniqueValues
			}
		case "date":
			if profile.DateMin != "" {
				stat.DateRange = fmt.Sprintf("%s..%s", profile.DateMin, profile.DateMax)
			}
		}
		enc.ColumnStats = append(enc.ColumnStats, stat)
	}

	data, err := json.MarshalIndent(enc, "", "  ")
	if err != nil {
		return fmt.Sprintf("grid encoding error: %v", err)
	}
	return string(data)
}

// CompressGridAwareFull produces a combined output: Grid-Aware JSON for the
// structure overview, plus the answer region detail.
func (c *Compressor) CompressGridAwareFull(ws *SheetData, answerPos string, opts CompressOptions) string {
	var b strings.Builder
	b.WriteString("=== Sheet Structure (Grid-Aware) ===\n")
	b.WriteString(c.CompressGridAware(ws, opts))
	b.WriteString("\n\n")
	if answerPos != "" {
		b.WriteString(c.CompressAnswerRegion(ws, answerPos))
	}
	return b.String()
}

func detectHeaderRow(ws *SheetData) int {
	for r := 1; r <= minInt(5, ws.MaxRow); r++ {
		cells := getCellsInRow(ws, r)
		if isHeaderRow(cells) {
			return r
		}
	}
	return 1
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
