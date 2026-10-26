# Eino Agent 两分支 Review：工程缺陷与学习项目缺口

> 日期：2026-10-26
> 范围：`feat/eino-code-agent`（ReAct）与 `feat/eino-code-agent-adk`（ADK）
> 方式：源码阅读 + 两分支构建/测试 + 本地 fake OpenAI SSE 计时 + 双读者 stdin 模拟 + `exec` WaitDelay 实验
> 结论：**实现完成度在“教学样例”里偏高，但还不能算一个“好用的学习项目”**；有 2 个 P0 体验缺陷、3 个 P1 安全/行为缺陷，教学层与评测层基本空白。

---

## 0. 先说做得好的部分

这些不是客套，是这份代码的真实优点，后续建议都建立在这些基础上：

- **独立 module，根目录 workshop 零改动**：`eino-agent/go.mod` 独立，6 个 `main` 不被破坏。
- **分层清楚**：`config / model / workspace / tools / agent / agentadk / contextmgr / session / memory / summary / approval / ui`，职责边界基本没有互相穿透。
- **workspace jail 做得比大多数教学项目认真**：相对/绝对路径、`filepath.Rel`、`EvalSymlinks`、不存在的父目录、symlink 逃逸都有测试（`internal/workspace`）。
- **审批不是摆设**：bash/edit 默认 `ask`，非 TTY 直接拒绝，read-only/yolo 分级，edit 展示 diff，bash 展示 cwd/timeout，always 只在进程内记忆。
- **上下文管理有真实的工程思考**：确定性压缩 + 最近 3 个 tool 结果 + 按 user turn 丢块 + tool call/result 配对 + 超大消息截断 + provider usage 校准 + plan.md 工作记忆。
- **离线测试可跑**：用 scripted fake `ToolCallingModel` 驱动真实的 ReAct 图/ADK Runner，不依赖 API key；核心包覆盖率约 57%–88%。
- **设计文档质量高**：有目标/非目标、备选方案、量化假设、威胁模型、验收矩阵，不是“写个 README 交差”。

所以下面的“缺失”更多是**下一步该补什么**，不是否定已有工作。

---

## 1. 实测结果（可复现）

### 1.1 构建与测试

两分支均：

```bash
cd eino-agent
go build ./...
go vet ./...
go test ./...
```

全部通过。覆盖率（`go test -cover ./...`）：

| 包 | 覆盖率 |
| --- | --- |
| `internal/approval` | 88.0% |
| `internal/contextmgr` | 83.0% |
| `internal/config` | 80.7% |
| `internal/session` | 76.7% |
| `internal/workspace` | 72.0% |
| `internal/summary` | 66.7% |
| `internal/tools` | 64.5% |
| `internal/model` | 63.4% |
| `internal/agent` | 61.2% |
| `internal/agentadk`（ADK 分支） | 80.8% |
| `internal/memory` | 57.1% |
| `cmd/eino-agent` | **0.0%** |
| `cmd/eino-agent-adk` | **0.0%** |
| `internal/ui` | **0.0%** |

> 首次跑 ReAct 的 `config` 测试失败，是因为我把 `GOTMPDIR` 设成了相对路径，测试里对 `t.TempDir()` 的绝对路径做了字符串比较；换成绝对路径后通过。这不是产品 bug，但说明测试对环境变量有一点隐式假设。

### 1.2 ReAct 的“流式”实测是伪流式

用本地 SSE fake server 发 10 个 chunk、每个间隔 300ms，记录 stdout 到达时间：

| 分支 | 首块到达 | 后续 | 结果 |
| --- | --- | --- | --- |
| ReAct | 4.13s | 无 | 10 个 chunk 一次性到达 |
| ADK | 1.71s | 每 ~0.31s 一块 | 逐块到达，4.78s 结束 |

原因不是模型，是 `internal/agent/agent.go:170` 的 `allChunksToolCallChecker`：它把模型流的副本一直读到 EOF 才决定走 `ToolsNode` 还是 `END`；ReAct 的图必须先完成分支判定才能把输出交给下游，所以最终回答也被缓冲到结束才一次性吐出。设计文档 `G3` 写的“流式逐块输出”（`docs/specs/...:72`）在 ReAct 路径上不成立；`README` 参数表写默认“流式”也只是“用了 Stream API”，不是“逐块可见”。ADK 路径没有这个 checker，所以是真流式。

