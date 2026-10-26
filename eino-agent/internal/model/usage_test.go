package model

import (
	"context"
	"testing"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/contextmgr"
)

type usageFake struct{}

func (m *usageFake) Generate(_ context.Context, _ []*schema.Message, _ ...einomodel.Option) (*schema.Message, error) {
	out := schema.AssistantMessage("ok", nil)
	out.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 300}}
	return out, nil
}

func (m *usageFake) Stream(context.Context, []*schema.Message, ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	chunk := schema.AssistantMessage("ok", nil)
	chunk.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 300}}
	return schema.StreamReaderFromArray([]*schema.Message{chunk}), nil
}

func (m *usageFake) WithTools([]*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	return m, nil
}

func TestUsageModelCalibratesOnGenerate(t *testing.T) {
	c := contextmgr.NewCounter()
	wrapped := NewUsageModel(&usageFake{}, c)
	input := []*schema.Message{schema.UserMessage("hello")}
	if _, err := wrapped.Generate(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if c.Ratio() == 1 {
		t.Fatalf("ratio did not calibrate: %f", c.Ratio())
	}
}

func TestUsageModelCalibratesOnStream(t *testing.T) {
	c := contextmgr.NewCounter()
	wrapped := NewUsageModel(&usageFake{}, c)
	input := []*schema.Message{schema.UserMessage("hello")}
	sr, err := wrapped.Stream(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := sr.Recv(); err != nil {
			break
		}
	}
	sr.Close()
	deadline := time.Now().Add(time.Second)
	for c.Ratio() == 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if c.Ratio() == 1 {
		t.Fatalf("stream ratio did not calibrate: %f", c.Ratio())
	}
}
