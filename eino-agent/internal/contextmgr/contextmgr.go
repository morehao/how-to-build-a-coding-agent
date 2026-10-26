// Package contextmgr keeps the messages sent to the model inside a token
// budget. It implements deterministic compaction: old tool results are
// replaced by short placeholders, old conversation turns are dropped, and
// oversized messages are truncated. No extra model call is made here.
package contextmgr

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino/schema"
)

// DefaultContextTokens is the default approximate token budget for one model
// call. It is deliberately conservative because providers and models differ.
const DefaultContextTokens = 32000

const (
	keepRecentToolResults = 3
	stubThresholdBytes    = 256
	truncateFloorBytes    = 400
)

// Stats describes what compaction changed. Zero values mean "nothing done".
type Stats struct {
	BeforeTokens      int
	AfterTokens       int
	StubbedTools      int
	DroppedBlocks     int
	TruncatedMessages int
}

// Changed reports whether compaction altered the input.
func (s Stats) Changed() bool {
	return s.BeforeTokens > s.AfterTokens || s.StubbedTools > 0 || s.DroppedBlocks > 0 || s.TruncatedMessages > 0
}

// EstimateTokens returns a conservative token estimate. It uses bytes/3
// rather than a provider tokenizer: English code is usually ~3-4 bytes/token,
// while CJK text is ~1 token per character (~3 bytes).
func EstimateTokens(messages []*schema.Message) int {
	total := 0
	for _, m := range messages {
		total += messageTokens(m)
	}
	return total
}

// WithSystem prepends a system message unless the input already starts with
// one.
func WithSystem(system string, messages []*schema.Message) []*schema.Message {
	if system == "" {
		return messages
	}
	if len(messages) > 0 && messages[0] != nil && messages[0].Role == schema.System {
		return messages
	}
	out := make([]*schema.Message, 0, len(messages)+1)
	out = append(out, schema.SystemMessage(system))
	out = append(out, messages...)
	return out
}

// Compact compacts messages to maxTokens. A non-positive budget disables
// compaction.
func Compact(messages []*schema.Message, maxTokens int) []*schema.Message {
	out, _ := CompactWithStats(messages, maxTokens)
	return out
}

// CompactWithStats is Compact plus a summary of the changes.
func CompactWithStats(messages []*schema.Message, maxTokens int) ([]*schema.Message, Stats) {
	stats := Stats{BeforeTokens: EstimateTokens(messages)}
	if maxTokens <= 0 || len(messages) == 0 || stats.BeforeTokens <= maxTokens {
		stats.AfterTokens = stats.BeforeTokens
		return messages, stats
	}

	out := make([]*schema.Message, len(messages))
	for i, m := range messages {
		out[i] = cloneMessage(m)
	}

	stubOldToolOutputs(out, &stats)
	if EstimateTokens(out) <= maxTokens {
		stats.AfterTokens = EstimateTokens(out)
		return out, stats
	}

	out = dropOldBlocks(out, maxTokens, &stats)
	if EstimateTokens(out) <= maxTokens {
		stats.AfterTokens = EstimateTokens(out)
		return out, stats
	}

	truncateToFit(out, maxTokens, &stats)
	stats.AfterTokens = EstimateTokens(out)
	return out, stats
}

func stubOldToolOutputs(messages []*schema.Message, stats *Stats) {
	var toolIdx []int
	for i, m := range messages {
		if m != nil && m.Role == schema.Tool {
			toolIdx = append(toolIdx, i)
		}
	}
	if len(toolIdx) <= keepRecentToolResults {
		return
	}
	cutoff := toolIdx[len(toolIdx)-keepRecentToolResults]

	for _, idx := range toolIdx {
		if idx >= cutoff {
			break
		}
		m := messages[idx]
		if len(m.Content) <= stubThresholdBytes {
			continue
		}
		original := len(m.Content)
		m.Content = fmt.Sprintf("[tool output omitted to save context: %d bytes. Re-run the tool with narrower limits if you need it again.]", original)
		stats.StubbedTools++
	}

	// Old assistant tool-call arguments can be large too (for example a full
	// edit_file replacement). Keep the most recent calls intact.
	for i, m := range messages {
		if i >= cutoff || m == nil || len(m.ToolCalls) == 0 {
			continue
		}
		for j := range m.ToolCalls {
			if len(m.ToolCalls[j].Function.Arguments) > stubThresholdBytes {
				m.ToolCalls[j].Function.Arguments = `{"truncated":true}`
				stats.StubbedTools++
			}
		}
	}
}

