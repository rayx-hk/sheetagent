package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cloudwego/eino/components/tool"
	eimodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/rayx-hk/sheetagent/config"
	"github.com/rayx-hk/sheetagent/internal/agent"
	"github.com/rayx-hk/sheetagent/internal/bench"
	"github.com/rayx-hk/sheetagent/internal/eval"
	"github.com/rayx-hk/sheetagent/internal/executor"
	"github.com/rayx-hk/sheetagent/internal/memory"
	"github.com/rayx-hk/sheetagent/internal/model"
	"github.com/rayx-hk/sheetagent/internal/orchestrator"
	"github.com/rayx-hk/sheetagent/internal/rag"
	"github.com/rayx-hk/sheetagent/internal/skills"
)

// version is injected at build time via -ldflags "-X main.version=<BUILD_TIME>".
// Format: YYYYMMDDHHmm (e.g. 202603042322). Defaults to "dev" for go run builds.
var version = "dev"

func main() {
	slog.Info("bench starting", "version", version)

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
	checkAPI := flag.Bool("check-api", false, "Send a single minimal API request to verify connectivity, then exit")
	flag.Parse()

	if *checkAPI {
		if err := runAPICheck(*configPath, *modelsPath); err != nil {
			slog.Error("API check FAILED", "error", err)
			os.Exit(1)
		}
		return
	}

	if err := run(*dataset, *concurrency, *maxRetry, *limit, *outputDir, *reportFile, *configPath, *modelsPath, *resume, *taskFilter); err != nil {
		slog.Error("benchmark failed", "error", err)
		os.Exit(1)
	}
}

