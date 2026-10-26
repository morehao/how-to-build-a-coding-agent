// Package hooks runs opt-in shell hooks around tool calls. Hooks are trusted
// repository configuration: they execute before/after the approval gate inside
// the tool, so only enable a hook file you have reviewed.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/policy"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

const (
	defaultTimeout = 30 * time.Second
	maxTimeout     = 5 * time.Minute
	maxOutputBytes = 64 * 1024
)

// Hook is one command. Tools filters by tool name; "*" means all tools. An
// empty Tools list is rejected so a typo cannot run a hook everywhere.
type Hook struct {
	Command        string   `json:"command"`
	Tools          []string `json:"tools"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
}

// Set is the parsed hooks file.
type Set struct {
	Before []Hook `json:"before_tool,omitempty"`
	After  []Hook `json:"after_tool,omitempty"`

	ws *workspace.Workspace
}

// Load reads a hooks file. A missing file is not an error; setting path empty
// disables hooks entirely.
func Load(ws *workspace.Workspace, path string) (*Set, error) {
	if ws == nil {
		return nil, errors.New("hooks: workspace is required")
	}
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read hooks: %w", err)
	}
	var s Set
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse hooks %s: %w", path, err)
	}
	for i, h := range append(append([]Hook(nil), s.Before...), s.After...) {
		if strings.TrimSpace(h.Command) == "" {
			return nil, fmt.Errorf("hooks: entry %d has an empty command", i)
		}
		if len(h.Tools) == 0 {
			return nil, fmt.Errorf("hooks: entry %d must list tools (use [\"*\"] for all)", i)
		}
		if h.TimeoutSeconds < 0 || h.TimeoutSeconds > int(maxTimeout.Seconds()) {
			return nil, fmt.Errorf("hooks: entry %d timeout_seconds must be between 0 and %d", i, int(maxTimeout.Seconds()))
		}
	}
	s.ws = ws
	return &s, nil
}

// Wrap returns a copy of tools with before/after hooks. A nil Set returns the
// input unchanged. Tools that are not invokable are passed through.
func (s *Set) Wrap(ctx context.Context, tools []tool.BaseTool) []tool.BaseTool {
	if s == nil || len(tools) == 0 {
		return tools
	}
	out := make([]tool.BaseTool, 0, len(tools))
	for _, t := range tools {
		inv, ok := t.(tool.InvokableTool)
		if !ok {
			out = append(out, t)
			continue
		}
		info, err := t.Info(ctx)
		if err != nil || info == nil {
			out = append(out, t)
			continue
		}
		out = append(out, &wrapped{inner: inv, name: info.Name, hooks: s})
	}
	return out
}

type wrapped struct {
	inner tool.InvokableTool
	name  string
	hooks *Set
}

func (w *wrapped) Info(ctx context.Context) (*schema.ToolInfo, error) {
	if base, ok := w.inner.(tool.BaseTool); ok {
		return base.Info(ctx)
	}
	return nil, errors.New("hooks: wrapped tool has no Info")
}

func (w *wrapped) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	beforeOut, err := w.hooks.run(ctx, w.hooks.Before, "before_tool", w.name, argumentsInJSON, "", "")
	if err != nil {
		return fmt.Sprintf("ERROR: before_tool hook blocked %s: %v\n%s", w.name, err, beforeOut), nil
	}
	out, runErr := w.inner.InvokableRun(ctx, argumentsInJSON, opts...)
	result := out
	errText := ""
	if runErr != nil {
		errText = runErr.Error()
	}
	afterOut, afterErr := w.hooks.run(ctx, w.hooks.After, "after_tool", w.name, argumentsInJSON, result, errText)
	if afterErr != nil {
		out += "\n[after_tool hook failed: " + afterErr.Error() + "]"
	} else if strings.TrimSpace(afterOut) != "" {
		out += "\n[after_tool hook]\n" + afterOut
	}
	if strings.TrimSpace(beforeOut) != "" {
		out = "[before_tool hook]\n" + beforeOut + "\n" + out
	}
	return out, runErr
}

func (s *Set) run(ctx context.Context, list []Hook, event, toolName, args, result, errText string) (string, error) {
	var combined strings.Builder
	for _, h := range list {
		if !h.matches(toolName) {
			continue
		}
		timeout := defaultTimeout
		if h.TimeoutSeconds > 0 {
			timeout = time.Duration(h.TimeoutSeconds) * time.Second
		}
		hookCtx, cancel := context.WithTimeout(ctx, timeout)
		payload, _ := json.Marshal(map[string]string{
			"event": event, "tool": toolName, "args": args, "result": result, "error": errText,
		})
		cmd := exec.CommandContext(hookCtx, "bash", "-c", h.Command)
		cmd.Dir = s.ws.Root()
		cmd.Env = append(policy.SanitizedEnv(os.Environ(), false),
			"EINO_HOOK_EVENT="+event,
			"EINO_HOOK_TOOL="+toolName,
		)
		cmd.Stdin = bytes.NewReader(payload)
		var buf limitedBuffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		runErr := cmd.Run()
		cancel()
		if buf.String() != "" {
			combined.WriteString(buf.String())
			if !strings.HasSuffix(combined.String(), "\n") {
				combined.WriteByte('\n')
			}
		}
		if runErr != nil {
			if hookCtx.Err() == context.DeadlineExceeded {
				return combined.String(), fmt.Errorf("timed out after %s", timeout)
			}
			return combined.String(), fmt.Errorf("%s hook %q: %w", event, h.Command, runErr)
		}
	}
	return combined.String(), nil
}

func (h Hook) matches(name string) bool {
	for _, t := range h.Tools {
		if t == "*" || t == name {
			return true
		}
	}
	return false
}

type limitedBuffer struct {
	buf bytes.Buffer
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remaining := maxOutputBytes - b.buf.Len()
	if remaining <= 0 {
		return len(p), nil
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *limitedBuffer) String() string { return b.buf.String() }