func dropOldBlocks(messages []*schema.Message, maxTokens int, stats *Stats) []*schema.Message {
	systemEnd := 0
	for systemEnd < len(messages) && messages[systemEnd] != nil && messages[systemEnd].Role == schema.System {
		systemEnd++
	}
	system := messages[:systemEnd]
	body := messages[systemEnd:]
	if len(body) == 0 {
		return messages
	}

	blocks := splitBlocks(body)
	if len(blocks) <= 1 {
		return messages
	}
	for len(blocks) > 1 {
		candidate := make([]*schema.Message, 0, len(system)+len(body))
		candidate = append(candidate, system...)
		for _, b := range blocks {
			candidate = append(candidate, b...)
		}
		if EstimateTokens(candidate) <= maxTokens {
			return candidate
		}
		blocks = blocks[1:]
		stats.DroppedBlocks++
	}
	out := make([]*schema.Message, 0, len(system)+len(blocks[0]))
	out = append(out, system...)
	out = append(out, blocks[0]...)
	return out
}

func splitBlocks(body []*schema.Message) [][]*schema.Message {
	var blocks [][]*schema.Message
	var current []*schema.Message
	for _, m := range body {
		if m != nil && m.Role == schema.User && len(current) > 0 {
			blocks = append(blocks, current)
			current = nil
		}
		current = append(current, m)
	}
	if len(current) > 0 {
		blocks = append(blocks, current)
	}
	return blocks
}

func truncateToFit(messages []*schema.Message, maxTokens int, stats *Stats) {
	lastUser := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i] != nil && messages[i].Role == schema.User {
			lastUser = i
			break
		}
	}

	for EstimateTokens(messages) > maxTokens {
		idx := largestTruncatable(messages, func(i int) bool {
			return messages[i] != nil && messages[i].Role != schema.System && i != lastUser
		})
		if idx < 0 {
			idx = largestTruncatable(messages, func(i int) bool {
				return messages[i] != nil && messages[i].Role != schema.System
			})
		}
		if idx < 0 {
			idx = largestTruncatable(messages, func(i int) bool { return messages[i] != nil })
		}
		if idx < 0 {
			break
		}
		if !truncateMessage(messages[idx]) {
			break
		}
		stats.TruncatedMessages++
	}
}

func largestTruncatable(messages []*schema.Message, allow func(int) bool) int {
	best, bestSize := -1, 0
	for i, m := range messages {
		if !allow(i) {
			continue
		}
		size := len(m.Content)
		for _, tc := range m.ToolCalls {
			size += len(tc.Function.Arguments)
		}
		if size > bestSize {
			best, bestSize = i, size
		}
	}
	return best
}

// truncateMessage halves the largest text field. It returns false when there
// is nothing left to shrink.
func truncateMessage(m *schema.Message) bool {
	if len(m.Content) >= toolCallArgBytes(m) {
		if len(m.Content) > truncateFloorBytes {
			keep := len(m.Content) / 2
			m.Content = truncateUTF8(m.Content, keep) + "\n... [truncated to save context]"
			return true
		}
		if m.Content != "[truncated]" && m.Content != "" {
			m.Content = "[truncated]"
			return true
		}
	}
	for i := range m.ToolCalls {
		if len(m.ToolCalls[i].Function.Arguments) > 2 {
			m.ToolCalls[i].Function.Arguments = `{"truncated":true}`
			return true
		}
	}
	return false
}

func toolCallArgBytes(m *schema.Message) int {
	total := 0
	for _, tc := range m.ToolCalls {
		total += len(tc.Function.Arguments)
	}
	return total
}

func cloneMessage(m *schema.Message) *schema.Message {
	if m == nil {
		return nil
	}
	c := *m
	if len(m.ToolCalls) > 0 {
		c.ToolCalls = append([]schema.ToolCall(nil), m.ToolCalls...)
	}
	c.MultiContent = append([]schema.ChatMessagePart(nil), m.MultiContent...)
	c.UserInputMultiContent = append([]schema.MessageInputPart(nil), m.UserInputMultiContent...)
	c.AssistantGenMultiContent = append([]schema.MessageOutputPart(nil), m.AssistantGenMultiContent...)
	return &c
}

func messageTokens(m *schema.Message) int {
	if m == nil {
		return 0
	}
	n := 4 + textTokens(string(m.Role)) + textTokens(m.Content) + textTokens(m.Name) + textTokens(m.ReasoningContent)
	for _, tc := range m.ToolCalls {
		n += textTokens(tc.Function.Name) + textTokens(tc.Function.Arguments)
	}
	if m.ToolCallID != "" || m.ToolName != "" {
		n += 2
	}
	return n
}

func textTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 2) / 3
}

func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.ValidString(s[:cut]) {
		cut--
	}
	return strings.TrimRight(s[:cut], "\n")
}
