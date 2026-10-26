# 07. 失败画廊

每条包含：症状、根因、修复、回归测试。失败是 agent 工程最好的入口。

## 1. 输入 `y` 没反应

**症状**：REPL 里审批提示出现，输入 `y` 后命令仍不执行，或输入被 REPL 吃掉。

**根因**：REPL 和审批各自持有 stdin reader，竞争同一个 pipe。

**修复**：`internal/input.Source` 单 owner；REPL 和审批共用；`Close` 不关闭 stdin。

**测试**：`TestFakeProviderApprovalAndTrace` 用 `io.Pipe`，第一次 `y` 必须生效。

## 2. Claude 流式“只说了半句话”

**症状**：模型先输出文本，再发 tool call，但 agent 提前结束。

**根因**：Eino 默认 `StreamToolCallChecker` 只看第一个 chunk。

**修复**：`allChunksToolCallChecker` 读到 tool call 或 EOF。

**测试**：`internal/agent/agent_test.go` 的 checker 测试；fake provider 的流式分片测试。

## 3. bash 超时后子进程残留

**症状**：`sleep 100 &` 超时返回，但后台进程还在。

**根因**：`exec.CommandContext` 只杀 shell，不杀进程组。

**修复**：`Setpgid` + 超时杀 `-pid` 进程组 + `WaitDelay`。

**测试**：`TestBashTimeoutKillsProcessGroup`。

**边界**：Windows 走单独实现；极端 daemon 化进程仍可能逃逸。

## 4. 审批 payload 太大

**症状**：模型生成 1 MB 的 patch，审批界面滚动很久，用户看不清就同意。

**根因**：没有上限，或截断后仍允许执行。

**修复**：超过 256 KiB 直接拒绝，提示拆分或用 yolo。

**测试**：`TestApprovalRefusesOversizedPayload`。

## 5. 没读就改 / 读后外部又改

**症状**：模型用旧内容覆盖新文件；或编辑一个从未读过的文件。

**根因**：没有把“读”与“写”建立状态关联。

**修复**：`filestate` 记录 size/modtime/hash；写前检查；写后更新。

**测试**：`TestEditRequiresReadWhenEnforced`、`TestApplyPatchReadBeforeEdit`。

## 6. patch 上下文不匹配

**症状**：apply_patch 失败，但文件已经被改了一半。

**根因**：边解析边写。

**修复**：先解析、校验所有 hunk，全部成功后才审批/写盘。

**测试**：`TestApplyPatchRejectsMismatchWithoutWriting`。

## 7. 上下文截断拆散 tool 配对

**症状**：provider 报 400 `tool message without preceding tool call`。

**根因**：按消息条数截断，没按 turn/block 截断。

**修复**：`splitBlocks` 按 user turn 分块；整块丢弃；保留最近 tool 结果配对。

**测试**：`TestCompactPreservesToolCallPairing`。

## 8. 工具失败却显示 ✓

**症状**：`ERROR: ...` 或 `exit code: 1` 在终端显示成功。

**根因**：工具错误是字符串，UI 只根据 Go error 判断。

**修复**：`toolResultError` 识别 `ERROR:` 和非零 exit code，传给 UI/trace。

**测试**：可用 callbacks 单测或 CLI trace 验证 `tool_end.error`。

## 9. 二进制文件被 edit

**症状**：`edit_file` 在 PNG/二进制上替换字节，文件损坏。

**根因**：只检查了 UTF-8 读取，没检查写路径。

**修复**：编辑前检查 NUL；新建内容含 NUL 也拒绝。

**测试**：`TestEditRejectsBinary`。

## 10. 重定向输出仍有颜色

**症状**：`eino-agent > log.txt` 里出现 ANSI 转义。

**根因**：只用 stdin 判断 TTY。

**修复**：stdout 也必须是终端才启用颜色。

**测试**：CLI 测试使用 buffer，输出应无 ANSI。

## 11. MCP server 被当成“可信工具”

**症状**：接入一个 MCP server 后，它读取了任意文件/执行了命令，agent 没有拦住。

**根因**：MCP 是独立进程，agent 只能审批“是否调用”，不能约束进程内部。

**修复**：每次调用审批；read-only 模式拒绝；配置和环境变量按可信代码审查；不要给 server 不需要的密钥。

**测试**：`TestGatedToolReadOnlyDenied`、live stdio test。

## 12. 子 agent 递归爆炸

**症状**：`task` 调用 `task`，无限递归/成本失控。

**根因**：子 agent 工具集包含 `task`。

