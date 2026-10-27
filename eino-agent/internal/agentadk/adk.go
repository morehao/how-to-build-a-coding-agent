// Package agentadk runs the same coding-agent tools through Eino's ADK
// (ChatModelAgent + Runner) instead of the ReAct graph.
package agentadk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/contextmgr"
	contextsummary "github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/summary"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/trace"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/ui"
)

// Config configures the ADK runner.
type Config struct {
	Model           model.ToolCallingChatModel
	Tools           []tool.BaseTool
	SystemPrompt    string
	MaxSteps        int
	ContextTokens   int
	Counter         *contextmgr.Counter
	Stream          bool
	UI              *ui.UI
	Trace           *trace.Recorder
	CheckPointStore adk.CheckPointStore
}

// Agent wraps an ADK ChatModelAgent and Runner.
type Agent struct {
	runner *adk.Runner
	ui     *ui.UI
	trace  *trace.Recorder
	turn   int
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
	if cfg.ContextTokens <= 0 {
		cfg.ContextTokens = contextmgr.DefaultContextTokens
	}
	if cfg.Counter == nil {
		cfg.Counter = contextmgr.NewCounter()
	}

	toolNames, err := toolNames(ctx, cfg.Tools)
	if err != nil {
		return nil, err
	}
	unknownHandler := func(_ context.Context, name, _ string) (string, error) {
		return fmt.Sprintf("ERROR: unknown tool %q; available tools: %s", name, strings.Join(toolNames, ", ")), nil
	}

	// ADK summarization runs first when the conversation grows past 2/3 of
	// the budget. The deterministic GenModelInput below is the hard cap: even
	// if summarization is skipped or fails to shrink enough, the model never
	// receives more than cfg.ContextTokens (approximately).
	summaryTrigger := cfg.ContextTokens * 2 / 3
	if summaryTrigger < 1024 {
		summaryTrigger = 1024
	}
	summarizer, err := summarization.New(ctx, &summarization.Config{
		Model: cfg.Model,
		Trigger: &summarization.TriggerCondition{
			ContextTokens: summaryTrigger,
		},
		UserInstruction: contextsummary.StructuredInstruction,
		TokenCounter: func(_ context.Context, input *summarization.TokenCounterInput) (int, error) {
			return cfg.Counter.Count(input.Messages), nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create ADK summarization middleware: %w", err)
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
		Handlers: []adk.ChatModelAgentMiddleware{
			&fallbackSummarizer{TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.Message]{}, inner: summarizer, ui: cfg.UI},
		},
		GenModelInput: func(_ context.Context, instruction string, input *adk.AgentInput) ([]*schema.Message, error) {
			msgs := contextmgr.WithSystem(instruction, input.Messages)
			msgs, _ = cfg.Counter.CompactWithStats(msgs, cfg.ContextTokens)
			return msgs, nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create ADK chat model agent: %w", err)
	}

	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           chatAgent,
		EnableStreaming: cfg.Stream,
		CheckPointStore: cfg.CheckPointStore,
	})
	return &Agent{runner: runner, ui: cfg.UI, trace: cfg.Trace}, nil
}

// fallbackSummarizer turns a failed summarization model call into a warning
// and lets the deterministic GenModelInput cap handle the overflow. Context
// cancellation is never swallowed.
type fallbackSummarizer struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.Message]
	inner adk.ChatModelAgentMiddleware
	ui    *ui.UI
}

func (f *fallbackSummarizer) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	newCtx, newState, err := f.inner.BeforeModelRewriteState(ctx, state, mc)
	if err == nil {
		return newCtx, newState, nil
	}
	if ctx.Err() != nil {
		return ctx, nil, err
	}
	if f.ui != nil {
		f.ui.Warn("context summarization failed; falling back to deterministic compaction: %v", err)
	}
	return ctx, state, nil
}

// RunTurn executes one user turn and consumes the ADK event stream to
// completion, returning the last assistant message.
func (a *Agent) RunTurn(ctx context.Context, history []*schema.Message) (final *schema.Message, err error) {
	a.turn++
	turn := a.turn
	start := time.Now()
	a.trace.TurnStart(turn, lastUserContent(history))
	defer func() {
		var content string
		var usage *schema.TokenUsage
		if final != nil {
			content = final.Content
			if final.ResponseMeta != nil {
				usage = final.ResponseMeta.Usage
			}
		}
		a.trace.TurnEnd(turn, content, usage, err, time.Since(start))
	}()

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
			msg, getErr := mv.GetMessage()
			if getErr != nil {
				return nil, fmt.Errorf("adk tool result: %w", getErr)
			}
			var toolErr error
			if msg != nil {
				toolErr = toolResultError(msg.Content)
				if a.ui != nil {
					a.ui.ToolEnd(mv.ToolName, msg.Content, toolErr)
				}
				a.trace.ToolEnd(mv.ToolName, msg.Content, toolErr)
			}

		case schema.Assistant:
			content, usage, hasToolCalls, handleErr := a.handleAssistant(ctx, mv)
			if handleErr != nil {
				return nil, handleErr
			}
			if !hasToolCalls && strings.TrimSpace(content) != "" {
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
	final = schema.AssistantMessage(finalContent, nil)
	if finalUsage != nil {
		final.ResponseMeta = &schema.ResponseMeta{Usage: finalUsage}
	}
	return final, nil
}

func (a *Agent) handleAssistant(_ context.Context, mv *adk.MessageVariant) (string, *schema.TokenUsage, bool, error) {
	if !mv.IsStreaming {
		msg := mv.Message
		if msg == nil {
			return "", nil, false, nil
		}
		hasToolCalls := len(msg.ToolCalls) > 0
		if hasToolCalls {
			for _, tc := range msg.ToolCalls {
				if a.ui != nil {
					a.ui.ToolStart(tc.Function.Name, tc.Function.Arguments)
				}
				a.trace.ToolStart(tc.Function.Name, tc.Function.Arguments)
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
		return msg.Content, usage, hasToolCalls, nil
	}

	if mv.MessageStream == nil {
		return "", nil, false, errors.New("adk: assistant stream is nil")
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
			return "", nil, false, fmt.Errorf("adk assistant stream: %w", err)
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
	hasToolCalls := len(toolChunks) > 0
	if hasToolCalls {
		if merged, err := schema.ConcatMessages(toolChunks); err == nil && merged != nil {
			for _, tc := range merged.ToolCalls {
				if a.ui != nil {
					a.ui.ToolStart(tc.Function.Name, tc.Function.Arguments)
				}
				a.trace.ToolStart(tc.Function.Name, tc.Function.Arguments)
			}
		}
	}
	return content.String(), usage, hasToolCalls, nil
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

func toolResultError(content string) error {
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "ERROR:") {
		return errors.New(firstLine(trimmed))
	}
	if strings.HasPrefix(trimmed, "exit code:") {
		var code int
		if _, err := fmt.Sscanf(firstLine(trimmed), "exit code: %d", &code); err == nil && code != 0 {
			return errors.New(firstLine(trimmed))
		}
	}
	return nil
}

func lastUserContent(history []*schema.Message) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i] != nil && history[i].Role == schema.User {
			return history[i].Content
		}
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
