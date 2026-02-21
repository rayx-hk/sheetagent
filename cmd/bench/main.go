package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/cloudwego/eino/components/tool"
	eimodel "github.com/cloudwego/eino/components/model"

	"github.com/rayx-hk/sheetagent/config"
	"github.com/rayx-hk/sheetagent/internal/agent"
	"github.com/rayx-hk/sheetagent/internal/bench"
	"github.com/rayx-hk/sheetagent/internal/eval"
	"github.com/rayx-hk/sheetagent/internal/executor"
	"github.com/rayx-hk/sheetagent/internal/model"
	"github.com/rayx-hk/sheetagent/internal/orchestrator"
)

func main() {
	dataset := flag.String("dataset", "200", "Dataset: 200 | 400 | full")
	concurrency := flag.Int("concurrency", 0, "Concurrency level (0 = use config default)")
	maxRetry := flag.Int("max-retry", 0, "Max retries per task (0 = use config default)")
	outputDir := flag.String("output", "", "Output directory (default from config)")
	reportFile := flag.String("report", "", "Report file prefix (default: auto-generated)")
	configPath := flag.String("config", "config/config.yaml", "Path to config.yaml")
	modelsPath := flag.String("models", "config/models.yaml", "Path to models.yaml")
	resume := flag.Bool("resume", false, "Skip tasks that already passed in the output directory")
	limit := flag.Int("limit", 0, "Limit the number of tasks to run (0 = no limit)")
	taskFilter := flag.String("task-filter", "", "Path to file containing task IDs to run (one per line, format: taskID or taskID#caseNo)")
	flag.Parse()

	if err := run(*dataset, *concurrency, *maxRetry, *limit, *outputDir, *reportFile, *configPath, *modelsPath, *resume, *taskFilter); err != nil {
		slog.Error("benchmark failed", "error", err)
		os.Exit(1)
	}
}

func run(datasetName string, concurrency, maxRetry, limit int, outputDir, reportFile, configPath, modelsPath string, resume bool, taskFilterPath string) error {
	config.LoadEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()

	cfg, err := config.LoadAll(configPath, modelsPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if concurrency <= 0 {
		concurrency = cfg.Bench.Concurrency
	}
	if maxRetry <= 0 {
		maxRetry = cfg.Bench.MaxRetry
	}
	if outputDir == "" {
		outputDir = cfg.Bench.OutputDir
	}
	reportDir := cfg.Bench.ReportDir

	datasetDir := resolveDatasetDir(cfg.Bench.DatasetDir, datasetName)
	slog.Info("loading dataset", "dir", datasetDir, "name", datasetName)

	ds := bench.NewSpreadsheetBenchDataset(datasetName)
	tasks, err := ds.Load(ctx, datasetDir)
	if err != nil {
		return fmt.Errorf("load dataset: %w", err)
	}
	if taskFilterPath != "" {
		tasks, err = bench.ApplyTaskFilter(tasks, taskFilterPath)
		if err != nil {
			return fmt.Errorf("apply task filter: %w", err)
		}
		slog.Info("task filter applied", "remaining", len(tasks), "filter", taskFilterPath)
	}
	if limit > 0 && len(tasks) > limit {
		tasks = tasks[:limit]
	}
	slog.Info("dataset loaded", "tasks", len(tasks))

	slog.Info("creating model router...")
	router, err := model.NewRouter(ctx, cfg.Models)
	if err != nil {
		return fmt.Errorf("init model router: %w", err)
	}
	slog.Info("model router created")

	rateInterval := 3 * time.Second
	wrapModel := func(role model.Role) (eimodel.ToolCallingChatModel, error) {
		m, err := router.Get(role)
		if err != nil {
			return nil, err
		}
		fs := model.NewForceStreamModel(m)
		rl := model.NewRateLimitedModel(fs, rateInterval, string(role))
		return model.NewContextManagedModel(rl, 0), nil
	}

	coderModel, err := wrapModel(model.RoleCoder)
	if err != nil {
		return err
	}

	exec := executor.NewEmbedded(cfg.Executor.PythonPath, cfg.Executor.Timeout)
	defer exec.Close()

	replExec := executor.NewREPLExecutor(cfg.Executor.PythonPath, cfg.Executor.Timeout)

	runnerTool, err := agent.NewPythonRunnerTool(exec)
	if err != nil {
		return err
	}

	tools := []tool.BaseTool{
		runnerTool,
	}

	codeActAgent, err := agent.NewCodeActAgent(ctx, coderModel, tools)
	if err != nil {
		return err
	}

	judge := eval.NewOJJudge()
	builder := orchestrator.NewPromptBuilder()

	// Replace bench.Runner with our orchestrator run inside a loop 
	// (or update internal/bench/runner.go to use the orchestrator)
	runner := bench.NewRunner(bench.RunConfig{
		Concurrency:  concurrency,
		MaxRetry:     maxRetry,
		OutputDir:    outputDir,
		ReportDir:    reportDir,
		Resume:       resume,
		REPLExecutor: replExec,
	}, codeActAgent, judge, builder)

	report, err := runner.Run(ctx, tasks)
	if err != nil {
		return fmt.Errorf("run benchmark: %w", err)
	}

	report.Dataset = datasetName

	if err := os.MkdirAll(reportDir, 0755); err != nil {
		return fmt.Errorf("create report dir: %w", err)
	}

	if reportFile == "" {
		reportFile = fmt.Sprintf("bench_%s_%s", datasetName, time.Now().Format("20060102_150405"))
	}

	jsonPath := filepath.Join(reportDir, reportFile+".json")
	mdPath := filepath.Join(reportDir, reportFile+".md")

	if err := report.WriteJSON(jsonPath); err != nil {
		return fmt.Errorf("write json report: %w", err)
	}
	if err := report.WriteMarkdown(mdPath); err != nil {
		return fmt.Errorf("write markdown report: %w", err)
	}

	slog.Info("benchmark complete",
		"pass_rate", fmt.Sprintf("%.2f%%", report.PassRate*100),
		"pass", report.Pass,
		"total", report.Total,
		"duration", report.Duration.Round(time.Second),
		"json_report", jsonPath,
		"md_report", mdPath,
	)

	return nil
}

func resolveDatasetDir(baseDir, name string) string {
	switch name {
	case "200":
		return filepath.Join(baseDir, "sample_data_200")
	case "400":
		return filepath.Join(baseDir, "spreadsheetbench_verified_400")
	case "912", "full":
		return filepath.Join(baseDir, "all_data_912_v0.1")
	default:
		return filepath.Join(baseDir, name)
	}
}
