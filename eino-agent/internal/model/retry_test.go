package model

import (
	"context"
	"errors"
	"testing"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/retry"
)

type flakyModel struct {
	calls int
}

func (m *flakyModel) Generate(context.Context, []*schema.Message, ...einomodel.Option) (*schema.Message, error) {
	m.calls++
	if m.calls < 2 {
		return nil, errors.New("503 service unavailable")
	}
	return schema.AssistantMessage("ok", nil), nil
}
func (m *flakyModel) Stream(context.Context, []*schema.Message, ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("not implemented")
}
func (m *flakyModel) WithTools([]*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	return m, nil
}

func TestRetryModelGenerate(t *testing.T) {
	inner := &flakyModel{}
	m := NewRetryModel(inner, retry.Config{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond})
	msg, err := m.Generate(context.Background(), nil)
	if err != nil || msg.Content != "ok" || inner.calls != 2 {
		t.Fatalf("msg=%+v err=%v calls=%d", msg, err, inner.calls)
	}
}
