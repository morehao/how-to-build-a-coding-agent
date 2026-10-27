# OS 级 sandbox（可选）

`--sandbox` 把 `bash` 放进宿主提供的 OS sandbox；它是审批之外的第二层，不是默认开启的安全承诺。

## 1. 后端

| 平台 | 后端 | 默认限制 |
| --- | --- | --- |
| macOS | `sandbox-exec` | 拒绝网络；写只允许 workspace、`/tmp`、`/private/tmp`、`/dev`、`/private/var/folders` |
| Linux | `bubblewrap` (`bwrap`) | `--ro-bind / /`，workspace 可写，`/tmp` tmpfs，`--unshare-net` |
| 其他 | 不支持 | 启动直接失败，不会静默降级 |

开关：

```bash
eino-agent --sandbox --workspace .
eino-agent --sandbox --sandbox-allow-network --workspace .
```

`--sandbox-allow-network` 只放宽网络；写边界仍然生效。没有 `--sandbox` 时 bash 仍是普通 `bash -c`，审批/权限规则是唯一门禁。

## 2. 为什么不是“读也隔离”

macOS profile 允许 `file-read*`：Go module cache、SDK、shell 配置都在 workspace 外，完全拒绝读会让 `go test`/编译器无法工作。当前边界是：

- **写**：只允许 workspace 和临时目录；
- **网络**：默认拒绝；
- **进程**：允许 fork/exec（工具链需要），但 `--die-with-parent`/进程组取消会回收；
- **读**：仍可读系统文件和 module cache，`--allow-sensitive` 的敏感文件规则只覆盖文件工具，不覆盖被批准的 bash。

所以它降低“误写仓库外文件 / 意外联网”的风险，不防“读秘密再通过允许的网络通道外传”。需要后者时用容器 + 只读挂载 + 网络策略，或独立用户。

## 3. 失败行为

- `--sandbox` 但找不到 `sandbox-exec`/`bwrap`：启动失败，退出码 2；
- 平台不支持：启动失败；
- sandbox 内部命令被拒绝：bash 返回非零退出码，模型看到 `exit code: N` 和 stderr；
- 某些宿主（包括已经运行在 sandbox 里的开发环境）会禁止嵌套 `sandbox-exec apply`，表现为 `sandbox_apply: Operation not permitted`；此时应去掉 `--sandbox` 或在容器/CI 里运行。

## 4. 测试

```bash
go test ./internal/sandbox -v
go test ./internal/tools -run TestBash -v
```

`internal/sandbox` 的 macOS 集成测试在无法 apply profile 的环境会 skip；CI 应至少跑 profile 字符串和命令包装的单测。不要用 `--sandbox` 代替审批、权限规则或容器隔离，它们是不同层。
