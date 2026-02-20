package executor

import (
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestForceCalculate(t *testing.T) {
	// Create a temporary Excel file with a formula
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test_formula.xlsx")

	f := excelize.NewFile()
	defer f.Close()

	// Write some numbers
	f.SetCellValue("Sheet1", "A1", 10)
	f.SetCellValue("Sheet1", "B1", 20)

	// Write a formula that sums A1 and B1
	f.SetCellFormula("Sheet1", "C1", "=SUM(A1:B1)")
	
	// Save the file
	if err := f.SaveAs(filePath); err != nil {
		t.Fatalf("Failed to save test file: %v", err)
	}

	// Read it back immediately to verify excelize reads the formula but NOT the calculated value
	f2, err := excelize.OpenFile(filePath)
	if err != nil {
		t.Fatalf("Failed to open test file: %v", err)
	}
	defer f2.Close()
	
	valBefore, _ := f2.GetCellValue("Sheet1", "C1")
	if valBefore != "" {
		t.Logf("Warning: Excelize calculated value before MS Excel intervention: %s (expected empty or 0)", valBefore)
	}

	// Run our ForceCalculate which uses AppleScript + MS Excel
	err = ForceCalculate(filePath)
	if err != nil {
		// If MS Excel is not installed or accessible in this CI environment, skip the rest
		t.Skipf("Skipping test because MS Excel ForceCalculate failed (is MS Excel installed?): %v", err)
		return
	}

	// Open the file again and check if the calculated value is now present
	f3, err := excelize.OpenFile(filePath)
	if err != nil {
		t.Fatalf("Failed to open test file after recalc: %v", err)
	}
	defer f3.Close()

	valAfter, err := f3.GetCellValue("Sheet1", "C1")
	if err != nil {
		t.Fatalf("Failed to get cell value after recalc: %v", err)
	}

	if valAfter != "30" {
		t.Errorf("Expected calculated value '30', got %q", valAfter)
	}
}
