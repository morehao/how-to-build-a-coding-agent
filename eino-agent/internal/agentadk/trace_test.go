package agentadk

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/tools"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/trace"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/ui"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

func TestRunTurnTraceAndFinalContent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/hello.txt", []byte("hello from ADK trace\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	toolList, err := tools.New(context.Background(), tools.Options{Workspace: ws, Mode: "yolo"})
	if err != nil {
		t.Fatal(err)
	}
	args := `{"path":"hello.txt"}`
	script := []*schema.Message{
		schema.AssistantMessage("let me read it", []schema.ToolCall{{
			ID: "call-1", Type: "function",
			Function: schema.FunctionCall{Name: "read_file", Arguments: args},
		}}),
		schema.AssistantMessage("the final answer", nil),
	}
	state := &fakeState{script: script}
	var out bytes.Buffer
	var recBuf bytes.Buffer
	rec := trace.New(&recBuf)
	a, err := New(context.Background(), Config{
		Model: &fakeModel{state: state}, Tools: toolList, SystemPrompt: "test",
		MaxSteps: 10, Stream: false, UI: ui.New(&out, &out, false, false), Trace: rec,
	})
	if err != nil {
		t.Fatal(err)
	}
	final, err := a.RunTurn(context.Background(), []*schema.Message{schema.UserMessage("read hello.txt")})
	if err != nil {
		t.Fatal(err)
	}
	if final.Content != "the final answer" {
		t.Fatalf("final = %q, want final answer only", final.Content)
	}

	types := map[string]int{}
	var toolsSeen []string
	for _, line := range strings.Split(strings.TrimSpace(recBuf.String()), "\n") {
		if line == "" {
			continue
		}
		var ev struct {
			Type string `json:"type"`
			Tool string `json:"tool"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("invalid trace line %q: %v", line, err)
		}
		types[ev.Type]++
		if ev.Type == "tool_start" || ev.Type == "tool_end" {
			toolsSeen = append(toolsSeen, ev.Tool)
		}
	}
	for _, want := range []string{"turn_start", "tool_start", "tool_end", "turn_end"} {
		if types[want] == 0 {
			t.Fatalf("trace missing %q: %s", want, recBuf.String())
		}
	}
	if len(toolsSeen) != 2 || toolsSeen[0] != "read_file" || toolsSeen[1] != "read_file" {
		t.Fatalf("tool trace = %v", toolsSeen)
	}
}

func TestToolResultError(t *testing.T) {
	if toolResultError("ERROR: nope") == nil {
		t.Fatal("ERROR prefix not detected")
	}
	if toolResultError("exit code: 2\nboom") == nil {
		t.Fatal("non-zero exit not detected")
	}
	if toolResultError("exit code: 0\nok") != nil {
		t.Fatal("zero exit marked as error")
	}
	if toolResultError("plain result") != nil {
		t.Fatal("plain result marked as error")
	}
}
