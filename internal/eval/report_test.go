package eval

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBenchReport_AddResult(t *testing.T) {
	r := &BenchReport{Timestamp: time.Now()}
	r.AddResult("task1", "Cell-Level", true, "")
	r.AddResult("task2", "Cell-Level", false, "")
	r.AddResult("task3", "Sheet-Level", false, "agent error: no response from agent")

	if r.Total != 3 {
		t.Errorf("expected total 3, got %d", r.Total)
	}
	if r.Pass != 1 {
		t.Errorf("expected pass 1, got %d", r.Pass)
	}
	if r.Fail != 1 {
		t.Errorf("expected fail 1, got %d", r.Fail)
	}
	if r.Error != 1 {
		t.Errorf("expected error 1, got %d", r.Error)
	}
}

func TestBenchReport_CalcPassRate(t *testing.T) {
	r := &BenchReport{Total: 10, Pass: 4}
	r.CalcPassRate()
	if r.PassRate != 0.4 {
		t.Errorf("expected pass rate 0.4, got %f", r.PassRate)
	}
}

func TestBenchReport_WriteJSON(t *testing.T) {
	tmpDir := t.TempDir()
	r := &BenchReport{
		Timestamp: time.Now(),
		Dataset:   "test",
		Total:     2,
		Pass:      1,
		Fail:      1,
		Duration:  5 * time.Second,
	}

	path := filepath.Join(tmpDir, "report.json")
	if err := r.WriteJSON(path); err != nil {
		t.Fatalf("write json: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty json")
	}
}

func TestBenchReport_WriteMarkdown(t *testing.T) {
	tmpDir := t.TempDir()
	r := &BenchReport{
		Timestamp: time.Now(),
		Dataset:   "test",
		Total:     2,
		Pass:      1,
		Fail:      1,
		Duration:  5 * time.Second,
	}

	path := filepath.Join(tmpDir, "report.md")
	if err := r.WriteMarkdown(path); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty markdown")
	}
}
