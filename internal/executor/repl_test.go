package executor

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestREPLSession_Stateful(t *testing.T) {
	r := NewREPLExecutor("python3", 30*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tmpDir := t.TempDir()
	session, err := r.StartSession(ctx, tmpDir)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer session.Close()

	// First call: set a variable
	result, err := session.Execute(ctx, `x = 42
print("x =", x)`)
	if err != nil {
		t.Fatalf("Execute 1: %v", err)
	}
	if !strings.Contains(result.Stdout, "x = 42") {
		t.Errorf("expected 'x = 42' in stdout, got %q", result.Stdout)
	}

	// Second call: variable persists
	result, err = session.Execute(ctx, `print("x is still", x)`)
	if err != nil {
		t.Fatalf("Execute 2: %v", err)
	}
	if !strings.Contains(result.Stdout, "x is still 42") {
		t.Errorf("expected 'x is still 42' in stdout, got %q", result.Stdout)
	}

	// Third call: imports persist
	result, err = session.Execute(ctx, `import json
print(json.dumps({"a": 1}))`)
	if err != nil {
		t.Fatalf("Execute 3: %v", err)
	}
	if !strings.Contains(result.Stdout, `{"a": 1}`) {
		t.Errorf("expected json output, got %q", result.Stdout)
	}

	result, err = session.Execute(ctx, `print(json.dumps({"b": 2}))`)
	if err != nil {
		t.Fatalf("Execute 4: %v", err)
	}
	if !strings.Contains(result.Stdout, `{"b": 2}`) {
		t.Errorf("expected json output, got %q", result.Stdout)
	}
}

func TestREPLSession_ErrorRecovery(t *testing.T) {
	r := NewREPLExecutor("python3", 30*time.Second)
	ctx := context.Background()

	tmpDir := t.TempDir()
	session, err := r.StartSession(ctx, tmpDir)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer session.Close()

	// Error in first call
	result, err := session.Execute(ctx, `raise ValueError("oops")`)
	if err != nil {
		t.Fatalf("Execute (error case): %v", err)
	}
	if !strings.Contains(result.Stdout, "ValueError") {
		t.Errorf("expected ValueError in output, got stdout=%q", result.Stdout)
	}

	// Session should still work
	result, err = session.Execute(ctx, `print("recovered")`)
	if err != nil {
		t.Fatalf("Execute after error: %v", err)
	}
	if !strings.Contains(result.Stdout, "recovered") {
		t.Errorf("expected 'recovered', got %q", result.Stdout)
	}
}
