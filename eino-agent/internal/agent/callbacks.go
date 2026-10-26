package agent

import (
	"context"
	"io"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/tool"
	template "github.com/cloudwego/eino/utils/callbacks"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/ui"
)

// NewCallbacks renders Eino tool lifecycle events in the terminal. Tool calls
// are executed inside the ReAct graph, so callbacks are the clean way to show
// what happened without reimplementing the loop.
func NewCallbacks(u *ui.UI) callbacks.Handler {
	if u == nil {
		u = ui.New(io.Discard, io.Discard, false, false)
	}
	handler := &template.ToolCallbackHandler{
		OnStart: func(ctx context.Context, info *callbacks.RunInfo, input *tool.CallbackInput) context.Context {
			args := ""
			if ci := tool.ConvCallbackInput(input); ci != nil {
				args = ci.ArgumentsInJSON
			}
			u.ToolStart(info.Name, args)
			return ctx
		},
		OnEnd: func(ctx context.Context, info *callbacks.RunInfo, output *tool.CallbackOutput) context.Context {
			result := ""
			if co := tool.ConvCallbackOutput(output); co != nil {
				result = co.Response
			}
			u.ToolEnd(info.Name, result, nil)
			return ctx
		},
		OnError: func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
			u.ToolEnd(info.Name, "", err)
			return ctx
		},
	}
	return template.NewHandlerHelper().Tool(handler).Handler()
}
