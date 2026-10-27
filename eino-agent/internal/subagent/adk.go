package subagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/agent"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/agentadk"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/budget"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/config"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/contextmgr"
	modelpkg "github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/model"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/tools"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/ui"
)

// ADKRunner is a read-only nested ADK agent used by the task tool on the ADK
// branch.
type ADKRunner struct {
	agent          *agentadk.Agent
	timeout        time.Duration
	maxResultBytes int
}

// NewADKRunner creates a read-only nested ADK runner.
func NewADKRunner(ctx context.Context, cfg Config) (*ADKRunner, error) {
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
		BashTimeout:    10 * time.Second,
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
	nested, err := agentadk.New(ctx, agentadk.Config{
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
	return &ADKRunner{agent: nested, timeout: cfg.Timeout, maxResultBytes: cfg.MaxResultBytes}, nil
}

func (r *ADKRunner) Run(ctx context.Context, prompt string) (string, error) {
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
