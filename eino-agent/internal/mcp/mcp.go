// Package mcp loads tools from local MCP servers over stdio and gates each
// call through the same approval gate as bash/edit. MCP is a process boundary:
// the server is a separate executable, not part of the agent process.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	mcpp "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/approval"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/config"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/policy"
)

// FileConfig is the JSON format accepted by --mcp-config.
type FileConfig struct {
	Servers []ServerConfig `json:"servers"`
}

// ServerConfig describes one stdio MCP server.
type ServerConfig struct {
	Name       string   `json:"name"`
	Command    string   `json:"command"`
	Args       []string `json:"args,omitempty"`
	Env        []string `json:"env,omitempty"`
	Tools      []string `json:"tools,omitempty"`
	TimeoutSec int      `json:"timeout_seconds,omitempty"`
}

// Load reads and validates the config file.
func Load(path string) (*FileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read mcp config %s: %w", path, err)
	}
	var cfg FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse mcp config %s: %w", path, err)
	}
	if len(cfg.Servers) == 0 {
		return nil, errors.New("mcp config has no servers")
	}
	seen := map[string]struct{}{}
	for i := range cfg.Servers {
		s := &cfg.Servers[i]
		s.Name = strings.TrimSpace(s.Name)
		s.Command = strings.TrimSpace(s.Command)
		if s.Name == "" || s.Command == "" {
			return nil, fmt.Errorf("mcp server %d: name and command are required", i)
		}
		if _, ok := seen[s.Name]; ok {
			return nil, fmt.Errorf("duplicate mcp server name %q", s.Name)
		}
		seen[s.Name] = struct{}{}
		if s.TimeoutSec <= 0 {
			s.TimeoutSec = 15
		}
	}
	return &cfg, nil
}

// Start launches all configured servers and returns their tools. The returned
// cleanup closes every client and is safe to call once.
func Start(ctx context.Context, cfg *FileConfig, approver approval.Approver, mode string) ([]tool.BaseTool, func() error, error) {
	if cfg == nil {
		return nil, func() error { return nil }, nil
	}
	var (
		clients []*client.Client
		tools   []tool.BaseTool
		once    sync.Once
	)
	cleanup := func() error {
		var errs []string
		once.Do(func() {
			for _, c := range clients {
				if err := c.Close(); err != nil {
					errs = append(errs, err.Error())
				}
			}
		})
		if len(errs) > 0 {
			return errors.New(strings.Join(errs, "; "))
		}
		return nil
	}

	for _, srv := range cfg.Servers {
		timeout := time.Duration(srv.TimeoutSec) * time.Second
		startCtx, cancel := context.WithTimeout(ctx, timeout)
		env := append(policy.SanitizedEnv(os.Environ(), false), srv.Env...)
		cli, err := client.NewStdioMCPClient(srv.Command, env, srv.Args...)
		if err != nil {
			cancel()
			_ = cleanup()
			return nil, cleanup, fmt.Errorf("mcp server %q: start %s: %w", srv.Name, srv.Command, err)
		}
		clients = append(clients, cli)
		initReq := mcpgo.InitializeRequest{}
		initReq.Params.ProtocolVersion = mcpgo.LATEST_PROTOCOL_VERSION
		initReq.Params.ClientInfo = mcpgo.Implementation{Name: "eino-agent", Version: config.Version}
		if _, err := cli.Initialize(startCtx, initReq); err != nil {
			cancel()
			_ = cleanup()
			return nil, cleanup, fmt.Errorf("mcp server %q: initialize: %w", srv.Name, err)
		}
		cancel()

		loaded, err := mcpp.GetTools(ctx, &mcpp.Config{Cli: cli, ToolNameList: srv.Tools})
		if err != nil {
			_ = cleanup()
			return nil, cleanup, fmt.Errorf("mcp server %q: list tools: %w", srv.Name, err)
		}
		for _, base := range loaded {
			invoke, ok := base.(tool.InvokableTool)
			if !ok {
				_ = cleanup()
				return nil, cleanup, fmt.Errorf("mcp server %q: tool is not invokable", srv.Name)
			}
			tools = append(tools, &gatedTool{
				inner:    base,
				invoke:   invoke,
				server:   srv.Name,
				approver: approver,
				mode:     mode,
			})
		}
	}
	return tools, cleanup, nil
}

type gatedTool struct {
	inner    tool.BaseTool
	invoke   tool.InvokableTool
	server   string
	approver approval.Approver
	mode     string
}

func (g *gatedTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	info, err := g.inner.Info(ctx)
	if err != nil || info == nil {
		return info, err
	}
	clone := *info
	clone.Name = "mcp__" + sanitizeName(g.server) + "__" + sanitizeName(info.Name)
	clone.Desc = fmt.Sprintf("[MCP:%s] %s", g.server, info.Desc)
	return &clone, nil
}

func (g *gatedTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	if g.mode == config.PermissionReadOnly {
		return "", errors.New("MCP tools are disabled in read-only mode")
	}
	if g.mode != config.PermissionYolo {
		if g.approver == nil {
			return "", errors.New("MCP tool denied: no approver configured")
		}
		info, err := g.Info(ctx)
		name := g.server
		if err == nil && info != nil {
			name = info.Name
		}
		decision, err := g.approver.Approve(ctx, approval.Request{
			Kind: approval.KindMCP,
			Tool: name,
			Args: argumentsInJSON,
		})
		if err != nil {
			return "", fmt.Errorf("MCP approval failed: %w", err)
		}
		if decision == approval.Deny {
			return "", errors.New("MCP tool denied by user")
		}
	}
	return g.invoke.InvokableRun(ctx, argumentsInJSON, opts...)
}

func sanitizeName(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "unnamed"
	}
	return b.String()
}
