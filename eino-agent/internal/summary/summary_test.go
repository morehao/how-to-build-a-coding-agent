package summary

import (
	"context"
	"errors"
	"strings"
	"testing"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type fakeModel struct {
	input []*schema.Message
	out   string
	err   error
}

func (m *fakeModel) Generate(_ context.Context, input []*schema.Message, _ ...einomodel.Option) (*schema.Message, error) {
	m.input = input
	if m.err != nil {
		return nil, m.err
	}
	return schema.AssistantMessage(m.out, nil), nil
}

func (m *fakeModel) Stream(context.Context, []*schema.Message, ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("not implemented")
}

func TestGenerateStructuredSummary(t *testing.T) {
	m := &fakeModel{out: "## Goal\nship it\n## Next steps\nrun tests"}
	got, err := Generate(context.Background(), m, []*schema.Message{
		schema.UserMessage("do the task"),
		schema.AssistantMessage("working on it", nil),
	}, 1000, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "## Goal") {
		t.Fatalf("summary = %q", got)
	}
	if len(m.input) != 2 || !strings.Contains(m.input[1].Content, "## Next steps") || !strings.Contains(m.input[1].Content, "do the task") {
		t.Fatalf("prompt not structured: %+v", m.input)
	}
}

func TestGenerateRejectsEmpty(t *testing.T) {
	_, err := Generate(context.Background(), &fakeModel{}, []*schema.Message{schema.UserMessage("x")}, 1000, nil)
	if err == nil || !strings.Contains(err.Error(), "empty summary") {
		t.Fatalf("error = %v", err)
	}
}
