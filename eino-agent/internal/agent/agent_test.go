package agent

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

func TestRunTurnExecutesToolAndReturnsFinalAnswer(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/hello.txt", []byte("hi from the test fixture\n"), 0o644); err != nil {
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

	toolArgs, _ := json.Marshal(map[string]string{"path": "hello.txt"})
	script := []*schema.Message{
		schema.AssistantMessage("", []schema.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: schema.FunctionCall{
				Name:      "read_file",
				Arguments: string(toolArgs),
			},
		}}),
		schema.AssistantMessage("The fixture says hi from the test fixture.", nil),
	}

	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "generate", true: "stream"}[stream], func(t *testing.T) {
			state := &fakeState{script: script}
			var out, errOut bytes.Buffer
			a, err := New(context.Background(), Config{
				Model:        &fakeModel{state: state},
				Tools:        toolList,
				SystemPrompt: "test system prompt",
				MaxSteps:     10,
				Stream:       stream,
				UI:           ui.New(&out, &errOut, false, true),
			})
			if err != nil {
				t.Fatal(err)
			}
			final, err := a.RunTurn(context.Background(), []*schema.Message{schema.UserMessage("read hello.txt")})
			if err != nil {
				t.Fatalf("RunTurn: %v", err)
			}
			if !strings.Contains(final.Content, "fixture says hi") {
				t.Fatalf("final content = %q", final.Content)
			}
			if !strings.Contains(out.String(), "read_file") {
				t.Fatalf("tool callback output missing:\n%s", out.String())
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.calls != 2 {
				t.Fatalf("model calls = %d, want 2", state.calls)
			}
			// The tool result is appended to the second model call's input.
			foundTool := false
			for _, m := range state.lastInput {
				if m.Role == schema.Tool && strings.Contains(m.Content, "hi from the test fixture") {
					foundTool = true
				}
			}
			if !foundTool {
				t.Fatalf("tool result not present in second model input: %+v", state.lastInput)
			}
		})
	}
}

func TestAllChunksToolCallChecker(t *testing.T) {
	sr := schema.StreamReaderFromArray([]*schema.Message{
		schema.AssistantMessage("thinking...", nil),
		schema.AssistantMessage("", []schema.ToolCall{{ID: "1", Type: "function", Function: schema.FunctionCall{Name: "read_file"}}}),
	})
	ok, err := allChunksToolCallChecker(context.Background(), sr)
	if err != nil || !ok {
		t.Fatalf("checker = %v, %v", ok, err)
	}

	sr = schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage("done", nil)})
	ok, err = allChunksToolCallChecker(context.Background(), sr)
	if err != nil || ok {
		t.Fatalf("checker without tool = %v, %v", ok, err)
	}
}

func TestToolNames(t *testing.T) {
	names, err := toolNames(context.Background(), nil)
	if err != nil || len(names) != 0 {
		t.Fatalf("toolNames(nil) = %v, %v", names, err)
	}
}

func TestRunTurnToolErrorIsRecoverable(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	toolList, err := tools.New(context.Background(), tools.Options{Workspace: ws, Mode: "read-only"})
	if err != nil {
		t.Fatal(err)
	}

	args, _ := json.Marshal(map[string]string{"path": "does-not-exist.txt"})
	editArgs, _ := json.Marshal(map[string]string{"path": "new.txt", "old_str": "", "new_str": "x"})
	script := []*schema.Message{
		schema.AssistantMessage("", []schema.ToolCall{{
			ID: "call-1", Type: "function",
			Function: schema.FunctionCall{Name: "read_file", Arguments: string(args)},
		}}),
		schema.AssistantMessage("", []schema.ToolCall{{
			ID: "call-2", Type: "function",
			Function: schema.FunctionCall{Name: "edit_file", Arguments: string(editArgs)},
		}}),
		schema.AssistantMessage("recovered after tool errors", nil),
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
	if state.calls != 3 {
		t.Fatalf("calls = %d, want 3", state.calls)
	}
	var sawReadErr, sawEditErr bool
	for _, m := range state.lastInput {
		if m.Role == schema.Tool && strings.Contains(m.Content, "ERROR: read does-not-exist.txt") {
			sawReadErr = true
		}
		if m.Role == schema.Tool && strings.Contains(m.Content, "disabled in read-only mode") {
			sawEditErr = true
		}
	}
	if !sawReadErr || !sawEditErr {
		t.Fatalf("tool errors not returned to model: %+v", state.lastInput)
	}
}