> 这本身是一个很好的教学点：**编排图的形状会决定 UX**。为了兼容 Claude“先文本后 tool call”而读完整条流，代价就是首 token 延迟等于整段回答延迟。项目应该把这个 trade-off 明确写出来，而不是当成已完成的流式能力。

### 1.3 REPL 里审批输入会被 scanner 抢走（CLI 级复现）

`cmd/eino-agent/main.go:176-179` 在 REPL 里起了一个常驻 goroutine：

```go
go func() {
    for scanner.Scan() {
        lines <- scanner.Text()
    }
    scanErr <- scanner.Err()
}()
```

它会在 agent 跑一轮（包括等审批）时继续阻塞读 stdin。与此同时，`approval.New`（`main.go:58`）把同一个 `os.Stdin` 包成另一个 `bufio.Reader`，在 `approval.go:118` 里 `ReadString('\n')`。

用 `os.Pipe` + 两个并发 reader 模拟同样的“scanner 先阻塞、approval 后阻塞”顺序，跑 50 次：

```text
map[scanner:50]
```

50/50 都是 scanner 抢到 `y\n`，然后它阻塞在无缓冲的 `lines <-`（主 goroutine 正在跑 turn，没人接收），审批永远读不到输入。真实终端下调度不保证 100% 这么走，但这是结构性的输入所有权冲突，不是小概率。

进一步用 `cmd/eino-agent` 的临时 CLI 测试直接复现（`run(args, stdin, stdout, stderr, true)` + `io.Pipe` + 本地 fake OpenAI server，测试后已删除）：

```text
firstYApproved=false secondYApproved=true
REPRODUCED: first y was consumed by the scanner; second y reached approval
```

第一次输入 `y` 没有触发编辑，第二次 `y` 才到达审批并创建文件——说明第一行被常驻 scanner 吃掉，且被塞进了后续的 REPL 输入队列。这条路径与真实终端只差 stdin 是否为 pty，双读者结构完全相同。

影响：**默认 `ask` 模式下，交互式 REPL 的 `edit_file` / `bash` 审批很可能直接卡死**；用户看到的 `[approval] ... allow?` 之后输入 `y` 没反应。更糟的是 `approval.Approve` 只在入口检查了一次 `ctx.Err()`（`approval.go:82`），`ReadString` 不感知 context，Ctrl-C 取消 ctx 后读仍在阻塞，REPL 也收不到 `ctx.Done()`（它在等 `RunTurn` 返回），可能连 Ctrl-C 都退不出来。

> 沙箱禁止 `openpty`（`script: openpty: Operation not permitted`），所以没在真实 pty 里复现；但上面的 CLI 测试已经走的是同一个 `run`/`approval.New` 代码路径，双读者结构完全相同。ADK 分支的 `cmd/eino-agent-adk/main.go` 是同一份逻辑，问题同样存在。

### 1.4 bash 的 timeout 不是硬超时

`internal/tools/bash.go:62-70` 只用了 `exec.CommandContext`，没有 `cmd.WaitDelay`，也没有进程组：

```go
cmd := exec.CommandContext(runCtx, "bash", "-c", command)
...
runErr := cmd.Run()
```

实验：100ms 超时 + `bash -c 'sleep 3 &'`：

```text
WaitDelay=0s   elapsed=3.01s
WaitDelay=500ms elapsed=510ms
```

`bash` 自己退出了，但后台子进程继承了 stdout/stderr 管道，`cmd.Wait` 要等管道关闭，于是 100ms 超时实际跑了 3s。如果子进程是 `sleep 100000 &`，就是无限挂起；而且被 `CommandContext` 杀掉的只是 shell，子进程可能继续写文件。

---

## 2. 缺陷清单（按严重度）

### P0（默认路径不可用 / 能力宣称不成立）

