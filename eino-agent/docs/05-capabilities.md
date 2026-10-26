# 05. v2 能力章节

这一章按“问题 → 实现 → 用法 → 边界”组织。每个能力都尽量配一个确定性测试。

## 1. 单 stdin owner + 审批修复

**问题**：REPL 的 `bufio.Scanner` 和审批的 reader 同时读 `os.Stdin`，pipe 场景下输入会被一方抢走，出现“输入 y 没反应”。

**实现**：`internal/input.Source` 后台唯一读 stdin，REPL 和 `approval.Terminal` 共用它；`Close` 不关闭 stdin。

**测试**：`cmd/eino-agent/main_test.go` 用 `io.Pipe` 模拟交互，第一次 `y` 必须生效。

**边界**：仍然假设只有一个消费者；如果未来加并行子 agent 的交互审批，需要明确队列/所有权。

## 2. 读后写

**问题**：模型可以凭记忆编辑一个从未读过的文件，或读到旧内容后覆盖别人刚改的内容。

**实现**：`internal/filestate` 记录 size/modtime/SHA-256；`edit_file`/`apply_patch` 默认强制检查。

**用法**：`--no-read-before-edit` 关闭。

**边界**：不是文件锁；检查与写入之间仍有 TOCTOU 窗口。真正并发写需要锁或版本控制。

## 3. apply_patch

**问题**：`edit_file` 一次只能替换一处，跨文件修改需要多次审批。

**实现**：`internal/patch` 解析 unified diff，支持多文件、多 hunk、新建、删除、`\ No newline`；先全量校验，再审批，再写盘。

**边界**：只支持文本；不支持二进制、rename、mode 变更；上下文必须精确匹配，不做模糊定位。

## 4. checkpoint 与 /undo

**问题**：模型改错了，用户只能 `git checkout`，但未提交的改动会一起丢。

**实现**：写前快照到 `.eino-agent/checkpoints`，`/undo` 恢复最近一次。

**边界**：只覆盖文件工具；bash 副作用、git 操作、目录创建不在范围内。一次多文件 patch 是一个整体。

## 5. repo_map 与 .einoignore

**问题**：`list_files` 在大仓库里仍然很长；模型需要“有哪些文件、哪些符号”而不是完整内容。

**实现**：`internal/repomap` 输出目录、文件大小、Go 顶层 `package`/`func`/`type`；读取 `.einoignore`（gitignore 子集：`*`、`**`、`?`、`!`、前导 `/`、尾随 `/`）。

**用法**：模型调用 `repo_map`；用户在根目录写 `.einoignore`。

**边界**：符号提取是正则，不是 AST；忽略规则不是完整 gitignore；不会索引依赖或生成代码。

## 6. 重试与退避

**问题**：429/5xx/网络抖动会让一次 turn 直接失败。

**实现**：`internal/retry` 对模型创建调用做指数退避；`internal/model/retry.go` 包装 `Generate`/`Stream`。

**边界**：只重试“创建流”阶段；流已经开始后不重放，避免重复文本/工具调用；错误分类是字符串启发式。

**用法**：`--max-retries`、`--retry-base-ms`。

## 7. token 预算

**问题**：长任务可能悄悄烧掉大量 token。

**实现**：`internal/budget` 累计 usage；`usageModel` 在下一次模型调用前检查上限；`/stats` 显示。

**边界**：provider 不返回 usage 时无法精确统计；预算只阻止新调用，不中断正在进行的一轮。

## 8. 敏感文件与环境变量

**问题**：模型好奇 `.env`，或 bash 把 API key 打印出来。

**实现**：`internal/policy` 默认拒绝敏感文件读取/编辑；bash 默认移除 secret-looking 环境变量。

**边界**：不是 DLP；被批准的 bash 仍可读文件。`--allow-sensitive`/`--allow-env` 是显式降级。

## 9. MCP

**问题**：工具生态在 MCP server 里，不想为每个工具写 Go 适配。

**实现**：`internal/mcp` 用 eino-ext `components/tool/mcp` 加载 stdio server；工具名 `mcp__<server>__<tool>`；每次调用走审批（read-only 拒绝，yolo 放行）。

**边界**：MCP server 是独立进程，拥有自己的权限；agent 只能控制是否调用，不能限制 server 内部行为。配置和环境变量要当作可信代码审查。

## 10. Skill（`SKILL.md`）

**问题**：团队流程和领域知识很长，全部塞进 system prompt 会挤占上下文；写成 Go 工具又太重。

**实现**：`internal/skill` 扫描工作区 `skills/*/SKILL.md`，只把 name/description 注入 prompt；模型调用 `skill` 工具时才加载正文。支持 `context: inline` 与 `context: fork`，后者用只读子 agent 执行。详见 [skills.md](skills.md)。

**用法**：`--skills-dir` 改目录，`--no-skills` 关闭。

**边界**：不扫描嵌套目录、不支持热加载、不支持 `fork_with_context`、没有 skill 级工具白名单；`agent`/`model` 字段预留但不生效。Skill 正文是仓库内容，不能覆盖安全规则或获得额外权限。

## 11. 子 agent（task）

**问题**：父 agent 做大型调查时，读文件会把主上下文淹没。

**实现**：`internal/subagent` 构造只读嵌套 ReAct runner，暴露 `task`（单个）和 `tasks`（并行）工具；子 agent 工具集只有 `read_file`/`list_files`/`code_search`/`repo_map`，可额外拿到只读 `skill` 工具；不能再调用 `task`/`tasks`。每个子 agent 有 2 分钟默认超时、64 KiB 报告上限，以及独立的 token 预算（`--subagent-max-tokens`，默认 16000）。`tasks` 一次最多并发 `--subagent-max-tasks`（默认 4）个，每个 task 新建独立 runner，结果按输入顺序合并。

**边界**：独立预算只覆盖子 agent 自己发起的模型调用，不是硬沙箱；父 session 的 `--max-total-tokens` 仍然会看到这些调用并可能先触发。并行 task 共享同一个 workspace，只读工具之间没有写冲突，但如果将来加入写工具，需要额外的锁/隔离。报告被截断后父 agent 需要自己补读关键文件。

## 12. 项目指令 `@import`

**问题**：根 `AGENTS.md` 很长，或子目录有独立规则。

**实现**：`LoadProjectInstructions` 读取第一个 `AGENTS.md`/`AGENT.md`/`CLAUDE.md`，并把单独一行 `@relative/path.md` 递归展开（深度 3，防循环）。

**边界**：只展开显式 import，不自动发现子目录 `AGENTS.md`；总大小 16 KiB。

## 13. 优先级建议

如果继续迭代，建议顺序：

1. 真实 provider 的集成 smoke test（可选 nightly）；
2. trace 的 HTML viewer、按 turn/tool 的瀑布图和 `jq` 配方；
3. skill 的热加载、skill 级工具白名单、ADK `AgentHub`/`fork_with_context`；
4. 更完整的 `.einoignore`/gitignore 语义；
5. 把 `recall` 从关键词匹配升级为可选 embedding 索引（保持无密钥默认路径）。
