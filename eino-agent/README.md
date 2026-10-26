# Eino Code Agent

一个运行在本地终端里的教学向 coding agent，基于 [CloudWeGo Eino](https://github.com/cloudwego/eino)。它从 ReAct 最小闭环出发，逐步补齐 agent 工程里真正难的部分：工作区边界、审批、读后写、可回滚的文件修改、上下文预算、trace 和确定性 eval。

这个目录是仓库根目录 workshop 示例之外的**独立 Go module**。

## 两个分支

| 分支 | 运行时 | 重点 |
| --- | --- | --- |
| `feat/eino-code-agent` | Eino `flow/agent/react` | 最小、直接，适合先把控制流读透 |
| `feat/eino-code-agent-adk` | Eino ADK `ChatModelAgent` + `Runner` | 对比 middleware、checkpoint、interrupt/resume |

共享层（config/workspace/tools/approval/trace/eval/fake provider）尽量保持同一套契约；差异见 [`docs/06-react-vs-adk.md`](docs/06-react-vs-adk.md)。

## 快速开始

要求 Go 1.24.2+。`ripgrep` 可选；没有 `rg` 时 `code_search` 回退到 Go 遍历。

```bash
cd eino-agent
go test ./...                                  # 不需要 API key

# ReAct 入口
go build -o eino-agent ./cmd/eino-agent

# ADK 入口（当前分支新增，ReAct 入口仍然保留）
go build -o eino-agent-adk ./cmd/eino-agent-adk

# OpenAI 或任意 OpenAI 兼容端点
export OPENAI_API_KEY=sk-...
./eino-agent-adk --provider openai --model gpt-4o-mini --workspace /path/to/repo

# Anthropic 原生接口
export ANTHROPIC_API_KEY=sk-ant-...
./eino-agent-adk --provider claude --model <claude-model> --workspace /path/to/repo

# 火山方舟（model 填 endpoint id）
export ARK_API_KEY=...
./eino-agent-adk --provider ark --model <endpoint-id> --workspace /path/to/repo
```

把 `./eino-agent-adk` 换成 `./eino-agent` 即为 ReAct 版本；两者参数一致，ADK 版额外使用 summarization middleware 和 ADK Runner。

单次执行：

```bash
./eino-agent -p "读一下 go.mod，用三句话总结依赖" --workspace .
```

不联网的可复现运行：

```bash
./eino-agent --provider fake \
  --script testdata/scenarios/read_edit.json \
  --workspace /tmp/eino-demo --no-stream --trace /tmp/eino-demo/trace.jsonl
```

REPL 命令：

```text
/help /clear /compact /plan /session /stats /undo /checkpoints /exit
```

## 参数

| 参数 | 说明 | 默认 |
| --- | --- | --- |
| `--provider` | `auto` / `openai` / `claude` / `ark` / `fake` | `auto` |
| `--model` | 模型名；Ark 填 endpoint id | 必填，或读 provider 环境变量 |
| `--base-url` | OpenAI 兼容或 Anthropic 代理地址 | provider 默认 |
| `--workspace` | 工作区根目录 | 当前目录 |
| `--max-steps` | ReAct 图最大步数 / ADK 最大迭代轮数（不是模型调用次数） | `30` |
| `--history-bytes` | session 持久化历史预算 | `262144` |
| `--context-tokens` | 单次模型调用近似预算 | `32000` |
| `--permission-mode` | `ask` / `read-only` / `yolo` | `ask` |
| `--session-dir` | session 快照目录 | 用户配置目录下 `eino-agent/sessions` |
| `--resume` | 按 id 或 JSON 路径恢复 | 无 |
| `--no-stream` | 关闭流式输出 | 流式 |
| `--verbose` | stderr 诊断 | 关闭 |
| `-p, --prompt` | 单次 prompt | 无 |
| `--version` | 版本 | — |
| `--max-tokens` | Claude 最大 completion tokens | `8192` |
| `--max-total-tokens` | session token 上限，0 不限 | `0` |
| `--max-retries` | 429/5xx/timeout 重试次数 | `2` |
| `--retry-base-ms` | 退避基数 | `500` |
| `--no-checkpoints` | 关闭 `/undo` 快照和 ADK Runner checkpoint | 开启 |
| `--no-read-before-edit` | 关闭读后写检查 | 开启 |
| `--allow-sensitive` | 允许读写 `.env`/密钥/凭据 | 拒绝 |
| `--allow-env` | bash 传入完整环境变量 | 过滤 secret |
| `--trace` | JSONL trace 路径 | 无 |
| `--script` | fake provider 脚本 | 无 |
| `--mcp-config` | MCP server JSON 配置 | 无 |

退出码：`0` 成功，`1` 运行时错误，`2` 配置/用法错误，`130` SIGINT。

`--provider auto` 只在 `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` / `ARK_API_KEY` 恰好一个存在时自动选择；多个同时存在必须显式传 `--provider`。

## 工具

模型看到八个内置工具（`task` 由 CLI 额外注入时是第九个）：

| 工具 | 行为 | 关键限制 |
| --- | --- | --- |
| `read_file` | 带行号读取 UTF-8 文本 | 默认 400 行，最多 2000；>10 MiB 必须给 `limit` |
| `list_files` | 目录树，跳过 VCS/依赖/构建目录和 `.einoignore` | 深度默认 2、最大 5；默认 500 条、最大 2000 |
| `edit_file` | 唯一子串替换；`old_str` 为空时创建 | 只替换一次；默认要求先读；拒绝二进制 |
| `apply_patch` | unified diff，多文件/多 hunk/新建/删除 | 先校验全部 hunk 再写；拒绝二进制 |
| `repo_map` | 目录/文件/Go 顶层符号地图 | 只提取 `package`/`func`/`type`；遵守 `.einoignore` |
| `bash` | 工作区根目录 `bash -c` | 默认 120s、最大 600s；输出头尾各约 128 KiB；超时杀进程组 |
| `code_search` | 正则搜索，优先 `rg`，否则 Go walker | 默认 100 条、最大 500 条；单文件 `rg` 最多 100 |
| `update_plan` | 整体替换 `.eino-agent/plan.md` | 最大 32 KiB |
| `task` | 委派只读子 agent（可选） | 子 agent 无 edit/bash，不能再委派 |

工具的业务错误以 `ERROR: ...` 作为工具输出返回模型，不会直接中断图；UI 会把 `ERROR:` 和非零 `exit code` 显示为失败。所有工具输出有 256 KiB 硬上限。

## 安全边界

工作区由 `internal/workspace` 收口：

- 相对/绝对路径都做 `filepath.Rel` 检查；
- 已存在路径 `EvalSymlinks`，不存在的路径解析最近父目录，防 symlink 逃逸；
- 列表/搜索/`repo_map` 跳过 `.git`、`node_modules`、`.venv`、`dist`、`build`、`target`、`.cache`、`.dsh/tmp`、`.eino-agent`，以及 `.einoignore` 里的规则；
- 写入使用同目录临时文件 + `rename`。

| 模式 | 读/列/搜/repo_map | edit/apply_patch | bash/MCP |
| --- | --- | --- | --- |
| `ask` | 允许 | 每次审批 | 每次审批 |
| `read-only` | 允许 | 拒绝 | 拒绝 |
| `yolo` | 允许 | 直接执行 | 直接执行 |

`ask` 在非 TTY 下对写操作失败关闭，不会挂起。审批 payload 超过 256 KiB 直接拒绝，不截断后“同意”。`edit_file`/`apply_patch` 默认要求先读当前文件，且文件在读后不能被外部修改；`--no-read-before-edit` 可关闭。

敏感文件（`.env`、`id_rsa`、`*.pem`、`credentials.json` 等）默认拒绝读写；`bash` 默认移除名字含 `API_KEY`/`TOKEN`/`SECRET`/`PASSWORD`/`AWS_*` 的环境变量。它们是默认值，不是 DLP：被批准的 bash 仍可读取文件。真正执行不可信代码时，请使用容器/独立用户。

## 回滚

`edit_file` 和 `apply_patch` 在写前把旧内容快照到 `.eino-agent/checkpoints`：

- 已存在文件备份内容/权限；新文件记录 `existed=false`；
- `/undo` 恢复最近一个 checkpoint；
- `/checkpoints` 查看最近记录；
- 一次多文件 patch 是一个整体，undo 会一起回滚。

边界：只覆盖文件工具，不覆盖 bash 副作用、git 操作、目录/权限变更。

## 上下文与记忆

两个入口都先把会话历史控制在 `--history-bytes` 以内，再对**单次模型调用**做上下文控制；区别在单轮策略：

### ReAct 入口：确定性压缩

`MessageModifier` 在每次 ChatModel 调用前执行 `internal/contextmgr`：

1. 保留 system、最近 3 个 tool 结果和当前 user；
2. 旧 tool 结果替换为占位符，旧 tool call 参数压缩；
3. 仍超预算时按 user turn 整块丢弃，保证 tool call/result 配对；
4. 最后才截断最大消息。

这是确定性压缩，不额外调用模型；只有显式 `/compact` 才产生摘要调用。

### ADK 入口：摘要 + 确定性兜底

ADK 入口使用 Eino ADK 的 `summarization` middleware：

- 估算 token 超过 `--context-tokens` 的 2/3 时调用同一个模型生成结构化摘要；
- 摘要后仍超预算时，`GenModelInput` 再用 `internal/contextmgr` 做硬上限压缩；
- 摘要失败会打印警告并退回确定性压缩，不中断本轮。

摘要本身会产生一次额外模型调用；这是与 ReAct 入口最大的成本差异。

### 共同点

- 跨轮持久化由 `session.Trim` 处理，优先丢最旧消息并保证第一条是 user；
- token 默认按 `bytes/3` 估算，provider 返回 usage 时校准；
- `/compact` 两个入口都可用；
- `--max-total-tokens` 在 session 级累计，达到上限后下一次模型调用失败；`/stats` 查看。

## ADK checkpoint 与 interrupt

ADK 入口使用 `internal/agentadk.FileStore` 实现 `adk.CheckPointStore`，默认落在 `.eino-agent/adk-checkpoints`：

- 与 ReAct 的文件级 `/undo` 不同，ADK checkpoint 保存的是 Runner 运行时状态，适合中断后继续；
- checkpoint id 经 SHA-256 映射为文件名，避免路径逃逸；
- `/undo` 仍然只回滚文件，两者语义不同，不要混用；
- ADK Runner 本身提供 `Resume`/`ResumeWithParams`，但当前 CLI 没有暴露 `/resume`；需要时在测试或后续迭代中接入；
- 具体行为以 `internal/agentadk` 和 ADK 版本为准；升级 ADK 时先跑 ADK 单测和共享 eval。

## Trace 与 Eval

```bash
./eino-agent --trace .eino-agent/trace.jsonl ...
jq -c 'select(.type=="tool_start") | {tool,args}' .eino-agent/trace.jsonl
```

JSONL 事件：`turn_start`、`model_start`、`model_end`、`model_error`、`tool_start`、`tool_end`、`turn_end`，公共字段 `ts`/`seq`/`turn`/`type`。ReAct 入口发出全部七类；ADK 入口从 ADK 事件流翻译 `turn_*`/`tool_*`，不伪造模型生命周期事件。字符串截断到 16 KiB；环境变量名含 `KEY`/`TOKEN`/`SECRET`/`PASSWORD` 的值会被替换为 `[redacted]`。

fake provider + eval：

```bash
go test ./internal/agent -run TestEvalScenarios -v
```

场景在 `testdata/scenarios/*.json`，定义 fixtures、fake script、input 和期望（final、工具顺序/次数、禁止工具、文件内容、error）。`internal/eval` 从 trace 提取 `tool_start` 做断言。详细格式见 [`docs/04-trace-and-eval.md`](docs/04-trace-and-eval.md)。

## MCP

```json
{
  "servers": [
    {
      "name": "filesystem",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/dir"],
      "tools": ["read_file", "list_directory"],
      "timeout_seconds": 20
    }
  ]
}
```

```bash
./eino-agent --mcp-config ./mcp.json --permission-mode ask --workspace .
```

工具名变成 `mcp__<server>__<tool>`；每次调用走审批。server 是独立进程，agent 只能控制“是否调用”，不能限制其内部行为；配置和环境变量按可信代码审查。详见 [`docs/mcp.md`](docs/mcp.md)。

## 架构

```text
cmd/eino-agent/main.go          ReAct 入口
cmd/eino-agent-adk/main.go      ADK 入口
        │
        ├── internal/config      flag + env → Config
        ├── internal/model       openai / claude / ark / fake + usage + retry
        ├── internal/workspace   路径边界 + .einoignore
        ├── internal/tools       内置工具 + 错误软化 + 输出上限
        ├── internal/patch       unified diff 解析/应用
        ├── internal/checkpoint  文件快照 + /undo
        ├── internal/filestate   读后写状态
        ├── internal/approval    单 stdin owner + 审批
        ├── internal/input       stdin 单所有者
        ├── internal/trace       JSONL 事件
        ├── internal/eval        fake provider 场景
        ├── internal/repomap     仓库地图
        ├── internal/policy      敏感文件/环境变量
        ├── internal/budget      token 预算
        ├── internal/retry       指数退避
        ├── internal/subagent    只读嵌套 runner + task
        ├── internal/mcp         stdio MCP 工具
        ├── internal/agent       Eino ReAct + prompt + callbacks
        ├── internal/agentadk    Eino ADK ChatModelAgent + Runner + 事件渲染
        ├── internal/contextmgr  确定性压缩
        ├── internal/memory      plan 工作记忆
        ├── internal/summary     /compact
        ├── internal/session     JSON 快照
        └── internal/ui          终端渲染
```

一轮数据流：

```text
user input
  → session history + system prompt (MessageModifier)
  → ReAct: ChatModel
      ├─ tool call → ToolsNode (sequential) → tool result → ChatModel ...
      └─ final answer
  → stream/generate to terminal
  → persist user + assistant
```

实现要点：

- 工具串行执行，未知工具通过 `UnknownToolsHandler` 返回可用工具列表；
- 流式 `StreamToolCallChecker` 读到 tool call 或 EOF，兼容 Claude 这类先文本后 tool call 的 provider；
- system prompt 通过 `MessageModifier` 注入，保证每次模型调用都以 system 开头；
- 自动读取根目录第一个 `AGENTS.md`/`AGENT.md`/`CLAUDE.md`（最多 16 KiB），支持 `@relative/path` 递归 import；
- 工具生命周期用 Eino callbacks 渲染，不在 agent 包里重写 ReAct 循环。

## 开发与测试

```bash
cd eino-agent
go build ./...
go vet ./...
go test ./...
go test -race ./...
```

测试不需要 API key：

- `internal/agent`：fake `ToolCallingChatModel` 驱动真实 ReAct 图 + 真实工具；
- `internal/agent` / `internal/agentadk` eval：`testdata/scenarios` 的确定性场景，两个入口各跑一遍；
- `internal/contextmgr`：token 估算、校准、tool 配对、整块丢弃、截断；
- `internal/tools`：读/写/列/搜索/bash 审批/超时/截断、patch、repo_map、policy；
- `internal/approval`：交互、always 记忆、read-only、非 TTY、超大 payload；
- `internal/checkpoint` / `filestate`：快照/undo、读后写；
- `internal/patch`：多文件/hunk、上下文不匹配、行数校验；
- `internal/mcp`：配置校验、gating、真实 stdio server 集成；
- `internal/agentadk`：ADK trace、最终内容、FileStore checkpoint；
- `internal/trace`：脱敏、截断、JSONL 合法性；
- `internal/model`：fake script、retry、budget；
- `internal/workspace`：路径/symlink 逃逸、`.einoignore`；
- `internal/session`：保存/恢复/裁剪。

## 教学文档

完整学习路径见 [`docs/README.md`](docs/README.md)：

```text
docs/01-agent-loop.md          ReAct 控制流与流式 checker
docs/02-tools-and-workspace.md 工具协议、边界、审批、patch、checkpoint
docs/03-context-and-memory.md  压缩、token、plan、session
docs/04-trace-and-eval.md      trace 字段与 eval 场景
docs/05-capabilities.md        v2 能力逐项拆解
docs/06-react-vs-adk.md        两分支对比
docs/07-failure-gallery.md     失败模式与回归测试
docs/mcp.md                    MCP 接入
```

## 已知限制

- 教学/演示向实现，不是 Claude Code / Codex 的完整替代品；
- 没有多模态输入、持久化向量索引、并行子 agent；
- `repo_map` 的符号提取是正则，不是 AST/语言服务器；
- `.einoignore` 是 gitignore 子集，不是完整实现；
- retry 是字符串启发式，且只重试“创建流”阶段；
- trace 可能包含源码/用户输入，上传前需自行脱敏；
- 没有真实 provider 的网络集成测试；需要 API key 才能做端到端验证；
- ADK 分支的 checkpoint 语义与 ReAct 文件级 undo 不同；ADK 运行时的会话/中断行为以 `internal/agentadk` 为准；
- ADK 的 summarization middleware 会产生额外模型调用，成本高于 ReAct 的确定性压缩。

## 设计文档

```text
../.dsh/docs/specs/2026-10-26-eino-code-agent-design.md
```
