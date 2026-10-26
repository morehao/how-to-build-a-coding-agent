# 01. 从 chat 到 agent：一次 turn 的控制流

## 1. 最小差异：模型可以“要求做事”

普通 chat 的循环是“用户消息 → 模型文本”。agent 多了一个分支：模型可以返回 tool call，运行时执行工具，把结果作为 `tool` 消息再喂回模型，直到模型给出最终文本。

```text
user
  │
  ▼
┌─────────────┐    no tool call    ┌─────────────┐
│  ChatModel  │ ─────────────────► │ final answer│
└─────────────┘                    └─────────────┘
  │ tool call
  ▼
┌─────────────┐
│  ToolsNode  │  execute sequentially
└─────────────┘
  │ tool result
  └──────────────► ChatModel (next step)
```

`flow/agent/react` 把这套循环编译成一张 Eino graph。`internal/agent/agent.go` 只做四件事：

- 把工具注册进 `ToolsNodeConfig`，并设置 `ExecuteSequentially: true`；
- 用 `MessageModifier` 在每次模型调用前注入 system prompt、压缩历史；
- 设置 `StreamToolCallChecker`，解决“provider 先输出文本、后输出 tool call”的流式判断；
- 用 `MaxStep` 给循环设上限，避免模型无限调用工具。

## 2. `RunTurn`：一轮的边界

`Agent.RunTurn(ctx, history)` 接收完整历史，返回最终 assistant 消息。它不把中间 tool 消息暴露给调用方；ReAct 的状态在 graph 内部。调用方（CLI）只负责：

```text
session history + user input
  → RunTurn
  → final assistant
  → 保存 user/assistant 到 session
```

这样做的代价是：中间过程只能通过 callbacks/trace 观察；好处是 CLI 不需要理解 ReAct 状态机。

## 3. 流式模式的关键坑

Eino 默认的 `StreamToolCallChecker` 只看第一个非空 chunk 是否带 tool call。OpenAI 通常把 tool call 放在前面，但 Claude 可能先输出一段文本，再输出 tool call。如果仍用默认 checker，运行时会认为“没有工具调用”，提前结束 turn，模型看起来“只说了半句话”。

本项目的 `allChunksToolCallChecker` 会一直读到：

- 发现任意 chunk 带 `ToolCalls` → 返回 `true`；
- EOF → 返回 `false`；
- 读流出错 → 返回错误。

它必须 `defer sr.Close()`，否则上游 goroutine 可能泄漏。这个 checker 是 provider 兼容性代码，不是业务逻辑；改它之前先用 fake provider 的“文本 chunk + 末尾 tool call”脚本写回归测试。

## 4. 工具错误不是异常

工具的业务错误（文件不存在、`old_str` 不唯一、只读模式、用户拒绝）会变成字符串：

```text
ERROR: old_str not found in a.txt
```

它作为 tool 结果回到模型，模型可以换一种做法。只有上下文取消、模型流错误这类“运行时错误”才会让 `RunTurn` 返回 error。

这带来两个设计要求：

1. 工具错误必须**可读、可行动**，最好告诉模型下一步怎么做；
2. UI 仍要把 `ERROR:` 显示成失败（`✗`），不能因为字符串不是 Go error 就显示 `✓`。`internal/agent/callbacks.go` 里的 `toolResultError` 就是做这个转换的。

## 5. 步数上限的语义

`--max-steps` 传给 `react.AgentConfig.MaxStep`，限制的是 **graph 运行步数**（模型节点 + 工具节点等），不是“模型调用次数”。一个包含多次工具调用的 turn 会消耗多步。把它设得太小会在任务中途得到类似：

```text
max steps exceeded
```

这不是模型错误，而是运行时保护。eval 场景 `max_steps.json` 用 5 次 `list_files` 和 `max_steps: 4` 固定了这个行为。

## 6. 读 trace 验证控制流

```bash
go run ./cmd/eino-agent --provider fake \
  --script testdata/scenarios/read_edit.json \
  --workspace /tmp/eino-demo \
  --trace /tmp/eino-demo/trace.jsonl --no-stream
```

trace 里一次成功 turn 的最小序列：

```text
turn_start
model_start          messages=[system,user] tools=[read_file,...]
model_end            message.tool_calls=[read_file]
tool_start           tool=read_file
tool_end             tool=read_file
model_start          messages=[system,user,assistant(tool_call),tool]
model_end            message.tool_calls=[edit_file]
tool_start           tool=edit_file
tool_end             tool=edit_file
model_start
model_end            message.content="done"
turn_end             final="done"
```

读 trace 时优先看三件事：

1. `model_start.tools` 是否包含你期望的工具（漏注册会变成 `unknown tool`）；
2. `tool_start` 和 `tool_end` 是否成对（缺失通常意味着 panic/取消）；
3. `turn_end.error` 是否为空，`final` 是否符合预期。

## 7. 练习

1. 给 `read_file` 加一个 `start_line`/`end_line` 参数，并同步更新系统提示和 eval。
2. 构造一个 fake script：第一个 step 输出文本 chunk，第二个 step 才输出 tool call，观察流式 checker。
3. 把 `MaxStep` 调到 2，运行 `read_edit.json`，解释 trace 停在哪一步。
