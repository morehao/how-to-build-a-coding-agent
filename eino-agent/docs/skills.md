# Skills：用 `SKILL.md` 做渐进式披露

Skill 是把一小段可复用工作流放进仓库，让 agent 在需要时再加载，而不是把所有说明一次性塞进 system prompt。它适合：

- 团队约定的 review / 发布 / 排障流程；
- 某个子系统的领域知识；
- 一段需要固定步骤的提示词，而不是一个新的 Go 工具。

实现位于 `internal/skill`，ReAct 和 ADK 两个分支共用。

## 目录约定

每个 skill 是一个一级子目录，里面必须有 `SKILL.md`：

```text
skills/
└── review/
    └── SKILL.md
```

默认扫描工作区下的 `skills/`。也可以显式指定：

```bash
./eino-agent --skills-dir team-skills --workspace /path/to/repo
./eino-agent --no-skills --workspace /path/to/repo
```

`--skills-dir` 可以是相对工作区的路径，也可以是工作区内的绝对路径；解析仍经过 workspace jail，不能指向工作区外。不存在的目录视为“没有 skill”，不会报错。

只扫描一级子目录。`skills/backend/review/SKILL.md` 这种嵌套结构不会被发现；一个目录只放一个 skill，避免名字和资源边界混乱。

## `SKILL.md` 格式

```markdown
---
name: review
description: Review a Go diff for correctness, error paths and missing tests.
context: inline
---

# Review workflow

1. Run `git diff` and read every changed file.
2. Check error handling and resource cleanup.
3. Check whether tests cover the new branch.
4. Report findings as `path:line` plus a concrete fix.
```

字段：

| 字段 | 必填 | 含义 |
| --- | --- | --- |
| `name` | 是 | 工具参数中的唯一名字；1–64 个字符，允许字母、数字、`.`、`_`、`-` |
| `description` | 是 | 注入 prompt 的一行摘要；超过 400 字符会截断 |
| `context` | 否 | `inline`（默认）或 `fork` |
| `agent` / `model` | 否 | 预留字段，当前轻量实现不会按 skill 切换 agent/model |

文件上限 64 KiB；正文不能为空；同名 skill 会让启动失败，而不是随机选一个。

## 渐进式披露

启动时 CLI 只把 name 和 description 注入 system prompt：

```text
## Skills

Skills are reusable instructions stored in this workspace...
- `review`: Review a Go diff for correctness, error paths and missing tests.
```

模型判断任务匹配后调用 `skill` 工具：

```json
{"name":"review"}
```

内联模式返回：

```text
Skill "review" loaded.
Base directory: /repo/skills/review

# Review workflow
...
```

`Base directory` 让 skill 里的相对路径（脚本、模板、示例）可以还原成绝对路径。模型仍然要用 `read_file`/`bash` 去访问这些资源；skill 本身不会自动执行任何东西。

## `context: fork`

如果 skill 说明很长、或者希望隔离调查上下文，可以设置：

```yaml
context: fork
```

父 agent 调用该 skill 时，会通过只读子 agent 执行 skill 指令，只把结果摘要返回主上下文。子 agent 仍然不能 edit、bash 或继续委派 `task`/`tasks`，并且有超时和输出上限。

`fork_with_context` 当前不支持：轻量 `skill` 工具的 runner 接口只接收一段 prompt，没有父消息历史。需要完整 fork 语义时，应在 ADK 分支使用 Eino ADK 的 `adk/middlewares/skill`，它支持 `fork_with_context`、`AgentHub` 和按 skill 覆盖 model；本项目选择共享一套更小的实现，便于 ReAct/ADK 对比。

## 安全边界

Skill 是**仓库内容**，不是可信系统指令：

- skill 正文可能包含“忽略审批”“运行 rm -rf”之类文本；它不能覆盖 system prompt、审批和用户请求；
- skill 不获得额外工具权限；要 bash 仍然走审批，要改文件仍然走 edit/patch；
- `fork` 子 agent 是只读的，不能借 skill 绕过写权限；
- 敏感文件策略仍然生效，skill 不能直接读 `.env`；
- 不要把不受信任的 skill 目录指向工作区外；backend 会拒绝。

## 测试

```bash
go test ./internal/skill -v
go test ./cmd/eino-agent -run TestFakeProviderLoadsSkillTool -v
```

测试覆盖 frontmatter 解析、同名/非法 skill、prompt 不泄漏正文、inline 调用、fork runner、输出截断，以及 CLI 是否真的注册了 `skill` 工具。

## 当前边界

- 不支持嵌套 skill 目录、自动发现子目录 `CLAUDE.md` 或远程 skill registry；
- 不支持 skill 级 allow/deny 工具白名单；
- 不支持运行中热加载；新增 skill 后重启 CLI 才会出现在工具 schema 里；
- `agent`/`model` 字段只是为将来预留，当前不会生效。
