# 权限规则与 tool hooks

审批解决的是“这一次要不要放行”，权限规则解决“这类动作以后能不能自动放行/必须拒绝”，hooks 解决“工具调用前后要跑什么项目命令”。三者是不同层，不要混成一个布尔开关。

## 1. 权限规则

默认读取工作区下的 `.eino-agent/permissions.json`；不存在就等同于没有规则。用 `--permission-rules` 换路径，用 `--no-permission-rules` 关闭。

```json
{
  "allow": [
    {"tool": "bash", "command": "go test ./..."},
    {"tool": "edit_file", "path": "docs/*.md"},
    {"tool": "apply_patch", "path_prefix": "internal/"}
  ],
  "deny": [
    {"tool": "bash", "command_prefix": "rm -rf"},
    {"tool": "mcp__shell__run", "args_prefix": "rm "}
  ]
}
```

字段：

| 字段 | 含义 |
| --- | --- |
| `tool` | 必填；工具名，`*` 表示所有工具 |
| `command` | bash 命令精确匹配 |
| `command_prefix` | bash 命令前缀匹配 |
| `path` | 单文件精确匹配或 `path.Match` glob（如 `docs/*.md`） |
| `path_prefix` | 路径前缀 |
| `args_prefix` | MCP 等工具原始 JSON 参数的前缀；用于保守拦截 |

语义：

- **deny 优先**，而且 deny 在 `yolo` 模式下仍然生效；`read-only` 本来就会拒绝所有特权动作。
- allow 只跳过“弹窗”，不会把 `read-only` 变成可写；allow 在非 TTY 环境也生效，适合 CI/脚本。
- 一次 `apply_patch` 命中多个路径时，allow 必须覆盖**所有**路径；deny 命中任意一个路径就拒绝。
- 规则是字面匹配，不执行 shell、不做变量展开、不用正则。`path.Match` 的 `*` 不跨 `/`；需要递归目录时用 `path_prefix`。
- 规则文件损坏会直接启动失败，而不是静默放宽；这是 fail-closed。

示例：只允许测试和 docs 编辑。

```json
{
  "allow": [
    {"tool": "bash", "command": "go test ./..."},
    {"tool": "bash", "command": "go vet ./..."},
    {"tool": "edit_file", "path": "docs/*.md"}
  ],
  "deny": [
    {"tool": "bash", "command_prefix": "git push"},
    {"tool": "bash", "command_prefix": "curl "}
  ]
}
```

边界：规则文件本身是仓库内容。一个不受信任的仓库可以写一个很宽的 allow；启动前应 review。规则只覆盖审批入口，不能限制 MCP server 自己做什么。

## 2. Tool hooks

Hooks 是**显式 opt-in** 的：只有传 `--hooks <file>`（或 `EINO_AGENT_HOOKS`）才会加载。默认不执行，避免 clone 一个仓库就运行任意 shell。

```json
{
  "before_tool": [
    {"command": "gofmt -w .", "tools": ["edit_file", "apply_patch"], "timeout_seconds": 30}
  ],
  "after_tool": [
    {"command": "go test ./internal/...", "tools": ["edit_file", "apply_patch"], "timeout_seconds": 120}
  ]
}
```

- `tools` 必填；`["*"]` 表示所有工具，写具体工具名更安全。
- `timeout_seconds` 默认 30，最大 300。
- hook 的工作目录是 workspace root；环境变量经过 `policy.SanitizedEnv`，API key 等敏感变量会被移除。
- 标准输入是一行 JSON：`{"event","tool","args","result","error"}`；stdout/stderr 会附在工具输出里（上限 64 KiB）。
- `before_tool` 非零退出会**阻止**这次工具调用，模型收到 `ERROR: before_tool hook blocked ...`，可以换方案。
- `after_tool` 非零退出不会撤销已经发生的写操作，只在结果后追加 `[after_tool hook failed: ...]`；不要把“回滚”寄托在 after hook 上。

### 安全边界

Hooks 在工具内部、审批之前/之后运行：

```text
before hook → tool.InvokableRun (approval → action) → after hook
```

因此 before hook 可能先于审批执行 shell。它适合“格式化、生成、检查”这类可信仓库脚本，不适合处理不可信输入。需要强隔离时，应该在容器/独立用户里运行整个 agent，而不是只靠 hook。

## 3. 测试

```bash
go test ./internal/approval -run TestRules -v
go test ./internal/hooks -v
go test ./cmd/eino-agent -run 'TestPermissionRules|TestHooks' -v
```

测试覆盖 deny 优先、allow 非 TTY、路径 glob/多路径、hook 过滤/阻断/超时边界和 CLI 接线。
