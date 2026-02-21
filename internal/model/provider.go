package model

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/model"

	"github.com/rayx-hk/sheetagent/config"
)

type ProviderType string

const (
	ProviderClaude     ProviderType = "claude"
	ProviderGemini     ProviderType = "gemini"
	ProviderOpenRouter ProviderType = "openrouter"
)

func NewChatModel(ctx context.Context, cfg config.ModelProviderConfig) (model.ToolCallingChatModel, error) {
	switch ProviderType(cfg.Type) {
	case ProviderClaude:
		return newClaudeModel(ctx, cfg)
	case ProviderGemini:
		return newGeminiModel(ctx, cfg)
	case ProviderOpenRouter:
		return newOpenRouterModel(ctx, cfg)
	default:
		return nil, fmt.Errorf("unknown provider: %s", cfg.Type)
	}
}
