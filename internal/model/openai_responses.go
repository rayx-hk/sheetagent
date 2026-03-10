package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/rayx-hk/sheetagent/config"
)

// responsesModel implements ToolCallingChatModel via the OpenAI Responses API.
type responsesModel struct {
	apiKey          string
	baseURL         string
	modelName       string
	maxTokens       int
	reasoningEffort string
	tools           []respTool
	httpClient      *http.Client
}

func newResponsesModel(_ context.Context, cfg config.ModelProviderConfig) (model.ToolCallingChatModel, error) {
	apiKey := getOpenAIAPIKey()
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = os.Getenv("OPENAI_BASE_URL")
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	return &responsesModel{
		apiKey:          apiKey,
		baseURL:         baseURL,
		modelName:       cfg.Model,
		maxTokens:       cfg.MaxTokens,
		reasoningEffort: cfg.ReasoningEffort,
		httpClient:      &http.Client{},
	}, nil
}

func (m *responsesModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	cp := *m
	cp.tools = make([]respTool, 0, len(tools))
	for _, t := range tools {
		rt := respTool{
			Type: "function",
			Name: t.Name,
			Desc: t.Desc,
		}
		if t.ParamsOneOf != nil {
			if js, err := t.ParamsOneOf.ToJSONSchema(); err == nil && js != nil {
				if raw, err := json.Marshal(js); err == nil {
					rt.Parameters = raw
				}
			}
		}
		cp.tools = append(cp.tools, rt)
	}
	return &cp, nil
}

func (m *responsesModel) Generate(ctx context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	reqBody := m.buildRequest(input, false)
	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := responsesURL(m.baseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.apiKey)

	start := time.Now()
	resp, err := m.httpClient.Do(req)
	if err != nil {
		slog.Warn("responses API network error", "elapsed", time.Since(start).Round(time.Millisecond), "error", err)
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		slog.Warn("responses API error", "status", resp.StatusCode, "elapsed", time.Since(start).Round(time.Millisecond), "body", truncBody(body, 200))
		return nil, fmt.Errorf("error, status code: %d, status: %s, message: %s",
			resp.StatusCode, resp.Status, truncBody(body, 300))
	}

	var respObj respResponse
	if err := json.Unmarshal(body, &respObj); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w (body: %.200s)", err, body)
	}

	msg, err := m.parseResponse(&respObj)
	if err != nil {
		return nil, err
	}

	slog.Debug("responses API ok", "elapsed", time.Since(start).Round(time.Millisecond),
		"content_len", len(msg.Content), "tool_calls", len(msg.ToolCalls))
	return msg, nil
}

func (m *responsesModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}

	reader, writer := schema.Pipe[*schema.Message](1)
	go func() {
		defer writer.Close()
		_ = writer.Send(msg, nil)
	}()
	return reader, nil
}

func (m *responsesModel) buildRequest(input []*schema.Message, stream bool) map[string]any {
	req := map[string]any{
		"model":  m.modelName,
		"stream": stream,
	}

	if m.maxTokens > 0 {
		req["max_output_tokens"] = m.maxTokens
	}
	if m.reasoningEffort != "" {
		req["reasoning"] = map[string]any{"effort": m.reasoningEffort}
	}
	if len(m.tools) > 0 {
		req["tools"] = m.tools
	}

	var instructions string
	var items []map[string]any

	for _, msg := range input {
		switch msg.Role {
		case schema.System:
			instructions += msg.Content + "\n"

		case schema.User:
			items = append(items, map[string]any{
				"type":    "message",
				"role":    "user",
				"content": msg.Content,
			})

		case schema.Assistant:
			if len(msg.ToolCalls) > 0 {
				for _, tc := range msg.ToolCalls {
					items = append(items, map[string]any{
						"type":      "function_call",
						"call_id":   tc.ID,
						"name":      tc.Function.Name,
						"arguments": tc.Function.Arguments,
					})
				}
			} else {
				items = append(items, map[string]any{
					"type":    "message",
					"role":    "assistant",
					"content": msg.Content,
				})
			}

		case schema.Tool:
			items = append(items, map[string]any{
				"type":    "function_call_output",
				"call_id": msg.ToolCallID,
				"output":  msg.Content,
			})
		}
	}

	if instructions != "" {
		req["instructions"] = strings.TrimSpace(instructions)
	}
	if len(items) > 0 {
		req["input"] = items
	}

	return req
}

func (m *responsesModel) parseResponse(resp *respResponse) (*schema.Message, error) {
	msg := &schema.Message{Role: schema.Assistant}

	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			for _, c := range item.Content {
				if c.Type == "output_text" {
					msg.Content += c.Text
				}
			}

		case "function_call":
			msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
				ID:   item.CallID,
				Type: "function",
				Function: schema.FunctionCall{
					Name:      item.Name,
					Arguments: item.Arguments,
				},
			})

		case "reasoning":
			// encrypted reasoning — skip
		}
	}

	if resp.Usage != nil {
		msg.ResponseMeta = &schema.ResponseMeta{
			Usage: &schema.TokenUsage{
				PromptTokens:     resp.Usage.InputTokens,
				CompletionTokens: resp.Usage.OutputTokens,
				TotalTokens:      resp.Usage.TotalTokens,
			},
		}
	}

	return msg, nil
}

// ── Responses API types ──

type respTool struct {
	Type       string          `json:"type"`
	Name       string          `json:"name"`
	Desc       string          `json:"description,omitempty"`
	Parameters json.RawMessage `json:"parameters,omitempty"`
}

type respResponse struct {
	ID     string           `json:"id"`
	Status string           `json:"status"`
	Output []respOutputItem `json:"output"`
	Usage  *respUsage       `json:"usage,omitempty"`
}

type respOutputItem struct {
	Type      string              `json:"type"`
	Content   []respContentPart   `json:"content,omitempty"`
	Name      string              `json:"name,omitempty"`
	Arguments string              `json:"arguments,omitempty"`
	CallID    string              `json:"call_id,omitempty"`
}

type respContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type respUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}


func truncBody(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "..."
}

func responsesURL(baseURL string) string {
	trimmed := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(trimmed, "/v1") || strings.HasSuffix(trimmed, "/openai") {
		return trimmed + "/responses"
	}
	return trimmed + "/v1/responses"
}
