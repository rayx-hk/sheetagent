package model

import (
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestMergeToolCalls_StreamingChunks(t *testing.T) {
	msg := &schema.Message{Role: schema.Assistant}

	// Chunk 1: content_block_start — has ID and Name, no Arguments
	mergeToolCalls(msg, []schema.ToolCall{{
		ID:   "tooluse_abc123",
		Type: "function",
		Function: schema.FunctionCall{
			Name:      "python_runner",
			Arguments: "",
		},
	}})

	if len(msg.ToolCalls) != 1 {
		t.Fatalf("after start chunk: expected 1 tool call, got %d", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].ID != "tooluse_abc123" {
		t.Errorf("ID mismatch: %q", msg.ToolCalls[0].ID)
	}
	if msg.ToolCalls[0].Function.Name != "python_runner" {
		t.Errorf("Name mismatch: %q", msg.ToolCalls[0].Function.Name)
	}

	// Chunk 2: input_json_delta — NO ID, NO Name, has partial Arguments
	mergeToolCalls(msg, []schema.ToolCall{{
		Function: schema.FunctionCall{
			Arguments: `{"code": "import `,
		},
	}})

	if msg.ToolCalls[0].Function.Arguments != `{"code": "import ` {
		t.Errorf("after delta 1: args = %q", msg.ToolCalls[0].Function.Arguments)
	}

	// Chunk 3: more argument fragments
	mergeToolCalls(msg, []schema.ToolCall{{
		Function: schema.FunctionCall{
			Arguments: `openpyxl\nwb = openpyxl.load_workbook('test.xlsx')"}`,
		},
	}})

	expectedArgs := `{"code": "import openpyxl\nwb = openpyxl.load_workbook('test.xlsx')"}`
	if msg.ToolCalls[0].Function.Arguments != expectedArgs {
		t.Errorf("after delta 2: args = %q, want %q", msg.ToolCalls[0].Function.Arguments, expectedArgs)
	}

	// Final state: 1 tool call with full arguments
	if len(msg.ToolCalls) != 1 {
		t.Errorf("final: expected 1 tool call, got %d", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].Function.Name != "python_runner" {
		t.Errorf("final: name lost: %q", msg.ToolCalls[0].Function.Name)
	}
}

func TestMergeToolCalls_MultipleToolCalls(t *testing.T) {
	msg := &schema.Message{Role: schema.Assistant}

	// First tool call start
	mergeToolCalls(msg, []schema.ToolCall{{
		ID:   "tool_1",
		Type: "function",
		Function: schema.FunctionCall{Name: "python_runner", Arguments: ""},
	}})
	// First tool call arguments
	mergeToolCalls(msg, []schema.ToolCall{{
		Function: schema.FunctionCall{Arguments: `{"code": "print(1)"}`},
	}})

	// Second tool call start
	mergeToolCalls(msg, []schema.ToolCall{{
		ID:   "tool_2",
		Type: "function",
		Function: schema.FunctionCall{Name: "python_runner", Arguments: ""},
	}})
	// Second tool call arguments
	mergeToolCalls(msg, []schema.ToolCall{{
		Function: schema.FunctionCall{Arguments: `{"code": "print(2)"}`},
	}})

	if len(msg.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].Function.Arguments != `{"code": "print(1)"}` {
		t.Errorf("tool 1 args: %q", msg.ToolCalls[0].Function.Arguments)
	}
	if msg.ToolCalls[1].Function.Arguments != `{"code": "print(2)"}` {
		t.Errorf("tool 2 args: %q", msg.ToolCalls[1].Function.Arguments)
	}
}

func TestMergeToolCalls_EmptyChunk(t *testing.T) {
	msg := &schema.Message{Role: schema.Assistant}

	// Empty tool calls — should be no-op
	mergeToolCalls(msg, nil)
	mergeToolCalls(msg, []schema.ToolCall{})
	mergeToolCalls(msg, []schema.ToolCall{{
		Function: schema.FunctionCall{Arguments: "orphan"},
	}})

	if len(msg.ToolCalls) != 0 {
		t.Errorf("expected 0 tool calls for orphan chunk with no prior tool call, got %d", len(msg.ToolCalls))
	}
}
