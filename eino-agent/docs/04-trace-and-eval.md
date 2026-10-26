# 04. Trace 与 Eval：把 agent 行为变成证据

## 1. 为什么先做 trace

Agent 的失败往往不是“代码抛异常”，而是“行为不对”：模型没读文件就改、工具调用顺序错、上下文被截断、审批被绕过。日志文本难以稳定断言，所以本项目输出 JSONL trace：

```bash
eino-agent --trace .eino-agent/trace.jsonl ...
```

每行一个 JSON 对象，字段稳定，便于 `jq`、测试和教学分析。

## 2. 事件类型

| type | 何时写 | 关键字段 |
| --- | --- | --- |
| `turn_start` | 一次 `RunTurn` 开始 | `turn`, `input` |
| `model_start` | 每次模型调用前 | `messages`, `tools` |
| `model_end` | 模型返回（含流式结束） | `message`, `usage`, `duration_ms` |
| `model_error` | 模型调用失败 | `error`, `duration_ms` |
| `tool_start` | 工具开始 | `tool`, `args` |
| `tool_end` | 工具结束 | `tool`, `result`, `duration_ms`, `error` |
| `turn_end` | 一次 turn 结束 | `final`, `usage`, `error`, `duration_ms` |

公共字段：`ts`（RFC3339Nano）、`seq`（单调递增）、`turn`、`type`。

示例：

```json
{"ts":"2026-10-26T12:00:00.000000+08:00","seq":1,"turn":1,"type":"model_start","messages":[{"role":"system","content":"..."},{"role":"user","content":"edit a.txt"}],"tools":["read_file","edit_file"]}
{"ts":"...","seq":2,"turn":1,"type":"tool_start","tool":"read_file","args":"{\"path\":\"a.txt\"}"}
{"ts":"...","seq":3,"turn":1,"type":"tool_end","tool":"read_file","result":"..."}
{"ts":"...","seq":4,"turn":1,"type":"turn_end","final":"done","usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}
```

## 3. 脱敏与截断

- 环境变量名包含 `KEY`/`TOKEN`/`SECRET`/`PASSWORD` 且值长度 ≥ 8 的，会在 trace 中被替换为 `[redacted]`；
- 字符串字段截断到 16 KiB，`tool_end.result` 同样受限；
- 但 trace 仍可能包含源码、用户输入、文件内容。不要把 trace 上传到不可信位置；生产环境应再做结构化脱敏。

## 3.1 聚合与 `/trace`

`internal/trace.Summarize` 读取 JSONL，聚合 turn/model/tool 次数、prompt/completion/total token、模型/工具耗时和错误。REPL 内：

```text
/trace
trace: events=12 turns=1 model=3 tool=2 tool_errors=0 tokens=1234 (prompt=1100 completion=134) model_ms=3400 tool_ms=120
```

没有 `--trace` 时命令会提示。这个聚合不依赖 provider，适合在失败后快速判断“是模型调用太多、工具报错，还是 token 爆了”；细粒度定位仍然看原始 JSONL。

## 4. Fake provider：确定性回放

`--provider fake --script <json>` 用一个 JSON 脚本模拟模型：

```json
{
  "name": "read-edit",
  "steps": [
    {"tool_calls": [{"name": "read_file", "arguments": "{\"path\":\"hello.txt\"}"}]},
    {"tool_calls": [{"name": "edit_file", "arguments": "{\"path\":\"hello.txt\",\"old_str\":\"hello\",\"new_str\":\"goodbye\"}"}]},
    {"content": "done"}
  ]
}
```

支持字段：

- `content`、`reasoning`；
- `tool_calls`（id/name/arguments）；
- `usage`（prompt/completion/total）；
- `delay_ms`；
- `error`（模拟 provider 错误）；
- `chunks`（流式分片，默认每 24 rune 一片）。

脚本耗尽后报错 `fake script "..." exhausted after N step(s)`。这比“让模型随机发挥”更适合回归测试，也方便在 CI 里录教学 trace。

## 5. Eval 场景格式

`testdata/scenarios/*.json`：

```json
{
  "name": "read_then_edit",
  "files": {"hello.txt": "hello\n"},
  "script": {"name": "read-edit", "steps": [...]},
  "input": "change hello to goodbye",
  "permission_mode": "yolo",
  "max_steps": 12,
  "expect": {
    "final_contains": ["done"],
    "tools": ["read_file", "edit_file"],
    "tool_counts": {"edit_file": 1},
    "forbidden_tools": ["bash"],
    "files": {"hello.txt": "goodbye\n"},
    "files_contain": {"hello.txt": "goodbye"},
    "expect_error": false,
    "error_contains": "",
    "trace_contains": ["tool_start"],
    "trace_not_contains": ["bash"],
    "min_model_calls": 2,
    "max_model_calls": 5,
    "min_tool_calls": 1,
    "max_tool_calls": 3
  }
}
```

`internal/eval.Run` 会：

1. 创建临时工作区、写入 fixtures；
2. 用 fake provider 构造 runner；
3. 跑一轮 `RunTurn`；
4. 从 trace 中提取 `tool_start` 顺序；
5. 聚合 trace 的 model/tool 次数、token、错误；
6. 检查 final、工具顺序/次数、禁止工具、文件内容、error、trace 片段和调用上限。

注意：它**只跑一轮**，不模拟多轮用户对话；也不验证真实 provider 的网络行为。这是有意的取舍：CI 要稳定、快、无密钥。

## 6. 运行

```bash
cd eino-agent
go test ./internal/agent -run TestEvalScenarios -v
go test ./internal/agent -run 'TestEvalScenarios/read_then_edit' -v
```

失败时输出会包含 `FAIL` 行、错误列表和完整 trace，便于直接定位。

## 7. 如何加一个场景

1. 在 `testdata/scenarios/` 新建 JSON；
2. 在 `files` 放最小 fixture；
3. 在 `script` 里只写必要步骤（不要依赖模型“自由发挥”）；
4. 在 `expect` 里断言可观察结果，而不是实现细节；
5. 跑测试，失败时先读 trace 的 `model_start.tools` 和 `tool_start` 顺序。

## 8. Eval 的局限

- fake provider 不会产生真实的模型推理、格式漂移和 provider 错误；
- 场景只覆盖“一轮内的工具使用”，不覆盖审批交互、TTY、信号处理；
- 文件断言是精确匹配，适合小 fixture，不适合大仓库；
- trace 断言依赖事件名稳定；改动 trace schema 时要同步所有测试和文档；
- `min/max_model_calls` 在 ADK 分支上不可用（ADK trace 没有 `model_end`），共享场景只应断言 tool/turn 事件。

## 9. 练习

1. 加一个 `apply_patch` 的失败场景：上下文不匹配，断言文件未变、final 包含错误提示。
2. 加一个 `task` 场景：父 agent 调 `task`，子 agent 只读；断言父 trace 里出现 `task`，且没有 `edit_file`。
3. 用 `jq` 统计一次真实运行的 tool 调用次数和总 token。