func run(datasetName string, concurrency, maxRetry, limit int, outputDir, reportFile, configPath, modelsPath string, resume bool, taskFilterPath string) error {
	config.LoadEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()

	// Graceful shutdown: first SIGINT/SIGTERM cancels context (stop new tasks),
	// second signal forces immediate exit.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		slog.Info("received signal, stopping gracefully (in-flight tasks will finish)...", "signal", sig)
		cancel()
		sig = <-sigCh
		slog.Warn("received second signal, force exit", "signal", sig)
		os.Exit(1)
	}()

	cfg, err := config.LoadAll(configPath, modelsPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// ── RAG safety gate ─────────────────────────────────────────────
	// RAG MUST be disabled during benchmark to prevent test-set contamination.
	// We enforce this with a hard failure, not just a warning.
	if cfg.RAG.Enabled {
		return fmt.Errorf("FATAL: rag.enabled=true in config — " +
			"RAG is forbidden during benchmark evaluation (test-set contamination). " +
			"Set rag.enabled=false in config.yaml or remove the rag section entirely")
	}
	slog.Info("RAG status: DISABLED (benchmark mode — no test-set contamination)")

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

	rateInterval := 6 * time.Second
	// Single shared rate limiter across all roles to prevent concurrent burst
	// that triggers proxy 403. All API calls go through this one gate.
	sharedRL := model.NewSharedRateLimiter(rateInterval, "global")
	tokenTracker := model.NewTokenTracker()
	wrapModel := func(role model.Role) (eimodel.ToolCallingChatModel, error) {
		m, err := router.Get(role)
		if err != nil {
			return nil, err
		}
		var wrapped eimodel.ToolCallingChatModel = model.NewForceStreamModel(m)
		if router.PromptCacheEnabled(role) {
			wrapped = model.NewCacheEnabledModel(wrapped)
			slog.Info("prompt cache enabled", "role", role)
		}
		rl := model.NewRateLimitedModelWithLimiter(wrapped, sharedRL)
		cm := model.NewContextManagedModel(rl, 0)
		return model.NewTrackedModel(cm, tokenTracker), nil
	}

	coderModel, err := wrapModel(model.RoleCoder)
	if err != nil {
		return err
	}

	// Evaluator model for code reviewer (optional — uses evaluator role from config)
	reviewModel, err := wrapModel(model.RoleEvaluator)
	if err != nil {
		slog.Warn("evaluator model not available, code reviewer disabled", "error", err)
		reviewModel = nil
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

	judge := eval.NewOJJudge(cfg.Executor.PythonPath)
	builder := orchestrator.NewPromptBuilder()

	// ── Wire up all v0.5.x modules ──────────────────────────────────
	// Skills registry (builtin skills for formula/layout/conditional tasks)
	skillReg, _ := skills.NewRegistry(filepath.Join(outputDir, "skills"))
	for _, s := range skills.BuiltinSkills() {
		skillReg.Register(s)
	}

	// RAG: bench-locked store (no test-set contamination)
	ragStore := rag.NewBenchLockedStore()

	// Failure memory: persists across resume runs
	failStorePath := filepath.Join(outputDir, "failure_store.json")
	failStore, err := memory.NewFailureStore(failStorePath)
	if err != nil {
		slog.Warn("failure store init failed, continuing without it", "error", err)
		failStore = nil
	}

	// Engine: top-level orchestrator with SOP routing
	engine := orchestrator.NewEngine(orchestrator.EngineConfig{
		MaxRetry:      maxRetry,
		REPLExecutor:  replExec,
		PythonPath:    cfg.Executor.PythonPath,
		ReviewModel:   reviewModel,
		UseDualEngine: true,
		UseMCTS:       true,
		MCTSConfig:    orchestrator.DefaultMCTSConfig(),
	}, codeActAgent, judge, builder, skillReg, ragStore, failStore)

	slog.Info("engine initialized",
		"dual_engine", true,
		"mcts", true,
		"skills", len(skills.BuiltinSkills()),
		"failure_store", failStorePath,
		"rag", "DISABLED (bench mode)",
	)

	runner := bench.NewRunner(bench.RunConfig{
		Concurrency: concurrency,
		MaxRetry:    maxRetry,
		OutputDir:   outputDir,
		ReportDir:   reportDir,
		Resume:      resume,
	}, engine, judge, builder, tokenTracker)

	report, err := runner.Run(ctx, tasks)
	if err != nil {
		return fmt.Errorf("run benchmark: %w", err)
	}

	// Persist failure memory for future runs
	if fs := engine.GetFailureStore(); fs != nil {
		if saveErr := fs.Save(); saveErr != nil {
			slog.Warn("failure store save failed", "error", saveErr)
		} else {
			slog.Info("failure store saved", "records", fs.Size(), "path", failStorePath)
		}
	}

	report.Dataset = datasetName

	// Always write reports, even for interrupted (partial) runs.
	// This ensures Ctrl+C / graceful shutdown still produces output.
	if err := os.MkdirAll(reportDir, 0755); err != nil {
		return fmt.Errorf("create report dir: %w", err)
	}

	if reportFile == "" {
		suffix := ""
		if report.Total > 0 && ctx.Err() != nil {
			suffix = "_partial"
		}
		reportFile = fmt.Sprintf("bench_%s_%s%s", datasetName, time.Now().Format("20060102_150405"), suffix)
	}

	jsonPath := filepath.Join(reportDir, reportFile+".json")
	mdPath := filepath.Join(reportDir, reportFile+".md")

	if err := report.WriteJSON(jsonPath); err != nil {
		return fmt.Errorf("write json report: %w", err)
	}
	if err := report.WriteMarkdown(mdPath); err != nil {
		return fmt.Errorf("write markdown report: %w", err)
	}

	status := "benchmark complete"
	if ctx.Err() != nil {
		status = "benchmark interrupted (partial report written)"
	}
	slog.Info(status,
		"pass_rate", fmt.Sprintf("%.2f%%", report.PassRate*100),
		"pass", report.Pass,
		"total", report.Total,
		"duration", report.Duration.Round(time.Second),
		"json_report", jsonPath,
		"md_report", mdPath,
	)

	model.LogTotalStats(tokenTracker)

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

func runAPICheck(configPath, modelsPath string) error {
	config.LoadEnv()

	cfg, err := config.LoadAll(configPath, modelsPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	providerType := cfg.Models.Coder.Primary.Type
	modelName := cfg.Models.Coder.Primary.Model
	baseURL := cfg.Models.Coder.Primary.BaseURL

	var apiKey string
	switch providerType {
	case "openai", "sub2api", "crs":
		apiKey = os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			apiKey = os.Getenv("ANTHROPIC_AUTH_TOKEN")
		}
	default:
		apiKey = os.Getenv("ANTHROPIC_AUTH_TOKEN")
		if apiKey == "" {
			apiKey = os.Getenv("ANTHROPIC_API_KEY")
		}
	}

	slog.Info("=== API Health Check ===")
	slog.Info("config",
		"provider", providerType,
		"base_url", baseURL,
		"model", modelName,
		"key_prefix", apiKey[:min(12, len(apiKey))]+"...",
	)

	fmt.Println("\n--- Test 1: Raw HTTP ---")
	testRawHTTP(providerType, baseURL, apiKey, modelName)

	fmt.Println("\n--- Test 2: Via eino SDK ---")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	router, err := model.NewRouter(ctx, cfg.Models)
	if err != nil {
		return fmt.Errorf("init model router: %w", err)
	}
	m, err := router.Get(model.RoleCoder)
	if err != nil {
		return fmt.Errorf("get coder model: %w", err)
	}
	wrapped := model.NewForceStreamModel(m)
	start := time.Now()
	reader, err := wrapped.Stream(ctx, []*schema.Message{
		{Role: schema.User, Content: "Reply with exactly: OK"},
	})
	if err != nil {
		fmt.Printf("  FAIL: %v (took %s)\n", err, time.Since(start).Round(time.Millisecond))
	} else {
		var content string
		for {
			chunk, err := reader.Recv()
			if err != nil {
				break
			}
			if chunk != nil {
				content += chunk.Content
			}
		}
		reader.Close()
		fmt.Printf("  OK: %q (took %s)\n", content, time.Since(start).Round(time.Millisecond))
	}

	return nil
}

func testRawHTTP(providerType, baseURL, apiKey, modelName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var url, body string
	var headers map[string]string

	switch providerType {
	case "sub2api":
		trimmed := strings.TrimRight(baseURL, "/")
		url = trimmed + "/v1/responses"
		body = fmt.Sprintf(`{"model":"%s","max_output_tokens":64,"stream":false,"input":"Reply OK","reasoning":{"effort":"high"}}`, modelName)
		headers = map[string]string{
			"Content-Type":  "application/json",
			"Authorization": "Bearer " + apiKey,
		}
	case "crs":
		trimmed := strings.TrimRight(baseURL, "/")
		if strings.HasSuffix(trimmed, "/openai") || strings.HasSuffix(trimmed, "/v1") {
			url = trimmed + "/responses"
		} else {
			url = trimmed + "/v1/responses"
		}
		body = fmt.Sprintf(`{"model":"%s","max_output_tokens":64,"stream":false,"input":"Reply OK","reasoning":{"effort":"high"}}`, modelName)
		headers = map[string]string{
			"Content-Type":  "application/json",
			"Authorization": "Bearer " + apiKey,
		}
	case "openai":
		trimmed := strings.TrimRight(baseURL, "/")
		if !strings.HasSuffix(trimmed, "/v1") {
			trimmed += "/v1"
		}
		url = trimmed + "/chat/completions"
		body = fmt.Sprintf(`{"model":"%s","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"Reply OK"}]}`, modelName)
		headers = map[string]string{
			"Content-Type":  "application/json",
			"Authorization": "Bearer " + apiKey,
		}
	default:
		envBase := os.Getenv("ANTHROPIC_BASE_URL")
		if envBase == "" {
			envBase = baseURL
		}
		url = envBase + "/v1/messages"
		body = fmt.Sprintf(`{"model":"%s","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"Reply OK"}]}`, modelName)
		headers = map[string]string{
			"Content-Type":     "application/json",
			"X-Api-Key":        apiKey,
			"anthropic-version": "2023-06-01",
		}
	}

	req, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	fmt.Printf("  URL: %s\n", url)
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	elapsed := time.Since(start).Round(time.Millisecond)
	if err != nil {
		fmt.Printf("  FAIL (network): %v (took %s)\n", err, elapsed)
		return
	}
	defer resp.Body.Close()

	respBody := make([]byte, 2048)
	n, _ := resp.Body.Read(respBody)
	fmt.Printf("  Status: %s (took %s)\n", resp.Status, elapsed)
	fmt.Printf("  Body (first 300): %.300s\n", string(respBody[:n]))
}