| # | 问题 | 证据 | 建议 |
| --- | --- | --- | --- |
| P0-1 | REPL 审批与输入扫描器抢 stdin：第一次 `y` 被 scanner 吞掉，审批无响应；Ctrl-C 也未必能中断 | `cmd/eino-agent/main.go:58,176-179,196`；`internal/approval/approval.go:82,118`；CLI 复现 `firstYApproved=false secondYApproved=true` | 单一 stdin owner：reader goroutine 产出行到 channel，REPL 与 approval 串行消费；approval 用 `select { case <-ctx.Done(): ... case line := <-lines: }`；或审批直接读 `/dev/tty`。把 CLI 复现测试保留为回归测试。 |
| P0-2 | ReAct 的流式是伪流式，首 token 延迟 = 整段回答延迟 | `internal/agent/agent.go:79,170`；实测 4.13s 一次性到达；设计文档 `G3` 的“流式逐块输出” | 按 provider 选 checker：OpenAI 兼容用 Eino 默认 first-chunk；Claude 才用 all-chunks，并在 README 明说会缓冲；或把“要真流式”作为选 ADK 的理由。加一个 SSE 计时测试。 |

### P1（安全门/行为承诺被破坏）

| # | 问题 | 证据 | 建议 |
| --- | --- | --- | --- |
| P1-1 | 审批展示截断，但执行/写入的是完整内容：长命令可把危险部分藏在 500 字节之后；大 diff 只展示 12 KiB | `internal/approval/approval.go:146,150`；`tools/bash.go` 执行完整 command；`tools/edit.go` 应用完整 new_str | 审批必须展示完整 command/diff，或超过阈值时拒绝并要求显式 `yolo`；至少显示“还有 N 字节未展示”。顺手清理 ANSI/控制字符，避免终端转义隐藏内容。 |
| P1-2 | bash 超时不是硬超时，后台子进程可让工具无限挂起 | `internal/tools/bash.go:62-70`；实验 3.01s vs 510ms | `cmd.WaitDelay = 5*time.Second`；Unix 下 `SysProcAttr.Setpgid=true`，超时 kill 整个进程组；补测试 `sleep 3 &`。 |
| P1-3 | CLI/REPL 0% 测试覆盖，且没有 CI；P0-1 这种 bug 本可以被 CLI 测试挡住 | `cmd` 覆盖率 0.0%；仓库无 `.github/`；设计文档 `:605` 声称有 CLI 测试 | 把 `run(args, stdin, stdout, stderr, isTTY)` 做成可测入口（已经有雏形），加 `cmd/eino-agent` 测试：`--help`、缺 key、`-p`、非 TTY 拒绝、`/exit`、`--resume`；CI 跑两模块 build/vet/test/race。 |

### P2（正确性 / UX / 可维护性）

