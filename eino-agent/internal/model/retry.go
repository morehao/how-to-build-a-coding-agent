package model

import (
	"context"
	"fmt"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/retry"
)

// retryModel retries model creation calls. Once a stream has started, a
// mid-stream failure is returned to the agent: replaying a partially consumed
// stream would duplicate text or tool calls.
type retryModel struct {
	inner einomodel.ToolCallingChatModel
	cfg   retry.Config
}

// NewRetryModel wraps inner with a retry policy.
func NewRetryModel(inner einomodel.ToolCallingChatModel, cfg retry.Config) einomodel.ToolCallingChatModel {
	return &retryModel{inner: inner, cfg: cfg}
}

func (m *retryModel) Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	return retry.Do(ctx, m.cfg, func(ctx context.Context) (*schema.Message, error) {
		return m.inner.Generate(ctx, input, opts...)
	})
}

func (m *retryModel) Stream(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	sr, err := retry.Do(ctx, m.cfg, func(ctx context.Context) (*schema.StreamReader[*schema.Message], error) {
		return m.inner.Stream(ctx, input, opts...)
	})
	if err != nil {
		return nil, fmt.Errorf("model stream after retries: %w", err)
	}
	return sr, nil
}

func (m *retryModel) WithTools(tools []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	next, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &retryModel{inner: next, cfg: m.cfg}, nil
}
