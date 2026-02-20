package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudwego/eino/adk"

	"github.com/rayx-hk/dataagent/internal/eval"
	"github.com/rayx-hk/dataagent/internal/orchestrator"
)

type RunConfig struct {
	Concurrency int
	MaxRetry    int
	OutputDir   string
	ReportDir   string
	Resume      bool
}

type Runner struct {
	cfg       RunConfig
	orch      *orchestrator.Orchestrator
	builder   *orchestrator.PromptBuilder
	judge     *eval.OJJudge
	failureMu sync.Mutex
}

func NewRunner(cfg RunConfig, codeAct adk.Agent, judge *eval.OJJudge, builder *orchestrator.PromptBuilder) *Runner {
	orch := orchestrator.NewOrchestrator(orchestrator.OrchestratorConfig{
		MaxRetry: cfg.MaxRetry,
	}, codeAct, judge, builder)

	return &Runner{
		cfg:     cfg,
		orch:    orch,
		builder: builder,
		judge:   judge,
	}
}

type TaskResult struct {
	TaskID       string
	CaseNo       int
	Pass         bool
	Error        string
	Type         string
	AttemptCount int
	Instruction  string
	Duration     float64
}

func (r *Runner) Run(ctx context.Context, tasks []Task) (*eval.BenchReport, error) {
	start := time.Now()
	report := &eval.BenchReport{
		Timestamp: start,
		ByType:    make(map[string]eval.TypeStat),
	}

	if err := os.MkdirAll(r.cfg.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	sem := make(chan struct{}, r.cfg.Concurrency)
	resultCh := make(chan TaskResult, len(tasks)*4)

	var wg sync.WaitGroup
	var completed int64
	total := countTestCases(tasks)

	for _, task := range tasks {
		for _, tc := range task.TestCases {
			if r.cfg.Resume {
				if res, ok := r.tryResumeCase(ctx, task, tc); ok {
					resultCh <- res
					done := atomic.AddInt64(&completed, 1)
					fmt.Fprintf(os.Stderr, "\r[%d/%d] %s #%d: pass=%v (cached)",
						done, total, task.ID, tc.No, res.Pass)
					continue
				}
			}
			wg.Add(1)
			go func(task Task, tc TestCase) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				result := r.runSingleCase(ctx, task, tc)
				resultCh <- result

				done := atomic.AddInt64(&completed, 1)
				fmt.Fprintf(os.Stderr, "\r[%d/%d] %s #%d: pass=%v",
					done, total, task.ID, tc.No, result.Pass)
			}(task, tc)
		}
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	for result := range resultCh {
		detail := eval.FailureDetail{
			TaskID:          fmt.Sprintf("%s#%d", result.TaskID, result.CaseNo),
			InstructionType: result.Type,
			Category:        eval.ClassifyFailure(result.Error),
			AttemptCount:    result.AttemptCount,
			Reason:          result.Error,
			Instruction:     result.Instruction,
		}
		report.AddDetailedResult(detail, result.Pass)
	}

	report.Duration = time.Since(start)
	fmt.Fprintln(os.Stderr)
	return report, nil
}

type attemptTrace struct {
	Attempt    int    `json:"attempt"`
	AgentCode  string `json:"agent_code,omitempty"`
	JudgeScore string `json:"judge_score,omitempty"`
	Error      string `json:"error,omitempty"`
}

type taskResultRecord struct {
	TaskID          string         `json:"task_id"`
	CaseNo          int            `json:"case_no"`
	InstructionType string         `json:"instruction_type"`
	Instruction     string         `json:"instruction"`
	Pass            bool           `json:"pass"`
	AttemptCount    int            `json:"attempt_count"`
	DurationSeconds float64        `json:"duration_seconds"`
	Category        string         `json:"category,omitempty"`
	FinalError      string         `json:"final_error,omitempty"`
	Traces          []attemptTrace `json:"traces,omitempty"`
	FinishedAt      time.Time      `json:"finished_at"`
}

func (r *Runner) runSingleCase(ctx context.Context, task Task, tc TestCase) TaskResult {
	start := time.Now()
	result := TaskResult{
		TaskID:      task.ID,
		CaseNo:      tc.No,
		Type:        task.InstructionType,
		Instruction: task.Instruction,
	}

	defer func() {
		result.Duration = time.Since(start).Seconds()
	}()

	finalWorkDir := filepath.Join(r.cfg.OutputDir, fmt.Sprintf("%s_%d", task.ID, tc.No))
	
	// Create a temporary execution workspace to bypass macOS Sandbox/TCC prompts
	// (which happen when Excel or Python tries to access files on Desktop/Documents).
	// os.TempDir() gives an accessible path like /var/folders/...
	tempWorkDir, err := os.MkdirTemp("", fmt.Sprintf("dataagent_bench_%s_%d_*", task.ID, tc.No))
	if err != nil {
		result.Error = fmt.Sprintf("create temp workdir: %v", err)
		return result
	}
	defer func() {
		// After task finishes, copy all artifacts from tempWorkDir to the finalWorkDir
		os.MkdirAll(finalWorkDir, 0755)
		_ = copyDirContents(tempWorkDir, finalWorkDir)
		os.RemoveAll(tempWorkDir)
	}()

	workDir := tempWorkDir

	inputCopy := filepath.Join(workDir, filepath.Base(tc.InputFile))
	if err := copyFile(tc.InputFile, inputCopy); err != nil {
		result.Error = fmt.Sprintf("copy input: %v", err)
		return result
	}
	backupCopy := inputCopy + ".orig"
	if err := copyFile(tc.InputFile, backupCopy); err != nil {
		slog.Warn("backup copy failed", "error", err)
	}

	evalPosition := task.AnswerPosition
	if task.AnswerSheet != "" && !strings.Contains(evalPosition, "!") {
		evalPosition = "'" + task.AnswerSheet + "'!" + evalPosition
	}

	inputArgs, err := r.builder.BuildInput(ctx, task.Instruction, evalPosition, task.InstructionType, workDir, inputCopy)
	if err != nil {
		result.Error = fmt.Sprintf("build input: %v", err)
		return result
	}

	orchRes := r.orch.Run(ctx, inputArgs, tc.AnswerFile)

	result.Pass = orchRes.Success
	result.Error = orchRes.Error
	result.AttemptCount = orchRes.Attempt

	r.writeTaskResult(workDir, result)
	if len(orchRes.AgentTrace) > 0 {
		traceData, err := json.MarshalIndent(orchRes.AgentTrace, "", "  ")
		if err == nil {
			_ = os.WriteFile(filepath.Join(workDir, "trace.json"), traceData, 0644)
		}
	}

	if !result.Pass {
		r.appendFailureLog(task, tc, result, result.Error)
	}
	return result
}

func (r *Runner) writeTaskResult(workDir string, result TaskResult) {
	cat := eval.ClassifyFailure(result.Error)
	rec := taskResultRecord{
		TaskID:          result.TaskID,
		CaseNo:          result.CaseNo,
		InstructionType: result.Type,
		Instruction:     result.Instruction,
		Pass:            result.Pass,
		AttemptCount:    result.AttemptCount,
		DurationSeconds: result.Duration,
		Category:        string(cat),
		FinalError:      result.Error,
		FinishedAt:      time.Now(),
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(workDir, "task_result.json"), data, 0644)
}

func (r *Runner) appendFailureLog(task Task, tc TestCase, result TaskResult, finalErr string) {
	if r.cfg.ReportDir == "" {
		return
	}
	if err := os.MkdirAll(r.cfg.ReportDir, 0755); err != nil {
		return
	}
	cat := eval.ClassifyFailure(finalErr)
	rec := taskResultRecord{
		TaskID:          result.TaskID,
		CaseNo:          result.CaseNo,
		InstructionType: result.Type,
		Instruction:     task.Instruction,
		Pass:            false,
		AttemptCount:    result.AttemptCount,
		DurationSeconds: result.Duration,
		Category:        string(cat),
		FinalError:      finalErr,
		FinishedAt:      time.Now(),
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return
	}
	logPath := filepath.Join(r.cfg.ReportDir, "failures_live.jsonl")
	r.failureMu.Lock()
	defer r.failureMu.Unlock()
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%s\n", line)
}

func (r *Runner) tryResumeCase(ctx context.Context, task Task, tc TestCase) (TaskResult, bool) {
	workDir := filepath.Join(r.cfg.OutputDir, fmt.Sprintf("%s_%d", task.ID, tc.No))
	outputFile := filepath.Join(workDir, filepath.Base(tc.InputFile))

	if _, err := os.Stat(outputFile); err != nil {
		return TaskResult{}, false
	}

	evalPosition := task.AnswerPosition
	if task.AnswerSheet != "" && !strings.Contains(evalPosition, "!") {
		evalPosition = "'" + task.AnswerSheet + "'!" + evalPosition
	}

	jr, err := r.judge.Evaluate(ctx, outputFile, tc.AnswerFile, evalPosition)
	if err != nil || !jr.Pass {
		return TaskResult{}, false
	}

	slog.Info("resume: skipping passed task", "task", task.ID, "case", tc.No)
	return TaskResult{
		TaskID: task.ID,
		CaseNo: tc.No,
		Pass:   true,
		Type:   task.InstructionType,
	}, true
}

func countTestCases(tasks []Task) int64 {
	var n int64
	for _, t := range tasks {
		n += int64(len(t.TestCases))
	}
	return n
}

func copyFile(src, dst string) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()

	d, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer d.Close()

	_, err = io.Copy(d, s)
	return err
}

func copyDirContents(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, 0755); err != nil {
				return err
			}
			if err := copyDirContents(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	return nil
}