| # | 问题 | 证据 | 建议 |
| --- | --- | --- | --- |
| P2-1 | 工具业务错误以 `ERROR:` 字符串返回，UI 回调不检查前缀，失败也显示绿色 `✓` | `internal/tools/registry.go:114-124`；`internal/agent/callbacks.go:30-35` | `ToolEnd` 检查 `strings.HasPrefix(result, "ERROR:")`，显示 `✗`；或让工具返回结构化 `ToolResult{OK, ErrorCode}`。ADK 分支已经部分检查（会 warn），但仍是 `✓`，统一掉。 |
| P2-2 | 错误字符串绕过 256 KiB 输出上限 | `registry.go:121` 直接返回 `"ERROR: "+err.Error()`，没有 `capOutput` | 对错误文本也 `capOutput`；schema 里给 path/pattern 加长度上限。 |
| P2-3 | 上下文压缩把 tool call 参数 stub 掉，却保留它的 tool result | `internal/contextmgr/contextmgr.go:188,205`：cutoff 用 tool result 下标；紧邻 cutoff 的 assistant tool call 也被 stub | 用 tool call ID 追踪“哪些 result 被 stub”，只 stub 对应 call；或把 cutoff 移到 assistant tool-call 消息之前。临时测试复现：call b 的 args 变成 `{"truncated":true}`，result b 仍保留 1000 字节。 |
| P2-4 | `bash` 的 always 记忆按完整命令 SHA-256，但 UI 只展示前 500 字节；用户按 `a` 等于对没看全的命令长期放行 | `approval.go:146-150,165-174` | 与 P1-1 一起修；对超长命令禁用 `a`。 |
| P2-5 | `code_search` 的 rg 路径与 Go fallback 的忽略规则不一致：rg 依赖 `.gitignore`，fallback 用硬编码 `IsIgnoredRel`；`file_type` 未知时 rg 报错、fallback 搜全部 | `internal/tools/search.go:87-99` vs `:205,246` | 给 rg 显式 `--glob '!node_modules'` 等，或统一走一个 ignore 解析器；未知 file_type 明确报错。 |
| P2-6 | `edit_file` 不检测二进制文件，可能把 NUL 文件当文本替换 | `internal/tools/edit.go:22-75`；`read_file` 有 NUL 检测，edit 没有 | 编辑前做与 `read_file` 一致的二进制检查。 |
| P2-7 | 颜色开关用 stdin 的 TTY 状态，而不是 stdout；stdout 重定向到文件时仍可能输出 ANSI | `cmd/eino-agent/main.go:57` | 分开 `stdinTTY`（审批）和 `stdoutTTY`（颜色）。 |
| P2-8 | `session.Load` 没校验 session 记录的 workspace 是否存在（设计文档要求） | `internal/session/session.go:68-88`；设计文档 `:387` | 加载时 `os.Stat`，不存在给明确警告/错误。 |
| P2-9 | `session.Trim` 注释说“always keeps at least one”，但 `[assistant]` 这一种会被第二个循环清空 | `session.go:177-192` | 修注释或修逻辑；补测试。 |
| P2-10 | Claude 的 `MaxTokens` 固定 8192，没有 flag；长回答可能被截断 | `internal/model/factory.go:42` | `--max-tokens` / provider 配置；默认值写进 README。 |
| P2-11 | `-p` one-shot 默认不落盘（`persist=false`，除非 `--resume`），与 README“每轮成功结束后保存一次”不完全一致 | `cmd/eino-agent/main.go:94` | 要么 `-p` 也保存，要么 README 明确“one-shot 不保存，除非 --resume”。 |
| P2-12 | ADK 分支不打印 usage；`finalContent` 可能取到“工具调用前的旁白” | `internal/agentadk/adk.go:184,199-200` | 与 ReAct 对齐 `a.ui.Usage`；只把无 tool call 的 assistant 消息当最终回答。 |
| P2-13 | `--max-steps` 在两个 runtime 语义不同：ReAct 是图节点步数（≈15 轮），ADK 是迭代轮数（30 轮） | `README` 参数表；设计文档 `:120` | 统一口径或在 README 分别写清楚；对比实验时要固定“轮数”而不是同一个数字。 |

### P3（硬化 / 体验）

- `bash` 继承全部环境变量，`env` 会把 API key 打给模型；`read_file` 可以读 `.env`。建议默认脱敏/拒绝敏感文件，或加 `--allow-env`、`.einoignore`。
- 终端注入：模型生成的 tool args、命令、文件内容直接 `fmt.Fprint` 到终端；恶意内容可用 ANSI 转义隐藏审批信息。审批展示应 sanitize 控制字符。
- `AGENTS.md` / `.eino-agent/plan.md` 是 prompt injection 面；system prompt 有声明，但没有测试/红队用例。
- `eino-agent/.gitignore` 没有 `.eino-agent/`，`update_plan` 生成的文件会污染 `git status`。
- 没有嵌套 `AGENTS.md`（只读 workspace 根目录第一个），大型仓库的项目约定覆盖不到子目录。
- 没有 `--session-list` / `--last` / 删除会话；`/clear` 不删文件。
- 没有 retry/backoff（429/5xx）、没有 max output tokens、没有 cost 预算。
- `go.mod` 把 Claude/Anthropic/AWS/GCP 一堆间接依赖都拉进来；如果学习重点是 ReAct，考虑 build tag 拆 provider 或单独小 module。

---

## 3. 两个分支的对比（以及各自缺什么）

