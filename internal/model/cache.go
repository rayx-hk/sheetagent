package model

import (
	"context"
	"log/slog"

	"github.com/cloudwego/eino-ext/components/model/claude"
	eimodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// CacheEnabledModel wraps a ToolCallingChatModel and injects Claude's
// prompt caching option into every Generate/Stream call.
//
// Claude's prompt caching (cache_control: ephemeral) causes the API to
// cache the system prompt, tool definitions, and conversation prefix.
// Subsequent calls with the same prefix hit the cache, reducing input
// token cost by ~90% and significantly lowering TTFT.
//
// The auto-cache option sets breakpoints on:
//   - Last system message
//   - Last tool definition
//   - Last user message (session cache for multi-turn)
//
// For non-Claude models this option is harmlessly ignored (it uses
// model.WrapImplSpecificOptFn which only applies to Claude's options struct).
type CacheEnabledModel struct {
	inner eimodel.ToolCallingChatModel
}

func NewCacheEnabledModel(inner eimodel.ToolCallingChatModel) *CacheEnabledModel {
	return &CacheEnabledModel{inner: inner}
}

func (m *CacheEnabledModel) Generate(ctx context.Context, input []*schema.Message, opts ...eimodel.Option) (*schema.Message, error) {
	opts = append(opts, claude.WithEnableAutoCache(true))
	msg, err := m.inner.Generate(ctx, input, opts...)
	if err == nil && msg != nil {
		logCacheUsage(msg)
	}
	return msg, err
}

func (m *CacheEnabledModel) Stream(ctx context.Context, input []*schema.Message, opts ...eimodel.Option) (*schema.StreamReader[*schema.Message], error) {
	opts = append(opts, claude.WithEnableAutoCache(true))
	return m.inner.Stream(ctx, input, opts...)
}

func (m *CacheEnabledModel) WithTools(tools []*schema.ToolInfo) (eimodel.ToolCallingChatModel, error) {
	newInner, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &CacheEnabledModel{inner: newInner}, nil
}

func logCacheUsage(msg *schema.Message) {
	if msg.ResponseMeta == nil || msg.ResponseMeta.Usage == nil {
		return
	}
	u := msg.ResponseMeta.Usage
	cached := u.PromptTokenDetails.CachedTokens
	if cached > 0 {
		slog.Info("prompt cache HIT",
			"cached_tokens", cached,
			"prompt_tokens", u.PromptTokens,
			"completion_tokens", u.CompletionTokens)
	} else if u.PromptTokens > 0 {
		slog.Debug("prompt cache MISS",
			"prompt_tokens", u.PromptTokens,
			"completion_tokens", u.CompletionTokens)
	}
}
