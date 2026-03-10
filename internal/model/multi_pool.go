// Package model adds MultiModelPool for MCTS path diversity.
// Each pool entry wraps a ToolCallingChatModel with metadata
// so the MCTS runner can distribute candidate generation across
// models from different providers (Claude, Gemini, etc.).
package model

import (
	"context"
	"fmt"
	"log/slog"

	eimodel "github.com/cloudwego/eino/components/model"

	"github.com/rayx-hk/sheetagent/config"
)

// PoolEntry pairs a chat model with its provider metadata.
type PoolEntry struct {
	Model    eimodel.ToolCallingChatModel
	Provider string // "claude" | "gemini" | "openrouter"
	Name     string // human-readable, e.g. "claude-sonnet-4-20250514"
}

// MultiModelPool holds multiple models for concurrent sampling.
type MultiModelPool struct {
	entries []PoolEntry
}

// NewMultiModelPool creates a pool from the coder role config.
// It always includes the primary model; if a fallback is configured,
// it is added as a second entry for diversity.
func NewMultiModelPool(ctx context.Context, coderCfg config.RoleModelConfig) (*MultiModelPool, error) {
	pool := &MultiModelPool{}

	primary, err := NewChatModel(ctx, coderCfg.Primary)
	if err != nil {
		return nil, fmt.Errorf("primary model: %w", err)
	}
	pool.entries = append(pool.entries, PoolEntry{
		Model:    primary,
		Provider: coderCfg.Primary.Type,
		Name:     coderCfg.Primary.Model,
	})

	if coderCfg.Fallback != nil {
		fb, err := NewChatModel(ctx, *coderCfg.Fallback)
		if err != nil {
			slog.Warn("fallback model init failed, continuing with primary only", "error", err)
		} else {
			pool.entries = append(pool.entries, PoolEntry{
				Model:    fb,
				Provider: coderCfg.Fallback.Type,
				Name:     coderCfg.Fallback.Model,
			})
		}
	}

	slog.Info("multi-model pool initialized", "count", len(pool.entries))
	for i, e := range pool.entries {
		slog.Info("pool entry", "index", i, "provider", e.Provider, "model", e.Name)
	}

	return pool, nil
}

// AddModel adds an extra model to the pool (e.g. from extra config).
func (p *MultiModelPool) AddModel(ctx context.Context, cfg config.ModelProviderConfig) error {
	m, err := NewChatModel(ctx, cfg)
	if err != nil {
		return fmt.Errorf("add model %s/%s: %w", cfg.Type, cfg.Model, err)
	}
	p.entries = append(p.entries, PoolEntry{
		Model:    m,
		Provider: cfg.Type,
		Name:     cfg.Model,
	})
	return nil
}

// Get returns the model at the given index (wraps around if out of range).
func (p *MultiModelPool) Get(idx int) PoolEntry {
	return p.entries[idx%len(p.entries)]
}

// Len returns the number of models in the pool.
func (p *MultiModelPool) Len() int {
	return len(p.entries)
}

// All returns all pool entries.
func (p *MultiModelPool) All() []PoolEntry {
	return p.entries
}

// Models returns just the ToolCallingChatModel interfaces for agent creation.
func (p *MultiModelPool) Models() []eimodel.ToolCallingChatModel {
	models := make([]eimodel.ToolCallingChatModel, len(p.entries))
	for i, e := range p.entries {
		models[i] = e.Model
	}
	return models
}
