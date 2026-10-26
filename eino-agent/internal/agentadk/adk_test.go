package agentadk

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/tools"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/ui"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

type fakeState struct {
	mu        sync.Mutex
	calls     int
	script    []*schema.Message
	lastInput []*schema.Message
}

func (s *fakeState) next(input []*schema.Message) *schema.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastInput = input
	idx := s.calls
	if idx >= len(s.script) {
		idx = len(s.script) - 1
	}
	s.calls++
	return s.script[idx]
}

type fakeModel struct{ state *fakeState }

func (m *fakeModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return m.state.next(input), nil
}

func (m *fakeModel) Stream(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg := m.state.next(input)
	if len(msg.ToolCalls) > 0 || msg.Content == "" {
		return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
	}
	r := []rune(msg.Content)
	var chunks []*schema.Message
	for i := 0; i < len(r); i += 3 {
		end := i + 3
		if end > len(r) {
			end = len(r)
		}
		chunks = append(chunks, schema.AssistantMessage(string(r[i:end]), nil))
	}
	return schema.StreamReaderFromArray(chunks), nil
}

func (m *fakeModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func TestRunTurnADK(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/hello.txt", []byte("hi from the ADK fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	toolList, err := tools.New(context.Background(), tools.Options{Workspace: ws, Mode: "ask"})
	if err != nil {
		t.Fatal(err)
	}

	args, _ := json.Marshal(map[string]string{"path": "hello.txt"})
	script := []*schema.Message{
		schema.AssistantMessage("", []schema.ToolCall{{
			ID: "call-1", Type: "function",
			Function: schema.FunctionCall{Name: "read_file", Arguments: string(args)},
		}}),
		schema.AssistantMessage("The ADK fixture says hello.", nil),
	}

	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "generate", true: "stream"}[stream], func(t *testing.T) {
			state := &fakeState{script: script}
			var out, errOut bytes.Buffer
			a, err := New(context.Background(), Config{
				Model: &fakeModel{state: state}, Tools: toolList, SystemPrompt: "test",
				MaxSteps: 10, Stream: stream, UI: ui.New(&out, &errOut, false, true),
			})
			if err != nil {
				t.Fatal(err)
			}
			final, err := a.RunTurn(context.Background(), []*schema.Message{schema.UserMessage("read hello.txt")})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(final.Content, "ADK fixture says hello") {
				t.Fatalf("final = %q", final.Content)
			}
			if !strings.Contains(out.String(), "read_file") {
				t.Fatalf("tool output missing:\n%s", out.String())
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.calls != 2 {
				t.Fatalf("model calls = %d, want 2", state.calls)
			}
			foundTool := false
			for _, m := range state.lastInput {
				if m.Role == schema.Tool && strings.Contains(m.Content, "hi from the ADK fixture") {
					foundTool = true
				}
			}
			if !foundTool {
				t.Fatalf("tool result not present in second input: %+v", state.lastInput)
			}
		})
	}
}

func TestADKToolErrorIsRecoverable(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	toolList, err := tools.New(context.Background(), tools.Options{Workspace: ws, Mode: "read-only"})
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"path": "missing.txt"})
	script := []*schema.Message{
		schema.AssistantMessage("", []schema.ToolCall{{
			ID: "call-1", Type: "function",
			Function: schema.FunctionCall{Name: "read_file", Arguments: string(args)},
		}}),
		schema.AssistantMessage("recovered after the error", nil),
	}
	state := &fakeState{script: script}
	var out, errOut bytes.Buffer
	a, err := New(context.Background(), Config{
		Model: &fakeModel{state: state}, Tools: toolList, SystemPrompt: "test",
		MaxSteps: 10, Stream: false, UI: ui.New(&out, &errOut, false, false),
	})
	if err != nil {
		t.Fatal(err)
	}
	final, err := a.RunTurn(context.Background(), []*schema.Message{schema.UserMessage("go")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(final.Content, "recovered") {
		t.Fatalf("final = %q", final.Content)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	found := false
	for _, m := range state.lastInput {
		if m.Role == schema.Tool && strings.Contains(m.Content, "ERROR: read missing.txt") {
			found = true
		}
	}
	if !found {
		t.Fatalf("tool error not returned to model: %+v", state.lastInput)
	}
}
