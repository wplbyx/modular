# modular

`modular` 是一套模块化单体 Go 应用积木库（module path: `github.com/wplbyx/modular`，Go 1.26+）。它提供基础设施、生命周期和项目装配能力，但不接管业务代码，也不使用运行时依赖容器。

一个项目固定生成一个 `Application` 和一个部署单元，并按 DDD 限界上下文纵向划分 `Business Module`。模块化边界用于保护业务模型、接口与数据写入所有权，不模拟网络边界，也不提供模块到微服务的透明提取。

核心目标：

- `Business Module` 默认对应一个限界上下文，拥有业务规则、用例、公开 Go contract 与数据写入。
- 一个项目只有一个 `Application`、运行时身份、Logger、Telemetry、Health、EventBus，以及每种协议一个共享 Server。
- `Application` 只负责编排生命周期，不处理业务逻辑，也不知道 Business Module。
- `core.Endpoint` 表示接收流量或事件的入口，例如 HTTP、gRPC、SSE、消息订阅。
- `core.Resource` 表示支撑性基础设施，例如 DB、Redis、Storage、Telemetry。
- `core.ServiceNode` 表示服务实例身份，用于注册与发现；`Endpoint.Name()` / `Resource.Name()` 只作为日志标签。
- 模块之间只依赖 provider 的手写 Go contract，不依赖其实现或传输 DTO。
- 跨模块 ACID 事务可以由发起方模块显式编排；事务接口由具体项目按用例定义。

## 核心模型

| 模型 | 包 | 职责 |
| --- | --- | --- |
| `core.Resource` | `packages/core` | 基础设施生命周期：`Setup(ctx)` / `Close(ctx)`，不阻塞，不接流量。 |
| `core.Endpoint` | `packages/core` | 服务入口生命周期：`Startup(ctx)` / `Shutdown(ctx)`；`Startup` 必须阻塞到服务停止。 |
| `core.ProcessIdentity` | `packages/core` | Application 运行实例的名称、版本、实例 ID 和注册 metadata 值对象。 |
| `core.ServiceNode` | `packages/core` | 一个 `Application` 对应一个进程节点，由 ProcessIdentity 和多个 transport 构建。 |
| `registry.Registrar` | `packages/registry` | 将 `ServiceNode` 注册到 Consul 等注册中心。 |
| `registry.Discovery` | `packages/registry` | 按服务名发现实例，或 watch 实例变化。 |
| `health.Manager` | `packages/health` | 管理 Application 的 starting/ready/draining 状态和有界依赖检查。 |
| `app.Application` | `packages/app` | 统一管理 Resource、Endpoint、readiness、Registrar 和 ServiceNode 的启动与关闭顺序。 |

`Application.Run` 的顺序固定：

```text
Resource.Setup() FIFO
  -> Endpoint.Startup()/Ready() 并行
  -> Registrar.Register(ServiceNode)
  -> Ready
  -> Draining / Unregister
  -> Endpoint.Shutdown() 并行
  -> Resource.Close() LIFO
```

零 Endpoint 的 `Application` 会在 Resource Setup 后等待 Context 取消。接入 `Application` 的 Endpoint 必须保证 `Startup` 在正常运行时阻塞，且 `Shutdown` 能解除阻塞；内置 Endpoint 还实现 `core.ReadyEndpoint`。

## 模块总览

