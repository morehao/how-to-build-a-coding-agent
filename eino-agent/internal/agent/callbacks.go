package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cloudwego/eino/callbacks"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	template "github.com/cloudwego/eino/utils/callbacks"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/trace"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/ui"
)

type modelStartKey struct{}

// NewCallbacks renders tool/model lifecycle events in the terminal and, when
// tr is non-nil, records them as JSONL trace events.
func NewCallbacks(u *ui.UI, tr *trace.Recorder) callbacks.Handler {
	if u == nil {
		u = ui.New(io.Discard, io.Discard, false, false)
	}

	toolHandler := &template.ToolCallbackHandler{
		OnStart: func(ctx context.Context, info *callbacks.RunInfo, input *tool.CallbackInput) context.Context {
			args := ""
			if ci := tool.ConvCallbackInput(input); ci != nil {
				args = ci.ArgumentsInJSON
			}
			u.ToolStart(info.Name, args)
			tr.ToolStart(info.Name, args)
			return ctx
		},
		OnEnd: func(ctx context.Context, info *callbacks.RunInfo, output *tool.CallbackOutput) context.Context {
			result := ""
			if co := tool.ConvCallbackOutput(output); co != nil {
				result = co.Response
			}
			toolErr := toolResultError(result)
			u.ToolEnd(info.Name, result, toolErr)
			tr.ToolEnd(info.Name, result, toolErr)
			return ctx
		},
		OnError: func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
			u.ToolEnd(info.Name, "", err)
			tr.ToolEnd(info.Name, "", err)
			return ctx
		},
	}

	modelHandler := &template.ModelCallbackHandler{
		OnStart: func(ctx context.Context, _ *callbacks.RunInfo, input *einomodel.CallbackInput) context.Context {
			ci := einomodel.ConvCallbackInput(input)
			var msgs []*schema.Message
			var names []string
			if ci != nil {
				msgs = ci.Messages
				for _, t := range ci.Tools {
					if t != nil {
						names = append(names, t.Name)
					}
				}
			}
			tr.ModelStart(msgs, names)
			return context.WithValue(ctx, modelStartKey{}, time.Now())
		},
		OnEnd: func(ctx context.Context, _ *callbacks.RunInfo, output *einomodel.CallbackOutput) context.Context {
			co := einomodel.ConvCallbackOutput(output)
			var msg *schema.Message
			if co != nil {
				msg = co.Message
			}
			tr.ModelEnd(msg, elapsed(ctx))
			return ctx
		},
		OnEndWithStreamOutput: func(ctx context.Context, _ *callbacks.RunInfo, output *schema.StreamReader[*einomodel.CallbackOutput]) context.Context {
			defer output.Close()
			var content strings.Builder
			var last *schema.Message
			for {
				item, err := output.Recv()
				if err != nil {
					break
				}
				if item == nil || item.Message == nil {
					continue
				}
				last = item.Message
				content.WriteString(item.Message.Content)
			}
			msg := schema.AssistantMessage(content.String(), nil)
			if last != nil {
				msg.ReasoningContent = last.ReasoningContent
				msg.ResponseMeta = last.ResponseMeta
			}
			tr.ModelEnd(msg, elapsed(ctx))
			return ctx
		},
		OnError: func(ctx context.Context, _ *callbacks.RunInfo, err error) context.Context {
			tr.ModelError(err, elapsed(ctx))
			return ctx
		},
	}

	return template.NewHandlerHelper().ChatModel(modelHandler).Tool(toolHandler).Handler()
}

// toolResultError turns the agent's string error convention into an error for
// the terminal renderer. Tool handlers return errors as text so the model can
// recover; the UI should still show a failed call as failed.
func toolResultError(result string) error {
	trimmed := strings.TrimSpace(result)
	if strings.HasPrefix(trimmed, "ERROR:") {
		return errors.New(trimmed)
	}
	if strings.HasPrefix(trimmed, "exit code:") {
		line := strings.SplitN(trimmed, "\n", 2)[0]
		if line != "exit code: 0" {
			return fmt.Errorf("%s", line)
		}
	}
	return nil
}

func elapsed(ctx context.Context) time.Duration {
	start, ok := ctx.Value(modelStartKey{}).(time.Time)
	if !ok {
		return 0
	}
	return time.Since(start)
}
