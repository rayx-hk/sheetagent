package model

import (
	"context"
	"fmt"
	"log/slog"

	eimodel "github.com/cloudwego/eino/components/model"

	"github.com/rayx-hk/dataagent/config"
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
}

func NewRouter(ctx context.Context, cfg config.ModelsConfig) (*ModelRouter, error) {
	router := &ModelRouter{providers: make(map[Role][]eimodel.ToolCallingChatModel)}
	roles := map[Role]config.RoleModelConfig{
		RolePlanner:   cfg.Planner,
		RoleCoder:     cfg.Coder,
		RoleInformer:  cfg.Informer,
		RoleEvaluator: cfg.Evaluator,
	}
	for role, rc := range roles {
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
