package sheet

import (
	"context"
	"fmt"

	"github.com/xuri/excelize/v2"
)

type CellSnapshot struct {
	Address string `json:"address"`
	Before  string `json:"before"`
	After   string `json:"after"`
}

type ChangeLog struct {
	SheetName string         `json:"sheet_name"`
	Changes   []CellSnapshot `json:"changes"`
}

func TakeSnapshot(_ context.Context, filepath string) (map[string]map[string]string, error) {
	f, err := excelize.OpenFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", filepath, err)
	}
	defer f.Close()

	snapshot := make(map[string]map[string]string)
	for _, name := range f.GetSheetList() {
		cells := make(map[string]string)
		rows, err := f.GetRows(name)
		if err != nil {
			continue
		}
		for rowIdx, row := range rows {
			for colIdx, val := range row {
				ref, _ := excelize.CoordinatesToCellName(colIdx+1, rowIdx+1)
				cells[ref] = val
			}
		}
		snapshot[name] = cells
	}
	return snapshot, nil
}

func DiffSnapshots(before, after map[string]map[string]string) []ChangeLog {
	var logs []ChangeLog

	allSheets := make(map[string]bool)
	for s := range before {
		allSheets[s] = true
	}
	for s := range after {
		allSheets[s] = true
	}

	for sheet := range allSheets {
		bc := before[sheet]
		ac := after[sheet]
		if bc == nil {
			bc = make(map[string]string)
		}
		if ac == nil {
			ac = make(map[string]string)
		}

		var changes []CellSnapshot
		allCells := make(map[string]bool)
		for c := range bc {
			allCells[c] = true
		}
		for c := range ac {
			allCells[c] = true
		}
		for cell := range allCells {
			bv := bc[cell]
			av := ac[cell]
			if bv != av {
				changes = append(changes, CellSnapshot{
					Address: cell,
					Before:  bv,
					After:   av,
				})
			}
		}
		if len(changes) > 0 {
			logs = append(logs, ChangeLog{
				SheetName: sheet,
				Changes:   changes,
			})
		}
	}
	return logs
}
