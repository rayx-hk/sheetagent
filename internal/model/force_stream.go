package model

import (
	"context"
	"fmt"
	"log/slog"

	eimodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// ForceStreamModel wraps a ToolCallingChatModel and converts all Generate()
// calls into Stream() calls internally. This is needed when the API proxy
// rejects non-streaming requests (e.g., "streaming is strongly recommended").
type ForceStreamModel struct {
	inner eimodel.ToolCallingChatModel
}

func NewForceStreamModel(inner eimodel.ToolCallingChatModel) *ForceStreamModel {
	return &ForceStreamModel{inner: inner}
}

func (m *ForceStreamModel) Generate(ctx context.Context, input []*schema.Message, opts ...eimodel.Option) (*schema.Message, error) {
	slog.Debug("ForceStreamModel: converting Generate to Stream")
	reader, err := m.inner.Stream(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return collectStream(reader)
}

func (m *ForceStreamModel) Stream(ctx context.Context, input []*schema.Message, opts ...eimodel.Option) (*schema.StreamReader[*schema.Message], error) {
	return m.inner.Stream(ctx, input, opts...)
}

func (m *ForceStreamModel) WithTools(tools []*schema.ToolInfo) (eimodel.ToolCallingChatModel, error) {
	newInner, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &ForceStreamModel{inner: newInner}, nil
}

// collectStream reads all chunks from a StreamReader and merges them into a single Message.
func collectStream(reader *schema.StreamReader[*schema.Message]) (*schema.Message, error) {
	if reader == nil {
		return nil, fmt.Errorf("nil stream reader")
	}
	defer reader.Close()

	var merged *schema.Message
	for {
		chunk, err := reader.Recv()
		if err != nil {
			break
		}
		if chunk == nil {
			continue
		}
		if merged == nil {
			merged = &schema.Message{
				Role:    chunk.Role,
				Content: chunk.Content,
			}
			if chunk.Extra != nil {
				merged.Extra = make(map[string]any)
				for k, v := range chunk.Extra {
					merged.Extra[k] = v
				}
			}
		} else {
			merged.Content += chunk.Content
			if chunk.Extra != nil {
				if merged.Extra == nil {
					merged.Extra = make(map[string]any)
				}
				for k, v := range chunk.Extra {
					merged.Extra[k] = v
				}
			}
		}
		mergeToolCalls(merged, chunk.ToolCalls)
	}

	if merged == nil {
		return nil, fmt.Errorf("empty stream: no chunks received")
	}
	return merged, nil
}

// mergeToolCalls handles streamed tool call chunks from the Claude API.
//
// Claude streams tool calls as:
//   - content_block_start: chunk has ID + Name, empty Arguments
//   - input_json_delta:    chunk has NO ID, NO Name, only Arguments fragment
//   - content_block_stop:  no tool call data
//
// So we must track by ID when present, and append to the last tool call
// when a chunk has only argument fragments (no ID).
func mergeToolCalls(msg *schema.Message, incoming []schema.ToolCall) {
	for _, tc := range incoming {
		if tc.ID != "" {
			found := false
			for i := range msg.ToolCalls {
				if msg.ToolCalls[i].ID == tc.ID {
					msg.ToolCalls[i].Function.Arguments += tc.Function.Arguments
					if tc.Function.Name != "" {
						msg.ToolCalls[i].Function.Name = tc.Function.Name
					}
					found = true
					break
				}
			}
			if !found {
				msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Function: schema.FunctionCall{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				})
			}
		} else if tc.Function.Arguments != "" && len(msg.ToolCalls) > 0 {
			msg.ToolCalls[len(msg.ToolCalls)-1].Function.Arguments += tc.Function.Arguments
		} else if tc.Function.Name != "" {
			msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: schema.FunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
	}
}
