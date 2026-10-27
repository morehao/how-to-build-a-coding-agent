# 一个 basic coding agent 应该具备哪些能力，如何架构

> 文档类型：面向「想自己做一个 agent」的外部读者的总纲
> 取材：本仓库根目录 6 个 workshop 文件（2189 行）+ `eino-agent/` 独立 Go module（30 个包，15166 行）
> 写作原则：每个论断尽量指回本仓库的具体文件或实测数据；外部通用做法只作对照，不单独展开
> 最后更新：2026-10-27

---

## 0. 先说结论

**「basic」不是一个功能清单，而是一条深度轴。** 同一个 `coding agent` 这个词，在本仓库里对应两个东西：

| | 根目录 workshop | `eino-agent/` |
| --- | --- | --- |
| 规模 | 6 个 `package main`，各自能 `go run` | 独立 module，`internal/` 下 30 个包 |
| 回合一 | 手写 `for { model → tools }` | Eino `react.Agent` 编译成图 |
| 工具数 | 1 → 6 个（逐步叠加） | 9 个内置 + `task`/`tasks` + MCP |
| 安全边界 | 无（workspace 越界、无审批、无超时） | 围栏 + 审批 + 策略 + 软沙箱 |
| 可观测 | `--verbose` 打 stderr | JSONL trace + 确定性 eval 场景 |
| 测试 | 无 | 44 个测试文件 / 161 个 `Test*`，fake provider 全程离线 |

两者都是「basic」，只是**basic 的深度不同**：前者 basic 到能把循环跑起来，后者 basic 到一个真人敢在真实仓库里用。

所以本文不回答「basic agent 要有哪些功能」，而是回答两个更本质的问题：

1. **每增加一层能力，是为了消灭哪一个具体的失败？**（第 2 节的演进阶梯）
2. **把这些能力放进代码时，边界应该画在哪里，依赖应该怎么流？**（第 3 节的六层架构）

---

## 1. 什么才算「basic」：一个可用的判据

在开工之前，先给出可验证的判据。一个 coding agent 只要满足下面 6 条，就可以称为「basic 但完整」；缺哪条，它就不该被用在真实仓库上。

| 编号 | 判据 | 反例（本仓库里真实存在过） |
| --- | --- | --- |
| B1 | **能读出真实上下文**：不是靠把整个仓库塞进 prompt | 只 `list_files` 全量列举在万行仓库里直接撑爆上下文 |
| B2 | **能安全地写**：写之前能预演、写之后能回滚 | 早期 `edit_tool.go` 直接写盘，改错了只能 `git checkout`，未提交的改动一起丢 |
| B3 | **能执行并看见结果**：跑测试、跑构建、看退出码 | 手写循环里工具错误被吞成普通文本，模型不知道失败 |
| B4 | **有硬上限**：步数、输出、超时、预算任意一项都不能无限 | 手写 `for {}` 只在模型不再返回 tool call 时退出，没有 `MaxStep` |
| B5 | **越权会被挡住，而且失败方向是「关」** | 路径不做 workspace 校验；`bash` 无审批、无超时 |
| B6 | **行为可复现、可度量**：同一输入应产生可断言的轨迹 | 只有 `--verbose` 的 stderr，无法回归 |

这 6 条不是设计品味，而是**在这两个实现上 review 出来的**。拿 review notes 里两个 P0 缺陷对照：REPL 审批被常驻 stdin scanner 抢走，是「输入的单一所有权」没有被当成约束（L0 层的 B 类问题）；ReAct 分支的伪流式（首 token 延迟等于整段回答延迟），是「一个为了兼容 Claude 而读完整条流」的局部正确决定破坏了全局的 UX 承诺。判据的价值在于：只要有一条不成立，症状迟早会在**别的地方**冒出来——上面两个缺陷分别表现为「输入 y 没反应」和「流式不流」，都不是它们看起来所属的那个模块的问题。

---

## 2. 演进阶梯：每加一层，消灭一个具体失败

把 workshop 和 `eino-agent` 放在一起看，会得到一条清晰的 9 级台阶。**关键不是台阶的数量，而是每一级台阶对应一个不能靠 prompt 解决的失败**——这是判断「某能力现在该不该加」的唯一依据。

