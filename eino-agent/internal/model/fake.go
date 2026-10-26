package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// FakeScript is a deterministic provider used by demos, evals and tests. Each
// model call consumes the next step; a step may return text, tool calls, or an
// error. The script is intentionally small so it can be read in a tutorial.
type FakeScript struct {
	Name  string     `json:"name"`
	Steps []FakeStep `json:"steps"`
}

// FakeStep is one scripted model response.
type FakeStep struct {
	Content   string             `json:"content,omitempty"`
	Reasoning string             `json:"reasoning,omitempty"`
	ToolCalls []FakeToolCall     `json:"tool_calls,omitempty"`
	Usage     *schema.TokenUsage `json:"usage,omitempty"`
	DelayMS   int                `json:"delay_ms,omitempty"`
	Error     string             `json:"error,omitempty"`
	Chunks    []string           `json:"chunks,omitempty"`
}

// FakeToolCall is a tool call in a script. Arguments is a JSON string, exactly
// as the model would emit it.
type FakeToolCall struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
}

// LoadFakeScript reads and validates a script file.
func LoadFakeScript(path string) (*FakeScript, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fake script %s: %w", path, err)
	}
	var script FakeScript
	if err := json.Unmarshal(data, &script); err != nil {
		return nil, fmt.Errorf("parse fake script %s: %w", path, err)
	}
	if err := script.Validate(); err != nil {
		return nil, fmt.Errorf("fake script %s: %w", path, err)
	}
	return &script, nil
}

// Validate checks the parts the model would otherwise discover at runtime.
func (s *FakeScript) Validate() error {
	if s == nil || len(s.Steps) == 0 {
		return errors.New("script must contain at least one step")
	}
	for i, step := range s.Steps {
		if step.DelayMS < 0 {
			return fmt.Errorf("step %d: delay_ms must be >= 0", i)
		}
		if step.Error != "" && (step.Content != "" || len(step.ToolCalls) > 0) {
			return fmt.Errorf("step %d: error cannot be combined with content/tool_calls", i)
		}
		for j, tc := range step.ToolCalls {
			if strings.TrimSpace(tc.Name) == "" {
				return fmt.Errorf("step %d tool call %d: name is required", i, j)
			}
		}
	}
	return nil
}

type fakeState struct {
	mu     sync.Mutex
	script *FakeScript
	next   int
}

// FakeModel implements model.ToolCallingChatModel. Copies made by WithTools
// share the script cursor, so the step order is preserved across the ReAct
// graph's model instances.
type FakeModel struct {
	state *fakeState
	tools []*schema.ToolInfo
}

// NewFakeModel constructs a fake model from a validated script.
func NewFakeModel(script *FakeScript) (*FakeModel, error) {
	if err := script.Validate(); err != nil {
		return nil, err
	}
	return &FakeModel{state: &fakeState{script: script}}, nil
}

func (m *FakeModel) next(ctx context.Context) (*FakeStep, error) {
	m.state.mu.Lock()
	if m.state.next >= len(m.state.script.Steps) {
		n := len(m.state.script.Steps)
		m.state.mu.Unlock()
		return nil, fmt.Errorf("fake script %q exhausted after %d step(s)", m.state.script.Name, n)
	}
	step := m.state.script.Steps[m.state.next]
	m.state.next++
	m.state.mu.Unlock()

	if step.DelayMS > 0 {
		timer := time.NewTimer(time.Duration(step.DelayMS) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	if step.Error != "" {
		return nil, errors.New(step.Error)
	}
	return &step, nil
}

func (m *FakeModel) Generate(ctx context.Context, _ []*schema.Message, _ ...einomodel.Option) (*schema.Message, error) {
	step, err := m.next(ctx)
	if err != nil {
		return nil, err
	}
	return fakeMessage(step), nil
}

func (m *FakeModel) Stream(ctx context.Context, _ []*schema.Message, _ ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	step, err := m.next(ctx)
	if err != nil {
		return nil, err
	}
	chunks := fakeChunks(step)
	return schema.StreamReaderFromArray(chunks), nil
}

func (m *FakeModel) WithTools(tools []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	return &FakeModel{state: m.state, tools: tools}, nil
}

func fakeMessage(step *FakeStep) *schema.Message {
	msg := schema.AssistantMessage(step.Content, fakeToolCalls(step.ToolCalls))
	msg.ReasoningContent = step.Reasoning
	msg.ResponseMeta = &schema.ResponseMeta{Usage: usageFor(step)}
	return msg
}

func fakeChunks(step *FakeStep) []*schema.Message {
	parts := step.Chunks
	if len(parts) == 0 && step.Content != "" {
		parts = splitChunks(step.Content)
	}
	if len(parts) == 0 {
		parts = []string{""}
	}
	chunks := make([]*schema.Message, 0, len(parts)+1)
	for _, part := range parts {
		msg := schema.AssistantMessage(part, nil)
		msg.ReasoningContent = step.Reasoning
		chunks = append(chunks, msg)
	}
	if len(step.ToolCalls) > 0 {
		chunks = append(chunks, schema.AssistantMessage("", fakeToolCalls(step.ToolCalls)))
	}
	chunks[len(chunks)-1].ResponseMeta = &schema.ResponseMeta{Usage: usageFor(step)}
	return chunks
}

func fakeToolCalls(in []FakeToolCall) []schema.ToolCall {
	if len(in) == 0 {
		return nil
	}
	out := make([]schema.ToolCall, 0, len(in))
	for i, tc := range in {
		args := tc.Arguments
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		id := tc.ID
		if id == "" {
			id = fmt.Sprintf("call-%d", i+1)
		}
		idx := i
		out = append(out, schema.ToolCall{
			Index: &idx,
			ID:    id,
			Type:  "function",
			Function: schema.FunctionCall{
				Name:      tc.Name,
				Arguments: args,
			},
		})
	}
	return out
}

func usageFor(step *FakeStep) *schema.TokenUsage {
	if step.Usage != nil {
		u := *step.Usage
		if u.TotalTokens == 0 {
			u.TotalTokens = u.PromptTokens + u.CompletionTokens
		}
		return &u
	}
	completion := estimateFakeTokens(step.Content)
	for _, tc := range step.ToolCalls {
		completion += estimateFakeTokens(tc.Name + tc.Arguments)
	}
	if completion == 0 {
		completion = 1
	}
	return &schema.TokenUsage{CompletionTokens: completion, TotalTokens: completion}
}

func estimateFakeTokens(s string) int {
	if s == "" {
		return 0
	}
	n := utf8.RuneCountInString(s)
	if n < 4 {
		return 1
	}
	return n / 4
}

func splitChunks(s string) []string {
	const size = 24
	runes := []rune(s)
	var out []string
	for len(runes) > 0 {
		n := size
		if n > len(runes) {
			n = len(runes)
		}
		out = append(out, string(runes[:n]))
		runes = runes[n:]
	}
	return out
}
