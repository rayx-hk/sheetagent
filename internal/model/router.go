package model

import (
	"context"
	"fmt"
	"log/slog"

	eimodel "github.com/cloudwego/eino/components/model"

	"github.com/rayx-hk/sheetagent/config"
)

type Role string

const (
	RolePlanner   Role = "planner"
	RoleCoder     Role = "coder"
	RoleInformer  Role = "informer"
	RoleEvaluator Role = "evaluator"
)

type ModelRouter struct {
	providers map[Role][]eimodel.ToolCallingChatModel
	configs   map[Role]config.RoleModelConfig
}

func NewRouter(ctx context.Context, cfg config.ModelsConfig) (*ModelRouter, error) {
	router := &ModelRouter{
		providers: make(map[Role][]eimodel.ToolCallingChatModel),
		configs: map[Role]config.RoleModelConfig{
			RolePlanner:   cfg.Planner,
			RoleCoder:     cfg.Coder,
			RoleInformer:  cfg.Informer,
			RoleEvaluator: cfg.Evaluator,
		},
	}
	for role, rc := range router.configs {
		primary, err := NewChatModel(ctx, rc.Primary)
		if err != nil {
			return nil, fmt.Errorf("model for %s: %w", role, err)
		}
		models := []eimodel.ToolCallingChatModel{primary}
		if rc.Fallback != nil {
			fb, err := NewChatModel(ctx, *rc.Fallback)
			if err != nil {
				slog.Warn("fallback model init failed", "role", role, "error", err)
			} else {
				models = append(models, fb)
			}
		}
		router.providers[role] = models
	}
	return router, nil
}

func (r *ModelRouter) Get(role Role) (eimodel.ToolCallingChatModel, error) {
	m := r.providers[role]
	if len(m) == 0 {
		return nil, fmt.Errorf("no model for role: %s", role)
	}
	return m[0], nil
}

func (r *ModelRouter) GetFallback(role Role) (eimodel.ToolCallingChatModel, error) {
	m := r.providers[role]
	if len(m) < 2 {
		return nil, fmt.Errorf("no fallback model for role: %s", role)
	}
	return m[1], nil
}

// PromptCacheEnabled returns whether prompt caching is configured for the
// primary model of the given role.
func (r *ModelRouter) PromptCacheEnabled(role Role) bool {
	rc, ok := r.configs[role]
	if !ok {
		return false
	}
	return rc.Primary.PromptCache
}
