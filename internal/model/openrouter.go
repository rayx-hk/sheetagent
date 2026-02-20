package model

import (
	"context"

	"github.com/cloudwego/eino-ext/components/model/openrouter"
	"github.com/cloudwego/eino/components/model"

	"github.com/rayx-hk/dataagent/config"
)

func newOpenRouterModel(ctx context.Context, cfg config.ModelProviderConfig) (model.ToolCallingChatModel, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}

	maxTokens := cfg.MaxTokens
	return openrouter.NewChatModel(ctx, &openrouter.Config{
		APIKey:    config.MustGetEnv("OPENROUTER_API_KEY"),
		BaseURL:   baseURL,
		Model:     cfg.Model,
		MaxTokens: &maxTokens,
	})
}
