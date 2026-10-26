package subagent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
)

type parallelFakeRunner struct {
	out    string
	delay  time.Duration
	active *int32
	max    *int32
}

func (f *parallelFakeRunner) Run(context.Context, string) (string, error) {
	n := atomic.AddInt32(f.active, 1)
	for {
		old := atomic.LoadInt32(f.max)
		if n <= old || atomic.CompareAndSwapInt32(f.max, old, n) {
			break
		}
	}
	time.Sleep(f.delay)
	atomic.AddInt32(f.active, -1)
	return f.out, nil
}

func TestTasksRunInParallelAndKeepOrder(t *testing.T) {
	var active, max int32
	factory := func() (Runner, error) {
		return &parallelFakeRunner{out: "same", delay: 80 * time.Millisecond, active: &active, max: &max}, nil
	}
	tl, err := NewTasksTool(factory, 4)
	if err != nil {
		t.Fatal(err)
	}
	out, err := tl.(tool.InvokableTool).InvokableRun(context.Background(),
		`{"tasks":[{"description":"first","prompt":"a"},{"description":"second","prompt":"b"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&max) < 2 {
		t.Fatalf("tasks did not overlap; max concurrency = %d\n%s", max, out)
	}
	first := strings.Index(out, "## first")
	second := strings.Index(out, "## second")
	if first < 0 || second < 0 || first > second {
		t.Fatalf("results are not in input order:\n%s", out)
	}
	if strings.Count(out, "same") != 2 {
		t.Fatalf("missing results:\n%s", out)
	}
}

func TestTasksRejectTooMany(t *testing.T) {
	tl, err := NewTasksTool(func() (Runner, error) {
		return &parallelFakeRunner{out: "x", active: new(int32), max: new(int32)}, nil
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tl.(tool.InvokableTool).InvokableRun(context.Background(),
		`{"tasks":[{"prompt":"a"},{"prompt":"b"},{"prompt":"c"}]}`)
	if err == nil || !strings.Contains(err.Error(), "too many tasks") {
		t.Fatalf("err = %v", err)
	}
}

func TestTasksRecordIndividualFailure(t *testing.T) {
	var n int32
	tl, err := NewTasksTool(func() (Runner, error) {
		if atomic.AddInt32(&n, 1) == 1 {
			return &failingRunner{err: context.DeadlineExceeded}, nil
		}
		return &parallelFakeRunner{out: "ok", active: new(int32), max: new(int32)}, nil
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	out, err := tl.(tool.InvokableTool).InvokableRun(context.Background(),
		`{"tasks":[{"description":"bad","prompt":"a"},{"description":"good","prompt":"b"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ERROR: subagent task failed") || !strings.Contains(out, "ok") {
		t.Fatalf("output = %s", out)
	}
}

type failingRunner struct{ err error }

func (f *failingRunner) Run(context.Context, string) (string, error) { return "", f.err }
