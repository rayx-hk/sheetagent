//go:build ignore

package main

import (
	"fmt"
	"github.com/xuri/excelize/v2"
)

func main() {
	genSimple()
	genMultiSheet()
	fmt.Println("test xlsx files generated")
}

func genSimple() {
	f := excelize.NewFile()
	f.SetCellValue("Sheet1", "A1", "Name")
	f.SetCellValue("Sheet1", "B1", "Age")
	f.SetCellValue("Sheet1", "C1", "Score")
	f.SetCellValue("Sheet1", "A2", "Alice")
	f.SetCellValue("Sheet1", "B2", 25)
	f.SetCellValue("Sheet1", "C2", 90.5)
	f.SetCellValue("Sheet1", "A3", "Bob")
	f.SetCellValue("Sheet1", "B3", 30)
	f.SetCellValue("Sheet1", "C3", 85.0)
	f.SetCellFormula("Sheet1", "C4", "SUM(C2:C3)")
	f.SaveAs("simple.xlsx")
}

func genMultiSheet() {
	f := excelize.NewFile()
	f.SetCellValue("Sheet1", "A1", "ID")
	f.SetCellValue("Sheet1", "B1", "Value")
	f.SetCellValue("Sheet1", "A2", 1)
	f.SetCellValue("Sheet1", "B2", 100)
	f.SetCellValue("Sheet1", "A3", 2)
	f.SetCellValue("Sheet1", "B3", 200)

	f.NewSheet("Data")
	f.SetCellValue("Data", "A1", "Category")
	f.SetCellValue("Data", "B1", "Amount")
	f.SetCellValue("Data", "A2", "Food")
	f.SetCellValue("Data", "B2", 50)
	f.SetCellValue("Data", "A3", "Transport")
	f.SetCellValue("Data", "B3", 30)
	f.MergeCell("Data", "A5", "B5")
	f.SetCellValue("Data", "A5", "Total")

	f.SaveAs("multisheet.xlsx")
}