| 级 | 增加的能力 | 消灭的失败 | 代价 | 本仓库落点 |
| --- | --- | --- | --- | --- |
| L0 | 模型对话（`Anthropic` client） | — | 无 | `chat.go` |
| L1 | 读文件工具 | 模型只能凭想象谈代码 | 每个工具一份 JSON Schema | `read.go` |
| L2 | 目录列举 / 仓库地图 | 反复猜文件名、猜路径 | 输出可能过长 | `list_files.go`、`internal/repomap` |
| L3 | 代码搜索 | 靠读文件「人肉 grep」 | 需要 `rg` 或 fallback | `code_search_tool.go`、`internal/tools/search.go` |
| L4 | 编辑文件 | 只会说、不会改 | **写盘不可逆** → 立刻需要 L6 | `edit_tool.go`、`internal/tools/edit.go` |
| L5 | 执行命令 | 改完不知道对不对 | 任意命令 = 任意副作用 | `bash_tool.go`、`internal/tools/bash.go` |
| L6 | 审批 + 工作区围栏 + 读后写 | 模型越界改文件、执行危险命令 | 每步都要打断人 | `internal/workspace`、`internal/approval`、`internal/filestate` |
| L7 | 上下文压缩 + 会话持久化 + checkpoint | 长任务丢上下文、改坏不能回滚 | 压缩可能压掉关键信息 | `internal/contextmgr`、`internal/session`、`internal/checkpoint` |
| L8 | 子 agent + MCP + Skill + trace/eval | 单上下文塞不下大任务、工具生态不可复用、改好改坏靠感觉 | 并发与上下文隔离的复杂度 | `internal/subagent`、`internal/mcp`、`internal/skill`、`internal/trace`、`internal/eval` |

有三条台阶规律值得单独记住，它们在本仓库里都有明确的代码对应：

**规律一：L4 和 L5 是「阶跃」，不是「渐进」。**
L4（写）和 L5（执行）一出现，agent 就从「只读助手」变成「有副作用的执行者」，风险等级断崖式上升。所以这两级**必须和 L6 同时上线**，否则就是把一个没有刹车的车交付出去。`eino-agent` 的做法是：`internal/tools` 的注册函数一次性把 `approval` / `filestate` / `policy` / `workspace` 全部接进来（`internal/tools/registry.go:47` 的 `Options`），工具本身不提供「不经过审批」的调用路径。

**规律二：L6 的价值不在于「拦住」，而在于「失败方向是关」。**
`internal/approval/approval.go:132` 在非 TTY 时**直接拒绝**而不是默认放行；`read-only` 模式下特权操作一律 deny（`approval.go:120`）；审批载荷超过 256 KiB 时拒绝而不是截断展示后再执行（`approval.go:185-197`）。这三处都是「宁可让 agent 停下来，也不要在人没看清的情况下执行」。判断一个 agent 是否可信，就看它的边界在拿不准时是默认放行还是默认拒绝。

**规律三：L7 之后，上下文管理从「优化」变成「正确性」。**
`internal/contextmgr` 不是简单的滑动窗口：它要保证 tool call 和 tool result 的配对不被切断（否则 provider 直接报错），要在 provider 返回 usage 时校准 token 估算，还要在压缩后让 `update_plan` 写下的工作记忆仍然可达——`internal/contextmgr/contextmgr.go:211` 把被压掉的 tool call 参数换成 `{"truncated":true}`，正是为了保住配对形态。`eino-agent/docs/03-context-and-memory.md` 把这条线的失效模式列成了清单，值得逐条对照。压缩做错不会「慢一点」，而是「模型开始基于错误的历史做决策」。

---

## 3. 架构：六层，依赖只能向下

![coding agent 六层架构](assets/coding-agent-layers.svg)

图之外的文字版本：

| 层 | 职责 | 包含的包 | 这一层唯一该关心的问题 |
| --- | --- | --- | --- |
| L0 入口与配置 | flag/env → 一个 `Config`；stdin 只有一个人读 | `cmd/*`、`config`、`input` | 谁拥有输入？配置从哪来？ |
| L1 编排 | 把一次用户请求驱动成多轮循环，决定何时停 | `agent`、`agentadk`、`ui`、`summary` | 一轮从哪开始、到哪结束？ |
| L2 模型接入 | provider 抽象、重试、token 预算、技能加载 | `model`、`retry`、`budget`、`skill` | 换模型要不改上层；429 怎么办？ |
| L3 上下文 | 历史压缩、工作记忆、会话快照、轨迹记录 | `contextmgr`、`session`、`memory`、`trace` | 送进模型的历史应该长什么样？ |
| L4 工具与扩展 | agent 作用于世界的**唯一出口** | `tools`、`patch`、`repomap`、`mcp`、`subagent` | 工具协议是什么？错误怎么回给模型？ |
| L5 边界 | 围栏、审批、策略、文件状态、沙箱、回滚、评测 | `workspace`、`approval`、`policy`、`filestate`、`sandbox`、`checkpoint`、`eval`、`hooks` | 拿不准的时候，关还是开？ |