| 维度 | ReAct 分支 | ADK 分支 |
| --- | --- | --- |
| 编排 | `react.NewAgent` + `compose.ToolsNodeConfig` | `adk.NewChatModelAgent` + `adk.NewRunner` |
| 流式 | **伪流式**（checker 读完整条流） | **真流式**（AgentEvent 逐块） |
| 上下文 | 确定性压缩（`MessageModifier`） | summarization middleware + 确定性硬上限（`GenModelInput`） |
| 工具轨迹 | Eino callbacks | AgentEvent（tool result / assistant stream） |
| 代码量 | 少，适合看 ReAct 图 | +约 1k 行，多 middleware/event 概念 |
| 中断/恢复 | 无 | ADK 有 checkpoint 能力，但**没有接入**（没有 interrupt/resume） |
| 子 agent | 无 | ADK 有 DeepAgent/subagent 能力，但**没有接入** |
| CLI | 独立 `main`（约 300 行） | 同一份 `main` 复制，diff 只有 57 行 |
| 工具错误展示 | `✓`（P2-1） | `✓` + warn（仍不是 `✗`） |
| 适合教学 | 看清“模型↔工具”循环 | 看清“运行时/事件/中间件” |

**分支层面的建议**：

1. 把共享 CLI 抽成 `internal/cli` + 一个 `Runner` 接口（`RunTurn(ctx, history) (*schema.Message, error)`），两个 `main` 只负责选 runtime。现在 300 行重复会让修 P0-1 要改两遍，也让“ReAct vs ADK 的差异”被淹没。
2. 如果目标是学习/对比，考虑一个分支 + `--runtime react|adk`，而不是两个长期分支 + merge；否则读者要先 checkout 才能对比，且 ADK 分支会不断 merge 回 ReAct 的修复，历史很绕。
3. ADK 分支既然已经引入 ADK，最值得补的两个能力是 **checkpoint/中断恢复** 和 **subagent**；否则 ADK 只剩“真流式 + 摘要 middleware”，没有发挥它相对 ReAct 的结构性优势。

---

## 4. 作为“学习 agent 开发”的项目，缺什么

这是本文最重要的部分。代码是“一个能跑的实现”，但学习项目还需要“让人看懂、能练、能度量”。

### 4.1 教学层：没有课程，只有成品 + 设计文档

根目录 workshop 是 step-by-step 的（chat → read → list → bash → edit → search），eino-agent 却是“一次性给一个完整工程 + 873 行设计文档”。对学习者来说，缺少从“最小 ReAct”到“完整 agent”的台阶。

建议补一个 7 篇的教程系列（每篇一个可运行 commit/目录）：

1. **最小 ReAct**：一个模型 + 一个 `read_file`，50 行内跑通，观察 `ChatModel → ToolsNode → ChatModel`。
2. **工具与 schema**：`utils.InferTool`、jsonschema tag、工具错误为什么返回字符串而不是 error。
3. **上下文**：token 估算、最近 N 个 tool 结果、按 turn 丢块、tool call/result 配对；用固定 transcript 演示压缩前后。
4. **安全边界**：workspace jail、symlink、approval、非 TTY、read-only/yolo；用“恶意路径/长命令”做练习。
5. **会话与记忆**：session JSON、`--resume`、plan.md、`/compact`；演示压缩后继续任务。
6. **ADK**：同一组工具换成 `ChatModelAgent + Runner`，对比事件模型、流式、summarization、MaxIterations。
7. **评测与观测**：fake model、scenario、trace、成本/延迟；让学习者自己加一个失败用例。

每篇配“要观察什么 / 常见坑 / 练习题”，比堆设计文档更能体现教学价值。

### 4.2 没有 fake provider / 离线 demo

现在要看到 agent loop，必须配 API key；测试里的 fake model 只在 `_test.go` 里，学习者看不到。建议加 `--provider fake`（或 `--script demo.json`）：

- 按脚本返回 tool call / 最终回答，可配置延迟；
- 内置 3 个 demo：read → edit → test、审批拒绝后改方案、上下文压缩后继续；
- 同一套脚本可用于 CI 的 CLI E2E（包括审批输入），一石三鸟。

这是我认为**性价比最高**的一个补充。

### 4.3 没有 trace / 可观测性

