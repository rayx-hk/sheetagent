package model

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openrouter"
	"github.com/cloudwego/eino/components/model"

	"github.com/rayx-hk/sheetagent/config"
)

func newOpenAIModel(ctx context.Context, cfg config.ModelProviderConfig) (model.ToolCallingChatModel, error) {
	apiKey := getOpenAIAPIKey()

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = os.Getenv("OPENAI_BASE_URL")
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(baseURL, "/v1") {
		baseURL += "/v1"
	}

	oCfg := &openrouter.Config{
		APIKey:    apiKey,
		BaseURL:   baseURL,
		Model:     cfg.Model,
		MaxTokens: &cfg.MaxTokens,
		HTTPClient: &http.Client{
			Timeout:   120 * time.Second,
			Transport: http.DefaultTransport,
		},
	}

	if cfg.ReasoningEffort != "" {
		oCfg.Reasoning = &openrouter.Reasoning{
			Effort: openrouter.Effort(cfg.ReasoningEffort),
		}
	}

	return openrouter.NewChatModel(ctx, oCfg)
}

func getOpenAIAPIKey() string {
	if v := os.Getenv("OPENAI_API_KEY"); v != "" {
		return v
	}
	if v := os.Getenv("ANTHROPIC_AUTH_TOKEN"); v != "" {
		return v
	}
	return ""
}
