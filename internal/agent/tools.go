package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"

	"github.com/rayx-hk/sheetagent/internal/executor"
)

// --- PythonRunnerTool ---

type pythonRunInput struct {
	Code    string `json:"code" jsonschema:"description=Python code to execute"`
	WorkDir string `json:"work_dir" jsonschema:"description=Working directory for execution"`
}

type pythonRunOutput struct {
	Stdout        string   `json:"stdout"`
	Stderr        string   `json:"stderr"`
	ExitCode      int      `json:"exit_code"`
	Files         []string `json:"files,omitempty"`
	MutationError string   `json:"mutation_error,omitempty"`
}

func NewPythonRunnerTool(exec executor.Executor) (tool.BaseTool, error) {
	return toolutils.InferTool(
		"python_runner",
		"Execute Python code in a sandbox. Returns stdout, stderr, and exit code. The code should use openpyxl for xlsx operations. When a stateful REPL session is active, variables and imports persist across calls.",
		func(ctx context.Context, input pythonRunInput) (string, error) {
			var result *executor.ExecResult
			var err error
			if session := REPLSessionFromContext(ctx); session != nil {
				result, err = session.Execute(ctx, input.Code)
			} else {
				result, err = exec.Execute(ctx, input.Code, input.WorkDir)
			}
			if err != nil {
				return "", fmt.Errorf("execute python: %w", err)
			}
			result.Stdout = truncateTail(result.Stdout, 3000)
			result.Stderr = truncateTail(result.Stderr, 1000)

			out := pythonRunOutput{
				Stdout:   result.Stdout,
				Stderr:   result.Stderr,
				ExitCode: result.ExitCode,
				Files:    result.Files,
			}

			if ap, _ := ctx.Value(answerPositionKey).(string); ap != "" && result.ExitCode == 0 {
				if sr, _ := executor.ParseStructuredResult(result.Stdout); sr != nil {
					if mutErr := executor.ValidateMutationGating(sr.ChangeLog, ap); mutErr != "" {
						out.MutationError = mutErr
					}
				}
			}

			b, err := json.Marshal(out)
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

// --- FormulaEvalTool (Formula Micro-Sandbox) ---

type formulaEvalInput struct {
	Formula      string                 `json:"formula" jsonschema:"description=Excel formula to evaluate (e.g. =SUM(A1:A10) or =VLOOKUP(A2,Sheet2!B:D,3,FALSE))"`
	ContextCells map[string]interface{} `json:"context_cells" jsonschema:"description=Map of cell addresses to values providing context for formula evaluation (e.g. {\"A1\": 10, \"A2\": 20})"`
	SheetFile    string                 `json:"sheet_file,omitempty" jsonschema:"description=Optional path to an xlsx file to use as context for formula evaluation"`
}

type formulaEvalOutput struct {
	Result string `json:"result"`
	Error  string `json:"error,omitempty"`
}

func NewFormulaEvalTool(evaluator *executor.FormulaEvaluator) (tool.BaseTool, error) {
	return toolutils.InferTool(
		"test_formula_eval",
		"Test a single Excel formula instantly without writing to the file. Provide the formula and context cells (neighboring cell values the formula references). Returns the computed result. Use this to verify formula correctness BEFORE writing it to the workbook.",
		func(ctx context.Context, input formulaEvalInput) (string, error) {
			result, err := evaluator.EvaluateSingle(input.Formula, input.ContextCells, input.SheetFile)

			out := formulaEvalOutput{}
			if err != nil {
				out.Error = err.Error()
				out.Result = ""
			} else {
				out.Result = result
			}

			b, jsonErr := json.Marshal(out)
			if jsonErr != nil {
				return "", jsonErr
			}
			return string(b), nil
		},
	)
}
