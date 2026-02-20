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

	"github.com/cloudwego/eino/components/tool"
	"github.com/rayx-hk/dataagent/config"
	"github.com/rayx-hk/dataagent/internal/agent"
	"github.com/rayx-hk/dataagent/internal/eval"
	"github.com/rayx-hk/dataagent/internal/executor"
	"github.com/rayx-hk/dataagent/internal/model"
	"github.com/rayx-hk/dataagent/internal/orchestrator"
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

	coderModel, err := router.Get(model.RoleCoder)
	if err != nil {
		return err
	}

	exec := executor.NewEmbedded(cfg.Executor.PythonPath, cfg.Executor.Timeout)
	defer exec.Close()

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

	orch := orchestrator.NewOrchestrator(orchestrator.OrchestratorConfig{
		MaxRetry: 3,
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
