// Package agentadk runs the same coding-agent tools through Eino's ADK
// (ChatModelAgent + Runner) instead of the ReAct graph.
package agentadk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/ui"
)

// Config configures the ADK runner.
type Config struct {
	Model        model.ToolCallingChatModel
	Tools        []tool.BaseTool
	SystemPrompt string
	MaxSteps     int
	Stream       bool
	UI           *ui.UI
}

// Agent wraps an ADK ChatModelAgent and Runner.
type Agent struct {
	runner *adk.Runner
	ui     *ui.UI
}

// New constructs the ADK agent. ADK owns the tool loop, iteration limit and
// event stream; this package only translates events to terminal output.
func New(ctx context.Context, cfg Config) (*Agent, error) {
	if cfg.Model == nil {
		return nil, errors.New("agentadk: model is required")
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

	chatAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "eino-code-agent-adk",
		Description: "A local coding agent with file, search and shell tools.",
		Instruction: cfg.SystemPrompt,
		Model:       cfg.Model,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools:               cfg.Tools,
				ExecuteSequentially: true,
				UnknownToolsHandler: unknownHandler,
			},
		},
		MaxIterations: cfg.MaxSteps,
	})
	if err != nil {
		return nil, fmt.Errorf("create ADK chat model agent: %w", err)
	}

	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           chatAgent,
		EnableStreaming: cfg.Stream,
	})
	return &Agent{runner: runner, ui: cfg.UI}, nil
}

// RunTurn executes one user turn and consumes the ADK event stream to
// completion, returning the last assistant message.
func (a *Agent) RunTurn(ctx context.Context, history []*schema.Message) (*schema.Message, error) {
	iter := a.runner.Run(ctx, history)
	var (
		finalContent string
		finalUsage   *schema.TokenUsage
	)
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			return nil, fmt.Errorf("adk run: %w", event.Err)
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		mv := event.Output.MessageOutput

		switch mv.Role {
		case schema.Tool:
			msg, err := mv.GetMessage()
			if err != nil {
				return nil, fmt.Errorf("adk tool result: %w", err)
			}
			if a.ui != nil && msg != nil {
				a.ui.ToolEnd(mv.ToolName, msg.Content, nil)
				if strings.HasPrefix(msg.Content, "ERROR:") {
					a.ui.Warn("tool %s reported: %s", mv.ToolName, firstLine(msg.Content))
				}
			}

		case schema.Assistant:
			content, usage, err := a.handleAssistant(ctx, mv)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(content) != "" {
				finalContent = content
			}
			if usage != nil {
				finalUsage = usage
			}
		}
	}

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if strings.TrimSpace(finalContent) == "" {
		return nil, errors.New("adk: no assistant response")
	}
	final := schema.AssistantMessage(finalContent, nil)
	if finalUsage != nil {
		final.ResponseMeta = &schema.ResponseMeta{Usage: finalUsage}
	}
	return final, nil
}

func (a *Agent) handleAssistant(_ context.Context, mv *adk.MessageVariant) (string, *schema.TokenUsage, error) {
	if !mv.IsStreaming {
		msg := mv.Message
		if msg == nil {
			return "", nil, nil
		}
		if len(msg.ToolCalls) > 0 && a.ui != nil {
			for _, tc := range msg.ToolCalls {
				a.ui.ToolStart(tc.Function.Name, tc.Function.Arguments)
			}
		}
		if msg.Content != "" {
			if a.ui != nil {
				a.ui.AgentMessage(msg.Content)
			}
		}
		var usage *schema.TokenUsage
		if msg.ResponseMeta != nil {
			usage = msg.ResponseMeta.Usage
		}
		return msg.Content, usage, nil
	}

	if mv.MessageStream == nil {
		return "", nil, errors.New("adk: assistant stream is nil")
	}
	defer mv.MessageStream.Close()

	var (
		content    strings.Builder
		started    bool
		toolChunks []*schema.Message
		usage      *schema.TokenUsage
	)
	for {
		chunk, err := mv.MessageStream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", nil, fmt.Errorf("adk assistant stream: %w", err)
		}
		if chunk == nil {
			continue
		}
		if chunk.Content != "" {
			if !started {
				if a.ui != nil {
					a.ui.AgentStart()
				}
				started = true
			}
			content.WriteString(chunk.Content)
			if a.ui != nil {
				a.ui.AgentChunk(chunk.Content)
			}
		}
		if len(chunk.ToolCalls) > 0 {
			toolChunks = append(toolChunks, chunk)
		}
		if chunk.ResponseMeta != nil && chunk.ResponseMeta.Usage != nil {
			usage = chunk.ResponseMeta.Usage
		}
	}
	if started && a.ui != nil {
		a.ui.AgentEnd()
	}
	if len(toolChunks) > 0 && a.ui != nil {
		if merged, err := schema.ConcatMessages(toolChunks); err == nil && merged != nil {
			for _, tc := range merged.ToolCalls {
				a.ui.ToolStart(tc.Function.Name, tc.Function.Arguments)
			}
		}
	}
	return content.String(), usage, nil
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

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
