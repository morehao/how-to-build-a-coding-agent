// Package agent wires the Eino ReAct graph, system prompt and terminal
// callbacks into a single-turn runner.
package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	flowagent "github.com/cloudwego/eino/flow/agent"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/ui"
)

// Config configures the ReAct runner.
type Config struct {
	Model        model.ToolCallingChatModel
	Tools        []tool.BaseTool
	SystemPrompt string
	MaxSteps     int
	Stream       bool
	UI           *ui.UI
}

// Agent is a thin wrapper around Eino's ReAct agent.
type Agent struct {
	runner    *react.Agent
	callbacks callbacks.Handler
	ui        *ui.UI
	stream    bool
}

// New constructs the ReAct graph. Tools are executed sequentially so approval
// prompts, terminal output and tool side effects are deterministic.
func New(ctx context.Context, cfg Config) (*Agent, error) {
	if cfg.Model == nil {
		return nil, errors.New("agent: model is required")
	}
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = 30
	}

	toolNames, err := toolNames(ctx, cfg.Tools)
	if err != nil {
		return nil, err
	}
	unknownHandler := func(_ context.Context, name, _ string) (string, error) {
		return fmt.Sprintf("ERROR: unknown tool %q; available tools: %s", name, strings.Join(toolNames, ", ")), nil
	}

	runner, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: cfg.Model,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools:               cfg.Tools,
			ExecuteSequentially: true,
			UnknownToolsHandler: unknownHandler,
		},
		MaxStep:               cfg.MaxSteps,
		MessageModifier:       Modifier(cfg.SystemPrompt),
		StreamToolCallChecker: allChunksToolCallChecker,
		GraphName:             "EinoCodeAgent",
	})
	if err != nil {
		return nil, fmt.Errorf("create react agent: %w", err)
	}

	return &Agent{
		runner:    runner,
		callbacks: NewCallbacks(cfg.UI),
		ui:        cfg.UI,
		stream:    cfg.Stream,
	}, nil
}

// RunTurn executes one user turn over the supplied history and returns the
// final assistant message. Intermediate ReAct messages stay inside the graph.
func (a *Agent) RunTurn(ctx context.Context, history []*schema.Message) (*schema.Message, error) {
	opts := []flowagent.AgentOption{
		flowagent.WithComposeOptions(compose.WithCallbacks(a.callbacks)),
	}
	if !a.stream {
		msg, err := a.runner.Generate(ctx, history, opts...)
		if err != nil {
			return nil, fmt.Errorf("model generate: %w", err)
		}
		if msg == nil {
			return nil, errors.New("model returned no message")
		}
		if a.ui != nil {
			a.ui.AgentMessage(msg.Content)
			if msg.ResponseMeta != nil && msg.ResponseMeta.Usage != nil {
				u := msg.ResponseMeta.Usage
				a.ui.Usage(u.PromptTokens, u.CompletionTokens, u.TotalTokens)
			}
		}
		return msg, nil
	}

	sr, err := a.runner.Stream(ctx, history, opts...)
	if err != nil {
		return nil, fmt.Errorf("model stream: %w", err)
	}
	defer sr.Close()

	if a.ui != nil {
		a.ui.AgentStart()
	}
	var content strings.Builder
	var usage *schema.TokenUsage
	for {
		chunk, recvErr := sr.Recv()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				break
			}
			return nil, fmt.Errorf("model stream: %w", recvErr)
		}
		if chunk == nil {
			continue
		}
		if chunk.Content != "" {
			content.WriteString(chunk.Content)
			if a.ui != nil {
				a.ui.AgentChunk(chunk.Content)
			}
		}
		if chunk.ResponseMeta != nil && chunk.ResponseMeta.Usage != nil {
			usage = chunk.ResponseMeta.Usage
		}
		if chunk.ReasoningContent != "" && a.ui != nil {
			a.ui.Verbosef("reasoning: %s", chunk.ReasoningContent)
		}
	}
	if a.ui != nil {
		a.ui.AgentEnd()
		if usage != nil {
			a.ui.Usage(usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
		}
	}

	final := schema.AssistantMessage(content.String(), nil)
	if usage != nil {
		final.ResponseMeta = &schema.ResponseMeta{Usage: usage}
	}
	return final, nil
}

// allChunksToolCallChecker is required for providers (notably Claude) that may
// stream text before tool calls. Eino's default checker only inspects the first
// non-empty chunk; this one scans until it sees a tool call or EOF.
func allChunksToolCallChecker(_ context.Context, sr *schema.StreamReader[*schema.Message]) (bool, error) {
	defer sr.Close()
	for {
		msg, err := sr.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return false, nil
			}
			return false, err
		}
		if msg != nil && len(msg.ToolCalls) > 0 {
			return true, nil
		}
	}
}

func toolNames(ctx context.Context, tools []tool.BaseTool) ([]string, error) {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		info, err := t.Info(ctx)
		if err != nil {
			return nil, fmt.Errorf("read tool info: %w", err)
		}
		names = append(names, info.Name)
	}
	return names, nil
}