现在只有 `--verbose` 的 stderr 和 session 的最终消息，看不到一轮里模型调了什么、工具耗时多少、token 怎么变。建议：

- `--trace trace.jsonl`：记录 turn、model request/response（可脱敏）、tool call/args/result 摘要、usage、耗时；
- 一个 `trace` 子命令或小脚本，把 JSONL 渲染成时间线；
- 用 trace 做 replay/eval，而不是只靠 session。

Eino 的 callbacks 已经能拿到 model/tool 生命周期，接一个 JSONL recorder 成本不高。对学习来说，“看见 loop”比“读文档”有效得多。

### 4.4 没有 eval harness

单元测试覆盖的是组件行为，不是“agent 能不能完成任务”。建议加 `evals/`：

- 场景格式：`input`、`workspace fixture`、`expected tool calls`（顺序/次数）、`expected final contains`、`forbidden tool calls`；
- 用 fake provider 驱动，确定性、无网络；
- 覆盖：正常读改、工具错误恢复、审批拒绝、max steps、unknown tool、malformed args、context overflow、provider 429/stream 中断；
- 输出通过率 + 每场景 token/耗时，作为回归基线。

设计文档 `:605/:633/:657` 已经列了 CLI 测试、MaxStep、unknown tool、损坏 session，但实现里没有；eval harness 可以一并补上。

### 4.5 没有 React vs ADK 的实测对比

两个分支最大的价值就是“同一组工具，两种运行时”。但现在没有一张对比表。建议用同一 fake 脚本跑：

- 首 token 延迟 / 总耗时（本次已测：ReAct 伪流式 vs ADK 真流式）；
- 相同 `max-steps` 下的实际轮数；
- 上下文压缩前后的 token；
- 代码量/概念数/调试难度；
- checkpoint/中断恢复/子 agent 的“有 API 但未接入”。

这比任何主观评价都更有教学价值。

### 4.6 没有“失败画廊”

学习 agent 开发，失败模式比成功路径更重要。建议做一组可复现的失败案例：

- 模型调用不存在的工具；
- 工具参数 JSON 坏了；
- `old_str` 不唯一 / 文件被外部改过；
- 审批被拒绝后模型死循环；
- `MaxStep` 触发；
- provider 401/429/超时/流中断；
- 上下文压缩把关键信息丢了；
- `bash` 超时留下子进程。

每个案例给出：现象、trace、根因、修复/规避。这会是这个仓库最有价值的文档之一。

### 4.7 缺少的 agent 能力（按学习价值排序）

| 能力 | 为什么值得学 | 现状 |
| --- | --- | --- |
| **undo / checkpoint** | 让 agent 改文件从“不可逆”变“可回滚”；也是 ADK checkpoint 的入口 | 无；edit 直接写盘 |
| **apply_patch / 多文件事务** | 真实 coding task 往往是多 hunk；单 unique substring 不够 | 只有 `edit_file` 单点替换 |
| **read-before-edit 强制** | 从 prompt 约束升级为状态机约束；教学上很好讲“有状态工具” | 只有 prompt 要求 |
| **repo map / @file / .gitignore 解析** | 上下文选择是 agent 的核心能力，不只是 grep | 硬编码 ignore，无 repo map |
| **MCP** | 工具生态的标准接入方式；学“工具协议/进程边界” | 无 |
| **subagent / task 工具** | 上下文隔离、并行、角色分工；ADK 的强项 | 无 |
| **plan mode / ask_user** | 人机协作不只“批准命令”，还有“先对齐计划” | 只有 update_plan 记忆 |
| **retry/backoff + 预算** | 真实 provider 会 429/5xx；成本控制是生产化第一课 | 无；usage 只在 verbose |
| **sandbox（容器/OS）** | 审批是“人肉沙箱”，不是隔离 | 无；文档已声明残余风险 |
| **secret redaction / 敏感文件策略** | `read_file .env`、`bash env` 是真实的泄露路径 | 无；只有 prompt 提醒 |
| **parallel tool calls** | 延迟优化 vs 审批确定性的权衡 | 刻意串行，可做对比实验 |
| **config file** | 每项目不同模型/权限/ignore，flag 不够 | 只有 flag/env |

