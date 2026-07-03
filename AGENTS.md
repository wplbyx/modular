# AGENTS.md instructions for /Users/xinyue/code/modular

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
  core/           ← 零依赖核心抽象：Endpoint, Resource, ServiceNode, Transport
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

类型包括 `feat`、`fix`、`refactor`、`chore`、`docs`，scope 对应受影响的包（如 `storage`、`config`）。PR 需关联设计文档（`docs/superpowers/specs`、`docs/superpowers/plans`，注意没有顶层 `docs/specs` 或 `docs/plans` 目录），保持变更范围聚焦于单包或单模块。

## Agent & Tooling Notes

`.claude/`、`.superpowers/`、`docs/superpowers/` 为 AI 协作工具的配置与产出，属工作产物而非应用源码；改动前注意区分。`.env`、`go.work`、`storage/upload/` 等本地/产物文件已被 `.gitignore` 忽略，请勿提交。

## Gotchas & Codebase Realities

以下是文档未明说、但改动前必须知道的事实（来源：对 `packages/` 的实际审计）。

### 生命周期契约（最重要的规则）

- `core.Endpoint.Startup(ctx)` **必须阻塞**直到服务停止；`Application.Run` 把*任何*返回（nil 或 error）都当作退出信号。`Shutdown` 才是解除 `Startup` 阻塞的手段。见 `packages/core/adapter.go`。
- `Application.Run` 内部顺序：`Resource.Setup`（FIFO）→ `registrar.Register(node)` → 全部 `Endpoint.Startup` **并行**（errgroup）→ 退出时：`Endpoint.Shutdown`（并行）→ `Unregister` → `Resource.Close`（LIFO）。Shutdown 由 `sync.Once` 保护，只执行一次，整体在单一 `shutdownTimeout` 预算内完成（默认 10s）。
- **零 endpoint 的 `Application` 会打印 warning 并立即返回**（`application.go`），不要期望它会阻塞。
- `app` 只**向下**导入（core + config + log + registry），**不导入 `transport`**——endpoint 永远以 `core.Endpoint` 接口注入。务必保持这条边界：不要让 app 反向依赖 transport/server。

### Protobuf 只是设计目标，仓库里并不存在

- 尽管"proto 解耦"是设计哲学，仓库里**没有任何 `.proto` 文件、没有 `_pb.go`、没有 buf/protoc 工具链**。`grpc`/`protobuf` 依赖仅用于 gRPC transport server/client 与 registry resolver，并非本地代码生成。不要去找或期待已生成的服务代码。

### 错误处理 —— `errs` 采用范围很窄

- `packages/errs` **只被 `packages/resilience` 引入**。其余所有包（transport/app/infra/registry/config）的主流写法是 `fmt.Errorf("...: %w", err)` + 用 `errors.Join` 做聚合。在 resilience 里用 `errs`；在别处对齐 `fmt.Errorf`。不要全仓强制 `errs`。

### 日志是全局单例，不走 context

- 直接调包级函数 `log.Infof(...)` / `log.Error(...)`；`GetLogger()` 返回 zap logger（未初始化时返回 `zap.NewNop()`）。**没有 logger-in-context 模式**。`NewLoggerManager` 若一个输出 core 都没加会报错；通过 `WithOutputConsole()` / `WithOutputFiles(ctx)` / `WithOutputTelemetry(...)` 添加。每日文件轮转需要传 context（lumberjack 的 watcher goroutine 绑定 ctx）。

### Option 模式并非通用

- 真正的函数式选项（`NewXxx(cfg, opts...)` + `WithXxx`）：`app`、`transport/server/http`、`transport/pubsub/{kafka,mqtt}`、`errs`。
- **仅 Config 结构体、没有 `WithXxx`**（与文档约定有出入）：`transport/client/http`、`infra/cache/redis`、`infra/database/{bun,gorm}`、`infra/storage/{disk,alioss}`。resilience 用 `Config` 结构体 + 在构造函数里手动对零值回退到 `Default*Config`。
- **`transport/server/rpc` 的 `Option` 返回 `error`**（`type Option func(*Server) error`）——全仓唯一带 error 返回的 Option 类型。应用时要处理该错误；新代码**不要**沿用这个形状。
- `adapter.go`-接口置顶的约定只被 `core`、`registry`、`resilience`、`patterns/caching` 遵守——当作偏好而非硬规则。

### Storage —— 先读重构文档

- 改 `packages/infra/storage` 或 storage 配置结构体前，先读 `docs/superpowers/specs/2026-06-30-storage-merge-design.md` 和 `docs/superpowers/plans/2026-06-30-storage-merge.md`。
- 只有两个后端：`disk` 和 `oss`（s3/minio/ftp 已删除）。OSS **只能用 v2 SDK**（`alibabacloud-oss-go-sdk-v2`），禁止 v1。
- `NewStorage` 分发工厂**当前被注释掉**（`storage.go`）——调用方必须直接构造 `disk.NewDiskStorage` / `alioss.NewOSSStorage`。`ErrUnsupportedStorageType` 已导出。
- HTTP server 在**构造时即监听端口**（"构造即监听"），所以 `Port=0` 也能拿到真实端口。

### 全局单例（注意）

- `infra/cache/redis`、`infra/database/{bun,gorm}`、`transport/client/{http,rpc}` 都保留了包级全局（`Init/GetX`）。这与 storage 文档里"不要全局单例"的要求**自相矛盾**。接入 `Application` 时，优先把返回的实例当依赖注入，而不是依赖全局。

### 代码生成 / 构建工具

- `//go:generate` 指令**只存在于 `packages/config/`**，且依赖外部工具 `gomodifytags`（重新生成 `mapstructure` 标签）。编辑配置结构体后若需要刷新标签，运行 `go generate ./packages/config/...`。
- 无 CI、无 Makefile、无 `.golangci.*`、无 linter 配置——质量门禁是手动的：`go vet ./...` 与 `gofmt -l .`。

### 其它坑

- `packages/registry/consul.go` 的注释存在乱码（GBK 被当作 UTF-8 读取）；同目录其它文件是干净的 UTF-8。除非明确要求，**不要**自行"修复"重打；新注释一律用正常 UTF-8。
- 注释语言混用：较老/核心代码是中文，较新的 transport/pubsub 是英文。沿袭所在文件的风格即可。
- K8s registry **只实现 `Discovery`**——`Register`/`Unregister` 是 no-op（K8s 自身通过 Deployment+Service 完成注册，发现走 SharedInformerFactory）。Consul registry 同时实现 `Registrar` 和 `Discovery`，并按 **每个 Transport 注册一条记录**（ID 加协议后缀），使单个 node 可同时发布 HTTP+gRPC。
- gRPC resolver：`BuildConsulTarget("svc") → "consul:///svc"`；Scheme 为 `"consul"`；只过滤 `protocol=="grpc"` 的 transport。