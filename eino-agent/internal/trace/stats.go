package trace

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Stats is a compact aggregate of a JSONL trace. It is intentionally
// dependency-free so it can be printed by the CLI, used in evals, or embedded
// in a bug report.
type Stats struct {
	Events           int
	Turns            int
	ModelCalls       int
	ToolCalls        int
	ToolErrors       int
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	ModelMS          int64
	ToolMS           int64
	Started          time.Time
	Ended            time.Time
	Errors           []string
}

// Summarize reads JSONL events and aggregates counts, token usage, durations
// and errors. Malformed lines are skipped; an empty trace is valid.
func Summarize(r io.Reader) (Stats, error) {
	var st Stats
	if r == nil {
		return st, nil
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		st.Events++
		if ts, ok := ev["ts"].(string); ok {
			if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
				if st.Started.IsZero() {
					st.Started = t
				}
				st.Ended = t
			}
		}
		switch ev["type"] {
		case "turn_start":
			st.Turns++
		case "model_end":
			st.ModelCalls++
			addUsage(&st, ev["usage"])
			addDuration(&st.ModelMS, ev["duration_ms"])
		case "model_error":
			st.Errors = append(st.Errors, stringField(ev, "error"))
			addDuration(&st.ModelMS, ev["duration_ms"])
		case "tool_end":
			st.ToolCalls++
			addDuration(&st.ToolMS, ev["duration_ms"])
			if msg := stringField(ev, "error"); msg != "" {
				st.ToolErrors++
				st.Errors = append(st.Errors, msg)
			}
		case "turn_end":
			addUsage(&st, ev["usage"])
			if msg := stringField(ev, "error"); msg != "" {
				st.Errors = append(st.Errors, msg)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return st, fmt.Errorf("read trace: %w", err)
	}
	return st, nil
}

// SummarizeFile reads a trace file.
func SummarizeFile(path string) (Stats, error) {
	f, err := os.Open(path)
	if err != nil {
		return Stats{}, err
	}
	defer f.Close()
	return Summarize(f)
}

// String renders one line plus up to three errors.
func (s Stats) String() string {
	if s.Events == 0 {
		return "trace: no events"
	}
	line := fmt.Sprintf("trace: events=%d turns=%d model=%d tool=%d tool_errors=%d tokens=%d (prompt=%d completion=%d) model_ms=%d tool_ms=%d",
		s.Events, s.Turns, s.ModelCalls, s.ToolCalls, s.ToolErrors, s.TotalTokens, s.PromptTokens, s.CompletionTokens, s.ModelMS, s.ToolMS)
	if len(s.Errors) > 0 {
		line += "\n  errors:"
		for i, e := range s.Errors {
			if i >= 3 {
				line += fmt.Sprintf("\n  ... and %d more", len(s.Errors)-3)
				break
			}
			line += "\n  - " + e
		}
	}
	return line
}

func addUsage(st *Stats, raw any) {
	m, ok := raw.(map[string]any)
	if !ok {
		return
	}
	st.PromptTokens += intField(m, "prompt_tokens")
	st.CompletionTokens += intField(m, "completion_tokens")
	st.TotalTokens += intField(m, "total_tokens")
}

func addDuration(dst *int64, raw any) {
	switch n := raw.(type) {
	case float64:
		*dst += int64(n)
	case json.Number:
		v, _ := n.Int64()
		*dst += v
	}
}

func intField(m map[string]any, key string) int {
	switch n := m[key].(type) {
	case float64:
		return int(n)
	case json.Number:
		v, _ := n.Int64()
		return int(v)
	}
	return 0
}

func stringField(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}
