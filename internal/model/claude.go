package model

import (
	"context"
	"net/http"
	"os"

	"github.com/cloudwego/eino-ext/components/model/claude"
	"github.com/cloudwego/eino/components/model"

	"github.com/rayx-hk/sheetagent/config"
)

// proxyCleanTransport strips SDK-injected headers that third-party proxies
// (e.g. cursor.scihub.edu.kg) reject. The Anthropic Go SDK automatically adds
// X-Stainless-* telemetry headers, which some proxies interpret as
// non-Cursor clients and respond with 403 Forbidden.
type proxyCleanTransport struct {
	base http.RoundTripper
}

func (t *proxyCleanTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var toDelete []string
	for key := range req.Header {
		if len(key) > 12 && key[:12] == "X-Stainless-" {
			toDelete = append(toDelete, key)
		}
	}
	for _, key := range toDelete {
		req.Header.Del(key)
	}
	if ua := req.Header.Get("User-Agent"); ua != "" {
		req.Header.Set("User-Agent", "sheetagent/1.0")
	}
	return t.base.RoundTrip(req)
}

func newClaudeModel(ctx context.Context, cfg config.ModelProviderConfig) (model.ToolCallingChatModel, error) {
	apiKey := getAnthropicAPIKey()

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = os.Getenv("ANTHROPIC_BASE_URL")
	}

	useProxy := baseURL != "" && !isOfficialAnthropicURL(baseURL)

	cc := &claude.Config{
		APIKey:    apiKey,
		Model:     cfg.Model,
		MaxTokens: cfg.MaxTokens,
	}

	if baseURL != "" {
		cc.BaseURL = &baseURL
	}

	if useProxy {
		cc.HTTPClient = &http.Client{
			Transport: &proxyCleanTransport{base: http.DefaultTransport},
		}
	}

	return claude.NewChatModel(ctx, cc)
}

func isOfficialAnthropicURL(u string) bool {
	return u == "" ||
		u == "https://api.anthropic.com" ||
		u == "https://api.anthropic.com/"
}

func getAnthropicAPIKey() string {
	if v := os.Getenv("ANTHROPIC_AUTH_TOKEN"); v != "" {
		return v
	}
	return config.GetEnv("ANTHROPIC_API_KEY", "")
}
