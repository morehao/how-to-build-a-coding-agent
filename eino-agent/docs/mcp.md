# MCP 接入指南

## 1. 它是什么

MCP（Model Context Protocol）把工具放进独立进程，通过 JSON-RPC 暴露工具列表和调用。agent 侧只负责：

1. 启动/连接 server；
2. 拉取 tool schema；
3. 把 tool 暴露给模型；
4. 每次调用前走审批；
5. 关闭进程。

本项目用 stdio transport（`client.NewStdioMCPClient`），适合本地工具。

## 2. 配置

```json
{
  "servers": [
    {
      "name": "filesystem",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/allowed/dir"],
      "env": ["LOG_LEVEL=error"],
      "tools": ["read_file", "list_directory"],
      "timeout_seconds": 20
    }
  ]
}
```

启动：

```bash
eino-agent --mcp-config ./mcp.json --permission-mode ask --workspace .
```

`tools` 为空表示加载 server 暴露的全部工具。工具名会变成：

```text
mcp__<server>__<tool>
```

例如 `mcp__filesystem__read_file`。前缀避免和内置工具重名。

## 3. 审批

| 模式 | 行为 |
| --- | --- |
| `ask` | 每次调用显示 MCP 工具名和参数，询问 y/a/n |
| `read-only` | 直接拒绝 |
| `yolo` | 直接执行 |

MCP 参数超过 256 KiB 会被拒绝审批，和 bash/patch 一样。

## 4. 环境变量

agent 默认只给 server 传“非 secret”的环境变量（复用 bash 的 `policy.SanitizedEnv`），再加上配置里的 `env`。如果 server 真的需要 API key，应显式放进 `env`，并意识到这会把密钥交给该进程。

不要把不信任的 server 配置进 `mcp.json`；它和 `bash` 同级的信任边界。

## 5. 调试

- 启动失败：先用同样的 `command`/`args` 在终端手动运行，确认 server 能启动；
- 工具列表为空：检查 `tools` 过滤、server 的 initialize 能力；
- 调用挂起：调大 `timeout_seconds`，或检查 server 是否等待 stdin；
- 输出异常：MCP 的 content 可能是结构化 JSON，模型需要自己解析；必要时在 server 侧返回纯文本。

## 6. 测试

`internal/mcp/live_test.go` 用一个测试二进制作为 stdio server，验证：

1. `Start` 能启动并 initialize；
2. `GetTools` 能拿到工具；
3. `mcp__demo__echo` 能实际调用并返回结果。

运行：

```bash
go test ./internal/mcp -v
```

## 7. 设计取舍

- 没有做 MCP resources/prompts，只做 tools；
- 没有做 server 热重载；
- 没有做工具名冲突的自动重命名（只加前缀）；
- 没有做 per-server 权限策略（统一走全局审批）。
