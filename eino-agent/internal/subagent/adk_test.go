package subagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/model"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

func TestADKNestedRunnerIsReadOnly(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "marker")
	script := &model.FakeScript{Name: "adk-sub", Steps: []model.FakeStep{
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
	r, err := NewADKRunner(context.Background(), Config{Workspace: ws, Model: fake, MaxSteps: 6})
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
		t.Fatalf("bash ran in ADK subagent: %v", err)
	}
}

func TestADKNestedRunnerCapsReport(t *testing.T) {
	root := t.TempDir()
	script := &model.FakeScript{Name: "adk-cap", Steps: []model.FakeStep{
		{Content: strings.Repeat("x", 200)},
	}}
	fake, err := model.NewFakeModel(script)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewADKRunner(context.Background(), Config{
		Workspace:      ws,
		Model:          fake,
		MaxResultBytes: 32,
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Run(context.Background(), "summarize")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "truncated") || len(out) > 64 {
		t.Fatalf("out length=%d: %q", len(out), out)
	}
}
