package sheet

import (
	"context"
	"testing"
)

func TestTakeSnapshot(t *testing.T) {
	snap, err := TakeSnapshot(context.Background(), testdataPath("simple.xlsx"))
	if err != nil {
		t.Fatalf("take snapshot: %v", err)
	}
	if len(snap) == 0 {
		t.Fatal("expected non-empty snapshot")
	}
	cells, ok := snap["Sheet1"]
	if !ok {
		t.Fatal("expected Sheet1 in snapshot")
	}
	if len(cells) == 0 {
		t.Fatal("expected cells in Sheet1")
	}
}

func TestDiffSnapshots_NoDiff(t *testing.T) {
	snap, err := TakeSnapshot(context.Background(), testdataPath("simple.xlsx"))
	if err != nil {
		t.Fatalf("take snapshot: %v", err)
	}
	diffs := DiffSnapshots(snap, snap)
	if len(diffs) != 0 {
		t.Errorf("expected no diffs, got %d", len(diffs))
	}
}

func TestDiffSnapshots_WithDiff(t *testing.T) {
	before := map[string]map[string]string{
		"Sheet1": {"A1": "old", "B1": "same"},
	}
	after := map[string]map[string]string{
		"Sheet1": {"A1": "new", "B1": "same"},
	}
	diffs := DiffSnapshots(before, after)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 change log, got %d", len(diffs))
	}
	if len(diffs[0].Changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(diffs[0].Changes))
	}
	if diffs[0].Changes[0].Before != "old" || diffs[0].Changes[0].After != "new" {
		t.Errorf("unexpected change: %+v", diffs[0].Changes[0])
	}
}
