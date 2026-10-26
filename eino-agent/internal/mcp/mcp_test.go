package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/approval"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/config"
)

func TestLoadValidatesServers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	good := `{"servers":[{"name":"demo","command":"echo","args":["hi"],"timeout_seconds":2}]}`
	if err := os.WriteFile(path, []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil || len(cfg.Servers) != 1 || cfg.Servers[0].TimeoutSec != 2 {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
	bad := `{"servers":[{"name":"demo","command":"a"},{"name":"demo","command":"b"}]}`
	if err := os.WriteFile(path, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("err=%v", err)
	}
}

type stubTool struct {
	name   string
	called bool
}

func (s *stubTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: s.name, Desc: "echo args"}, nil
}
func (s *stubTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	s.called = true
	return "ok", nil
}

type allowApprover struct{ calls int }

func (a *allowApprover) Approve(context.Context, approval.Request) (approval.Decision, error) {
	a.calls++
	return approval.Once, nil
}

func TestGatedToolNameAndApproval(t *testing.T) {
	stub := &stubTool{name: "echo"}
	ap := &allowApprover{}
	g := &gatedTool{inner: stub, invoke: stub, server: "demo", approver: ap, mode: config.PermissionAsk}
	info, err := g.Info(context.Background())
	if err != nil || info.Name != "mcp__demo__echo" {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	if _, err := g.InvokableRun(context.Background(), `{"x":1}`); err != nil {
		t.Fatal(err)
	}
	if !stub.called || ap.calls != 1 {
		t.Fatalf("called=%v approvals=%d", stub.called, ap.calls)
	}
}

func TestGatedToolReadOnlyDenied(t *testing.T) {
	stub := &stubTool{name: "echo"}
	g := &gatedTool{inner: stub, invoke: stub, server: "demo", mode: config.PermissionReadOnly}
	if _, err := g.InvokableRun(context.Background(), "{}"); err == nil {
		t.Fatal("expected read-only denial")
	}
	if stub.called {
		t.Fatal("tool should not run")
	}
}
