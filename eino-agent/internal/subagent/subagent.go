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
	"sync"
	"time"
	"unicode/utf8"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/agent"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/budget"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/config"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/contextmgr"
	modelpkg "github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/model"
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
	agent          *agent.Agent
	timeout        time.Duration
	maxResultBytes int
}

// Config builds a read-only nested runner.
type Config struct {
	Workspace      *workspace.Workspace
	Model          einomodel.ToolCallingChatModel
	Trace          *trace.Recorder
	MaxSteps       int
	MaxOutputBytes int
	ContextTokens  int
	// SkillTool is an optional read-only skill loader exposed to the nested
	// agent. SkillPrompt is the matching progressive-disclosure prompt section.
	SkillTool      tool.BaseTool
	SkillPrompt    string
	Timeout        time.Duration
	MaxResultBytes int
	// MaxTotalTokens caps this nested runner's own token usage. It is
	// independent of the parent's budget; 0 inherits no extra cap.
	MaxTotalTokens int
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
	if cfg.Timeout <= 0 {
		cfg.Timeout = 2 * time.Minute
	}
	if cfg.MaxResultBytes <= 0 {
		cfg.MaxResultBytes = 64 * 1024
	}
	nestedModel := cfg.Model
	if cfg.MaxTotalTokens > 0 {
		nestedModel = modelpkg.NewUsageModel(nestedModel, contextmgr.NewCounter(), budget.New(cfg.MaxTotalTokens))
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
	if cfg.SkillTool != nil {
		if info, infoErr := cfg.SkillTool.Info(ctx); infoErr == nil && info != nil {
			nestedTools = append(nestedTools, cfg.SkillTool)
			names = append(names, info.Name)
		}
	}
	if len(nestedTools) == 0 {
		return nil, errors.New("subagent: no read-only tools available")
	}
	prompt, err := agent.BuildSystemPrompt(cfg.Workspace, config.PermissionReadOnly)
	if err != nil {
		return nil, err
	}
	prompt += cfg.SkillPrompt
	prompt += "\n\nYou are a read-only subagent. Investigate the task and report concise findings. " +
		"You cannot edit files, run bash, or delegate further. Available tools: " + strings.Join(names, ", ") + "."
	silent := ui.New(io.Discard, io.Discard, false, false)
	nested, err := agent.New(ctx, agent.Config{
		Model:         nestedModel,
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
	return &ReActRunner{agent: nested, timeout: cfg.Timeout, maxResultBytes: cfg.MaxResultBytes}, nil
}

// Run executes one independent task.
func (r *ReActRunner) Run(ctx context.Context, prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", errors.New("subagent: empty task")
	}
	if r.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.timeout)
		defer cancel()
	}
	final, err := r.agent.RunTurn(ctx, []*schema.Message{schema.UserMessage(prompt)})
	if err != nil {
		return "", err
	}
	if final == nil {
		return "", errors.New("subagent: empty final message")
	}
	return truncateUTF8(final.Content, r.maxResultBytes), nil
}

func truncateUTF8(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.ValidString(s[:cut]) {
		cut--
	}
	return s[:cut] + "\n... [subagent report truncated]"
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
			out = truncateUTF8(out, 64*1024)
			return fmt.Sprintf("subagent (%s):\n%s", strings.TrimSpace(in.Description), out), nil
		},
	)
}

type tasksInput struct {
	Tasks []taskInput `json:"tasks" jsonschema:"required" jsonschema_description:"Independent read-only investigations to run in parallel (max 4)."`
}

// RunnerFactory creates an isolated runner for one parallel task. Each runner
// must own its turn state and token budget; sharing one Runner across
// goroutines is not safe.
type RunnerFactory func() (Runner, error)

// NewTasksTool is the parallel variant of task. Results are returned in input
// order even though the runners execute concurrently.
func NewTasksTool(factory RunnerFactory, maxTasks int) (tool.BaseTool, error) {
	if factory == nil {
		return nil, errors.New("subagent: runner factory is required")
	}
	if maxTasks <= 0 {
		maxTasks = 4
	}
	if maxTasks > 8 {
		maxTasks = 8
	}
	return utils.InferTool(
		"tasks",
		"Run multiple independent read-only investigations in parallel. Each task gets its own read-only subagent and token budget. Use this instead of repeated task calls when the questions do not depend on each other. Results are returned in input order.",
		func(ctx context.Context, in tasksInput) (string, error) {
			if len(in.Tasks) == 0 {
				return "", errors.New("tasks is empty")
			}
			if len(in.Tasks) > maxTasks {
				return "", fmt.Errorf("too many tasks: %d (max %d)", len(in.Tasks), maxTasks)
			}
			type result struct {
				text string
				err  error
			}
			results := make([]result, len(in.Tasks))
			sem := make(chan struct{}, maxTasks)
			var wg sync.WaitGroup
			for i := range in.Tasks {
				i := i
				wg.Add(1)
				go func() {
					defer wg.Done()
					select {
					case sem <- struct{}{}:
						defer func() { <-sem }()
					case <-ctx.Done():
						results[i].err = ctx.Err()
						return
					}
					r, err := factory()
					if err != nil {
						results[i].err = err
						return
					}
					out, err := r.Run(ctx, in.Tasks[i].Prompt)
					if err != nil {
						results[i].err = err
						return
					}
					results[i].text = truncateUTF8(out, 64*1024)
				}()
			}
			wg.Wait()
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var b strings.Builder
			fmt.Fprintf(&b, "parallel subagents (%d):", len(in.Tasks))
			for i, r := range results {
				desc := strings.TrimSpace(in.Tasks[i].Description)
				if desc == "" {
					desc = fmt.Sprintf("task %d", i+1)
				}
				b.WriteString("\n\n## ")
				b.WriteString(desc)
				b.WriteString("\n")
				if r.err != nil {
					b.WriteString("ERROR: subagent task failed: ")
					b.WriteString(r.err.Error())
				} else {
					b.WriteString(r.text)
				}
			}
			return truncateUTF8(b.String(), 256*1024), nil
		},
	)
}
