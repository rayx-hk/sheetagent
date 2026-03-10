package agent

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

//go:embed prompt/codeact.md
var codeactPrompt string

// NewCodeActAgent creates the sole LLM agent for the system
func NewCodeActAgent(ctx context.Context, chatModel model.ToolCallingChatModel, tools []tool.BaseTool) (adk.Agent, error) {
	// Use ADK ChatModelAgent
	cfg := &adk.ChatModelAgentConfig{
		Name:        "CodeAct",
		Description: "Spreadsheet Code Expert",
		Instruction: codeactPrompt,
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: tools,
			},
		},
		Exit:          &adk.ExitTool{},
		MaxIterations: 15,
	}

	agent, err := adk.NewChatModelAgent(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create adk agent: %w", err)
	}

	return agent, nil
}

// RunCodeAct is a helper to run the agent
func RunCodeAct(ctx context.Context, ag adk.Agent, input CodeActInput) (*schema.Message, []string, []*schema.Message, error) {
	prevErrSec := ""
	if input.PreviousError != "" {
		// Truncate error feedback to prevent context overflow
		prevErr := input.PreviousError
		if len(prevErr) > 4000 {
			prevErr = prevErr[:4000] + "\n...[feedback truncated to prevent context overflow]"
		}
		prevErrSec = fmt.Sprintf(`
=== PREVIOUS ATTEMPT FAILED (Attempt %d) ===
%s

IMPORTANT: Analyze the failure above carefully. Common issues:
- If expected has value but got is empty → code did not write to the target cells
- If values differ → check calculation logic or format
- Regenerate the COMPLETE code with fixes applied.
`, input.Attempt, prevErr)
	}

	sopSec := ""
	if input.PromptAdditions != "" {
		sopSec = "\n" + input.PromptAdditions + "\n"
	}

	// Truncate compressed sheet overview for very large spreadsheets
	compressed := input.Compressed
	if len(compressed) > 8000 {
		compressed = compressed[:8000] + "\n...[sheet overview truncated — use PythonRunnerTool to explore data]"
	}

	instruction := fmt.Sprintf(`%s

Task Details:
- Input File: %s
- Answer Position: %s
- Instruction Type: %s
- Work Directory: %s

Compressed Sheet Overview:
%s
%s%s`, input.Instruction, input.InputFile, input.AnswerPosition, input.InstructionType, input.WorkDir, compressed, sopSec, prevErrSec)

	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent: ag,
	})

	runCtx := context.WithValue(ctx, answerPositionKey, input.AnswerPosition)
	initialMsg := schema.UserMessage(instruction)
	iter := runner.Run(runCtx, []*schema.Message{initialMsg})

	var lastMsg *schema.Message
	var toolOutputs []string
	trace := []*schema.Message{initialMsg}

	for {
		event, ok := iter.Next()
		if !ok {
			break
		}

		if event.Err != nil {
			return nil, nil, trace, event.Err
		}

		if msg, _, err := adk.GetMessage(event); err == nil && msg != nil {
			trace = append(trace, msg)
			if msg.Role == schema.Assistant {
				lastMsg = msg
			}
			if msg.Role == schema.Tool {
				toolOutputs = append(toolOutputs, msg.Content)
			}
		}
	}

	if lastMsg == nil {
		return nil, nil, trace, fmt.Errorf("no response from agent")
	}

	return lastMsg, toolOutputs, trace, nil
}