不需要一次全做。对“学习项目”来说，**undo、fake provider、trace、eval、MCP、subagent** 是六个最有代表性的章节。

### 4.8 工程/仓库层面

- **根目录完全没提 eino-agent**：`README.md`、`README.zh-CN.md`、`AGENT.md`、`Makefile` 都没有引用（grep 无结果）。如果这两个分支是学习成果，trunk 上的读者根本发现不了；合并后至少加一节“Eino 版本”+ 分支/目录链接 + `make eino-agent` 目标。
- **没有 CI**：无 `.github/workflows`；建议两个 module 分别 build/vet/test，加 `-race`、`gofmt -l`、覆盖率上传；eino-agent 是嵌套 module，CI 要 `cd eino-agent`。
- **没有 lint 配置**：`golangci-lint` / `staticcheck` 一个都没有；`go vet` 只能覆盖一部分。
- **没有 benchmark / fuzz**：`contextmgr` 压缩、token 估算、search fallback、bash 截断都适合 benchmark；`workspace.Resolve` / diff 适合 fuzz。
- **没有 release/version 注入**：`config.Version` 硬编码 `0.1.0`，可用 `-ldflags`。
- **文档/实现有漂移**：设计文档声称的 CLI 测试、MaxStep 测试、unknown tool 测试、损坏 session 测试都没实现；`session.Load` 的 workspace 校验也没做。建议把“验收矩阵”变成真正的 checklist，或者在文档里标注“未实现”。
- **eino-agent README 只有中文**：根 README 是中英双语，这个子项目可以补英文版，或明确“中文优先”。

---

## 5. 建议的推进顺序

### Phase 0：先让它“能用”（1–2 天）

1. 修 P0-1：单一 stdin owner + context-aware approval；加 CLI 集成测试。
2. 修 P0-2：provider-specific checker；在 README 明确 ReAct 流式会缓冲；或默认引导 Claude 用户用 ADK/`--no-stream`。
3. 修 P1-1：审批展示完整 command/diff（或超限拒绝）。
4. 修 P1-2：`WaitDelay` + 进程组。
5. 修 P1-3：CI + `cmd` 测试。

### Phase 1：让它“可学”（3–5 天）

6. `--provider fake` + 3 个 demo 脚本。
7. `--trace` JSONL + 一个最小 viewer。
8. 教程 7 篇（可以先写 1/2/6/7，安全/上下文后补）。
9. React vs ADK 对比文档 + 实测表。

### Phase 2：让它“可度量”（3–5 天）

10. `evals/` 场景 + fake provider + 通过率/token/耗时基线。
11. benchmark（compaction/search/bash）+ fuzz（path jail/diff）。
12. CI 里跑 `-race` + 覆盖率阈值。

### Phase 3：让它“像现代 agent”（按兴趣选）

13. undo/checkpoint（编辑前快照 + `/undo`）。
14. apply_patch / 多文件事务。
15. repo map / `.einoignore` / 嵌套 AGENTS.md。
16. MCP 客户端。
17. ADK subagent + interrupt/resume。
18. config file + 成本预算 + retry/backoff。
19. secret redaction + 容器 sandbox。

---

## 6. 附：本次验证的边界

- 真实 pty 被沙箱禁止（`openpty: Operation not permitted`），所以 P0-1 不是用真实终端复现的；但已在 `cmd/eino-agent` 的同一 `run` 代码路径上用 `io.Pipe` + `isTTY=true` + fake OpenAI server 直接复现（第一次 `y` 被 scanner 吃掉，第二次才审批）。真实终端下建议再补一次手工验证。
- 流式计时用本地 fake SSE server，不依赖真实 provider；真实网络的首包延迟会不同，但“ReAct 缓冲 vs ADK 逐块”的结构性差异与 provider 无关。
- `WaitDelay` 实验用独立 Go 程序复现 `exec` 语义，不是直接调用 `tools.bash`；但 `tools.bash` 用的正是同一模式，结论一致。
- contextmgr 配对问题用临时 `_test.go` 复现后已删除，`git status` 干净。
