// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package toolhost

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/sashabaranov/go-openai"
)

// openAIClient bundles the chat client and the model/deployment name to send on each request.
type openAIClient struct {
	client *openai.Client
	model  string
}

func newOpenAIClientFromModel(model string) (*openAIClient, error) {
	// Accept the LiteLLM-style "azure/<deployment>" spelling as an alias for "azure:<deployment>".
	if rest, ok := strings.CutPrefix(model, "azure/"); ok {
		model = "azure:" + rest
	}

	switch {
	case strings.HasPrefix(model, "azure:"):
		return newAzureClient(model)
	case strings.HasPrefix(model, "ollama:"):
		return newOllamaCompatibleClient(strings.TrimPrefix(model, "ollama:"))
	default:
		return newOpenAICompatibleClient(model)
	}
}

// azureOpenAIBaseURL returns the Azure OpenAI endpoint base URL.
// AZURE_OPENAI_BASE_URL is preferred (matches scanner/behavioral and common Azure SDK naming);
// AZURE_OPENAI_ENDPOINT is still accepted as a fallback.
func azureOpenAIBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("AZURE_OPENAI_BASE_URL")); v != "" {
		return v
	}

	return strings.TrimSpace(os.Getenv("AZURE_OPENAI_ENDPOINT"))
}

func newAzureClient(fullModel string) (*openAIClient, error) {
	apiKey := os.Getenv("AZURE_OPENAI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("AZURE_OPENAI_API_KEY is required for azure: models")
	}

	baseURL := azureOpenAIBaseURL()
	if baseURL == "" {
		return nil, fmt.Errorf("AZURE_OPENAI_BASE_URL or AZURE_OPENAI_ENDPOINT is required for azure: models")
	}

	cfg := openai.DefaultAzureConfig(apiKey, baseURL)
	if v := os.Getenv("AZURE_OPENAI_API_VERSION"); v != "" {
		cfg.APIVersion = v
	}

	// Deployment name: AZURE_OPENAI_DEPLOYMENT wins, otherwise the part after "azure:".
	// It is used both in the request URL (via the mapper) and as the request body "model",
	// which Azure ignores but Azure-fronting gateways route on, so it must not carry the prefix.
	deployment := strings.TrimSpace(os.Getenv("AZURE_OPENAI_DEPLOYMENT"))
	if deployment == "" {
		deployment = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(fullModel), "azure:"))
	}

	cfg.AzureModelMapperFunc = func(string) string { return deployment }

	return &openAIClient{
		client: openai.NewClientWithConfig(cfg),
		model:  deployment,
	}, nil
}

func newOllamaCompatibleClient(model string) (*openAIClient, error) {
	baseURL := os.Getenv("OLLAMA_BASE_URL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:11434/v1"
	}

	cfg := openai.DefaultConfig("ollama")
	cfg.BaseURL = baseURL

	return &openAIClient{
		client: openai.NewClientWithConfig(cfg),
		model:  strings.TrimSpace(model),
	}, nil
}

func newOpenAICompatibleClient(model string) (*openAIClient, error) {
	// openai:gpt-4o -> OpenAI API; also supports OPENAI_BASE_URL for proxies.
	model = strings.TrimPrefix(model, "openai:")

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY is required for OpenAI-compatible models (use azure: prefix for Azure OpenAI)")
	}

	cfg := openai.DefaultConfig(apiKey)
	if base := os.Getenv("OPENAI_BASE_URL"); base != "" {
		cfg.BaseURL = base
	}

	return &openAIClient{
		client: openai.NewClientWithConfig(cfg),
		model:  strings.TrimSpace(model),
	}, nil
}

var _ ChatCompleter = (*openAIClient)(nil)

func (c *openAIClient) Model() string {
	if c == nil {
		return ""
	}

	return c.model
}

func (c *openAIClient) ChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	if c == nil || c.client == nil {
		return openai.ChatCompletionResponse{}, fmt.Errorf("openai client is nil")
	}

	resp, err := c.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return openai.ChatCompletionResponse{}, fmt.Errorf("openai chat completion: %w", err)
	}

	return resp, nil
}
