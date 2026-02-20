package main

import (
	"fmt"
	"os"

	"github.com/xuri/excelize/v2"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run main.go <file.xlsx>")
		os.Exit(1)
	}
	filePath := os.Args[1]

	f, err := excelize.OpenFile(filePath)
	if err != nil {
		fmt.Printf("Error opening file: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	for _, name := range f.GetSheetList() {
		rows, err := f.GetRows(name)
		if err != nil {
			fmt.Printf("Error getting rows for %s: %v\n", name, err)
			continue
		}

		for rowIdx, row := range rows {
			for colIdx, val := range row {
				cellRef, _ := excelize.CoordinatesToCellName(colIdx+1, rowIdx+1)
				formula, _ := f.GetCellFormula(name, cellRef)

				if formula != "" && val == "" {
					fmt.Printf("Calculating %s!%s (Formula: %s)\n", name, cellRef, formula)
					calcVal, err := f.CalcCellValue(name, cellRef)
					if err != nil {
						fmt.Printf("  Error calculating: %v\n", err)
					} else {
						fmt.Printf("  Result: %v\n", calcVal)
					}
				}
			}
		}
	}
	fmt.Println("Done")
}
