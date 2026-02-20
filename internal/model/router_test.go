package model

import (
	"testing"
)

func TestRoleConstants(t *testing.T) {
	roles := []Role{RolePlanner, RoleCoder, RoleInformer, RoleEvaluator}
	for _, r := range roles {
		if string(r) == "" {
			t.Errorf("role should not be empty")
		}
	}
}

func TestProviderTypes(t *testing.T) {
	types := []ProviderType{ProviderClaude, ProviderGemini, ProviderOpenRouter}
	for _, pt := range types {
		if string(pt) == "" {
			t.Errorf("provider type should not be empty")
		}
	}
}
