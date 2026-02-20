package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"

	"github.com/rayx-hk/dataagent/internal/executor"
)

// --- PythonRunnerTool ---

type pythonRunInput struct {
	Code    string `json:"code" jsonschema:"description=Python code to execute"`
	WorkDir string `json:"work_dir" jsonschema:"description=Working directory for execution"`
}

func NewPythonRunnerTool(exec executor.Executor) (tool.BaseTool, error) {
	return toolutils.InferTool(
		"python_runner",
		"Execute Python code in a sandbox. Returns stdout, stderr, and exit code. The code should use openpyxl for xlsx operations.",
		func(ctx context.Context, input pythonRunInput) (string, error) {
			result, err := exec.Execute(ctx, input.Code, input.WorkDir)
			if err != nil {
				return "", fmt.Errorf("execute python: %w", err)
			}
			result.Stdout = truncateTail(result.Stdout, 4000)
			result.Stderr = truncateTail(result.Stderr, 4000)
			b, err := json.Marshal(result)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	)
}

func truncateTail(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return "...[truncated]..." + s[len(s)-max:]
}

func truncateHead(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...[truncated]..."
}