| 模块 | 内容 |
| --- | --- |
| `packages/core` | 零依赖核心抽象：`Resource`、`Endpoint`、`ProcessIdentity`、`Transport`、`ServiceNode`。 |
| `packages/app` | 应用生命周期编排器，提供 `WithServiceNode`、`WithHealthManager`、`WithRegistrar`、`WithResource`、`WithEndpoint`。 |
| `packages/config` | Viper 配置加载器与 Cobra 命令集成，支持本地文件、远程 KV、环境变量和自动模块 flags。 |
| `packages/config/configitem` | 可组合的强类型基础设施配置：Application、HTTP、GRPC、Database、Redis、Storage、Logging、Telemetry 和消息中间件。 |
| `packages/log` | Context 强制日志接口；Zap 输出与基于 `cyub/ringbuffer` 的异步分发，支持文件轮转、动态 OpenTelemetry sink 和有界降级。 |
| `packages/metadata` | 不可变、分 scope 的上下文元数据，统一 HTTP/gRPC/消息 Header 与 OTel trace context 穿透。 |
| `packages/eventbus` | 进程内有序 EventBus Resource；队列数据直接存放在 `cyub/ringbuffer.MpscRingBuffer`。 |
| `packages/errs` | Kratos 风格统一错误、多语言 YAML Catalog、错误链/堆栈诊断与客户端/日志分流。 |
| `packages/generate` | 可安装的错误 Catalog 生成工具。 |
| `packages/util` | AES/RSA/ECC、随机字符串、URL、HTTP 请求和 context 工具。 |
| `packages/transport/server/http` | 基于 Gin 的 HTTP endpoint，支持中间件、健康检查、TLS、h2c；构造时即监听端口。 |
| `packages/transport/server/rpc` | gRPC endpoint，支持健康检查、拦截器和 mTLS。 |
| `packages/transport/server/sse` | SSE 服务，可挂载到 HTTP 路由，作为 `core.Endpoint` 管理连接生命周期。 |
| `packages/transport/client` | 显式注入的 HTTP / gRPC 客户端，默认接入 Metadata、OTel、访问日志和弹性保护。 |
| `packages/transport/pubsub` | 消息订阅 endpoint 抽象，以及 Kafka、MQTT、RocketMQ、Redis Pub/Sub、Redis Stream 适配。 |
| `packages/registry` | Consul 注册发现、K8s discovery、gRPC resolver；Consul 按 transport 注册服务记录。 |
| `packages/infra/database` | Bun / GORM / MongoDB 数据库连接能力；Bun、GORM 提供可直接注入 Application 的 Resource。 |
| `packages/infra/cache/redis` | go-redis 客户端、布隆过滤器、幂等工具。 |
| `packages/infra/storage` | 对象存储富接口与可选直传预签名接口，当前实现为本地磁盘 `filedisk` 和阿里云 OSS v2 `alioss`。 |
| `packages/telemetry` | OpenTelemetry trace、metric、log provider，作为 `core.Resource` 注入应用。 |
| `packages/resilience` | 熔断、重试、限流、隔板，以及基于 Kratos Aegis BBR/SRE 的 Transport 自适应保护。 |
| `packages/patterns` | 缓存模式（Cache-Aside、Write-Through、Write-Behind、Refresh-Ahead）和并发模式。 |
| `packages/pool` | 可观测的有界异步任务池；支持满载立即拒绝或有界排队，并作为 Resource 管理。 |
| `packages/idgen` | 不透明业务 ID seam，以及 UUIDv7、可配置 Snowflake 和节点租约适配器。 |

## 典型使用方式

下游项目在 `cmd/<application>` 里显式构造模块，生成的 `cmd/<application>` 只负责共享基础设施和生命周期。模块实现位于 `modules/<name>/internal`；其他模块只能导入其手写 `contract` 包。

异步任务池需要显式注入并交给 Application 管理：

```go
workerPool, err := pool.New(pool.Config{
	Name:          "email-workers",
	Capacity:      32,
	Policy:        pool.Queue,
	QueueCapacity: 256,
}, logger)
application, err := app.NewApplication(ctx, &cfg.Application, logger,
	app.WithResource(workerPool),
)
```

业务代码通过 `core.Provider[idgen.Generator]` 使用不透明 ID。UUIDv7 不需要分布式协调，是单体和微服务的默认方案：

```go
ids := idresource.NewUUIDv7("business-id")
application, err := app.NewApplication(ctx, &cfg.Application, logger,
	app.WithResource(ids),
)
repository := NewRepository(ids)
```

Snowflake 使用显式、不可变的位布局。单体可以用 `snowflake.StaticNode(0)`；微服务必须注入能保证节点号唯一、续租失败即关闭 `Lost()` 的租约适配器。静态节点号本身不代表自动高可用。

配置入口推荐使用 `config.NewRootCommand[T]`。它会扫描业务聚合配置中实现 `FlagProvider` 的模块，注册 PascalCase Cobra flags，并按以下优先级合并：

```text
显式 Cobra 配置参数 > 环境变量 > 本地文件 > 远程 KV > FlagSpec 默认值
```

共享配置源参数：

