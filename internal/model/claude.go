package model

import (
	"context"
	"os"

	"github.com/cloudwego/eino-ext/components/model/claude"
	"github.com/cloudwego/eino/components/model"

	"github.com/rayx-hk/dataagent/config"
)

func newClaudeModel(ctx context.Context, cfg config.ModelProviderConfig) (model.ToolCallingChatModel, error) {
	apiKey := getAnthropicAPIKey()

	cc := &claude.Config{
		APIKey:    apiKey,
		Model:     cfg.Model,
		MaxTokens: cfg.MaxTokens,
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = os.Getenv("ANTHROPIC_BASE_URL")
	}
	if baseURL != "" {
		cc.BaseURL = &baseURL
	}

	return claude.NewChatModel(ctx, cc)
}

func getAnthropicAPIKey() string {
	if v := os.Getenv("ANTHROPIC_AUTH_TOKEN"); v != "" {
		return v
	}
	return config.GetEnv("ANTHROPIC_API_KEY", "")
}
