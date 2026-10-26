package eval

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/model"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/trace"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

type traceRunner struct{ rec *trace.Recorder }

func (r *traceRunner) RunTurn(_ context.Context, _ []*schema.Message) (*schema.Message, error) {
	r.rec.TurnStart(1, "hi")
	r.rec.ModelStart(nil, nil)
	r.rec.ModelEnd(schema.AssistantMessage("ok", nil), time.Millisecond)
	r.rec.TurnEnd(1, "ok", nil, nil, 2*time.Millisecond)
	return schema.AssistantMessage("ok", nil), nil
}

func TestTraceAssertions(t *testing.T) {
	sc := Scenario{
		Name:   "trace",
		Input:  "hi",
		Script: model.FakeScript{Name: "x", Steps: []model.FakeStep{{Content: "ok"}}},
		Expect: Expect{
			FinalContains: []string{"ok"},
			TraceContains: []string{"model_end", "turn_end"},
			MinModelCalls: 1,
			MaxModelCalls: 1,
			MinToolCalls:  0,
		},
	}
	res := Run(context.Background(), sc, func(_ context.Context, _ *workspace.Workspace, _ *model.FakeModel, rec *trace.Recorder) (Runner, error) {
		return &traceRunner{rec: rec}, nil
	})
	if !res.Passed {
		t.Fatalf("failed: %v\n%s", res.Errors, res.Trace)
	}
	if res.Stats.ModelCalls != 1 || res.Stats.Turns != 1 {
		t.Fatalf("stats = %+v", res.Stats)
	}
}

func TestTraceAssertionFailure(t *testing.T) {
	sc := Scenario{
		Name:   "trace-fail",
		Input:  "hi",
		Script: model.FakeScript{Name: "x", Steps: []model.FakeStep{{Content: "ok"}}},
		Expect: Expect{
			TraceNotContains: []string{"model_end"},
		},
	}
	res := Run(context.Background(), sc, func(_ context.Context, _ *workspace.Workspace, _ *model.FakeModel, rec *trace.Recorder) (Runner, error) {
		return &traceRunner{rec: rec}, nil
	})
	if res.Passed || len(res.Errors) == 0 {
		t.Fatalf("expected failure, got %+v", res)
	}
}
