// Package trace writes a JSONL record of an agent run. It is deliberately
// small: one event per line, no external service, and redaction of obvious
// secret environment values. The file is useful for debugging, evals and
// turning a run into a teaching artifact.
package trace

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"
)

// Recorder is safe for concurrent use. A nil *Recorder is a no-op, so callers
// can pass nil when tracing is disabled.
type Recorder struct {
	mu      sync.Mutex
	enc     *json.Encoder
	closer  io.Closer
	seq     int
	turn    int
	toolAt  map[string]time.Time
	modelAt time.Time
	redact  []string
	err     error
}

// Open creates (or truncates) path and returns a recorder. Parent directories
// are created. The caller must Close the recorder.
func Open(path string) (*Recorder, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create trace directory: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create trace %s: %w", path, err)
	}
	r := New(f)
	r.closer = f
	return r, nil
}

// New writes events to w. It returns nil for a nil writer.
func New(w io.Writer) *Recorder {
	if w == nil {
		return nil
	}
	return &Recorder{
		enc:    json.NewEncoder(w),
		toolAt: make(map[string]time.Time),
		redact: secretValues(),
	}
}

// Close flushes and closes the underlying file, if any.
func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closer != nil {
		return r.closer.Close()
	}
	return nil
}

// Err returns the first write error, if any.
func (r *Recorder) Err() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func (r *Recorder) emit(event string, fields map[string]any) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return
	}
	m := make(map[string]any, len(fields)+4)
	m["ts"] = time.Now().Format(time.RFC3339Nano)
	m["seq"] = r.seq
	m["turn"] = r.turn
	m["type"] = event
	for k, v := range fields {
		m[k] = r.clean(v)
	}
	r.seq++
	if err := r.enc.Encode(m); err != nil {
		r.err = err
	}
}

// TurnStart starts a logical turn. Turn numbers are assigned by the caller.
func (r *Recorder) TurnStart(turn int, input string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.turn = turn
	r.mu.Unlock()
	r.emit("turn_start", map[string]any{"input": input})
}

// TurnEnd records the final answer, usage, duration and error.
func (r *Recorder) TurnEnd(turn int, final string, usage *schema.TokenUsage, err error, d time.Duration) {
	if r == nil {
		return
	}
	fields := map[string]any{
		"final":       final,
		"duration_ms": d.Milliseconds(),
	}
	if usage != nil {
		fields["usage"] = usage
	}
	if err != nil {
		fields["error"] = err.Error()
	}
	r.emit("turn_end", fields)
}

// ModelStart records the messages sent to the model.
func (r *Recorder) ModelStart(messages []*schema.Message, toolNames []string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.modelAt = time.Now()
	r.mu.Unlock()
	r.emit("model_start", map[string]any{
		"messages": summaryMessages(messages),
		"tools":    toolNames,
	})
}

// ModelEnd records the model output.
func (r *Recorder) ModelEnd(msg *schema.Message, d time.Duration) {
	if r == nil {
		return
	}
	fields := map[string]any{"duration_ms": d.Milliseconds()}
	if msg != nil {
		fields["message"] = summaryMessage(msg)
		if msg.ResponseMeta != nil && msg.ResponseMeta.Usage != nil {
			fields["usage"] = msg.ResponseMeta.Usage
		}
	}
	r.emit("model_end", fields)
}

// ModelError records a model error.
func (r *Recorder) ModelError(err error, d time.Duration) {
	if r == nil {
		return
	}
	r.emit("model_error", map[string]any{"error": err.Error(), "duration_ms": d.Milliseconds()})
}

// ToolStart records a tool invocation. Tools execute sequentially in this
// agent, so a name-keyed start time is sufficient.
func (r *Recorder) ToolStart(name, args string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.toolAt[name] = time.Now()
	r.mu.Unlock()
	r.emit("tool_start", map[string]any{"tool": name, "args": args})
}

// ToolEnd records a tool result.
func (r *Recorder) ToolEnd(name, result string, err error) {
	if r == nil {
		return
	}
	r.mu.Lock()
	start := r.toolAt[name]
	delete(r.toolAt, name)
	r.mu.Unlock()
	fields := map[string]any{"tool": name, "result": result}
	if !start.IsZero() {
		fields["duration_ms"] = time.Since(start).Milliseconds()
	}
	if err != nil {
		fields["error"] = err.Error()
	} else if strings.HasPrefix(strings.TrimSpace(result), "ERROR:") {
		fields["error"] = strings.TrimSpace(result)
	}
	r.emit("tool_end", fields)
}

// Event records an arbitrary event, e.g. approval or compaction.
func (r *Recorder) Event(event string, fields map[string]any) {
	if r == nil {
		return
	}
	if fields == nil {
		fields = map[string]any{}
	}
	r.emit(event, fields)
}

func (r *Recorder) clean(v any) any {
	switch x := v.(type) {
	case string:
		x = r.redactString(x)
		return truncate(x, 16*1024)
	case []string:
		out := make([]string, len(x))
		for i := range x {
			out[i] = truncate(r.redactString(x[i]), 4096)
		}
		return out
	case messageTrace:
		x.Content = truncate(r.redactString(x.Content), 16*1024)
		x.Reasoning = truncate(r.redactString(x.Reasoning), 16*1024)
		for i := range x.ToolCalls {
			x.ToolCalls[i].Arguments = truncate(r.redactString(x.ToolCalls[i].Arguments), 16*1024)
		}
		return x
	case []messageTrace:
		out := make([]messageTrace, len(x))
		for i := range x {
			cleaned, _ := r.clean(x[i]).(messageTrace)
			out[i] = cleaned
		}
		return out
	case toolCallTrace:
		x.Arguments = truncate(r.redactString(x.Arguments), 16*1024)
		return x
	case []toolCallTrace:
		out := make([]toolCallTrace, len(x))
		for i := range x {
			cleaned, _ := r.clean(x[i]).(toolCallTrace)
			out[i] = cleaned
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = r.clean(val)
		}
		return out
	case *schema.TokenUsage:
		return x
	default:
		return v
	}
}

func (r *Recorder) redactString(s string) string {
	for _, secret := range r.redact {
		s = strings.ReplaceAll(s, secret, "[redacted]")
	}
	return s
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("... [truncated %d bytes]", len(s)-max)
}

type messageTrace struct {
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	Reasoning  string          `json:"reasoning,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolCalls  []toolCallTrace `json:"tool_calls,omitempty"`
}

type toolCallTrace struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
}

func summaryMessage(m *schema.Message) messageTrace {
	if m == nil {
		return messageTrace{}
	}
	out := messageTrace{
		Role:       string(m.Role),
		Content:    m.Content,
		Reasoning:  m.ReasoningContent,
		ToolCallID: m.ToolCallID,
	}
	for _, tc := range m.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, toolCallTrace{
			ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments,
		})
	}
	return out
}

func summaryMessages(msgs []*schema.Message) []messageTrace {
	out := make([]messageTrace, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, summaryMessage(m))
	}
	return out
}

func secretValues() []string {
	var out []string
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || len(v) < 8 {
			continue
		}
		upper := strings.ToUpper(k)
		if strings.Contains(upper, "KEY") || strings.Contains(upper, "TOKEN") ||
			strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD") {
			out = append(out, v)
		}
	}
	return out
}
