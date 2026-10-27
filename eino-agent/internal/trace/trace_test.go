package trace

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

func TestTraceRedactsAndTruncates(t *testing.T) {
	t.Setenv("TEST_API_KEY", "super-secret-value")
	var buf bytes.Buffer
	r := New(&buf)
	r.TurnStart(1, "ask")
	r.ModelStart([]*schema.Message{
		schema.UserMessage(strings.Repeat("x", 20000) + " super-secret-value"),
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			ID: "1", Function: schema.FunctionCall{Name: "bash", Arguments: `{"command":"echo super-secret-value"}`},
		}}},
	}, []string{"bash"})
	r.ModelEnd(schema.AssistantMessage("done", nil), time.Millisecond)
	r.TurnEnd(1, "done", &schema.TokenUsage{PromptTokens: 1, CompletionTokens: 2}, nil, time.Millisecond)
	r.Close()

	data := buf.String()
	if strings.Contains(data, "super-secret-value") {
		t.Fatalf("secret leaked:\n%s", data)
	}
	if !strings.Contains(data, "[redacted]") || !strings.Contains(data, "[truncated") {
		t.Fatalf("expected redaction/truncation:\n%s", data)
	}
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		var v map[string]any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Fatalf("invalid JSONL line %q: %v", line, err)
		}
	}
}
