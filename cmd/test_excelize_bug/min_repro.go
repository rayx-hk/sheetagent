package main

import (
	"fmt"
	"github.com/xuri/excelize/v2"
)

func main() {
	f := excelize.NewFile()
	err := f.SetCellFormula("Sheet1", "C2", `=IFERROR(SMALL(IF(ISNUMBER(FIND(ROW($0:$9),TEXT(A2,"0"))),ROW($0:$9)),1),"")`)
	if err != nil {
		fmt.Println("Error setting formula:", err)
		return
	}
	
	// This call will trigger the panic: runtime error: index out of range [-1]
	fmt.Println("Triggering calc...")
	_, err = f.CalcCellValue("Sheet1", "C2")
	fmt.Println("Done or error:", err)
}
