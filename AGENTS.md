# Repository Guidelines

## Project Structure & Module Organization

`modular` 是一个 Go 单体仓库（module path: `modular`，Go 1.26+），采用模块化的 "modular monolith" 风格。源码全部位于 `packages/` 下，按领域分包，无 `cmd/` 入口；应用生命周期由 `packages/app` 的 `Application` 统一编排。

- `packages/` — 全部业务/基础设施源码
  - `app` — 应用生命周期、启动/关闭编排（`application.go`、`application_options.go`）
  - `config`, `errs`, `log`, `util` — 配置、错误、日志、通用工具
  - `transport` — 传输层：`http` / `rpc` / `sse` / `kafka` / `mqtt`，统一抽象见 `endpoint.go`
  - `registry`, `resilience`, `pool`, `patterns` — 服务注册发现、容错、协程池、设计模式
  - `infra` — 基础设施适配：`database`（bun/gorm）、`cache`、`redis`、`storage`（disk/aliyunoss）
  - `auth`, `command`, `telemetry` — 认证、CLI、OpenTelemetry 遥测
- `docs/` — 设计文档：`specs/`（规格）、`plans/`（实现计划）、`superpowers/`
- 测试与源码同包，`*_test.go` 并置。

## Build, Test, and Development Commands

仓库为纯 Go，使用标准工具链，无 Makefile：

```bash
go build ./...              # 编译全部包
go test ./...               # 运行所有测试
go test ./packages/app -v   # 运行单个包的测试（带详情）
go vet ./...                # 静态检查
gofmt -l .                  # 列出格式不符的文件
go mod tidy                 # 同步依赖
```

## Coding Style & Naming Conventions

- 遵循 Go 官方风格：`gofmt`/`goimports` 强制格式化，缩进使用 Tab。
- 包名简短、全小写；导出标识符用大驼峰（如 `NewApplication`、`Storage`）。
- 错误通过 `packages/errs` 统一封装；日志走 `zap`/`otelzap`。
- 代码注释以中文为主（见 `application.go` 中 `// 应用程序生命周期管理器` 这类），新代码沿用此约定即可。
- 导入分组顺序：标准库 → 第三方 → 本仓库（`modular/packages/...`）。

## Testing Guidelines

- 测试框架：`github.com/stretchr/testify`（require/assert）。
- 测试文件与被测代码同目录，命名为 `xxx_test.go`。
- 测试函数 `TestXxx_Yyy`，子测试用 `t.Run`，如 `TestDiskStorage_PersistToUploadDir`。
- Mock / 富接口风格优先，见 `packages/infra/storage` 的 mock 测试。
- 提交前确保 `go test ./...` 通过。

## Commit & Pull Request Guidelines

提交信息遵循 Conventional Commits，描述可为中文，示例如下：

```
feat(storage): OssStorage v2 SDK 重写（富接口 + mock 测试）
fix(storage): apply final review fixes for storage-merge
refactor(config): 存储配置收敛为 disk|oss
chore: ignore packages/infra/storage/upload test artifact
```

类型包括 `feat`、`fix`、`refactor`、`chore`、`docs`，scope 对应受影响的包（如 `storage`、`config`）。PR 需关联设计文档（`docs/specs`、`docs/plans`），保持变更范围聚焦于单包或单模块。

## Agent & Tooling Notes

`.claude/`、`.superpowers/`、`docs/superpowers/` 为 AI 协作工具的配置与产出，属工作产物而非应用源码；改动前注意区分。`.env`、`go.work`、`storage/upload/` 等本地/产物文件已被 `.gitignore` 忽略，请勿提交。
