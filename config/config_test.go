package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	os.WriteFile(configPath, []byte(`
server:
  port: 9090
executor:
  type: embedded
  python_path: python3
  timeout: 60s
bench:
  concurrency: 4
`), 0644)

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Server.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Server.Port)
	}
	if cfg.Executor.Type != "embedded" {
		t.Errorf("expected type embedded, got %s", cfg.Executor.Type)
	}
	if cfg.Bench.Concurrency != 4 {
		t.Errorf("expected concurrency 4, got %d", cfg.Bench.Concurrency)
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "empty.yaml")
	os.WriteFile(configPath, []byte(""), 0644)

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Server.Port != 8090 {
		t.Errorf("expected default port 8090, got %d", cfg.Server.Port)
	}
	if cfg.Executor.PythonPath != "python3" {
		t.Errorf("expected default python path, got %s", cfg.Executor.PythonPath)
	}
	if cfg.Bench.Concurrency != 8 {
		t.Errorf("expected default concurrency 8, got %d", cfg.Bench.Concurrency)
	}
}

func TestLoadModelsConfig(t *testing.T) {
	tmpDir := t.TempDir()
	modelsPath := filepath.Join(tmpDir, "models.yaml")
	os.WriteFile(modelsPath, []byte(`
model_routing:
  planner:
    primary:
      type: claude
      model: claude-opus-4-6
      max_tokens: 4096
  coder:
    primary:
      type: claude
      model: claude-opus-4-6
      max_tokens: 8192
  informer:
    primary:
      type: gemini
      model: gemini-3-pro
      max_tokens: 8192
  evaluator:
    primary:
      type: gemini
      model: gemini-3-pro
      max_tokens: 8192
`), 0644)

	models, err := LoadModelsConfig(modelsPath)
	if err != nil {
		t.Fatalf("load models: %v", err)
	}
	if models.Planner.Primary.Type != "claude" {
		t.Errorf("expected planner type claude, got %s", models.Planner.Primary.Type)
	}
	if models.Informer.Primary.Model != "gemini-3-pro" {
		t.Errorf("expected informer model gemini-3-pro, got %s", models.Informer.Primary.Model)
	}
}

func TestGetEnv(t *testing.T) {
	os.Setenv("TEST_ENV_VAR", "test_value")
	defer os.Unsetenv("TEST_ENV_VAR")

	if v := GetEnv("TEST_ENV_VAR", "default"); v != "test_value" {
		t.Errorf("expected test_value, got %s", v)
	}
	if v := GetEnv("NONEXISTENT_VAR", "fallback"); v != "fallback" {
		t.Errorf("expected fallback, got %s", v)
	}
}
