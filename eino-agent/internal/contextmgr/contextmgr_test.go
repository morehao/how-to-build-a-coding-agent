package contextmgr

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func toolCall(id, name, args string) *schema.Message {
	return schema.AssistantMessage("", []schema.ToolCall{{
		ID: id, Type: "function",
		Function: schema.FunctionCall{Name: name, Arguments: args},
	}})
}

func toolResult(id, content string) *schema.Message {
	return schema.ToolMessage(content, id, schema.WithToolName("read_file"))
}

func TestEstimateTokens(t *testing.T) {
	if got := EstimateTokens(nil); got != 0 {
		t.Fatalf("empty = %d", got)
	}
	ascii := EstimateTokens([]*schema.Message{schema.UserMessage(strings.Repeat("a", 300))})
	cjk := EstimateTokens([]*schema.Message{schema.UserMessage(strings.Repeat("中", 300))})
	if ascii <= 0 || cjk <= ascii {
		t.Fatalf("estimates ascii=%d cjk=%d", ascii, cjk)
	}
}

func TestCompactUnderBudgetIsUnchanged(t *testing.T) {
	msgs := []*schema.Message{schema.SystemMessage("sys"), schema.UserMessage("hi")}
	out, stats := CompactWithStats(msgs, 1000)
	if len(out) != len(msgs) || stats.Changed() {
		t.Fatalf("out=%d stats=%+v", len(out), stats)
	}
}

func TestCompactStubsOldToolResults(t *testing.T) {
	var msgs []*schema.Message
	msgs = append(msgs, schema.SystemMessage("system"))
	for i := 1; i <= 4; i++ {
		msgs = append(msgs,
			schema.UserMessage(fmt.Sprintf("task-%d", i)),
			toolCall(fmt.Sprintf("call-%d", i), "read_file", `{"path":"x"}`),
			toolResult(fmt.Sprintf("call-%d", i), fmt.Sprintf("old-%d:%s", i, strings.Repeat("x", 5000))),
		)
	}
	msgs = append(msgs, schema.UserMessage("latest"))

	out, stats := CompactWithStats(msgs, 6000)
	if stats.StubbedTools == 0 {
		t.Fatalf("expected stubbing, stats=%+v", stats)
	}
	var sawStub, sawRecent bool
	for _, m := range out {
		if m.Role == schema.Tool && strings.Contains(m.Content, "tool output omitted") {
			sawStub = true
		}
		if m.Role == schema.Tool && strings.Contains(m.Content, "old-4") {
			sawRecent = true
		}
	}
	if !sawStub || !sawRecent {
		t.Fatalf("stub=%v recent=%v", sawStub, sawRecent)
	}
	if EstimateTokens(out) > 6000 {
		t.Fatalf("still over budget: %d", EstimateTokens(out))
	}
}

func TestCompactDropsOldTurns(t *testing.T) {
	msgs := []*schema.Message{schema.SystemMessage("system")}
	for i := 1; i <= 6; i++ {
		msgs = append(msgs, schema.UserMessage(fmt.Sprintf("task-%d %s", i, strings.Repeat("u", 1000))))
		msgs = append(msgs, schema.AssistantMessage(fmt.Sprintf("answer-%d %s", i, strings.Repeat("a", 1000)), nil))
	}
	out, stats := CompactWithStats(msgs, 600)
	if stats.DroppedBlocks == 0 {
		t.Fatalf("expected dropped blocks, stats=%+v", stats)
	}
	if len(out) < 2 || out[0].Role != schema.System {
		t.Fatalf("system not preserved: %+v", out)
	}
	last := out[len(out)-1]
	if !strings.Contains(last.Content, "task-6") && !strings.Contains(last.Content, "answer-6") {
		t.Fatalf("latest turn lost: %+v", out)
	}
	if EstimateTokens(out) > 600 {
		t.Fatalf("still over budget: %d", EstimateTokens(out))
	}
}

