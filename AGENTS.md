# AGENTS.md instructions for D:\code\modular

# Repository Guidelines

## Design Philosophy

`modular` 是一套 Go 基础设施积木库（module path: `modular`，Go 1.26+），目标是提供一套可复用的、符合 Go 惯用模式的基础设施方案，让业务项目依赖它来快速组建应用。

核心理念：

- **积木式组装**：所有组件通过 `Option + 构造函数` 模式注入，业务侧零框架绑定。`Application` 只负责生命周期编排，不处理任何业务逻辑。
- **两种资源类型**：`core.Endpoint`（接流量的服务对象：HTTP/gRPC/SSE/Pub-Sub）和 `core.Resource`（支撑性基础设施：DB/Redis/Cache/Storage/Telemetry）。Application 统一管理两者的生命周期。
- **身份模型**：一个 Application 对应一个 `core.ServiceNode`，从启动配置构建。`ServiceNode` 是服务实例的完整元数据，用于服务注册与发现。`Endpoint.Name()` / `Resource.Name()` 仅用于日志区分组件模块，不是服务身份。
- **proto 解耦**：业务层通过 proto 定义接口，生成 `_pb.go` Server/Client。模块间的依赖通过 proto 接口解耦，业务代码完全相同，只是 `cmd` 入口的注入方式不同。
- **单体 <-> 微服务自由切换**：
  - 单体架构：多个 pb server 共享同一个 `Application` 实例，进程内直接调用（`127.0.0.1`），不需要 `Registrar`。
  - 微服务架构：每个 pb server 对应一个 `Application` 实例和一个 `cmd` 入口，通过 `Registrar` 做服务发现，跨进程调用。
  - 切换方式：`cmd` 层注入不同的 Application 组装配置，internal 业务代码不变。
- **谁最了解数据，谁负责生产数据**：Endpoint 不再向 Application 暴露裸 URL，也不做 ServiceNode 转换。ServiceNode 从配置构建，Application 只负责在 node 和 registrar 之间传值。

## Package Architecture

```
packages/
  core/           ← 零依赖核心抽象：Endpoint, Resource, ServiceNode, Transport, Identity
  app/            ← Application 生命周期编排器 + Option 注入
  config/         ← Viper 配置加载器 + 强类型配置结构体
  log/            ← Zap 日志封装（支持日志轮转）
  errs/           ← 统一错误封装（支持错误链、堆栈、上下文字段）
  util/           ← 通用工具（加密、随机、URL、请求）
  transport/
    server/       ← http, rpc, sse 服务器（实现 core.Endpoint）
    client/       ← http, rpc 客户端
    pubsub/       ← kafka, mqtt 订阅者端点（实现 core.Endpoint）
  registry/       ← Consul/K8s 服务注册发现 + gRPC Resolver
  infra/
    database/     ← bun, gorm 数据库适配器
    cache/        ← Redis 客户端 + 布隆过滤器
    storage/      ← disk, aliyunoss 对象存储
  resilience/     ← 断路器、重试、隔板、限流（Middleware Chain 模式）
  pool/           ← 协程池（标准 + ants）
  patterns/       ← 缓存模式（Cache-Aside/Write-Through/Write-Behind）、并发模式
  auth/           ← Token 认证
  command/        ← CLI 命令框架
  telemetry/      ← OpenTelemetry 遥测（实现 core.Resource）
```

### 依赖层次（从底到顶）

1. **core** — 零依赖，定义 Endpoint/Resource/ServiceNode 接口
2. **config, errs, log, util** — 基础工具层，依赖标准库 + 少量第三方
3. **transport, registry, infra, resilience, pool, patterns, auth, command, telemetry** — 功能层，依赖 core + 基础工具
4. **app** — 编排层，依赖 core + config + registry + log

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
- 统一使用 `Option + 构造函数` 模式：每个组件暴露 `NewXxx(cfg, opts...)` 构造函数和 `WithXxx()` 选项函数。
- 接口定义放顶层 `adapter.go`，实现按文件拆分。
- 错误通过 `packages/errs` 统一封装；日志走 `packages/log`（zap）。
- 代码注释以中文为主（见 `application.go` 中 `// 应用程序生命周期管理器` 这类），新代码沿用此约定即可。
- 导入分组顺序：标准库 → 第三方 → 本仓库（`modular/packages/...`）。
- 命名约定：Endpoint 生命周期方法统一为 `Startup`/`Shutdown`（带 context），Resource 为 `Setup`/`Close`。

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