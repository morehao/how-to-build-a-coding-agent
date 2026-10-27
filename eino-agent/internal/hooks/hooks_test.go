package hooks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

type fakeTool struct {
	name  string
	calls *int
}

func (f *fakeTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: f.name, Desc: "fake"}, nil
}

func (f *fakeTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	*f.calls++
	return "inner result", nil
}

func loadTestSet(t *testing.T, root, body string) *Set {
	t.Helper()
	path := filepath.Join(root, "hooks.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Load(ws, path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestWrapRunsBeforeAndAfter(t *testing.T) {
	root := t.TempDir()
	calls := 0
	s := loadTestSet(t, root, `{
  "before_tool": [{"command": "printf before >> marker", "tools": ["fake"]}],
  "after_tool": [{"command": "printf after >> marker", "tools": ["fake"]}]
}`)
	wrapped := s.Wrap(context.Background(), []tool.BaseTool{&fakeTool{name: "fake", calls: &calls}})
	out, err := wrapped[0].(tool.InvokableTool).InvokableRun(context.Background(), "{}")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !strings.Contains(out, "inner result") {
		t.Fatalf("calls=%d out=%q", calls, out)
	}
	data, err := os.ReadFile(filepath.Join(root, "marker"))
	if err != nil || string(data) != "beforeafter" {
		t.Fatalf("marker=%q err=%v", string(data), err)
	}
}

func TestBeforeHookBlocksTool(t *testing.T) {
	root := t.TempDir()
	calls := 0
	s := loadTestSet(t, root, `{
  "before_tool": [{"command": "exit 3", "tools": ["fake"]}]
}`)
	wrapped := s.Wrap(context.Background(), []tool.BaseTool{&fakeTool{name: "fake", calls: &calls}})
	out, err := wrapped[0].(tool.InvokableTool).InvokableRun(context.Background(), "{}")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || !strings.HasPrefix(out, "ERROR: before_tool hook blocked") {
		t.Fatalf("calls=%d out=%q", calls, out)
	}
}

func TestHookFiltersOtherTools(t *testing.T) {
	root := t.TempDir()
	calls := 0
	s := loadTestSet(t, root, `{
  "before_tool": [{"command": "exit 3", "tools": ["other"]}]
}`)
	wrapped := s.Wrap(context.Background(), []tool.BaseTool{&fakeTool{name: "fake", calls: &calls}})
	out, err := wrapped[0].(tool.InvokableTool).InvokableRun(context.Background(), "{}")
	if err != nil || calls != 1 || out != "inner result" {
		t.Fatalf("calls=%d out=%q err=%v", calls, out, err)
	}
}

func TestLoadRejectsEmptyTools(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "hooks.json")
	if err := os.WriteFile(path, []byte(`{"before_tool":[{"command":"true"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(ws, path); err == nil || !strings.Contains(err.Error(), "must list tools") {
		t.Fatalf("err = %v", err)
	}
}