### 3.1 三条硬性约束

**约束一：依赖单向。** 我对 30 个包做了 import 统计，除 4 处例外，所有内部依赖都指向更低的层：`agent` 依赖 10 个包（全部是 L2 及以下），`workspace` / `trace` / `ui` / `budget` / `retry` / `patch` / `filestate` / `policy` 都是**零内部依赖的叶子**。叶子越多越好测——这是 `eino-agent` 能在没有 API key 的情况下跑完 44 个测试文件的前提。

依赖方向上有四处刻意保留的例外，都有明确理由，不是顺手写的：

| 例外 | 理由 |
| --- | --- |
| `subagent → agent/agentadk` | 子 agent 复用父 agent 的 runner，而不是重新实现一遍循环 |
| `mcp → approval/policy` | 外部工具进程边界上，**每次调用都要重新过审批**，不能信任一次配置就长期放行 |
| `config → contextmgr` | 只取一个默认常量（`internal/config/config.go:264` 的 `contextmgr.DefaultContextTokens`），无行为耦合 |
| `agentadk → agent` | 复用 prompt 与 callbacks，避免两份 system prompt 漂移 |

**约束二：写操作只有一个出口。** 所有落盘都经过 `internal/workspace.WriteFileAtomic`（`internal/workspace/workspace.go:136`），所有 `apply_patch` 先全量校验再审批再写盘（`internal/patch` 的 `Parse` → `validateHunk` → `Apply`）。这条约束的意义：回滚、读后写检查、敏感文件策略只需要挂在一个点上，而不是逐个工具补。

**约束三：工具的业务失败不是 Go error。** `internal/tools/registry.go` 里的 `wrapTool` 把「文件不存在」「命令被拒」这类错误转成 `ERROR: ...` 字符串**返回给模型**，只有 ctx 取消才真的返回 Go error。原因写在注释里：Eino 的 `ToolsNode` 遇到 Go error 会中断整张图，而模型需要**看见失败并自己换方案**。这是 agent 与普通程序的根本区别之一——错误是给模型的输入，不是给调用栈的异常。

### 3.2 一轮里数据怎么流

```text
user input
  → session history + system prompt（MessageModifier 注入）
  → ReAct: ChatModel
      ├─ tool call → 审批（L5）→ 工具执行（L4）→ 结果 → ChatModel ...
      └─ final answer
  → 流式渲染（L1 ui）
  → 持久化 user + assistant（L3 session）+ 写 trace（L3 trace）
```

`Agent.RunTurn(ctx, history)`（`internal/agent/agent.go:102`，ADK 分支同签名在 `internal/agentadk/adk.go:153`）的边界刻意选在「轮」而不是「步」：中间的 tool 消息留在图内部，调用方只看得到最终 assistant 消息。这个选择让 CLI 可以完全不知道 ReAct 的存在，也让两个 runtime 能共用同一个入口。

---

## 4. 能力矩阵：十项能力 × 何时才值得加

下表是本仓库实测 + review 结论的汇总。「MVP 尺寸」指的是**只做最小可用版本需要多少代码**，用来判断某项能力是否配得上它的复杂度。

