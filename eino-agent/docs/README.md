# Eino Code Agent 教学文档

这套文档面向“想通过读一个能跑的 agent 学习 agent 工程”的读者。它不追求覆盖所有 API，而是围绕三条主线：

1. **控制流**：一次用户请求如何变成多轮 ReAct 循环，什么时候停，出错后如何恢复。
2. **边界**：路径、审批、读后写、密钥、子 agent 权限、MCP 进程边界。
3. **可观测性**：如何用 trace 和 eval 把“感觉它变好了”变成可复现的证据。

## 阅读路径

| 顺序 | 文档 | 读完后你应该能回答 |
| --- | --- | --- |
| 1 | [01-agent-loop.md](01-agent-loop.md) | ReAct 图如何驱动 tool call？一次 turn 的 trace 长什么样？ |
| 2 | [02-tools-and-workspace.md](02-tools-and-workspace.md) | 工具协议、工作区 jail、审批、apply_patch、checkpoint 如何配合？ |
| 3 | [03-context-and-memory.md](03-context-and-memory.md) | 上下文超了怎么办？plan 和 session 各保存什么？ |
| 4 | [04-trace-and-eval.md](04-trace-and-eval.md) | 如何写一个确定性 eval？trace 字段怎么读？ |
| 5 | [05-capabilities.md](05-capabilities.md) | v2 的每个能力解决什么问题、边界在哪？ |
| 6 | [skills.md](skills.md) | `SKILL.md` 如何做渐进式披露？inline/fork 的边界是什么？ |
| 7 | [permissions-and-hooks.md](permissions-and-hooks.md) | allow/deny 规则与 tool hooks 如何分层？谁能绕过审批？ |
| 8 | [sandbox.md](sandbox.md) | OS 级 sandbox 限制什么、不限制什么？失败了怎么办？ |
| 9 | [06-react-vs-adk.md](06-react-vs-adk.md) | 两个分支共享什么、差异在哪、何时用哪个？ |
| 10 | [07-failure-gallery.md](07-failure-gallery.md) | 常见失败模式的症状、根因、修复和回归测试。 |
| 11 | [mcp.md](mcp.md) | 如何接入一个 stdio MCP server，为什么每次调用还要审批？ |

## 先跑起来

```bash
cd eino-agent
go test ./...                       # 不需要 API key
go test ./internal/agent -run TestEvalScenarios -v
go test ./internal/agentadk -run TestADKEvalScenarios -v

go build -o eino-agent ./cmd/eino-agent
go build -o eino-agent-adk ./cmd/eino-agent-adk

# 两个入口共用 fake script 和 eval 场景
./eino-agent --provider fake \
  --script testdata/scenarios/read_edit.json \
  --workspace /tmp/eino-demo --no-stream
./eino-agent-adk --provider fake \
  --script testdata/scenarios/read_edit.json \
  --workspace /tmp/eino-demo --no-stream
```

fake provider 不访问网络：它按 JSON 脚本产生 assistant 消息和 tool call。适合读代码、写回归测试和录制教学 trace。

体验 skill：

```bash
mkdir -p skills/demo
cat > skills/demo/SKILL.md <<'EOF'
---
name: demo
description: A smallest skill for trying progressive disclosure.
---
Read `go.mod` and report the module name.
EOF
./eino-agent --provider fake --script testdata/scenarios/read_edit.json   --workspace . --no-stream -p "use the demo skill"
```

## 分支说明

- `feat/eino-code-agent`：Eino `flow/agent/react` 实现，是这两个分支里更小、更直接的一版。
- `feat/eino-code-agent-adk`：在共享层之上换成 `adk.ChatModelAgent` + `adk.Runner`，用于对比 ADK 的 middleware、checkpoint、interrupt 等抽象。

公共的 workspace / tools / approval / trace / eval / fake provider 尽量保持同一套契约；运行时差异见 [06-react-vs-adk.md](06-react-vs-adk.md)。

## 一个重要的学习习惯

读每个能力时，问三个问题：

1. 它把哪一个**隐式假设**变成了显式检查？（例如“模型会先读再改”）
2. 它在什么情况下**失败关闭**？（例如审批 payload 超过 256 KiB 直接拒绝）
3. 用什么**确定性测试**证明它没有退化？（fake script + trace 断言）
