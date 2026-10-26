package subagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/model"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

type fakeRunner struct{ prompt string }

func (f *fakeRunner) Run(_ context.Context, prompt string) (string, error) {
	f.prompt = prompt
	return "found it", nil
}

func TestTaskTool(t *testing.T) {
	r := &fakeRunner{}
	taskTool, err := NewTaskTool(r)
	if err != nil {
		t.Fatal(err)
	}
	info, err := taskTool.Info(context.Background())
	if err != nil || info.Name != "task" {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	inv, ok := taskTool.(tool.InvokableTool)
	if !ok {
		t.Fatal("task tool is not invokable")
	}
	out, err := inv.InvokableRun(context.Background(), `{"description":"look","prompt":"find the answer"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "found it") || r.prompt != "find the answer" {
		t.Fatalf("out=%q prompt=%q", out, r.prompt)
	}
}

type errorRunner struct{}

func (errorRunner) Run(context.Context, string) (string, error) {
	return "", context.DeadlineExceeded
}

func TestTaskToolSoftensRunnerError(t *testing.T) {
	taskTool, err := NewTaskTool(errorRunner{})
	if err != nil {
		t.Fatal(err)
	}
	inv := taskTool.(tool.InvokableTool)
	out, err := inv.InvokableRun(context.Background(), `{"description":"x","prompt":"y"}`)
	if err != nil {
		t.Fatalf("task error should be tool output, got %v", err)
	}
	if !strings.HasPrefix(out, "ERROR:") || !strings.Contains(out, "deadline exceeded") {
		t.Fatalf("out = %q", out)
	}
}

func TestNestedRunnerIsReadOnly(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "marker")
	script := &model.FakeScript{Name: "sub", Steps: []model.FakeStep{
		{ToolCalls: []model.FakeToolCall{{ID: "1", Name: "bash", Arguments: `{"command":"touch ` + marker + `"}`}}},
		{Content: "report"},
	}}
	fake, err := model.NewFakeModel(script)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewReActRunner(context.Background(), Config{Workspace: ws, Model: fake})
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Run(context.Background(), "check the repo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "report") {
		t.Fatalf("out = %q", out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("bash ran in subagent: %v", err)
	}
}
