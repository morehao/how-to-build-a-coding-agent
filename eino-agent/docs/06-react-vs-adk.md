# 06. ReAct 分支 vs ADK 分支

## 1. 共享层

两个分支共享：

```text
internal/config        flag/env
internal/workspace     路径边界 + .einoignore
internal/tools         read/list/edit/apply_patch/repo_map/bash/search/plan
internal/approval      单 stdin owner + 审批
internal/checkpoint    /undo
internal/filestate     读后写
internal/trace         JSONL
internal/eval          fake provider 场景
internal/model         openai/claude/ark/fake + usage + retry
internal/session       JSON 快照
internal/summary       /compact
internal/ui            终端渲染
```

因此对比运行时差异时，可以先把这些层当作不变量。

## 2. ReAct 分支

```text
adk? no
runtime: flow/agent/react
graph: START → ChatModel → (ToolsNode → ChatModel)* → END
state: Eino graph state
streaming: StreamToolCallChecker
checkpoint: 本项目文件级 checkpoint
interrupt/resume: 无
```

特点：

- 代码少，控制流直观；
- 工具错误通过字符串回给模型；
- 上下文压缩在 `MessageModifier` 里做，确定性、无额外模型调用；
- 想加“图内 middleware”需要自己包 model/tools。

适合：学习 ReAct 基本盘、快速实验工具、读完整控制流。

## 3. ADK 分支

```text
runtime: adk.ChatModelAgent + adk.Runner
state: ADK session/state
middleware: 本分支实际使用 summarization；ADK 生态另有 agentsmd / filesystem / plantask / patchtoolcalls 等
checkpoint: FileStore + RunnerConfig.CheckPointStore
interrupt: ADK interrupt/resume（库能力，CLI 暂未暴露 /resume）
```

特点：

- Runner 负责会话、checkpoint、resume；
- middleware 可以在模型调用前后做摘要、注入 AGENTS.md、文件系统工具等；
- 中断/恢复是框架能力，不需要自己保存图状态；
- 抽象更多，读代码时需要同时看 ADK 源码。

适合：学习“agent runtime 层”如何组织，以及 checkpoint/interrupt 的生产化形态。

## 4. 对照表

| 维度 | ReAct 分支 | ADK 分支 |
| --- | --- | --- |
| 入口 | `agent.New` + `react.NewAgent` | `adk.NewChatModelAgent` + `adk.Runner` |
| 循环 | Eino graph | ADK agent loop |
| 工具 | `compose.ToolsNode` | ADK tools node |
| 历史 | `RunTurn(history)` 显式传入 | Runner session/state |
| 压缩 | `MessageModifier` + contextmgr | summarization middleware |
| checkpoint | 文件级 `.eino-agent/checkpoints` | `FileStore` + `RunnerConfig.CheckPointStore` |
| 中断恢复 | 无 | `Runner.Resume` / `ResumeWithParams`（库能力） |
| 子 agent | 自定义只读 ReAct runner | `subagent.NewADKRunner`，同一个 `task` 工具 |
| 学习曲线 | 低 | 中高 |

## 5. 如何对比学习

建议用同一组 eval 场景分别跑两个分支，比较：

1. 同样的 fake script，工具调用顺序是否一致；
2. context 压缩后，ReAct 的 `model_start.messages` 是否保留 tool call/result 配对（ADK 分支看 `GenModelInput` 的结果）；
3. 中途取消后，ADK 是否能 resume，ReAct 分支行为如何；
4. 加一个 middleware（例如动态注入 AGENTS.md）在两边的成本。

注意：ADK 当前只从事件流翻译 `turn_*`/`tool_*`，没有 ReAct 的 `model_start`/`model_end`；ADK 的 checkpoint 是 Runner 状态，CLI 暂未暴露 `/resume`。共享 eval 断言应只依赖 `tool_start`/`final`/文件结果，避免绑定运行时细节。

## 6. 选择建议

- 想理解 agent 最小闭环：先读 ReAct 分支；
- 想做可恢复、可中断、带 middleware 的运行时：读 ADK 分支；
- 想教别人：两个都保留，用同一套 trace/eval 做对照，效果最好。