func TestCompactPreservesToolCallPairing(t *testing.T) {
	msgs := []*schema.Message{schema.SystemMessage("system")}
	for i := 1; i <= 4; i++ {
		id := fmt.Sprintf("call-%d", i)
		msgs = append(msgs,
			schema.UserMessage(fmt.Sprintf("task %d", i)),
			toolCall(id, "read_file", `{"path":"x"}`),
			toolResult(id, strings.Repeat("r", 5000)),
		)
	}
	out, _ := CompactWithStats(msgs, 3000)
	openCalls := map[string]bool{}
	for _, m := range out {
		if m.Role == schema.Assistant {
			for _, tc := range m.ToolCalls {
				openCalls[tc.ID] = true
			}
		}
		if m.Role == schema.Tool {
			if !openCalls[m.ToolCallID] {
				t.Fatalf("tool message %q has no preceding tool call", m.ToolCallID)
			}
		}
	}
}

func TestCompactTruncatesGiantLatestTool(t *testing.T) {
	msgs := []*schema.Message{
		schema.SystemMessage("system"),
		schema.UserMessage("read it"),
		toolCall("call-1", "read_file", `{"path":"x"}`),
		toolResult("call-1", strings.Repeat("z", 100000)),
	}
	out, stats := CompactWithStats(msgs, 1000)
	if stats.TruncatedMessages == 0 {
		t.Fatalf("expected truncation, stats=%+v", stats)
	}
	if EstimateTokens(out) > 1000 {
		t.Fatalf("still over budget: %d", EstimateTokens(out))
	}
	found := false
	for _, m := range out {
		if m.Role == schema.Tool && strings.Contains(m.Content, "truncated") && len(m.Content) < 5000 {
			found = true
		}
	}
	if !found {
		t.Fatalf("giant tool result not truncated: %+v", out)
	}
}

func TestWithSystem(t *testing.T) {
	msgs := []*schema.Message{schema.UserMessage("hi")}
	out := WithSystem("sys", msgs)
	if len(out) != 2 || out[0].Role != schema.System {
		t.Fatalf("out = %+v", out)
	}
	again := WithSystem("sys2", out)
	if len(again) != 2 || again[0].Content != "sys" {
		t.Fatalf("duplicate system added: %+v", again)
	}
}

func TestCounterCalibrates(t *testing.T) {
	c := NewCounter()
	msgs := []*schema.Message{schema.UserMessage(strings.Repeat("a", 3000))}
	base := c.Count(msgs)
	if base == 0 || c.Ratio() != 1 {
		t.Fatalf("base=%d ratio=%f", base, c.Ratio())
	}
	c.Observe(base*2, msgs)
	if c.Ratio() <= 1 {
		t.Fatalf("ratio = %f, want > 1", c.Ratio())
	}
	got := c.Count(msgs)
	if got <= base {
		t.Fatalf("count = %d, want > %d", got, base)
	}
	// Clamping keeps a bad sample from producing an absurd count.
	c.Observe(1, msgs)
	if c.Ratio() < 0.5 || c.Ratio() > 2 {
		t.Fatalf("ratio out of bounds: %f", c.Ratio())
	}
}

func TestCounterCompact(t *testing.T) {
	c := NewCounter()
	msgs := []*schema.Message{schema.SystemMessage("sys")}
	for i := 0; i < 6; i++ {
		msgs = append(msgs, schema.UserMessage(fmt.Sprintf("task-%d %s", i, strings.Repeat("u", 1000))))
		msgs = append(msgs, schema.AssistantMessage(strings.Repeat("a", 1000), nil))
	}
	out, stats := c.CompactWithStats(msgs, 600)
	if !stats.Changed() {
		t.Fatalf("no compaction: %+v", stats)
	}
	if got := c.Count(out); got > 600 {
		t.Fatalf("still over: %d", got)
	}
}