```text
--config, -c <path>   本地配置文件
--remote <url>        etcd://host/key 或 consul://host/key
```

本地与远程可以同时使用；远程读取失败时，只有已经成功读取的本地文件可以兜底。两者格式不一致时会记录 warning 并忽略远程配置。`etcd://` 默认映射到 etcd v3，远程值没有扩展名时默认按 YAML 解析，可用 `?format=json` 显式指定。

一个最小 HTTP 应用大致如下：

```go
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/gin-gonic/gin"

	"github.com/wplbyx/modular/packages/app"
	modularconfig "github.com/wplbyx/modular/packages/config"
	"github.com/wplbyx/modular/packages/core"
	"github.com/wplbyx/modular/packages/log"
	modulartransport "github.com/wplbyx/modular/packages/transport"
	httpserver "github.com/wplbyx/modular/packages/transport/server/http"

	projectconfig "<project>/config/user"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	command := modularconfig.NewRootCommand[projectconfig.Config](modularconfig.CommandOptions[projectconfig.Config]{
		Name:        "user",
		DefaultFile: "./config/user/config.yaml",
		EnvPrefix:   "USER",
		Run:         run,
	})
	command.SetContext(ctx)
	command.SilenceErrors = true
	command.SilenceUsage = true
	if err := command.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "application exited: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg *projectconfig.Config) error {
	// cfg 已由 config.NewRootCommand 加载；日志固定是第二个初始化步骤。
	loggerManager, err := log.NewLoggerManager(&cfg.Logging)
	if err != nil {
		return fmt.Errorf("create logger: %w", err)
	}
	restoreLogger := log.SetDefault(loggerManager.Logger())
	defer restoreLogger()
	defer loggerManager.Close(context.WithoutCancel(ctx))
	policy := modulartransport.NewPolicy(cfg.Application.Name, modulartransport.WithLogger(loggerManager.Logger()))

	httpSrv, err := httpserver.NewServer(&cfg.HTTP, httpserver.WithPolicy(policy))
	if err != nil {
		return fmt.Errorf("create HTTP server: %w", err)
	}
	httpSrv.RegisterRoute(func(r *gin.Engine) {
		r.GET("/ping", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})
	})

	identity, err := core.NewProcessIdentity(
		cfg.Application.Name,
		cfg.Application.Version,
		cfg.Application.InstanceID,
		cfg.Application.Metadata,
	)
	if err != nil {
		return fmt.Errorf("create process identity: %w", err)
	}
	node := core.NewServiceNodeFromProcess(identity, httpSrv.Transport())

	application, err := app.NewApplication(
		ctx,
		&cfg.Application,
		loggerManager.Logger(),
		app.WithServiceNode(node),
		app.WithEndpoint(httpSrv),
	)
	if err != nil {
		return fmt.Errorf("create application: %w", err)
	}

	return application.Run()
}
```

运行时可以组合配置来源和覆盖参数：

```bash
user --config ./config/user/config.yaml \
  --remote etcd://10.0.0.1:2379/config/user \
  --HTTP.Port 18080
```

进程配置嵌套其承载的模块业务配置，但 HTTP、gRPC、Database、Redis、Telemetry 等字段只在进程顶层出现一次。环境变量使用 `_` 表示层级，例如 `APP_STORAGE_OSS_ACCESSKEYID`；字段单词内部不插入下划线。

需要注册中心时，在 `cmd` 中构造 registrar 并注入：

```go
registrar, err := registry.NewConsulRegistry("127.0.0.1:8500")
if err != nil {
	panic(err)
}

application, err := app.NewApplication(
	ctx,
	&cfg.Application,
	loggerManager.Logger(),
	app.WithServiceNode(node),
	app.WithRegistrar(registrar),
	app.WithEndpoint(httpSrv),
)
```

需要基础设施时，将其包装或直接构造成 `core.Resource` 后通过 `app.WithResource(...)` 注入。`Resource.Setup` 会在所有 endpoint 启动前执行，`Resource.Close` 会在 endpoint 停止后按反向顺序执行。

### 统一错误与多语言

业务包集中定义与语言无关、可复用的文案对象，调用时只绑定模板参数：

