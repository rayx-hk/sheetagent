package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	eimodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/rayx-hk/sheetagent/config"
	"github.com/rayx-hk/sheetagent/internal/agent"
	"github.com/rayx-hk/sheetagent/internal/eval"
	"github.com/rayx-hk/sheetagent/internal/executor"
	"github.com/rayx-hk/sheetagent/internal/model"
	"github.com/rayx-hk/sheetagent/internal/orchestrator"
)

func main() {
	instruction := flag.String("instruction", "", "Spreadsheet operation instruction")
	inputFile := flag.String("input", "", "Path to input xlsx file")
	answerPosition := flag.String("answer-position", "", "Target cell range (e.g. B3:B14)")
	configPath := flag.String("config", "config/config.yaml", "Path to config.yaml")
	modelsPath := flag.String("models", "config/models.yaml", "Path to models.yaml")
	flag.Parse()

	if *instruction == "" || *inputFile == "" {
		fmt.Fprintln(os.Stderr, "usage: agent -instruction \"...\" -input path/to/input.xlsx [-answer-position \"B3:B14\"]")
		os.Exit(1)
	}

	if err := run(*instruction, *inputFile, *answerPosition, *configPath, *modelsPath); err != nil {
		slog.Error("agent failed", "error", err)
		os.Exit(1)
	}
}

func run(instruction, inputFile, answerPosition, configPath, modelsPath string) error {
	config.LoadEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cfg, err := config.LoadAll(configPath, modelsPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	router, err := model.NewRouter(ctx, cfg.Models)
	if err != nil {
		return fmt.Errorf("init model router: %w", err)
	}

	rawModel, err := router.Get(model.RoleCoder)
	if err != nil {
		return err
	}
	var wrapped eimodel.ToolCallingChatModel = model.NewForceStreamModel(rawModel)
	if router.PromptCacheEnabled(model.RoleCoder) {
		wrapped = model.NewCacheEnabledModel(wrapped)
		slog.Info("prompt cache enabled", "role", model.RoleCoder)
	}
	coderModel := model.NewContextManagedModel(wrapped, 0)

	exec := executor.NewEmbedded(cfg.Executor.PythonPath, cfg.Executor.Timeout)
	defer exec.Close()

	replExec := executor.NewREPLExecutor(cfg.Executor.PythonPath, cfg.Executor.Timeout)

	runnerTool, err := agent.NewPythonRunnerTool(exec)
	if err != nil {
		return err
	}

	formulaEval := executor.NewFormulaEvaluator(cfg.Executor.PythonPath)
	formulaEvalTool, err := agent.NewFormulaEvalTool(formulaEval)
	if err != nil {
		return err
	}

	tools := []tool.BaseTool{
		runnerTool,
		formulaEvalTool,
	}

	codeActAgent, err := agent.NewCodeActAgent(ctx, coderModel, tools)
	if err != nil {
		return err
	}

	judge := eval.NewOJJudge(cfg.Executor.PythonPath)
	builder := orchestrator.NewPromptBuilder()

	orch := orchestrator.NewOrchestrator(orchestrator.OrchestratorConfig{
		MaxRetry:      3,
		REPLExecutor:  replExec,
		PythonPath:    cfg.Executor.PythonPath,
		UseDualEngine: true,
	}, codeActAgent, judge, builder)

	absInput, err := filepath.Abs(inputFile)
	if err != nil {
		return err
	}
	workDir := filepath.Dir(absInput)

	input, err := builder.BuildInput(ctx, instruction, answerPosition, "cell_level", workDir, absInput)
	if err != nil {
		return fmt.Errorf("build input: %w", err)
	}

	slog.Info("running orchestrator", "instruction", instruction, "input", absInput, "answer_position", answerPosition)

	result := orch.Run(ctx, input, "") // answerFile is empty for standalone agent run

	resBytes, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(resBytes))

	if !result.Success {
		return fmt.Errorf("task failed: %s", result.Error)
	}
	return nil
}