| # | 能力 | 解决的问题 | MVP 尺寸 | 什么时候才值得加 | 本仓库 | 深读 |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | 工具协议（Schema + 分发） | 模型如何「要求做事」 | ~150 行 | L1，第一步就要 | ✅ 9 个内置工具 | [02](../eino-agent/docs/02-tools-and-workspace.md) |
| 2 | 工作区围栏 | 模型越界读写 | ~200 行 | 一接文件工具就要 | ✅ 含 symlink 逃逸测试 | [02](../eino-agent/docs/02-tools-and-workspace.md) |
| 3 | 审批分级 | 副作用不可逆 | ~250 行 | L4 上线时**同时**加 | ✅ ask / read-only / yolo | [permissions](../eino-agent/docs/permissions-and-hooks.md) |
| 4 | 读后写 | 覆盖别人刚改的内容 | ~80 行 | 多人协作仓库 | ✅ sha256 + modtime | [02](../eino-agent/docs/02-tools-and-workspace.md) |
| 5 | 原子写 + 回滚 | 改坏了无法恢复 | ~200 行 | 有修改就值得 | ✅ checkpoint + `/undo` | [05 §4](../eino-agent/docs/05-capabilities.md) |
| 6 | 上下文压缩 | 长任务撑爆窗口 | ~300 行 | 单轮超过 ~10 个 tool call | ✅ 确定性 + provider 校准 | [03](../eino-agent/docs/03-context-and-memory.md) |
| 7 | 会话持久化 | 关掉就丢 | ~200 行 | 任务跨会话 | ✅ `--resume` / `/sessions` | [03](../eino-agent/docs/03-context-and-memory.md) |
| 8 | 硬上限（步数/输出/超时） | 无限循环、挂死 | ~50 行 | 一上 `bash` 就要 | ✅ `MaxStep` + `WaitDelay` + 进程组 | [07](../eino-agent/docs/07-failure-gallery.md) |
| 9 | 子 agent | 单上下文塞不下、要并行 | ~400 行 | 有独立子任务且需要隔离 | ✅ 只读 + 独立预算 | [05 §11](../eino-agent/docs/05-capabilities.md) |
| 10 | trace + eval | 改好改坏无证据 | ~300 行 | 想让改动可信 | ✅ JSONL + 7 个场景 | [04](../eino-agent/docs/04-trace-and-eval.md) |

几处需要单独提醒：

- **第 8 项最便宜也最常被漏。** `internal/tools/bash.go:62` 的 `WaitDelay` 与 `internal/tools/proc_unix.go:13` 的 `Setpgid` 加起来不到 10 行，但缺了它们，一个 `sleep 3 &` 就能让 100ms 超时实际跑 3 秒。review notes 里有实测数据（3.01s vs 510ms）。
- **第 9 项（子 agent）的 MVP 远比想象中贵。** 它触及上下文隔离、预算继承、并发审批、错误聚合四个面。`eino-agent` 的选择是**先只做只读子 agent**（`internal/subagent/subagent.go:1` 的注释明说 read-only），把最难的那部分复杂度推迟——这是值得学习的取舍。
- **第 10 项（trace/eval）不应该等到最后做。** 它决定你后面每一次改动的验证成本。`eino-agent/docs/04-trace-and-eval.md` 的核心观点是：eval 场景必须是**确定性**的（fake provider + 固定脚本），否则它只是另一个会飘的测试。

---

## 5. 边界与安全：分层防御，但要说清每层拦不住什么

| 层 | 拦住什么 | **拦不住什么** |
| --- | --- | --- |
| prompt 约束 | 大部分常见的「顺手」越界 | 恶意 prompt injection、模型误解 |
| workspace 围栏 | 相对/绝对路径越界、symlink 逃逸、不存在父目录 | 合法路径里的危险内容（比如删除仓库内所有文件） |
| 审批 | 人可以叫停任何副作用 | 人看漏了（超长命令后半段）、人按下 `always` |
| policy | 敏感文件（`.env` 等）与环境变量泄露 | 别名的密钥文件、命令里自己 `cat` 的路径 |
| sandbox（opt-in） | OS 级文件/网络访问 | 默认关闭；关闭时只剩审批与围栏 |
| checkpoint | 文件级回滚 | bash 副作用、git 操作、目录创建 |

这张表的重点是**右列**。[`sandbox.md`](../eino-agent/docs/sandbox.md) 对 sandbox 这层的定位是「审批之外的第二层，不是默认开启的安全承诺」，并且明确写出连 sandbox 自己也允许 `file-read*`、也允许 fork/exec——因为完全拒绝读会让 `go test` 和编译器无法工作。这是一个值得照抄的写法：**每一层防御都写下自己的残余风险**，而不是一句「已做安全加固」。

一个具体的判据：**安全的实现细节必须能被测试。** 比如「非 TTY 拒绝」有测试、「超过 256 KiB 拒绝」有测试、「symlink 逃逸」有测试。没有被测试覆盖的安全承诺，在第一次重构后就会变成注释。

---

## 6. 非目标：一个能学的项目必须敢于说「不」

这是本文最容易被忽略的一节。架构设计的一半价值在于**明确不做什么**，否则每一层都会被「顺便」的需求撑爆。

