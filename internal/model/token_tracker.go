package model

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	eimodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// TokenStats holds accumulated token usage counters. All fields are safe for
// concurrent atomic access when used as a per-task counter via WithTaskStats.
type TokenStats struct {
	PromptTokens     int64
	CompletionTokens int64
	CachedTokens     int64
	Calls            int64
}

// TokenTracker accumulates token usage across all LLM calls (global),
// and optionally per-task when a *TokenStats is stored in context.
type TokenTracker struct {
	mu    sync.Mutex
	total TokenStats
}

func NewTokenTracker() *TokenTracker {
	return &TokenTracker{}
}

func (t *TokenTracker) Snapshot() TokenStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.total
}

// --- Per-task tracking via context ---

type taskStatsKey struct{}

// WithTaskStats returns a child context carrying a fresh per-task counter.
func WithTaskStats(ctx context.Context) (context.Context, *TokenStats) {
	ts := &TokenStats{}
	return context.WithValue(ctx, taskStatsKey{}, ts), ts
}

func GetTaskStats(ctx context.Context) *TokenStats {
	ts, _ := ctx.Value(taskStatsKey{}).(*TokenStats)
	return ts
}

// --- Model wrapper ---

// TrackedModel wraps a ToolCallingChatModel and records token usage from
// every Generate() response into both the global TokenTracker and the
// per-task *TokenStats (if present in ctx).
type TrackedModel struct {
	inner   eimodel.ToolCallingChatModel
	tracker *TokenTracker
}

func NewTrackedModel(inner eimodel.ToolCallingChatModel, tracker *TokenTracker) *TrackedModel {
	return &TrackedModel{inner: inner, tracker: tracker}
}

func (m *TrackedModel) Generate(ctx context.Context, input []*schema.Message, opts ...eimodel.Option) (*schema.Message, error) {
	msg, err := m.inner.Generate(ctx, input, opts...)
	if err == nil && msg != nil {
		m.accumulate(ctx, msg)
	}
	return msg, err
}

func (m *TrackedModel) Stream(ctx context.Context, input []*schema.Message, opts ...eimodel.Option) (*schema.StreamReader[*schema.Message], error) {
	return m.inner.Stream(ctx, input, opts...)
}

func (m *TrackedModel) WithTools(tools []*schema.ToolInfo) (eimodel.ToolCallingChatModel, error) {
	newInner, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &TrackedModel{inner: newInner, tracker: m.tracker}, nil
}

func (m *TrackedModel) accumulate(ctx context.Context, msg *schema.Message) {
	if msg.ResponseMeta == nil || msg.ResponseMeta.Usage == nil {
		return
	}
	u := msg.ResponseMeta.Usage
	prompt := int64(u.PromptTokens)
	completion := int64(u.CompletionTokens)
	cached := int64(u.PromptTokenDetails.CachedTokens)

	m.tracker.mu.Lock()
	m.tracker.total.PromptTokens += prompt
	m.tracker.total.CompletionTokens += completion
	m.tracker.total.CachedTokens += cached
	m.tracker.total.Calls++
	m.tracker.mu.Unlock()

	if ts := GetTaskStats(ctx); ts != nil {
		atomic.AddInt64(&ts.PromptTokens, prompt)
		atomic.AddInt64(&ts.CompletionTokens, completion)
		atomic.AddInt64(&ts.CachedTokens, cached)
		atomic.AddInt64(&ts.Calls, 1)
	}
}

// --- Logging helpers ---

func LogTaskStats(taskID string, ts *TokenStats) {
	if ts == nil {
		return
	}
	slog.Info("task token usage",
		"task", taskID,
		"prompt_tokens", ts.PromptTokens,
		"completion_tokens", ts.CompletionTokens,
		"cached_tokens", ts.CachedTokens,
		"total_tokens", ts.PromptTokens+ts.CompletionTokens,
		"llm_calls", ts.Calls)
}

func LogTotalStats(t *TokenTracker) {
	s := t.Snapshot()
	slog.Info("total token usage",
		"prompt_tokens", s.PromptTokens,
		"completion_tokens", s.CompletionTokens,
		"cached_tokens", s.CachedTokens,
		"total_tokens", s.PromptTokens+s.CompletionTokens,
		"llm_calls", s.Calls)
}
