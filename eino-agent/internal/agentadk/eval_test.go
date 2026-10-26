package agentadk_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/agent"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/agentadk"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/approval"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/config"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/contextmgr"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/eval"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/model"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/tools"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/trace"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

func TestADKEvalScenarios(t *testing.T) {
	scenarios, err := eval.LoadDir("../../testdata/scenarios")
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) == 0 {
		t.Fatal("no scenarios found")
	}
	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.Name, func(t *testing.T) {
			t.Parallel()
			res := eval.Run(context.Background(), sc, func(ctx context.Context, ws *workspace.Workspace, fake *model.FakeModel, rec *trace.Recorder) (eval.Runner, error) {
				mode := sc.PermissionMode
				if mode == "" {
					mode = config.PermissionYolo
				}
				approver := approval.New(mode, strings.NewReader(""), io.Discard, false)
				toolList, err := tools.New(ctx, tools.Options{
					Workspace: ws,
					Approver:  approver,
					Mode:      mode,
				})
				if err != nil {
					return nil, err
				}
				prompt, err := agent.BuildSystemPrompt(ws, mode)
				if err != nil {
					return nil, err
				}
				counter := contextmgr.NewCounter()
				wrapped := model.NewUsageModel(fake, counter)
				return agentadk.New(ctx, agentadk.Config{
					Model:         wrapped,
					Tools:         toolList,
					SystemPrompt:  prompt,
					MaxSteps:      sc.MaxSteps,
					ContextTokens: sc.ContextTokens,
					Counter:       counter,
					Stream:        false,
					Trace:         rec,
				})
			})
			if !res.Passed {
				t.Fatalf("scenario failed:\n%s\ntrace:\n%s", eval.FormatResults([]eval.Result{res}), res.Trace)
			}
		})
	}
}
