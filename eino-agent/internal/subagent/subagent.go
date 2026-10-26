// Package subagent provides a read-only nested agent exposed to the parent as
// the task tool. The nested agent cannot edit, run bash, or create another
// task; this keeps delegation useful without giving it unbounded recursion.
package subagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/agent"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/config"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/contextmgr"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/tools"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/trace"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/ui"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

// Runner is the minimal contract the task tool needs. The ADK branch can
// implement it with an ADK runner; the ReAct branch uses ReActRunner.
type Runner interface {
	Run(ctx context.Context, prompt string) (string, error)
}

// ReActRunner is a read-only nested ReAct agent.
type ReActRunner struct {
	agent *agent.Agent
}

// Config builds a read-only nested runner.
type Config struct {
	Workspace      *workspace.Workspace
	Model          einomodel.ToolCallingChatModel
	Trace          *trace.Recorder
	MaxSteps       int
	MaxOutputBytes int
	ContextTokens  int
}

// NewReActRunner creates a read-only nested agent with read/list/search/repo_map
// only. It shares the parent model and optional trace recorder.
func NewReActRunner(ctx context.Context, cfg Config) (*ReActRunner, error) {
	if cfg.Workspace == nil || cfg.Model == nil {
		return nil, errors.New("subagent: workspace and model are required")
	}
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = 6
	}
	if cfg.MaxSteps > 12 {
		cfg.MaxSteps = 12
	}
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = 64 * 1024
	}
	if cfg.ContextTokens <= 0 {
		cfg.ContextTokens = 20000
	}
	all, err := tools.New(ctx, tools.Options{
		Workspace:      cfg.Workspace,
		Mode:           config.PermissionReadOnly,
		MaxOutputBytes: cfg.MaxOutputBytes,
		BashTimeout:    10 * time.Second, // bash is filtered out below
	})
	if err != nil {
		return nil, fmt.Errorf("subagent tools: %w", err)
	}
	allowed := map[string]struct{}{
		"read_file": {}, "list_files": {}, "code_search": {}, "repo_map": {},
	}
	var nestedTools []tool.BaseTool
	var names []string
	for _, t := range all {
		info, err := t.Info(ctx)
		if err != nil || info == nil {
			continue
		}
		if _, ok := allowed[info.Name]; !ok {
			continue
		}
		nestedTools = append(nestedTools, t)
		names = append(names, info.Name)
	}
	if len(nestedTools) == 0 {
		return nil, errors.New("subagent: no read-only tools available")
	}
	prompt, err := agent.BuildSystemPrompt(cfg.Workspace, config.PermissionReadOnly)
	if err != nil {
		return nil, err
	}
	prompt += "\n\nYou are a read-only subagent. Investigate the task and report concise findings. " +
		"You cannot edit files, run bash, or delegate further. Available tools: " + strings.Join(names, ", ") + "."
	silent := ui.New(io.Discard, io.Discard, false, false)
	nested, err := agent.New(ctx, agent.Config{
		Model:         cfg.Model,
		Tools:         nestedTools,
		SystemPrompt:  prompt,
		MaxSteps:      cfg.MaxSteps,
		ContextTokens: cfg.ContextTokens,
		Counter:       contextmgr.NewCounter(),
		Stream:        false,
		UI:            silent,
		Trace:         cfg.Trace,
	})
	if err != nil {
		return nil, fmt.Errorf("subagent agent: %w", err)
	}
	return &ReActRunner{agent: nested}, nil
}

// Run executes one independent task.
func (r *ReActRunner) Run(ctx context.Context, prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", errors.New("subagent: empty task")
	}
	final, err := r.agent.RunTurn(ctx, []*schema.Message{schema.UserMessage(prompt)})
	if err != nil {
		return "", err
	}
	if final == nil {
		return "", errors.New("subagent: empty final message")
	}
	return final.Content, nil
}

type taskInput struct {
	Description string `json:"description" jsonschema:"required" jsonschema_description:"Short description of the delegated task."`
	Prompt      string `json:"prompt" jsonschema:"required" jsonschema_description:"Self-contained instructions for the read-only subagent."`
}

// NewTaskTool wraps a Runner as the task tool.
func NewTaskTool(r Runner) (tool.BaseTool, error) {
	if r == nil {
		return nil, errors.New("subagent: runner is required")
	}
	return utils.InferTool(
		"task",
		"Delegate a focused, read-only investigation (search, read, repo map) to a subagent. The subagent cannot edit files or run bash and cannot delegate further. Use it when a task is independent and would flood the main context.",
		func(ctx context.Context, in taskInput) (string, error) {
			if strings.TrimSpace(in.Prompt) == "" {
				return "", errors.New("task prompt is empty")
			}
			out, err := r.Run(ctx, in.Prompt)
			if err != nil {
				if ctx.Err() != nil {
					return "", ctx.Err()
				}
				return "ERROR: subagent task failed: " + err.Error(), nil
			}
			return fmt.Sprintf("subagent (%s):\n%s", strings.TrimSpace(in.Description), out), nil
		},
	)
}
