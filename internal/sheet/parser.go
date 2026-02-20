package sheet

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

type CellValue struct {
	Row   int    `json:"row"`
	Col   int    `json:"col"`
	Value string `json:"value"`
	Type  string `json:"type"` // string | number | formula | date | bool | empty
}

type SheetData struct {
	Name     string
	MaxRow   int
	MaxCol   int
	Cells    []CellValue
	Formulas []CellValue
}

type Parser interface {
	Parse(ctx context.Context, filepath string) ([]SheetData, error)
}

type ExcelizeParser struct{}

func NewParser() *ExcelizeParser {
	return &ExcelizeParser{}
}

func (p *ExcelizeParser) Parse(_ context.Context, filepath string) ([]SheetData, error) {
	f, err := excelize.OpenFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("open xlsx %s: %w", filepath, err)
	}
	defer f.Close()

	var sheets []SheetData
	for _, name := range f.GetSheetList() {
		sd, err := p.parseSheet(f, name)
		if err != nil {
			return nil, fmt.Errorf("parse sheet %s: %w", name, err)
		}
		sheets = append(sheets, sd)
	}
	return sheets, nil
}

func (p *ExcelizeParser) parseSheet(f *excelize.File, name string) (SheetData, error) {
	rows, err := f.GetRows(name)
	if err != nil {
		return SheetData{}, err
	}

	sd := SheetData{
		Name:   name,
		MaxRow: len(rows),
	}

	for rowIdx, row := range rows {
		if len(row) > sd.MaxCol {
			sd.MaxCol = len(row)
		}
		for colIdx, val := range row {
			cellRef, _ := excelize.CoordinatesToCellName(colIdx+1, rowIdx+1)
			formula, _ := f.GetCellFormula(name, cellRef)

			cv := CellValue{
				Row:   rowIdx + 1,
				Col:   colIdx + 1,
				Value: val,
				Type:  detectCellType(val, formula),
			}
			sd.Cells = append(sd.Cells, cv)

			if formula != "" {
				sd.Formulas = append(sd.Formulas, CellValue{
					Row:   rowIdx + 1,
					Col:   colIdx + 1,
					Value: formula,
					Type:  "formula",
				})
			}
		}
	}
	return sd, nil
}

func (p *ExcelizeParser) ParseOverview(_ context.Context, filepath string) (*SpreadsheetOverview, error) {
	info, err := os.Stat(filepath)
	if err != nil {
		return nil, err
	}

	f, err := excelize.OpenFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("open xlsx %s: %w", filepath, err)
	}
	defer f.Close()

	overview := &SpreadsheetOverview{
		FilePath:   filepath,
		FileSize:   info.Size(),
		SheetNames: f.GetSheetList(),
	}

	totalCells := 0
	for _, name := range overview.SheetNames {
		si, cells, err := p.parseSheetInfo(f, name)
		if err != nil {
			return nil, err
		}
		overview.Sheets = append(overview.Sheets, si)
		totalCells += cells
	}
	overview.TotalCells = totalCells
	return overview, nil
}

func (p *ExcelizeParser) parseSheetInfo(f *excelize.File, name string) (SheetInfo, int, error) {
	rows, err := f.GetRows(name)
	if err != nil {
		return SheetInfo{}, 0, err
	}

	si := SheetInfo{
		Name:      name,
		RowCount:  len(rows),
		DataTypes: make(map[string]string),
	}

	mergedCells, _ := f.GetMergeCells(name)
	for _, mc := range mergedCells {
		si.MergedCells = append(si.MergedCells, mc.GetStartAxis()+":"+mc.GetEndAxis())
	}

	cellCount := 0
	colTypes := make(map[int]map[string]int)

	for rowIdx, row := range rows {
		if len(row) > si.ColCount {
			si.ColCount = len(row)
		}

		if rowIdx == 0 && len(si.Headers) == 0 {
			hasContent := false
			for _, v := range row {
				if strings.TrimSpace(v) != "" {
					hasContent = true
					break
				}
			}
			if hasContent {
				si.Headers = make([]string, len(row))
				copy(si.Headers, row)
			}
		}

		if rowIdx < 5 {
			si.SampleRows = append(si.SampleRows, row)
		}

		for colIdx, val := range row {
			if val != "" {
				cellCount++
			}
			cellRef, _ := excelize.CoordinatesToCellName(colIdx+1, rowIdx+1)
			formula, _ := f.GetCellFormula(name, cellRef)
			ct := detectCellType(val, formula)

			if colTypes[colIdx] == nil {
				colTypes[colIdx] = make(map[string]int)
			}
			if ct != "empty" {
				colTypes[colIdx][ct]++
			}
		}
	}

	for colIdx, types := range colTypes {
		colName, _ := excelize.ColumnNumberToName(colIdx + 1)
		dominant := dominantType(types)
		si.DataTypes[colName] = dominant
	}

	if si.RowCount > 0 && si.ColCount > 0 {
		topLeft, _ := excelize.CoordinatesToCellName(1, 1)
		botRight, _ := excelize.CoordinatesToCellName(si.ColCount, si.RowCount)
		si.ActiveRange = topLeft + ":" + botRight
	}

	return si, cellCount, nil
}

func detectCellType(val, formula string) string {
	if formula != "" {
		return "formula"
	}
	if val == "" {
		return "empty"
	}
	if _, err := strconv.ParseFloat(val, 64); err == nil {
		return "number"
	}
	lower := strings.ToLower(val)
	if lower == "true" || lower == "false" {
		return "bool"
	}
	return "string"
}

func dominantType(types map[string]int) string {
	maxCount := 0
	result := "string"
	for t, c := range types {
		if c > maxCount {
			maxCount = c
			result = t
		}
	}
	return result
}