设计文档 §2.3 里曾明确写下「不做子 agent / 不做 MCP / 不做 OS 级沙箱」；这三项后来都被实现了。这不是打脸，而是一个真实项目该有的样子：**非目标要随定位更新，但更新必须显式**。对照当前实现，真正站得住的非目标只剩下面这些：

| 非目标 | 来源与理由 |
| --- | --- |
| Web UI / HTTP 服务 / SSE | 设计文档 §2.3：服务化需要鉴权、并发、会话隔离，另立方案 |
| 容器 / 虚拟机级强隔离 | 设计文档 §2.3：强隔离需要 OS 级能力；现在做的是 opt-in 的 `--sandbox`，不是默认承诺 |
| Windows 原生支持 | 设计文档 §2.3：依赖 `bash` 与 `rg`，Windows 走 WSL |
| 自动 commit / push / 开 PR | 设计文档 §2.3：高风险写操作必须由人决定，agent 只改工作区 |
| 跨轮保留完整 tool trace | 设计文档 §2.3：刻意的上下文控制，只保留最终 user/assistant |
| 二进制文件编辑 | 文本假设是所有编辑工具协议的前提，打破它要重设 `edit_file` 契约 |
| 包管理与依赖解析 | 交给 `bash` 调用的现成工具；自己实现没有增量价值 |

「非目标」的写法也有讲究：**每一条都要给出「为什么不做」，而不是「以后再做」**。前者是设计决策，后者是待办事项——混在一起，非目标就失效了。

---

## 7. 如果你要从零做一个，按这个顺序

```text
1. chat.go 级别的循环（L0）
2. read_file + list_files（L1/L2）—— 先只读，跑通「模型要求做事」
3. code_search（L3）
4. 【同时】edit_file + workspace 围栏 + 审批 + 读后写（L4 + L6）
5. 【同时】bash + 超时 + 进程组 + 输出上限（L5 + L8 的硬上限部分）
6. contextmgr + session（L7）—— 当你第一次遇到「长任务丢上下文」
7. trace + eval（L8）—— 从这一刻起，后面每次改动都有证据
8. subagent / MCP / skill（L8）—— 按真实痛点选，不必全上
```

三条经验：

1. **第 4 步是唯一的「不能拆」时刻。** 写和边界必须一起上。这就是为什么 workshop 的 `edit_tool.go` 在真实使用中不安全——它是教学文件，不是产品。
2. **先有 trace，再谈优化。** 没有确定性轨迹，「换个 prompt 好像好一点」就是无法验证的断言。
3. **每加一个能力，问自己第 1 节那 6 条判据里的哪一条被加强了。** 没有对应的判据，就说明这个能力现在不该加。

---

## 8. 与现有文档的关系

本文是入口总纲，不重复各专题细节：

| 想深入 | 读这里 |
| --- | --- |
| 控制流 / 一次 turn 的作业图 | [`eino-agent/docs/01-agent-loop.md`](../eino-agent/docs/01-agent-loop.md) |
| 工具协议与工作区边界 | [`eino-agent/docs/02-tools-and-workspace.md`](../eino-agent/docs/02-tools-and-workspace.md) |
| 上下文压缩与会话记忆 | [`eino-agent/docs/03-context-and-memory.md`](../eino-agent/docs/03-context-and-memory.md) |
| trace 与 eval | [`eino-agent/docs/04-trace-and-eval.md`](../eino-agent/docs/04-trace-and-eval.md) |
| 各能力的实现与边界 | [`05-capabilities.md`](../eino-agent/docs/05-capabilities.md) |
| ReAct vs ADK | [`06-react-vs-adk.md`](../eino-agent/docs/06-react-vs-adk.md) |
| 失败模式与根因 | [`07-failure-gallery.md`](../eino-agent/docs/07-failure-gallery.md) |
| 审批、hooks、MCP、skill | [`permissions-and-hooks.md`](../eino-agent/docs/permissions-and-hooks.md)、[`mcp.md`](../eino-agent/docs/mcp.md)、[`skills.md`](../eino-agent/docs/skills.md)、[`sandbox.md`](../eino-agent/docs/sandbox.md) |
| 完整实现的设计取舍 | [`docs/2026-10-26-eino-code-agent-design.md`](2026-10-26-eino-code-agent-design.md) |
| 实测缺陷与改进顺序 | [`docs/2026-10-26-eino-agent-review-notes.md`](2026-10-26-eino-agent-review-notes.md) |
