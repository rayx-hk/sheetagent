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
	ProviderOpenAI     ProviderType = "openai"
	ProviderSub2API    ProviderType = "sub2api"
	ProviderCRS        ProviderType = "crs"
)

func NewChatModel(ctx context.Context, cfg config.ModelProviderConfig) (model.ToolCallingChatModel, error) {
	switch ProviderType(cfg.Type) {
	case ProviderClaude:
		return newClaudeModel(ctx, cfg)
	case ProviderGemini:
		return newGeminiModel(ctx, cfg)
	case ProviderOpenRouter:
		return newOpenRouterModel(ctx, cfg)
	case ProviderOpenAI:
		return newOpenAIModel(ctx, cfg)
	case ProviderSub2API:
		return newResponsesModel(ctx, cfg)
	case ProviderCRS:
		return newResponsesModel(ctx, cfg)
	default:
		return nil, fmt.Errorf("unknown provider: %s", cfg.Type)
	}
}
