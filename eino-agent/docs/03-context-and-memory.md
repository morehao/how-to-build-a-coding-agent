# 03. 上下文、工作记忆与会话

## 1. 为什么需要压缩

长任务里，历史会同时包含：用户消息、assistant 文本、tool call 参数、tool 结果。一个 `read_file` 可能返回几千 token，一个 `apply_patch` 的 diff 也可能很长。如果不压缩，模型调用会因为上下文超限直接失败。

本项目的压缩分三层：

```text
单轮模型调用前：contextmgr.CompactWithStats
跨轮持久化：session.Trim(--history-bytes)
手动/自动摘要：/compact 或 ADK summarization middleware
```

## 2. 确定性压缩的顺序

`internal/contextmgr` 不调用模型，顺序固定：

1. 保留 system、最近的 3 个 tool 结果和当前 user 消息；
2. 更旧的 tool 结果超过阈值时替换为 `[tool output omitted ...]`；
3. 旧 assistant 的 tool call 参数过大时替换为 `{"truncated":true}`；
4. 仍超预算时，按 user turn 从旧到新丢弃整块；
5. 仍超预算时，截断最大的消息，标注 `[truncated to save context]`。

第 4 步的“整块”很重要：一个 assistant tool call 和它的 tool result 必须一起保留或一起丢弃，否则模型会看到“没有对应调用的工具结果”，很多 provider 会直接报 400。

## 3. token 估算与校准

默认按 `bytes/3` 估算 token。每次 provider 返回 `usage.prompt_tokens` 时，`usageModel` 用实际值校准比例：

```text
ratio = observedPromptTokens / estimatedPromptTokens
```

后续估算乘以这个比例。它不是精确 tokenizer，只是让预算更接近实际。要精确计数需要 provider 的 tokenizer 或 `tiktoken` 类库，成本更高。

`--max-total-tokens` 是另一层：它在 session 级别累计 prompt+completion，达到上限后**下一次模型调用直接失败**，而不是等 provider 报错。`/stats` 显示累计值和当前校准比例。

## 4. 工作记忆：`.eino-agent/plan.md`

`update_plan` 工具整体替换 `.eino-agent/plan.md`，系统提示要求模型在长任务中维护：

```markdown
## Goal
...
## Decisions
...
## Changed files
...
## Commands and results
...
## Open errors
...
## Next steps
...
```

为什么是文件而不是只放在上下文里？

- 上下文压缩可能丢掉细节；
- 文件跨 turn、跨 session 都存在；
- 用户可以直接读、diff、编辑；
- 它不会自动进入 git（`.eino-agent/` 已忽略）。

限制：plan 是模型自己写的，可能过时或错误；它只是工作记忆，不是事实来源。关键结论仍应通过工具重新验证。

## 5. 会话快照

`internal/session` 保存 JSON：

```json
{
  "version": 1,
  "id": "...",
  "workspace": "/path/to/repo",
  "provider": "openai",
  "model": "...",
  "messages": [{"role": "user", "content": "..."}, ...],
  "summary": "..."
}
```

只保存最终的 user/assistant，不保存 ReAct 中间 tool 消息。原因：

- 中间消息格式依赖 provider，恢复时容易不兼容；
- session 的目标是“继续对话”，不是“精确重放图状态”；
- 需要精确重放时应该用 trace，而不是 session。

`--resume` 支持 session id 或 JSON 路径；恢复时会提示工作区是否变化。

## 6. `/compact` 与 ADK 的差异

ReAct 分支的自动压缩是确定性的；`/compact` 才会调用模型生成结构化摘要，替换旧历史并保留最近两条消息。ADK 分支使用 ADK 的 summarization middleware，可以在图内按策略触发摘要。

对比时重点看：

- 摘要失败时是否中断任务；
- 摘要是否保留 tool call/result 配对；
- 摘要后 system prompt 是否仍然在第一位；
- 是否有 token 预算上限。

## 7. 练习

1. 构造一个 20 轮 fake 对话，把 `--context-tokens` 设得很小，观察 trace 中 `model_start.messages` 的变化。
2. 在 `contextmgr` 里加一个统计：本次压缩丢弃了多少个 block，并显示到 UI。
3. 让 `/compact` 失败（例如 fake provider 只返回错误），确认 session 没有被破坏。