```go
var UserNotFound = errs.Define(
	"USER_NOT_FOUND",
	errs.Template("user %v not found", errs.Name("user_id")),
)

func findUser(id string) error {
	return errs.NotFound(
		UserNotFound.With("user_id", id),
		errs.WithCause(errors.New("database row not found")),
		errs.WithField("user_id", id),
	)
}
```

每种语言使用一个 YAML 文件，文件名是 BCP 47 locale，例如 `locales/zh-CN.yaml`：

```yaml
UNKNOWN: "请求处理失败"
USER_NOT_FOUND: "用户 {{.user_id}} 不存在"
```

代码模板只接受 `%v` 变量槽和 `%%` 字面量百分号。`%v` 按顺序映射到后续 `errs.Name`；产品可以修改任意文字并调整 `{{.name}}` 的顺序，但不能增删或改名。缺少运行时参数时仅将对应槽位替换为 `UNKNOWN`，其余文案保持不变，同时记录诊断日志。

使用 `err_template_gen` 从业务代码批量创建或更新语言文件：

```bash
go install github.com/wplbyx/modular/packages/generate/cmd/err_template_gen@latest

err_template_gen \
  --root . \
  --packages ./internal/user/... \
  --out ./config/user/locales \
  --languages zh-CN,en-US

# CI 中只校验源码定义、reason 和槽位契约，不修改文件
err_template_gen --root . --out ./config/user/locales --languages zh-CN,en-US --check
```

重复生成会保留已有产品文案和注释，只追加新 reason。非法槽位、冲突定义、源码已删除但 YAML 仍存在的 reason 都会使命令失败，且验证失败时不会写入任何语言文件。

Catalog 和 Handler 在 `cmd` 层显式构造，并同时注入 HTTP 与 gRPC server：

```go
catalog, err := errs.LoadCatalog(os.DirFS("."), "locales", "zh-CN")
if err != nil {
	return fmt.Errorf("load error catalog: %w", err)
}
errorHandler, err := errs.NewHandler(catalog, loggerManager.Logger())
if err != nil {
	return fmt.Errorf("create error handler: %w", err)
}

httpSrv, err := httpserver.NewServer(&cfg.HTTP, httpserver.WithErrorHandler(errorHandler))
grpcSrv, err := rpcserver.NewServer(&cfg.GRPC, register, rpcserver.WithErrorHandler(errorHandler))
```

HTTP handler 可通过 `httpserver.Wrap(func(*gin.Context) error)` 返回错误，也可使用 `c.Error(err)`。HTTP 从 `Accept-Language`、gRPC 从 `accept-language` metadata 选择文案。客户端只收到 `code`、`reason`、本地化 `message`；cause、内部 fields、完整错误链和堆栈只写入注入的 Context Logger，并携带 request/trace/span 关联字段。

### 数据库连接

`packages/infra/database` 提供显式依赖注入的托管资源：

| 后端 | Application Resource | 配置 |
| --- | --- | --- |
| Bun/PostgreSQL | `bun.NewResource(cfg)` | `configitem.Database{DSN: ...}` |
| GORM | `gorm/postgres.NewResource`、`gorm/mysql.NewResource`、`gorm/clickhouse.NewResource`、`gorm/sqlite.NewResource` | `configitem.Database{DSN: ...}` |
| MongoDB | `mongo.NewResource(cfg)` | `configitem.Mongo{URI: ...}` |

所有资源都基于 `core.ManagedResource[T]`，同时实现 `core.Resource` 和 `core.Provider[T]`。Application 负责 Setup/Close，repository 保存 Provider 并在处理请求时调用 `Value()`；不要在 `Application.Run` 前提前取值。Redis 使用 `redis.NewResource(cfg)`，Storage 使用 `storage/resource.New(cfg)`。数据库、Redis 和 HTTP client 不再提供包级全局实例。

GORM 方言由 `cmd` 装配层通过子包选择。SQLite 子包使用纯 Go 驱动，可在 `CGO_ENABLED=0` 下编译和测试。

### 健康检查与 HTTP client

HTTP server 默认 `/health` 只表示进程存活。生成的 Application 使用 `httpserver.WithHealthManager(path, manager)` 暴露 starting/ready/draining 和依赖检查；独立使用时也可通过 `WithReadiness(path, checkers...)` 注入检查。就绪接口返回 200/503，且 `Transport.HealthPath` 指向 readiness 路径。流式响应可将 `WriteTimeout` 设为 `httpserver.NoWriteTimeout`。

