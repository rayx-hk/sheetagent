package sheet

import (
	"path/filepath"
	"runtime"
	"testing"
	"github.com/xuri/excelize/v2"
)

func testdataPath(name string) string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "testdata", name)
}

func TestSafeCalcCellValue(t *testing.T) {
	f := excelize.NewFile()
	// Set the "poison" formula that causes a panic in excelize v2.10.0
	err := f.SetCellFormula("Sheet1", "C2", `=IFERROR(SMALL(IF(ISNUMBER(FIND(ROW($0:$9),TEXT(A2,"0"))),ROW($0:$9)),1),"")`)
	if err != nil {
		t.Fatalf("failed to set cell formula: %v", err)
	}

	// safeCalcCellValue should return an error encapsulating the panic, rather than crashing
	val, err := safeCalcCellValue(f, "Sheet1", "C2")
	if err == nil {
		t.Errorf("expected an error due to panic recovery, but got nil and value: %q", val)
	} else {
		t.Logf("Successfully caught panic as error: %v", err)
	}
}