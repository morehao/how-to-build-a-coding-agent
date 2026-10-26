package agent

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
	"unicode/utf8"

	"github.com/cloudwego/eino/schema"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

//go:embed prompts/system.md
var systemPromptSource string

var systemPromptTemplate = template.Must(template.New("system").Parse(systemPromptSource))

const maxProjectInstructions = 16 * 1024

type promptData struct {
	Workspace           string
	OS                  string
	Arch                string
	Mode                string
	InstructionFile     string
	ProjectInstructions string
}

// BuildSystemPrompt renders the system prompt and appends the first repository
// instruction file it finds (AGENTS.md, AGENT.md, CLAUDE.md).
func BuildSystemPrompt(ws *workspace.Workspace, mode string) (string, error) {
	name, instructions := LoadProjectInstructions(ws)
	data := promptData{
		Workspace:           ws.Root(),
		OS:                  runtime.GOOS,
		Arch:                runtime.GOARCH,
		Mode:                mode,
		InstructionFile:     name,
		ProjectInstructions: instructions,
	}
	var b strings.Builder
	if err := systemPromptTemplate.Execute(&b, data); err != nil {
		return "", fmt.Errorf("render system prompt: %w", err)
	}
	return b.String(), nil
}

// LoadProjectInstructions returns the first existing instruction file and its
// content, truncated to 16 KiB. Missing files are not an error.
func LoadProjectInstructions(ws *workspace.Workspace) (string, string) {
	for _, name := range []string{"AGENTS.md", "AGENT.md", "CLAUDE.md"} {
		path := filepath.Join(ws.Root(), name)
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(f, maxProjectInstructions+1))
		_ = f.Close()
		if readErr != nil {
			continue
		}
		content := string(data)
		if len(content) > maxProjectInstructions {
			content = truncateUTF8Bytes(content, maxProjectInstructions) + "\n... [truncated]"
		}
		return name, content
	}
	return "", ""
}

// Modifier returns a react.MessageModifier that prepends the system prompt to
// every model call. ReAct's internal state accumulates tool messages, so this
// is the one place that guarantees the system message is always first.
func Modifier(systemPrompt string) func(context.Context, []*schema.Message) []*schema.Message {
	return func(_ context.Context, input []*schema.Message) []*schema.Message {
		if len(input) > 0 && input[0] != nil && input[0].Role == schema.System {
			return input
		}
		out := make([]*schema.Message, 0, len(input)+1)
		out = append(out, schema.SystemMessage(systemPrompt))
		out = append(out, input...)
		return out
	}
}

func truncateUTF8Bytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.ValidString(s[:cut]) {
		cut--
	}
	return s[:cut]
}
