package executor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// REPLDelim is the delimiter sent after each code block to signal end of input.
// The Python driver prints this after each execution to signal end of output.
const REPLDelim = "__DATAAGENT_REPL_END__"

// pythonREPLDriver is the inline Python script that runs in a loop, reading code
// from stdin and executing it. State (variables, imports) persists across executions.
// Stderr is merged into stdout so we only need to read one pipe.
const pythonREPLDriver = `
import sys
delim = "__DATAAGENT_REPL_END__"
while True:
    code = []
    for line in iter(sys.stdin.readline, ''):
        if line.rstrip() == delim:
            break
        code.append(line)
    if not code:
        break
    code_str = ''.join(code)
    orig_stderr = sys.stderr
    sys.stderr = sys.stdout
    try:
        exec(compile(code_str, '<repl>', 'exec'), globals())
    except SystemExit:
        break
    except Exception as e:
        import traceback
        traceback.print_exc()
    finally:
        sys.stderr = orig_stderr
    sys.stdout.flush()
    print(delim)
    sys.stdout.flush()
`

// REPLSession represents a persistent Python process. Variables and imports
// persist across Execute calls. Call Close when done.
type REPLSession interface {
	Execute(ctx context.Context, code string) (*ExecResult, error)
	Close() error
}

// REPLExecutor creates stateful REPL sessions. Each session is a separate
// Python process. Use one session per task attempt.
type REPLExecutor struct {
	pythonPath string
	timeout    time.Duration
}

// NewREPLExecutor creates a REPL executor with the given Python path and
// per-call timeout.
func NewREPLExecutor(pythonPath string, timeout time.Duration) *REPLExecutor {
	if pythonPath == "" {
		pythonPath = "python3"
	}
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &REPLExecutor{pythonPath: pythonPath, timeout: timeout}
}

// StartSession launches a persistent Python process in the given workdir.
// The process runs with cwd=workdir. Call Close on the returned session
// when the task attempt is finished.
func (r *REPLExecutor) StartSession(ctx context.Context, workdir string) (REPLSession, error) {
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

	cmd := exec.CommandContext(ctx, r.pythonPath, "-u", "-c", pythonREPLDriver)
	cmd.Dir = absWork

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		stdin.Close()
		return nil, fmt.Errorf("start python: %w", err)
	}

	s := &replSession{
		cmd:         cmd,
		stdin:       stdin,
		stdoutReader: bufio.NewReader(stdout),
		timeout:     r.timeout,
	}
	return s, nil
}

type replSession struct {
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stdoutReader *bufio.Reader
	timeout      time.Duration
	mu           sync.Mutex
	closed       bool
}

func (s *replSession) Execute(ctx context.Context, code string) (*ExecResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("repl session closed")
	}

	execCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	// Send code + delimiter
	input := strings.TrimSpace(code)
	if input == "" {
		return &ExecResult{Stdout: "", Stderr: "", ExitCode: 0}, nil
	}
	if _, err := s.stdin.Write([]byte(input + "\n" + REPLDelim + "\n")); err != nil {
		return nil, fmt.Errorf("write code: %w", err)
	}

	// Read stdout until delimiter (stderr merged into stdout by Python driver)
	var stdoutBuf bytes.Buffer
	readDone := make(chan error, 1)
	go func() {
		for {
			line, err := s.stdoutReader.ReadString('\n')
			if err != nil {
				readDone <- err
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == REPLDelim {
				readDone <- nil
				return
			}
			stdoutBuf.WriteString(line + "\n")
		}
	}()

	select {
	case err := <-readDone:
		if err != nil {
			return nil, fmt.Errorf("read stdout: %w", err)
		}
	case <-execCtx.Done():
		return nil, fmt.Errorf("repl execute timeout: %w", execCtx.Err())
	}

	return &ExecResult{
		Stdout:   stdoutBuf.String(),
		Stderr:   "",
		ExitCode: 0,
	}, nil
}

func (s *replSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	// Send empty block to trigger clean exit
	_, _ = s.stdin.Write([]byte(REPLDelim + "\n"))
	_ = s.stdin.Close()
	err := s.cmd.Wait()
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return nil
		}
		return err
	}
	return nil
}
