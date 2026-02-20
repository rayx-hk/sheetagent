package executor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type EmbeddedExecutor struct {
	pythonPath string
	timeout    time.Duration
}

func NewEmbedded(pythonPath string, timeout time.Duration) *EmbeddedExecutor {
	if pythonPath == "" {
		pythonPath = "python3"
	}
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &EmbeddedExecutor{pythonPath: pythonPath, timeout: timeout}
}

func (e *EmbeddedExecutor) Execute(ctx context.Context, code, workdir string) (*ExecResult, error) {
	tmpFile, err := os.CreateTemp(workdir, "agent_*.py")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.WriteString(code); err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("write code: %w", err)
	}
	tmpFile.Close()

	if workdir == "" {
		workdir = "."
	}
	absWork, err := filepath.Abs(workdir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absWork, 0755); err != nil {
		return nil, fmt.Errorf("create workdir %s: %w", absWork, err)
	}

	execCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, e.pythonPath, tmpPath)
	cmd.Dir = absWork

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	exitCode := 0
	runErr := cmd.Run()
	if runErr != nil {
		if execCtx.Err() != nil {
			return nil, fmt.Errorf("exec python: %w", execCtx.Err())
		}
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("exec python: %w", runErr)
		}
	}

	return &ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}, nil
}

func (e *EmbeddedExecutor) Close() error { return nil }
