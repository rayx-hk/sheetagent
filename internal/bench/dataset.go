package bench

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Task struct {
	ID              string     `json:"id"`
	Instruction     string     `json:"instruction"`
	SpreadsheetPath string     `json:"spreadsheet_path"`
	InstructionType string     `json:"instruction_type"`
	AnswerPosition  string     `json:"answer_position"`
	AnswerSheet     string     `json:"answer_sheet"`
	DataPosition    string     `json:"data_position"`
	TestCases       []TestCase `json:"test_cases"`
}

type TestCase struct {
	No         int    `json:"no"`
	InputFile  string `json:"input_file"`
	AnswerFile string `json:"answer_file"`
}

type Dataset interface {
	Load(ctx context.Context, dir string) ([]Task, error)
	Name() string
}

type SpreadsheetBenchDataset struct {
	name string
}

func NewSpreadsheetBenchDataset(name string) *SpreadsheetBenchDataset {
	return &SpreadsheetBenchDataset{name: name}
}

func (d *SpreadsheetBenchDataset) Name() string {
	return d.name
}

func (d *SpreadsheetBenchDataset) Load(_ context.Context, dir string) ([]Task, error) {
	rawItems, err := loadDatasetFile(dir)
	if err != nil {
		return nil, err
	}

	var tasks []Task
	for _, raw := range rawItems {
		task := Task{
			ID:              string(raw.ID),
			Instruction:     raw.Instruction,
			SpreadsheetPath: raw.SpreadsheetPath,
			InstructionType: raw.InstructionType,
			AnswerPosition:  raw.AnswerPosition,
			AnswerSheet:     raw.AnswerSheet,
			DataPosition:    raw.DataPosition,
		}

		spreadDir := filepath.Join(dir, raw.SpreadsheetPath)
		testCases, err := discoverTestCases(spreadDir, string(raw.ID))
		if err != nil {
			return nil, fmt.Errorf("discover test cases for %s: %w", string(raw.ID), err)
		}
		task.TestCases = testCases

		if len(task.TestCases) > 0 {
			tasks = append(tasks, task)
		}
	}

	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].ID < tasks[j].ID
	})

	return tasks, nil
}

type rawTask struct {
	ID              flexString `json:"id"`
	Instruction     string     `json:"instruction"`
	SpreadsheetPath string     `json:"spreadsheet_path"`
	InstructionType string     `json:"instruction_type"`
	AnswerPosition  string     `json:"answer_position"`
	AnswerSheet     string     `json:"answer_sheet"`
	DataPosition    string     `json:"data_position"`
}

// flexString handles JSON values that can be either a string or a number.
type flexString string

func (f *flexString) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*f = flexString(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(data, &n); err == nil {
		*f = flexString(n.String())
		return nil
	}
	return fmt.Errorf("cannot unmarshal %s into string", string(data))
}

// loadDatasetFile tries dataset.json first, then falls back to data.jsonl.
func loadDatasetFile(dir string) ([]rawTask, error) {
	jsonPath := filepath.Join(dir, "dataset.json")
	if data, err := os.ReadFile(jsonPath); err == nil {
		var items []rawTask
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, fmt.Errorf("parse %s: %w", jsonPath, err)
		}
		return items, nil
	}

	jsonlPath := filepath.Join(dir, "data.jsonl")
	f, err := os.Open(jsonlPath)
	if err != nil {
		return nil, fmt.Errorf("no dataset.json or data.jsonl found in %s", dir)
	}
	defer f.Close()

	var items []rawTask
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item rawTask
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, fmt.Errorf("parse data.jsonl line %d: %w", lineNum, err)
		}
		items = append(items, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", jsonlPath, err)
	}
	return items, nil
}

func discoverTestCases(dir, id string) ([]TestCase, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	inputFiles := make(map[int]string)
	answerFiles := make(map[int]string)

	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".xlsx") {
			continue
		}

		var no int
		var rest string
		if _, err := fmt.Sscanf(name, "%d_%s", &no, &rest); err != nil {
			continue
		}

		fullPath := filepath.Join(dir, name)
		switch {
		case strings.Contains(name, "_input.xlsx"), strings.Contains(name, "_init.xlsx"):
			inputFiles[no] = fullPath
		case strings.Contains(name, "_answer.xlsx"), strings.Contains(name, "_golden.xlsx"):
			answerFiles[no] = fullPath
		}
	}

	var cases []TestCase
	for no, inputFile := range inputFiles {
		answerFile, ok := answerFiles[no]
		if !ok {
			continue
		}
		cases = append(cases, TestCase{
			No:         no,
			InputFile:  inputFile,
			AnswerFile: answerFile,
		})
	}

	sort.Slice(cases, func(i, j int) bool {
		return cases[i].No < cases[j].No
	})

	return cases, nil
}
