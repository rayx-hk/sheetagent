package model

import (
	"context"

	"github.com/cloudwego/eino-ext/components/model/gemini"
	"github.com/cloudwego/eino/components/model"
	"google.golang.org/genai"

	"github.com/rayx-hk/dataagent/config"
)

func newGeminiModel(ctx context.Context, cfg config.ModelProviderConfig) (model.ToolCallingChatModel, error) {
	apiKey := config.GetEnv("GOOGLE_API_KEY", "")
	if apiKey == "" {
		apiKey = config.MustGetEnv("GEMINI_API_KEY")
	}

	cli, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, err
	}

	maxTokens := cfg.MaxTokens
	return gemini.NewChatModel(ctx, &gemini.Config{
		Client:    cli,
		Model:     cfg.Model,
		MaxTokens: &maxTokens,
	})
}
