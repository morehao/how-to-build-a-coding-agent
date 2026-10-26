# Agent 使用说明

[English](AGENT.md) | **简体中文**

## 开发环境

本项目使用 [devenv](https://devenv.sh/) 配合 Nix 提供可复现的开发环境。

## 常用命令

- `devenv shell` - 进入开发 shell
- `devenv test` - 运行测试（目前仅执行 git 版本检查）
- `go build` - 构建 Go 项目
- `go run main.go` - 运行聊天应用
- `go test ./...` - 运行全部 Go 测试
- `go test <package>` - 运行指定包的测试
- `go mod tidy` - 下载依赖
- `hello` - 自定义脚本，从开发环境输出问候语

### 应用命令

- `go run chat.go` - 与 Claude 的简单聊天界面
- `go run read.go` - 具备文件读取能力的聊天
- `go run list_files.go` - 具备目录列举与文件读取能力的聊天
- `go run bash_tool.go` - 具备文件操作与 bash 命令执行能力的聊天
- `go run edit_tool.go` - 具备完整文件操作能力（读取、列举、编辑、bash）的聊天

### 详细日志

所有 Go 应用都支持 `--verbose` 标志，用于输出详细的执行日志：

- `go run chat.go --verbose` - 启用详细日志以便调试
- `go run read.go --verbose` - 查看详细的工具执行与 API 调用过程
- `go run edit_tool.go --verbose` - 调试文件操作与工具使用

## 架构

- **环境**：基于 Nix 的开发环境，由 devenv 管理
- **Shell**：包含 Git、Go 工具链以及自定义问候脚本
- **结构**：通过 Anthropic API 与 Claude 交互的终端聊天应用

## 代码风格指南

- devenv.nix 配置遵循 Nix 约定
- 使用标准的 Git 工作流
- 开发环境配置应保持可复现

## 故障排查

### 详细日志

调试聊天应用问题时，可使用 `--verbose` 标志获取详细的执行日志：

```bash
go run edit_tool.go --verbose
```

**详细日志会展示以下内容：**

- 对 Claude 的 API 调用（模型、耗时、成功/失败）
- 工具执行细节（调用了哪些工具、输入参数、返回结果）
- 文件操作（读取、写入、列举文件及其大小/数量）
- Bash 命令执行（执行的命令、输出、错误）
- 对话流程（消息处理、内容块）
- 错误详情及堆栈信息

**日志输出位置：**

- **详细模式**：详细日志写入 stderr，带时间戳和文件位置
- **普通模式**：仅必要的输出写入 stdout

**常见排查场景：**

- **API 失败**：查看详细日志中的认证错误或限流信息
- **工具失败**：准确看到哪个工具失败、原因是什么（文件不存在、权限错误）
- **响应异常**：查看完整对话流程和 Claude 的推理过程
- **性能问题**：查看 API 调用耗时与响应大小

### 环境问题

- 确保已设置 `ANTHROPIC_API_KEY` 环境变量
- 运行 `devenv shell` 以确保处于正确的开发环境
- 使用 `go mod tidy` 确保依赖已安装

## 备注

- 需要设置 `ANTHROPIC_API_KEY` 环境变量
- 聊天应用提供与 Claude 交互的简单终端界面
- 使用 ctrl-c 退出聊天会话

## Eino Agent 模块

仓库还包含独立 Go module `eino-agent/`（ReAct 与 ADK 分支），不在根目录 `go test ./...` 范围内。

```bash
cd eino-agent
go build ./...
go vet ./...
go test ./...
go test ./internal/agent -run TestEvalScenarios -v
go test -race ./...
```

- `--provider fake --script testdata/scenarios/*.json` 无需 API key 即可确定性运行。
- `--trace <file>` 写 JSONL 事件；`internal/eval` 读取同一套事件。
- 改 agent 循环或 trace schema 前先读 `eino-agent/README.md` 和 `eino-agent/docs/README.md`。
- 不要提交 `.eino-agent/`（checkpoint/plan/trace）或 session 文件。
