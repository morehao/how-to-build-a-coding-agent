package mcp

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// TestMCPHelperProcess is the server side of TestLiveStdioServer. The parent
// re-executes this test binary with GO_WANT_MCP_HELPER=1.
func TestMCPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_HELPER") != "1" {
		return
	}
	s := mcpserver.NewMCPServer("demo", "1.0.0")
	s.AddTool(
		mcpgo.NewTool("echo", mcpgo.WithDescription("echo text"),
			mcpgo.WithString("text", mcpgo.Required(), mcpgo.Description("text to echo"))),
		func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
			text, err := req.RequireString("text")
			if err != nil {
				return nil, err
			}
			return mcpgo.NewToolResultText(text), nil
		},
	)
	if err := mcpserver.ServeStdio(s); err != nil {
		os.Exit(1)
	}
}

func TestLiveStdioServer(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a subprocess")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &FileConfig{Servers: []ServerConfig{{
		Name:       "demo",
		Command:    self,
		Args:       []string{"-test.run=TestMCPHelperProcess"},
		Env:        []string{"GO_WANT_MCP_HELPER=1"},
		TimeoutSec: 10,
	}}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tools, cleanup, err := Start(ctx, cfg, nil, "yolo")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for _, base := range tools {
		info, err := base.Info(ctx)
		if err != nil || info == nil || info.Name != "mcp__demo__echo" {
			continue
		}
		inv, ok := base.(tool.InvokableTool)
		if !ok {
			t.Fatal("mcp tool is not invokable")
		}
		out, err := inv.InvokableRun(ctx, `{"text":"hi"}`)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "hi") {
			t.Fatalf("out = %q", out)
		}
		return
	}
	t.Fatalf("mcp__demo__echo not found in %d tools", len(tools))
}
