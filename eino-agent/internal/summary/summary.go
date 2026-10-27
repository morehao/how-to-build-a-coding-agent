// Package summary builds a structured long-term summary for a coding-agent
// conversation. It is used by /compact and by the ADK summarization middleware
// instruction, so both paths preserve the same categories of information.
package summary

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/contextmgr"
)

// MaxBytes bounds the generated summary before it is stored in a session.
const MaxBytes = 32 * 1024

// StructuredInstruction is shared by manual /compact and ADK auto-summary.
const StructuredInstruction = `Summarize this coding-agent conversation into a compact working brief. Use exactly these sections and keep every bullet concrete:

## Goal
## Decisions
## Files and changes
## Commands and results
## Errors and blockers
## Next steps

Preserve exact file paths, command names, error strings and user constraints. Do not invent facts. Drop raw tool output and repeated reasoning; keep conclusions and evidence needed to continue the task.`

const systemPrompt = `You are a context compaction component for a coding agent. You receive a transcript of earlier user and assistant messages. Produce a dense working brief that lets the agent continue without the raw history. Follow the requested section structure exactly. Do not use tools and do not ask questions.`

// Generate summarizes history. The input is compacted to maxInputTokens before
// it is rendered, so a very long session does not overflow the summarizer.
func Generate(ctx context.Context, m einomodel.BaseModel[*schema.Message], history []*schema.Message, maxInputTokens int, counter *contextmgr.Counter) (string, error) {
	if m == nil {
		return "", errors.New("summary: model is required")
	}
	if len(history) == 0 {
		return "", errors.New("summary: no history to compact")
	}
	if maxInputTokens > 0 {
		var stats contextmgr.Stats
		if counter != nil {
			history, stats = counter.CompactWithStats(history, maxInputTokens)
		} else {
			history, stats = contextmgr.CompactWithStats(history, maxInputTokens)
		}
		_ = stats
	}

	input := []*schema.Message{
		schema.SystemMessage(systemPrompt),
		schema.UserMessage("Conversation transcript:\n\n" + renderTranscript(history) + "\n\n" + StructuredInstruction),
	}
	out, err := m.Generate(ctx, input)
	if err != nil {
		return "", fmt.Errorf("summary generate: %w", err)
	}
	if out == nil || strings.TrimSpace(out.Content) == "" {
		return "", errors.New("summary: model returned an empty summary")
	}
	// The CLI wraps the model in usageModel, which observes provider usage.
	// Do not observe here as well or the same sample is counted twice.
	content := strings.TrimSpace(out.Content)
	if len(content) > MaxBytes {
		content = truncateUTF8(content, MaxBytes) + "\n... [summary truncated]"
	}
	return content, nil
}

func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.ValidString(s[:cut]) {
		cut--
	}
	return s[:cut]
}

func renderTranscript(history []*schema.Message) string {
	var b strings.Builder
	for _, m := range history {
		if m == nil {
			continue
		}
		role := string(m.Role)
		switch m.Role {
		case schema.User:
			role = "User"
		case schema.Assistant:
			role = "Assistant"
		case schema.Tool:
			role = "Tool"
		case schema.System:
			role = "System"
		}
		b.WriteString("### ")
		b.WriteString(role)
		b.WriteString("\n")
		b.WriteString(m.Content)
		b.WriteString("\n\n")
	}
	return b.String()
}