**修复**：嵌套 runner 只暴露读工具；测试断言子 agent 调 `bash` 得到 `unknown tool`。

**测试**：`TestNestedRunnerIsReadOnly`。

## 13. token 预算没有生效

**症状**：`--max-total-tokens` 设了，但仍继续调用模型。

**根因**：只在 turn 结束时统计，没有在下一次调用前检查。

**修复**：`usageModel` 在 `Generate`/`Stream` 前 `budget.Check()`。

**测试**：`TestUsageModelBudgetStopsNextCall`。

## 14. fake script 提前耗尽

**症状**：eval 报 `fake script "..." exhausted`。

**根因**：脚本步骤少于实际模型调用次数（工具调用后还会再调一次模型）。

**修复**：每个工具调用后补一个最终 `content` step；或增加步骤。

**测试**：这是测试失败提示，不是产品 bug；保留清晰错误信息即可。

## 15. skill fork 递归/越权

**症状**：只读子 agent 加载一个 `context: fork` 的 skill 后，又启动了一层子 agent；或者 skill 试图借 fork 获得写权限。

**根因**：父 agent 和子 agent 共享了同一个 `*skill.Tool`，父 agent 的 runner 被带进了子 agent；或者把 edit/bash 工具误传给了 fork runner。

**修复**：给子 agent 传 `WithoutRunner()` 的副本，fork 在嵌套层直接返回 `ERROR`；fork runner 仍然只暴露 `read_file`/`list_files`/`code_search`/`repo_map`/`skill`（无 runner）。

**测试**：`TestWithoutRunnerDisablesFork`、`TestNestedRunnerIsReadOnly`。

## 16. skill 正文泄漏进 system prompt

**症状**：启动 trace 里 `model_start.messages[0]` 包含完整 `SKILL.md`，上下文被撑大。

**根因**：把 `Definition.Content` 拼进了 prompt，而不是只拼 name/description。

**修复**：`PromptSection` 只渲染索引；正文只通过 `skill` 工具返回。

**测试**：`TestPromptSectionDoesNotLeakBody`。

## 17. sandbox 静默降级

**症状**：加了 `--sandbox`，但 bash 仍然能写工作区外文件；或者换一台机器后行为变了。

**根因**：把“找不到 `sandbox-exec`/`bwrap`”当成 warning，回退到普通 bash；或者平台不支持时继续运行。

**修复**：`sandbox.New` 检测失败直接启动失败（退出码 2）；只有显式去掉 `--sandbox` 才回到审批模式。macOS profile 把写限制到 workspace/临时目录并默认拒网；Linux 用 `bwrap --unshare-net`。

**测试**：`TestDarwinProfileContainsBoundary`、`TestDarwinProfileLimitsWrites`（不能 apply 的环境会 skip）。

**边界**：读仍允许系统文件和 module cache；它不是容器。

## 18. hooks 绕过审批的错觉

**症状**：`before_tool` hook 执行了 `git push`/写文件，用户以为“审批会拦住”。

**根因**：hook 是仓库配置的 shell，在工具内部、审批之前执行；它不是被审批的动作。

**修复**：hooks 默认关闭，必须显式 `--hooks`；文档明确边界；敏感环境用容器/独立用户，而不是只靠 hook。

**测试**：`TestBeforeHookBlocksTool`、`TestWrapRunsBeforeAndAfter`。

## 19. 并行子 agent 共享 runner

**症状**：`tasks` 并发时偶发数据竞争、turn 编号错乱，或两个任务的结果互相覆盖。

**根因**：为了省事把同一个 `Runner` 传给多个 goroutine；nested runner 的 turn 状态、trace 和 budget 不是并发安全的。

**修复**：`tasks` 接受 `RunnerFactory`，每个 task 新建独立 runner（独立 counter/budget/agent），结果按下标写入，最后按输入顺序合并。

**测试**：`TestTasksRunInParallelAndKeepOrder`、`TestTasksRecordIndividualFailure`。

## 20. recall 把旧结论当事实

**症状**：`/recall` 命中一条旧 session，模型直接复用其中已经过时的路径/决定。

**根因**：检索结果是历史文本，不是当前仓库事实；关键词匹配也无法判断时效。

**修复**：结果标注 session id/role/时间；prompt 说明“背景，不是新指令”；关键结论仍要求用工具重新验证。没有命中时明确返回 `no memory hits`，不要让模型编造。

**测试**：`TestSearchPlanAndSessions`、`TestSlashRecall`。
