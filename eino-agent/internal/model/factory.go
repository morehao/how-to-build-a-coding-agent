// Package model builds the Eino ToolCallingChatModel for the selected provider.
package model

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/eino-ext/components/model/claude"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/config"
)

// New constructs a tool-calling chat model. Both adapters are pure
// configuration; no network request is made here.
func New(ctx context.Context, cfg *config.Config) (model.ToolCallingChatModel, error) {
	switch cfg.Provider {
	case config.ProviderOpenAI, config.ProviderArk:
		cm, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
			APIKey:  cfg.APIKey,
			BaseURL: cfg.BaseURL,
			Model:   cfg.Model,
			Timeout: 120 * time.Second,
		})
		if err != nil {
			return nil, fmt.Errorf("create %s model %q: %w", cfg.Provider, cfg.Model, err)
		}
		return cm, nil

	case config.ProviderClaude:
		var baseURL *string
		if cfg.BaseURL != "" {
			u := cfg.BaseURL
			baseURL = &u
		}
		cm, err := claude.NewChatModel(ctx, &claude.Config{
			APIKey:         cfg.APIKey,
			BaseURL:        baseURL,
			Model:          cfg.Model,
			MaxTokens:      8192,
			RequestTimeout: 120 * time.Second,
		})
		if err != nil {
			return nil, fmt.Errorf("create claude model %q: %w", cfg.Model, err)
		}
		return cm, nil

	default:
		return nil, fmt.Errorf("unsupported provider %q", cfg.Provider)
	}
}
