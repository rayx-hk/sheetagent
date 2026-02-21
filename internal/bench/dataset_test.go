package bench

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSpreadsheetBenchDataset_Load(t *testing.T) {
	tmpDir := t.TempDir()

	taskID := "test_001"
	tasks := []map[string]string{
		{
			"id":               taskID,
			"instruction":      "Sum column B",
			"spreadsheet_path": "spreadsheet/" + taskID,
			"instruction_type": "Cell-Level Manipulation",
			"answer_position":  "B3:B5",
		},
	}

	jsonlPath := filepath.Join(tmpDir, "data.jsonl")
	f, err := os.Create(jsonlPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		line, _ := json.Marshal(task)
		f.Write(line)
		f.WriteString("\n")
	}
	f.Close()

	spreadDir := filepath.Join(tmpDir, "spreadsheet", taskID)
	if err := os.MkdirAll(spreadDir, 0755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(spreadDir, "1_"+taskID+"_input.xlsx"), []byte("fake"), 0644)
	os.WriteFile(filepath.Join(spreadDir, "1_"+taskID+"_answer.xlsx"), []byte("fake"), 0644)

	ds := NewSpreadsheetBenchDataset("test")
	loadedTasks, err := ds.Load(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(loadedTasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(loadedTasks))
	}

	task := loadedTasks[0]
	if task.ID != taskID {
		t.Errorf("expected ID=%s, got %s", taskID, task.ID)
	}
	if task.Instruction != "Sum column B" {
		t.Errorf("unexpected instruction: %s", task.Instruction)
	}
	if len(task.TestCases) != 1 {
		t.Fatalf("expected 1 test case, got %d", len(task.TestCases))
	}
	if task.TestCases[0].No != 1 {
		t.Errorf("expected test case No=1, got %d", task.TestCases[0].No)
	}
}

func TestSpreadsheetBenchDataset_DatasetJSON(t *testing.T) {
	tmpDir := t.TempDir()

	taskID := "test_002"
	items := []map[string]string{
		{
			"id":               taskID,
			"instruction":      "Fill column C",
			"spreadsheet_path": "spreadsheet/" + taskID,
			"instruction_type": "Sheet-Level Manipulation",
			"answer_position":  "C1:C10",
			"answer_sheet":     "Sheet1",
			"data_position":    "A1:D10",
		},
	}

	data, _ := json.Marshal(items)
	os.WriteFile(filepath.Join(tmpDir, "dataset.json"), data, 0644)

	spreadDir := filepath.Join(tmpDir, "spreadsheet", taskID)
	os.MkdirAll(spreadDir, 0755)
	os.WriteFile(filepath.Join(spreadDir, "1_"+taskID+"_init.xlsx"), []byte("fake"), 0644)
	os.WriteFile(filepath.Join(spreadDir, "1_"+taskID+"_golden.xlsx"), []byte("fake"), 0644)

	ds := NewSpreadsheetBenchDataset("test_json")
	tasks, err := ds.Load(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].AnswerSheet != "Sheet1" {
		t.Errorf("expected answer_sheet=Sheet1, got %s", tasks[0].AnswerSheet)
	}
	if tasks[0].DataPosition != "A1:D10" {
		t.Errorf("expected data_position=A1:D10, got %s", tasks[0].DataPosition)
	}
	if len(tasks[0].TestCases) != 1 {
		t.Fatalf("expected 1 test case, got %d", len(tasks[0].TestCases))
	}
}

func TestSpreadsheetBenchDataset_RealDatasets(t *testing.T) {
	datasets := []struct {
		name string
		dir  string
		min  int
	}{
		{"400 verified", "/Users/razil/Desktop/razil/dev/opensource/sheetagent-design/datasets/spreadsheetbench_verified_400", 300},
		{"912 full", "/Users/razil/Desktop/razil/dev/opensource/sheetagent-design/datasets/all_data_912_v0.1", 800},
	}

	for _, ds := range datasets {
		t.Run(ds.name, func(t *testing.T) {
			if _, err := os.Stat(ds.dir); os.IsNotExist(err) {
				t.Skipf("dataset not available at %s", ds.dir)
			}
			loader := NewSpreadsheetBenchDataset(ds.name)
			tasks, err := loader.Load(context.Background(), ds.dir)
			if err != nil {
				t.Fatalf("load %s: %v", ds.name, err)
			}
			if len(tasks) < ds.min {
				t.Errorf("expected at least %d tasks, got %d", ds.min, len(tasks))
			}
			totalCases := 0
			for _, task := range tasks {
				totalCases += len(task.TestCases)
			}
			t.Logf("Dataset %s: %d tasks, %d test cases", ds.name, len(tasks), totalCases)
			if len(tasks) > 0 {
				t.Logf("  First task: id=%s type=%s sheet=%s", tasks[0].ID, tasks[0].InstructionType, tasks[0].AnswerSheet)
			}
		})
	}
}

func TestSpreadsheetBenchDataset_Name(t *testing.T) {
	ds := NewSpreadsheetBenchDataset("sample_200")
	if ds.Name() != "sample_200" {
		t.Errorf("expected name=sample_200, got %s", ds.Name())
	}
}
