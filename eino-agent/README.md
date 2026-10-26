# Eino Code Agent

一个运行在本地终端里的简易 coding agent，基于字节跳动 [CloudWeGo Eino](https://github.com/cloudwego/eino) 实现。它可以读代码、列目录、搜索、改文件、跑命令，并把会话保存到本地。当前分支提供 **ReAct** 与 **ADK** 两个入口，共用同一套工具、工作区边界、审批和会话。

这个目录是仓库根目录 workshop 代码之外的**独立 Go module**，不与根目录那些单文件示例共享 `package main`。根目录示例仍然按原样保留。

## 两个分支

| 分支 | 实现 | 特点 |
| --- | --- | --- |
| `feat/eino-code-agent` | Eino `flow/agent/react` | 轻量、直接、依赖少 |
| `feat/eino-code-agent-adk` | Eino `adk.ChatModelAgent` + `adk.Runner` | 当前分支；在 ReAct 版基础上增加 ADK 运行时，同时保留 ReAct 命令用于对比 |

ADK 分支从 ReAct 分支创建，因此公共的 config / workspace / tools / session / UI 完全复用，差异集中在 agent 运行时与入口命令。

## 快速开始

要求 Go 1.24.2+。`ripgrep` 可选；没有 `rg` 时 `code_search` 会退回 Go 自带的目录遍历。

```bash
cd eino-agent
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

# 火山方舟（Ark 的 OpenAI 兼容端点，model 填 endpoint id）
export ARK_API_KEY=...
./eino-agent-adk --provider ark --model <endpoint-id> --workspace /path/to/repo
```

把 `./eino-agent-adk` 换成 `./eino-agent` 即为 ReAct 版本；两者参数完全一致。可以直接运行：

```bash
go run ./cmd/eino-agent-adk --provider openai --model gpt-4o-mini --workspace .
```

ADK 版和 ReAct 版共用下面的参数、工具、权限与会话说明。

单次执行：

```bash
./eino-agent -p "读一下 go.mod，用三句话总结依赖" --workspace .
```

交互模式常用命令：

```text
/help      查看命令
/clear     清空当前会话历史和摘要
/compact   把历史压缩成结构化摘要
/plan      查看持久化工作记忆
/session   显示 session id 和快照路径
/exit      退出
```

## 参数

| 参数 | 说明 | 默认 |
| --- | --- | --- |
| `--provider` | `auto` / `openai` / `claude` / `ark` | `auto` |
| `--model` | 模型名；Ark 填 endpoint id | 必填，或读 provider 对应环境变量 |
| `--base-url` | OpenAI 兼容或 Anthropic 代理地址 | provider 默认 |
| `--workspace` | 工作区根目录 | 当前目录 |
| `--max-steps` | ReAct 图最大步数 / ADK 最大迭代轮数 | `30` |
| `--history-bytes` | 持久化历史预算（字节，近似值） | `262144` |
| `--context-tokens` | 单次模型调用的近似 token 预算；超出时压缩输入 | `32000` |
| `--permission-mode` | `ask` / `read-only` / `yolo` | `ask` |
| `--session-dir` | 会话快照目录 | 用户配置目录下 `eino-agent/sessions` |
| `--resume` | 按 session id 或 JSON 路径恢复 | 无 |
| `--no-stream` | 关闭流式输出，改用 `Generate` | 流式 |
| `--verbose` | 在 stderr 打印诊断信息 | 关闭 |
| `-p, --prompt` | 单次 prompt，执行后退出 | 无 |
| `--version` | 打印版本 | — |

退出码：`0` 成功，`1` 运行时错误，`2` 配置/用法错误，`130` 被 SIGINT 中断。

`--provider auto` 只有在 `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` / `ARK_API_KEY` **恰好设置了一个**时才会自动选择；多个同时存在时必须显式传 `--provider`。

## 工具

模型只看到六个工具，全部通过 Eino `utils.InferTool` 从 Go struct 生成 JSON Schema：

| 工具 | 行为 | 关键限制 |
| --- | --- | --- |
| `read_file` | 带行号读取 UTF-8 文本 | 默认 400 行，最多 2000 行；>10 MiB 文件必须给 `limit` |
| `list_files` | 列目录树，跳过 `.git`、`node_modules`、`dist` 等 | 深度默认 2、最大 5；默认 500 条、最大 2000 条 |
| `edit_file` | 唯一子串替换；`old_str` 为空时创建新文件 | 只替换一次；写入采用临时文件 + rename，保留权限 |
| `bash` | 在工作区根目录执行 `bash -c` | 默认 120s、最大 600s；输出头尾各保留约 128 KiB |
| `code_search` | 正则搜索，优先 `rg`，否则 Go walker | 默认 100 条、最大 500 条；单文件 `rg` 最多 100 条 |
| `update_plan` | 创建/替换 `.eino-agent/plan.md` 工作记忆 | 最大 32 KiB；内容整体替换 |

工具的业务错误（文件不存在、`old_str` 不唯一、只读模式禁用、用户拒绝命令）会以 `ERROR: ...` **作为工具输出返回给模型**，不会直接中断 ReAct 图；模型可以据此换一种做法。只有 context 取消这类错误才会终止本轮。

所有工具输出还有 256 KiB 的硬上限，超出后截断并标注。

## 工作记忆

长任务可以把目标、决策、改动文件、命令结果、未解决错误和下一步写入 `.eino-agent/plan.md`：

- 模型通过 `update_plan` 工具更新；
- 每次新的一轮会把它作为背景注入，不依赖对话历史；
- `/plan` 可以直接查看当前内容；
- 文件放在工作区内，方便用户检查；它不会自动加入 Git，建议按需加入 `.gitignore`。

这层记忆和上下文压缩互补：压缩可能丢细节，但 plan 文件会保留关键进度。

## 上下文管理

两个入口都先把会话历史控制在 `--history-bytes` 以内，再对**单次模型调用**做上下文控制；区别在于单轮策略：

### ReAct 分支：确定性压缩

`MessageModifier` 在每次 ChatModel 调用前执行 `internal/contextmgr`：

1. 保留 system prompt、最近的 3 个 tool 结果和当前 user 消息；
2. 更旧的 tool 结果替换成 `[tool output omitted ...]` 占位符，旧的 tool call 参数也会压缩；
3. 仍然超预算时，按 user turn 从旧到新丢弃整块，保证 tool call / tool result 不被拆散；
4. 仍然超预算时，截断最大的消息，并标注 `[truncated to save context]`。

这是**确定性**压缩，不额外调用模型，没有摘要成本，也不会因为摘要失败而中断任务。只有显式 `/compact` 才会产生一次摘要调用。token 数默认按 `bytes/3` 估算；每次模型响应带 `prompt_tokens` 时，`usageModel` 会用 provider 的 usage 校准估算比例，后续更接近实际值。默认 32000 对多数模型是安全值，模型上下文更大时可以调高。

### ADK 分支：摘要 + 确定性兜底

ADK 入口使用 Eino ADK 自带的 `summarization` middleware：

- 当估算 token 超过 `--context-tokens` 的 **2/3** 时，调用同一个模型生成摘要；摘要使用 `internal/summary` 的结构化模板，固定保留目标、决策、改动文件、命令结果、错误和下一步；
- 摘要后仍然超预算，或摘要没有触发时，`GenModelInput` 再用 `internal/contextmgr` 做硬上限压缩，保证模型输入不超过 `--context-tokens`；
- 摘要本身会产生一次额外模型调用；如果摘要调用失败，会打印警告并退回确定性压缩，而不是让本轮直接失败。

### 共同点与限制

- 跨轮持久化由 `session.Trim` 处理：`--history-bytes` 是保存到 session 的历史总量，优先丢掉最旧的消息，并保证第一条是 user；
- token 数默认是保守估算（约 `bytes/3`）；每次模型响应带 `prompt_tokens` 时，`usageModel` 会用 provider 的 usage 校准估算比例，后续更接近实际值。它不是离线 tokenizer，默认 32000 对多数模型是安全值；
- `/compact` 在两个入口都可用：调用模型生成结构化摘要，替换旧历史并保留最近两条消息；摘要会写入 session；
- 被省略/截断的内容会在 prompt 里提示模型：需要细节就带更小的 `limit` / `max_results` 重新调用工具。

## 权限与安全

工作区边界由 `internal/workspace` 统一收口：

- 相对路径、绝对路径都会先做 `filepath.Rel` 检查；
- 对已存在的路径用 `EvalSymlinks` 解析，对不存在的文件解析其最近的已存在父目录，防止通过 symlink 逃逸；
- 列表和搜索会跳过 `.git`、`node_modules`、`.venv`、`dist`、`build`、`target`、`.cache`、`.dsh/tmp` 等目录；
- 文件写入使用同目录临时文件 + `rename`，避免写一半留下损坏文件。

权限模式：

| 模式 | `read_file` / `list_files` / `code_search` | `edit_file` | `bash` |
| --- | --- | --- | --- |
| `ask` | 允许 | 每次询问 | 每次询问 |
| `read-only` | 允许 | 拒绝 | 拒绝 |
| `yolo` | 允许 | 直接执行 | 直接执行 |

`ask` 模式在非 TTY（管道、CI）下会**同时拒绝** `edit_file` 和 `bash`，不会卡住等待输入；用户需显式使用 `--permission-mode yolo`。

`edit_file` 的审批会展示路径、变更摘要和 unified diff，选项：

```text
[y] 本次应用
[a] 本进程内始终允许这个文件
[N] 拒绝
```

`bash` 的审批选项：

```text
[y] 本次允许
[a] 本进程内始终允许这条完全相同的命令
[N] 拒绝
```

审批只覆盖 `bash` 和 `edit_file`；`read_file` / `list_files` / `code_search` 不弹窗，`update_plan` 只写 agent 自有的 `.eino-agent/plan.md`。

工作区隔离和权限门禁不是操作系统级沙箱。真正执行不可信代码时，仍建议在容器或独立用户下运行。

## 会话

- 每轮成功结束后保存一次，只保存最终的 user / assistant 消息，不保存 ReAct 中间的 tool 消息；
- JSON 格式，`version: 1`，原子写入，权限 `0600`；
- 默认目录：`os.UserConfigDir()/eino-agent/sessions`；
- `--resume <id>` 在 session 目录中查找，`--resume <path.json>` 直接读文件；
- 恢复时会提示 session 记录的工作区与当前工作区是否不同；
- 历史按近似字节数裁剪，优先丢弃最旧的消息，并保证第一条是 user；
- `/compact` 会把结构化摘要写入可选 `summary` 字段，并保留最近两条消息；恢复会话时摘要会作为 background 注入。

## 架构

```text
cmd/eino-agent/main.go          ReAct 入口
cmd/eino-agent-adk/main.go      ADK 入口
        │
        ├── internal/config      flag + env → Config
        ├── internal/model       openai / claude / ark 适配器 + usage 采集
        ├── internal/workspace   路径解析与工作区边界
        ├── internal/tools       6 个工具 + 错误软化 + 输出上限
        ├── internal/memory      .eino-agent/plan.md 工作记忆
        ├── internal/summary     结构化摘要（/compact）
        ├── internal/approval    bash / edit 人工审批
        ├── internal/agent       Eino ReAct + system prompt + callbacks
        ├── internal/agentadk    Eino ADK ChatModelAgent + Runner + 事件渲染
        ├── internal/contextmgr  token 估算/校准 + 确定性上下文压缩
        ├── internal/session     JSON 快照 + 结构化摘要
        └── internal/ui          终端渲染
```

ReAct 路径的一轮对话：

```text
user input
  → session history + system prompt (MessageModifier)
  → ReAct: ChatModel
      ├─ tool call → ToolsNode (sequential) → tool result → ChatModel ...
      └─ final answer
  → stream/generate to terminal
  → persist user + assistant
```

ADK 路径的一轮对话：

```text
user input
  → session history + Instruction (ADK ChatModelAgent)
  → adk.Runner.Run（summarization middleware 可按预算摘要）
  → AgentEvent 流
      ├─ assistant 流式事件 → 终端
      ├─ tool result 事件  → 终端
      └─ 最后一个 assistant 消息 → 返回值
  → persist user + assistant
```

几个实现要点：

- 两个入口都是 `MaxStep / MaxIterations = 30`，工具串行执行，未知工具通过 `UnknownToolsHandler` 返回可用工具列表；
- ReAct 流式模式使用自定义 `StreamToolCallChecker`，会一直读到出现 tool call 或 EOF，兼容 Claude 这类先输出文本再发 tool call 的 provider；
- ADK 模式通过 `adk.NewRunner` 消费 `AgentEvent`，流式 assistant 事件逐块打印，tool 事件打印结果；`Runner` 必须消费到迭代器结束，否则事件流不完整；
- system prompt 在 ReAct 中通过 `MessageModifier` 注入，在 ADK 中通过 `ChatModelAgentConfig.Instruction` 注入；
- 自动读取工作区根目录第一个 `AGENTS.md` / `AGENT.md` / `CLAUDE.md`，最多 16 KiB，作为项目级补充说明；
- ReAct 工具生命周期用 Eino callbacks 渲染；ADK 直接用事件流渲染，避免重复。

## 开发与测试

```bash
cd eino-agent
go build ./...
go vet ./...
go test ./...
```

测试不需要 API key：

- `internal/agent` 用一个脚本化的 fake `ToolCallingChatModel` 驱动**真实 ReAct 图 + 真实工具**，覆盖 generate / stream、工具调用、工具错误恢复、上下文压缩；
- `internal/agentadk` 用同一类 fake model 驱动**真实 ADK Runner + 真实工具**，覆盖 generate / stream、工具调用、工具错误恢复、摘要触发；
- `internal/contextmgr` 覆盖 token 估算、provider usage 校准、旧 tool 结果占位、旧 turn 丢弃、tool call 配对和超大消息截断；
- `internal/summary` 覆盖结构化摘要 prompt 和空输出错误；`internal/memory` 覆盖 plan 读写与注入；
- `internal/tools` 覆盖读、写、列目录、bash 审批/超时/截断、搜索 fallback；
- `internal/approval` 覆盖 bash/edit 审批、always 记忆、read-only 和非 TTY 拒绝；
- `internal/workspace` 覆盖路径逃逸和 symlink 逃逸；
- `internal/session` 覆盖保存、恢复、裁剪；
- `internal/config` 覆盖 flag/env 优先级和错误输入。

## 已知限制

- 这是教学/演示向的简化实现，不是 Claude Code / Codex 的完整替代品；
- 没有 MCP、子 agent、多模态输入、undo 栈、仓库索引；
- ReAct 分支自动压缩是确定性的，只有 `/compact` 才调用模型做结构化摘要；ADK 分支使用 ADK 的 summarization middleware；
- `bash` 超时依赖 `exec.CommandContext`，被杀的是 shell 进程本身，极端情况下子进程可能残留；
- 没有 provider 真实网络集成测试；需要 API key 才能做端到端验证；
- ADK 版目前是单 agent 线性事件流，没有使用 ADK 的 multi-agent、checkpoint、interrupt/resume 能力；这些是后续可扩展点。

## 设计文档

完整设计见仓库根目录：

```text
.dsh/docs/specs/2026-10-26-eino-code-agent-design.md
```
