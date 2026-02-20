package executor

import (
	"context"
	"encoding/json"
	"strings"
)

type ExecResult struct {
	Stdout   string   `json:"stdout"`
	Stderr   string   `json:"stderr"`
	ExitCode int      `json:"exit_code"`
	Files    []string `json:"files,omitempty"`
}

type StructuredResult struct {
	Success    bool        `json:"success"`
	OutputFile string      `json:"output_file"`
	ChangeLog  interface{} `json:"change_log"`
	Warnings   []string    `json:"warnings"`
}

type Executor interface {
	Execute(ctx context.Context, code string, workdir string) (*ExecResult, error)
	Close() error
}

const resultMarker = "===RESULT==="

func ParseStructuredResult(stdout string) (*StructuredResult, error) {
	idx := strings.LastIndex(stdout, resultMarker)
	if idx < 0 {
		return nil, nil
	}
	jsonStr := strings.TrimSpace(stdout[idx+len(resultMarker):])
	var result StructuredResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, err
	}
	return &result, nil
}
