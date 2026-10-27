package model

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/budget"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/contextmgr"
)

func TestUsageModelBudgetStopsNextCall(t *testing.T) {
	script := &FakeScript{Name: "budget", Steps: []FakeStep{
		{Content: "one", Usage: &schema.TokenUsage{PromptTokens: 6, CompletionTokens: 5, TotalTokens: 11}},
		{Content: "two"},
	}}
	fake, err := NewFakeModel(script)
	if err != nil {
		t.Fatal(err)
	}
	tr := budget.New(10)
	m := NewUsageModel(fake, contextmgr.NewCounter(), tr)
	if _, err := m.Generate(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Generate(context.Background(), nil); err == nil {
		t.Fatal("expected budget error")
	}
}
