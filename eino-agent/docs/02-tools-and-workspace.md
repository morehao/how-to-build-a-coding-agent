# 02. 工具、工作区边界与审批

## 1. 工具协议

本项目所有工具都通过 Eino `utils.InferTool` 从 Go struct 生成 JSON Schema：

```go
type readFileInput struct {
    Path   string `json:"path" jsonschema:"required"`
    Offset int    `json:"offset,omitempty"`
    Limit  int    `json:"limit,omitempty"`
}

tool, err := utils.InferTool("read_file", "Read a UTF-8 text file...", fn)
```

`InferTool` 生成 `tool.BaseTool`，内部持有 `Info()`（名称、描述、参数 schema）和 `InvokableRun(ctx, jsonArgs)`。模型看到的是 schema，运行时拿到的是 JSON 字符串。

`internal/tools/registry.go` 是唯一注册入口。增删工具时同时检查：

- `agent.BuildSystemPrompt` 的 Tool usage 段；
- `tools.New` 返回的列表（未知工具错误会列出它）；
- eval 场景和 trace 断言。

## 2. 工作区 jail

`internal/workspace` 是所有路径的唯一入口：

```go
abs, err := ws.Resolve(userPath)   // 相对/绝对路径都收口
rel := ws.Rel(abs)                // 展示用
```

它做三件事：

1. 用 `filepath.Rel` 拒绝 `..` 逃逸；
2. 对已存在路径 `EvalSymlinks`，对不存在的路径解析最近的已存在父目录，防止“先创建 symlink 再写入”；
3. 提供 `WriteFileAtomic`：同目录临时文件 + `rename`，保留权限。

**这不是操作系统沙箱**。`bash` 一旦被批准，就可以访问工作区外的路径。工作区 jail 只约束专用文件工具，真正的隔离需要容器/独立用户/seccomp 等。

## 3. 审批：单 stdin owner

早期实现里 REPL 用 `bufio.Scanner` 读 stdin，审批又用另一个 reader 读 stdin，二者会抢同一个 pipe，导致“输入 `y` 没反应”。修复方式：

- `internal/input.Source` 在后台唯一读取 stdin，把行分发给 `ReadLine`；
- REPL 和 `approval.Terminal` 共用同一个 `*input.Source`；
- `Close` 只关闭内部 channel，不关闭 stdin 本身。

这是一个典型的“并发不是问题，所有权才是问题”案例。写 agent 时，任何会读 stdin 的组件都应该显式接收一个 `LineReader`，而不是各自 `bufio.NewReader(os.Stdin)`。

## 4. 三种权限模式

| 模式 | 读/列/搜/repo_map | edit/apply_patch | bash/MCP |
| --- | --- | --- | --- |
| `ask` | 允许 | 每次审批 | 每次审批 |
| `read-only` | 允许 | 拒绝 | 拒绝 |
| `yolo` | 允许 | 直接执行 | 直接执行 |

非 TTY 下 `ask` 对写操作**失败关闭**，不会挂起等待输入。

审批前会检查 payload 大小：命令、diff、MCP 参数超过 256 KiB 直接拒绝，而不是截断。原因很简单：审批者必须看到完整动作；截断后的“同意”没有意义。编辑审批会先打印 `diff: +N -M` 的规模，再显示完整 diff；长 diff 仍以完整内容为准。

REPL 输入支持用行尾 `\` 显式续行（下一行提示 `... `），适合粘贴多行 prompt；命令仍只在第一行以 `/` 开头时识别。

## 5. 读后写（read-before-edit）

`internal/filestate` 记录每个文件被读取时的：

- size；
- modtime；
- 全文件 SHA-256。

`edit_file`/`apply_patch` 在写之前检查：

```text
未读过          → "read the file before editing it"
读过后又变了    → "the file changed since it was last read"
```

设计要点：

- 检查的是**完整文件哈希**，不是“读过的片段”，避免“读了前 10 行就改第 100 行”；
- 新建文件不需要先读；
- `--no-read-before-edit` 可以关闭，用于兼容旧脚本或特殊工作流；
- 写入后会把新状态标记为已读，因此同一次 turn 里连续编辑同一文件是允许的。

## 6. `edit_file` vs `apply_patch`

`edit_file` 适合“一个唯一子串替换”。它的限制是：

- `old_str` 必须唯一；
- 不适合多处、跨文件修改；
- 大段替换的 diff 会很长，审批体验差。

`apply_patch` 接受 unified diff：

```diff
--- a/a.txt
+++ b/a.txt
@@ -1,2 +1,2 @@
 one
-two
+TWO
--- /dev/null
+++ b/new.txt
@@ -0,0 +1 @@
+created
```

实现分两步：

1. `internal/patch.Parse` 解析所有文件/hunk，并校验 hunk 行数与 header 一致；
2. 对每个文件 `Apply` 做**精确上下文匹配**，全部成功后才进入审批和写盘。

任何 hunk 上下文不匹配都会在写盘前失败，不会留下“改了一半”的状态。`apply_patch` 也支持新建（`--- /dev/null`）和删除（`+++ /dev/null`）。

## 7. checkpoint 与 `/undo`

写文件前，工具会调用 `internal/checkpoint` 保存旧内容：

- 已存在文件：备份内容 + 权限；
- 新文件：记录 `existed=false`，undo 时删除；
- manifest 写在 `.eino-agent/checkpoints/<id>/manifest.json`；
- `/undo` 恢复最近一个 checkpoint，并删除它。

边界：

- 只覆盖 `edit_file` 和 `apply_patch`，**不覆盖 bash 的副作用**；
- 不覆盖目录创建、权限变更、git 操作；
- 一次 `apply_patch` 多文件是一个 checkpoint，undo 会整体回滚；
- checkpoint 目录在 `.eino-agent/` 下，已被 `.einoignore`/workspace 内置忽略规则排除。

## 8. 敏感文件与环境变量

`internal/policy` 提供两层默认值：

- 读/写 `.env`、`id_rsa`、`*.pem`、`credentials.json` 等默认拒绝，除非 `--allow-sensitive`；
- `bash` 默认移除名字里含 `API_KEY`/`TOKEN`/`SECRET`/`PASSWORD`/`AWS_*` 的环境变量，除非 `--allow-env`。

这是“减少意外泄漏”，不是“防止恶意泄漏”。模型仍可以通过被批准的 `bash` 执行 `env` 或读取文件；真正的边界是审批和隔离环境。

## 9. 练习

1. 给 `edit_file` 增加 `replace_all`，并说明它如何影响审批 diff。
2. 用 `apply_patch` 删除一个文件，再 `/undo`，检查文件和 checkpoint 目录。
3. 在 `read_file` 读取 `.env` 时观察错误文本，然后加 `--allow-sensitive` 对比。
4. 构造一个 `old_str` 出现两次的文件，确认错误信息能指导模型加更多上下文。
