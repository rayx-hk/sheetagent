package sheet

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
)

func testdataPath(name string) string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "testdata", name)
}

func TestExcelizeParser_Parse(t *testing.T) {
	p := NewParser()
	ctx := context.Background()

	sheets, err := p.Parse(ctx, testdataPath("simple.xlsx"))
	if err != nil {
		t.Fatalf("parse simple.xlsx: %v", err)
	}
	if len(sheets) != 1 {
		t.Fatalf("expected 1 sheet, got %d", len(sheets))
	}
	s := sheets[0]
	if s.Name != "Sheet1" {
		t.Errorf("expected sheet name Sheet1, got %s", s.Name)
	}
	if s.MaxRow < 3 {
		t.Errorf("expected at least 3 rows, got %d", s.MaxRow)
	}
	if s.MaxCol < 3 {
		t.Errorf("expected at least 3 cols, got %d", s.MaxCol)
	}
}

func TestExcelizeParser_ParseMultiSheet(t *testing.T) {
	p := NewParser()
	ctx := context.Background()

	sheets, err := p.Parse(ctx, testdataPath("multisheet.xlsx"))
	if err != nil {
		t.Fatalf("parse multisheet.xlsx: %v", err)
	}
	if len(sheets) < 2 {
		t.Fatalf("expected at least 2 sheets, got %d", len(sheets))
	}
}

func TestExcelizeParser_ParseOverview(t *testing.T) {
	p := NewParser()
	ctx := context.Background()

	overview, err := p.ParseOverview(ctx, testdataPath("simple.xlsx"))
	if err != nil {
		t.Fatalf("parse overview: %v", err)
	}
	if overview.FilePath == "" {
		t.Error("expected file path to be set")
	}
	if overview.FileSize == 0 {
		t.Error("expected non-zero file size")
	}
	if len(overview.SheetNames) == 0 {
		t.Error("expected sheet names")
	}
	if len(overview.Sheets) == 0 {
		t.Error("expected sheet info")
	}
	si := overview.Sheets[0]
	if len(si.Headers) == 0 {
		t.Error("expected headers")
	}
	if si.RowCount == 0 {
		t.Error("expected non-zero row count")
	}
}