HTTP client 是显式构造的 `*httpclient.Client`，主接口为 `Do(*http.Request)`。重试仅适用于可重放的幂等请求；POST/PATCH 需要 `Idempotency-Key` 或自定义 `RetryPolicy`。

## 推荐项目分层

v0.4 脚手架生成一个 Application，并按限界上下文维护 Business Module。CLI 管理确定性的配置和共享基础设施；Agent/用户在 typed composition root 中构造模块。

装配分为两层：`cmd/<application>` 创建共享资源、按依赖 DAG 连接模块、挂载入口并配置生命周期；`modules/<module>/bootstrap.go` 用明确的依赖组装本模块 adapter、用例和领域对象。业务流程、事务和失败恢复留在发起用例中。

兄弟模块使用 `contract` 业务契约；cmd 使用模块根包的 `Config`、`Dependencies`、`Module` 和 `New(cfg Config, deps Dependencies) (*Module, error)` 装配接口。新 CLI 骨架提供这一形状，已有 scaffold-once 文件保持用户所有。构造仅连接对象，保留 Provider，不提前读资源或启动任务。

接口不仅是签名，还包含成功保证、错误与副作用，以及适用的幂等、并发和事务语义。设计或审核业务接口时读取 [接口设计指南](agent/modular/references/interface-design.md)，按业务需要选择抽象，不要求固定接口分类或层级。两层装配规则和双模块示例见 [分层与装配](agent/modular/references/layering.md)。

```text
<project>/
  .modular/
    architecture.yaml        # Application 能力和模块依赖 DAG
    manifest.json            # 文件所有权、生成哈希、模板版本和最小生成来源
    profile.toml             # 当前项目的附加检查策略
    make/modular.mk          # 受管 Make 目标
    tool/                    # 项目内可迁移的脚手架及模板
  cmd/
    <application>/main.go    # 应用启动入口
    <application>/resources.go # 共享资源装配
    <application>/modules.go # 应用级装配类型与模块连接入口
  config/
    <application>/           # identity、transport、共享 Resource 与模块配置聚合
      config.gen.go
      config.yaml
  modules/<module>/
      bootstrap.go           # 模块局部装配入口与导出能力，用户维护
      config.go              # 模块业务配置，与 bootstrap 同包
      contract/              # 手写的窄 Go 接口和 Command/Query/Result
      internal/              # Go 编译器阻止兄弟模块穿透
        app/                 # 业务用例、私有输入输出与出站接口；直接实现公开 contract
        domain/              # 仅在有聚合、不变量或策略时创建
      infrastructure/        # 模块自有 HTTP/消息/持久化适配器，协议 DTO 与映射就近放置
  go.mod    

```

约束：

- `module add` 不要求 transport；transport 和 Resource 属于唯一 Application。
- Application 对每种启用协议只构造一个共享 Server，应用装配结果聚合 route/register/checker/lifecycle。
- 强类型资源直接位于 `Platform.Resources.<Resource>`；模块只接收实际需要的 Provider 或窄接口。
- `Assembly.Registrar` 默认 nil；需要注册 Application 实例时在 scaffold-once wiring 中显式注入。
- `managed` 文件只有在当前哈希与 manifest 一致时才更新；`scaffold-once` 文件创建后归用户和 Agent 维护。
- `.modular/architecture.yaml` 是架构来源；`.modular/manifest.json` 只负责生成重放与所有权。
- 跨模块调用只依赖 provider 的 `contract`，同步依赖必须形成 DAG，不导入其他模块的 `internal/`。
- proto、Buf 和 wire codegen 不属于模块架构；启用 gRPC 的项目自行维护外部协议并在 adapter 中映射。
- `api` 只做入口映射；简单用例留在 app，只有真实领域复杂度才创建 domain。
- 业务代码不依赖 `app.Application`，也不从全局容器查依赖。
- 跨模块 ACID 流程由发起方模块拥有，只调用公开 contract；项目按用例定义最小事务接口，modular 不提供通用 UoW。
- EventBus 只承诺 best-effort 进程内通知；与外部系统的可靠事件由项目显式实现。
- `cmd` 可以依赖 `github.com/wplbyx/modular/packages/*`，负责把资源、endpoint 和业务实现接起来。
- 未实现的契约必须显式返回 Unimplemented 并携带 `modular:contract-unimplemented`，不能返回空成功响应。
- 框架阶段通过 `make scaffold-check`，Go 契约阶段通过 `make contract-check`，业务完成后通过 `make verify`；覆盖率只报告，不设通用数值门槛。

