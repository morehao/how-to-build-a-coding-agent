package model

import (
	"context"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/contextmgr"
)

// usageModel wraps a chat model and feeds provider-reported prompt_tokens back
// into the context counter. It is transparent to callers: messages and streams
// pass through unchanged.
type usageModel struct {
	inner   einomodel.ToolCallingChatModel
	counter *contextmgr.Counter
}

// NewUsageModel wraps inner. A nil counter is replaced with a fresh one.
func NewUsageModel(inner einomodel.ToolCallingChatModel, counter *contextmgr.Counter) einomodel.ToolCallingChatModel {
	if counter == nil {
		counter = contextmgr.NewCounter()
	}
	return &usageModel{inner: inner, counter: counter}
}

func (m *usageModel) Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	out, err := m.inner.Generate(ctx, input, opts...)
	if err == nil && out != nil && out.ResponseMeta != nil && out.ResponseMeta.Usage != nil {
		m.counter.Observe(out.ResponseMeta.Usage.PromptTokens, input)
	}
	return out, err
}

func (m *usageModel) Stream(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	sr, err := m.inner.Stream(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	copies := sr.Copy(2)
	go func() {
		defer copies[0].Close()
		var usage *schema.TokenUsage
		for {
			chunk, recvErr := copies[0].Recv()
			if recvErr != nil {
				break
			}
			if chunk != nil && chunk.ResponseMeta != nil && chunk.ResponseMeta.Usage != nil {
				usage = chunk.ResponseMeta.Usage
			}
		}
		if usage != nil {
			m.counter.Observe(usage.PromptTokens, input)
		}
	}()
	return copies[1], nil
}

func (m *usageModel) WithTools(tools []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	next, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &usageModel{inner: next, counter: m.counter}, nil
}
