package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/rayx-hk/sheetagent/config"
	"github.com/rayx-hk/sheetagent/internal/model"
)

func main() {
	config.LoadEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	modelsPath := "config/models.test.yaml"
	if len(os.Args) > 1 {
		modelsPath = os.Args[1]
	}

	models, err := config.LoadModelsConfig(modelsPath)
	if err != nil {
		slog.Error("load models config", "error", err)
		os.Exit(1)
	}

	m, err := model.NewChatModel(ctx, models.Planner.Primary)
	if err != nil {
		slog.Error("create model", "error", err)
		os.Exit(1)
	}
	slog.Info("model created", "type", models.Planner.Primary.Type, "model", models.Planner.Primary.Model)

	// Test 1: basic generate
	slog.Info("[Test 1] basic generate...")
	resp, err := m.Generate(ctx, []*schema.Message{
		schema.SystemMessage("You are a helpful assistant. Reply concisely."),
		schema.UserMessage("Say hello in exactly 5 words."),
	})
	if err != nil {
		slog.Error("generate failed", "error", err)
		os.Exit(1)
	}
	fmt.Printf("  Response: %s\n", resp.Content)
	printUsage(resp)
	slog.Info("[Test 1] PASSED")

	// Test 2: tool calling
	slog.Info("[Test 2] tool calling...")
	toolModel, err := m.WithTools([]*schema.ToolInfo{
		{
			Name: "get_weather",
			Desc: "Get the current weather for a city",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"city": {
					Type: "string",
					Desc: "The city name",
				},
			}),
		},
	})
	if err != nil {
		slog.Error("bind tools", "error", err)
		os.Exit(1)
	}

	resp2, err := toolModel.Generate(ctx, []*schema.Message{
		schema.SystemMessage("You are a helpful assistant that uses tools."),
		schema.UserMessage("What's the weather in Beijing?"),
	})
	if err != nil {
		slog.Error("generate with tools failed", "error", err)
		os.Exit(1)
	}
	fmt.Printf("  Response content: %q\n", resp2.Content)
	fmt.Printf("  Tool calls: %d\n", len(resp2.ToolCalls))
	for _, tc := range resp2.ToolCalls {
		fmt.Printf("    -> %s(%s)\n", tc.Function.Name, tc.Function.Arguments)
	}
	printUsage(resp2)
	slog.Info("[Test 2] PASSED")

	slog.Info("all smoke tests PASSED")
}

func printUsage(msg *schema.Message) {
	if msg.ResponseMeta != nil && msg.ResponseMeta.Usage != nil {
		u := msg.ResponseMeta.Usage
		fmt.Printf("  Tokens: prompt=%d completion=%d total=%d cached=%d\n",
			u.PromptTokens, u.CompletionTokens, u.TotalTokens, u.PromptTokenDetails.CachedTokens)
	}
}
