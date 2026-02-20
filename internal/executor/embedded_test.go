package executor

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestEmbeddedExecutor_SimpleCode(t *testing.T) {
	e := NewEmbedded("python3", 30*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tmpDir := t.TempDir()
	result, err := e.Execute(ctx, `print("hello world")`, tmpDir)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d (stderr: %s)", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "hello world") {
		t.Errorf("expected 'hello world' in stdout, got %q", result.Stdout)
	}
}

func TestEmbeddedExecutor_ErrorCode(t *testing.T) {
	e := NewEmbedded("python3", 30*time.Second)
	ctx := context.Background()

	tmpDir := t.TempDir()
	result, err := e.Execute(ctx, `raise ValueError("test error")`, tmpDir)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.ExitCode == 0 {
		t.Error("expected non-zero exit code")
	}
	if !strings.Contains(result.Stderr, "ValueError") {
		t.Errorf("expected ValueError in stderr, got %q", result.Stderr)
	}
}

func TestEmbeddedExecutor_StructuredResult(t *testing.T) {
	e := NewEmbedded("python3", 30*time.Second)
	ctx := context.Background()

	code := `
import json
result = {"success": True, "output_file": "test.xlsx", "change_log": {}, "warnings": []}
print("===RESULT===")
print(json.dumps(result))
`
	tmpDir := t.TempDir()
	result, err := e.Execute(ctx, code, tmpDir)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	sr, err := ParseStructuredResult(result.Stdout)
	if err != nil {
		t.Fatalf("parse structured result: %v", err)
	}
	if sr == nil {
		t.Fatal("expected non-nil structured result")
	}
	if !sr.Success {
		t.Error("expected success=true")
	}
	if sr.OutputFile != "test.xlsx" {
		t.Errorf("expected output_file=test.xlsx, got %s", sr.OutputFile)
	}
}

func TestEmbeddedExecutor_Timeout(t *testing.T) {
	e := NewEmbedded("python3", 30*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	tmpDir := t.TempDir()
	_, err := e.Execute(ctx, `import time; time.sleep(10)`, tmpDir)
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestEmbeddedExecutor_FileWrite(t *testing.T) {
	e := NewEmbedded("python3", 30*time.Second)
	ctx := context.Background()

	tmpDir := t.TempDir()
	code := `
with open("output.txt", "w") as f:
    f.write("test content")
print("done")
`
	result, err := e.Execute(ctx, code, tmpDir)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code %d: %s", result.ExitCode, result.Stderr)
	}

	content, err := os.ReadFile(tmpDir + "/output.txt")
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(content) != "test content" {
		t.Errorf("expected 'test content', got %q", content)
	}
}