## Agent 使用方式

仓库内提供一个顶层 `modular` skill。`SKILL.md` 负责路由 init、CRUD、domain、resource、migration 和 audit 工作流；确定性的框架改动由 `modular.py` 完成，业务边界和业务代码由 Agent 根据需求编写。

默认使用原子复制安装；只有本地开发 skill 时才使用符号链接：

```bash
./install-modular-skill.sh modular
./install-modular-skill.sh modular --development-link
```

```powershell
.\install-modular-skill.ps1 modular
.\install-modular-skill.ps1 modular -DevelopmentLink
```

可以这样让 Agent 使用它：

```text
使用 modular skill 初始化一个模块化单体项目，项目名叫 myapp
使用 modular skill 添加 user 和 order 模块，并声明 order 依赖 user
使用 modular skill 为 user 设计简单 CRUD 契约，不要创建 domain 空壳
使用 modular skill 为订单聚合设计领域对象、端口和稳定错误码
使用 modular skill 给项目接入 redis resource
使用 modular skill 审计当前项目结构
```

确定性命令：

| 命令 | 用途 |
| --- | --- |
| `init <project>` | 创建唯一 Application、architecture、项目内工具和可编译框架。 |
| `module add/remove/depend` | 管理 Business Module 与显式依赖。 |
| `transport add/remove` | 修改 Application 的 HTTP/gRPC 能力。 |
| `resource add/remove` | 管理 Application 共享 DB、Redis、Storage、Telemetry 和 EventBus。 |
| `sync` / `prune` | 幂等同步受管文件，或安全删除不再需要且未被修改的受管文件。 |
| `project upgrade` | 更新项目内工具和已发布的 modular 依赖版本。 |
| `doctor` / `verify` | 执行只读审计和 framework/contract/complete 阶段门禁。 |
| `coverage` | 运行无数值门槛的覆盖率报告。 |

Agent 处理这些任务时会按需读取 `agent/modular/references/`：

- `references/workflows/init.md`
- `references/workflows/crud.md`
- `references/workflows/domain.md`
- `references/workflows/resource.md`
- `references/workflows/migration.md`
- `references/workflows/audit.md`

## 开发与验证

仓库是纯 Go 项目，没有 Makefile 或 CI 配置。常用命令：

```bash
go build ./...
go test ./...
go test ./packages/app -v
go vet ./...
gofmt -l .
go mod tidy
```

编辑配置结构体时注意 `packages/config` 下存在 `//go:generate` 指令，依赖外部工具 `gomodifytags`。只有确实需要刷新 `mapstructure` tag 时才运行：

```bash
go generate ./packages/config/...
```

## 重要现实情况

- proto 和 Buf 不属于 modular 架构或脚手架；使用外部 gRPC 的项目自行维护标准协议与生成流程。
- `app` 不导入 `transport`，只接收 `core.Endpoint` 和 `core.Resource`。
- 请求边缘和需要稳定业务 reason 的错误使用 `packages/errs`；生命周期初始化/关闭错误仍使用 `fmt.Errorf("...: %w", err)` 和 `errors.Join`。
- 所有日志方法强制接收 `context.Context`；`log.Default()` 未安装时为 no-op。进程必须在配置加载后第二步创建 `LoggerManager`，再由 `cmd` 显式安装默认 logger。
- `log.NewLoggerManager(&cfg.Logging)` 按 `Output` 创建 console/file 输出，空列表默认 console；telemetry 由 Resource.Setup 后挂载，配置中不能仅有 telemetry。显式输出 Options 优先于 `Output`，不会叠加配置输出；文件轮转由 Manager.Close 停止。
- storage 当前只有 `disk` 和 `oss` 两类实现；OSS 使用 `alibabacloud-oss-go-sdk-v2`，不要引入 v1 SDK。
- DB、Redis、Storage 和 transport client 都通过构造函数显式注入，不提供业务可依赖的包级全局实例。
