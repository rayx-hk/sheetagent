package config

import (
	"fmt"
	"os"
	"time"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Executor ExecutorConfig `yaml:"executor"`
	Docker   DockerConfig   `yaml:"docker"`
	Bench    BenchConfig    `yaml:"bench"`
	RAG      RAGConfig      `yaml:"rag"`
	Models   ModelsConfig   `yaml:"model_routing"`
}

type ServerConfig struct {
	Port int `yaml:"port"`
}

type ExecutorConfig struct {
	Type       string        `yaml:"type"` // embedded | docker | jupyter
	PythonPath string        `yaml:"python_path"`
	Timeout    time.Duration `yaml:"timeout"`
	WorkDir    string        `yaml:"work_dir"`
}

type DockerConfig struct {
	ExecutorImage string `yaml:"executor_image"`
	EvalImage     string `yaml:"eval_image"`
}

type BenchConfig struct {
	Concurrency int    `yaml:"concurrency"`
	MaxRetry    int    `yaml:"max_retry"`
	DatasetDir  string `yaml:"dataset_dir"`
	OutputDir   string `yaml:"output_dir"`
	ReportDir   string `yaml:"report_dir"`
}

// RAGConfig controls the RAG few-shot injection system.
//
// IMPORTANT: RAG MUST be disabled during benchmark evaluation to prevent
// test-set contamination. When RAG is populated from benchmark AC results,
// those solutions are indirectly validated by golden answers — reusing them
// on the same benchmark constitutes data leakage. RAG is a product-mode
// feature only.
type RAGConfig struct {
	// Enabled controls whether RAG few-shot injection is active.
	// MUST be false for benchmark runs. Only enable for production/serving.
	Enabled  bool   `yaml:"enabled"`
	// StorePath is the file path for the RAG JSON store.
	StorePath string `yaml:"store_path"`
}

type ModelsConfig struct {
	Planner   RoleModelConfig `yaml:"planner"`
	Coder     RoleModelConfig `yaml:"coder"`
	Informer  RoleModelConfig `yaml:"informer"`
	Evaluator RoleModelConfig `yaml:"evaluator"`
}

type RoleModelConfig struct {
	Primary  ModelProviderConfig  `yaml:"primary"`
	Fallback *ModelProviderConfig `yaml:"fallback,omitempty"`
}

type ModelProviderConfig struct {
	Type             string `yaml:"type"` // claude | gemini | openrouter | openai
	Model            string `yaml:"model"`
	MaxTokens        int    `yaml:"max_tokens"`
	BaseURL          string `yaml:"base_url,omitempty"`
	PromptCache      bool   `yaml:"prompt_cache,omitempty"`
	ReasoningEffort  string `yaml:"reasoning_effort,omitempty"` // none | low | medium | high (for OpenAI-compatible models)
}

func LoadEnv(paths ...string) {
	if len(paths) == 0 {
		paths = []string{".env"}
	}
	_ = godotenv.Load(paths...)
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	applyDefaults(&cfg)
	return &cfg, nil
}

func LoadModelsConfig(path string) (*ModelsConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read models config %s: %w", path, err)
	}
	var wrapper struct {
		ModelRouting ModelsConfig `yaml:"model_routing"`
	}
	if err := yaml.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("parse models config %s: %w", path, err)
	}
	return &wrapper.ModelRouting, nil
}

func LoadAll(configPath, modelsPath string) (*Config, error) {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	models, err := LoadModelsConfig(modelsPath)
	if err != nil {
		return nil, err
	}
	cfg.Models = *models
	return cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8090
	}
	if cfg.Executor.Type == "" {
		cfg.Executor.Type = "embedded"
	}
	if cfg.Executor.PythonPath == "" {
		cfg.Executor.PythonPath = "python3"
	}
	if cfg.Executor.Timeout == 0 {
		cfg.Executor.Timeout = 120 * time.Second
	}
	if cfg.Executor.WorkDir == "" {
		cfg.Executor.WorkDir = "./output/workdir"
	}
	if cfg.Bench.Concurrency == 0 {
		cfg.Bench.Concurrency = 8
	}
	if cfg.Bench.MaxRetry == 0 {
		cfg.Bench.MaxRetry = 3
	}
	if cfg.Bench.DatasetDir == "" {
		cfg.Bench.DatasetDir = "./data"
	}
	if cfg.Bench.OutputDir == "" {
		cfg.Bench.OutputDir = "./output"
	}
	if cfg.Bench.ReportDir == "" {
		cfg.Bench.ReportDir = "./reports"
	}
}

func MustGetEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("required env var %s not set", key))
	}
	return v
}

func GetEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
