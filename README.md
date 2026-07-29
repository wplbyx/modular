# modular

`modular` 是一套 Go 基础设施模块化积木库（module path: `github.com/wplbyx/modular`，Go 1.26+）。它不是业务框架，也不接管业务代码；它提供可复用的基础设施组件、生命周期编排、配置加载、服务注册发现、传输层适配和常用工程模式，让业务项目通过依赖注入把应用组装起来。

核心目标：

- `Application` 只负责编排生命周期，不处理业务逻辑。
- `core.Endpoint` 表示接收流量或事件的入口，例如 HTTP、gRPC、SSE、消息订阅。
- `core.Resource` 表示支撑性基础设施，例如 DB、Redis、Storage、Telemetry。
- `core.ServiceNode` 表示服务实例身份，用于注册与发现；`Endpoint.Name()` / `Resource.Name()` 只作为日志标签。
- 业务层应通过 proto 生成的接口解耦，单体和微服务切换只改 `cmd` 装配层。

## 核心模型

| 模型 | 包 | 职责 |
| --- | --- | --- |
| `core.Resource` | `packages/core` | 基础设施生命周期：`Setup(ctx)` / `Close(ctx)`，不阻塞，不接流量。 |
| `core.Endpoint` | `packages/core` | 服务入口生命周期：`Startup(ctx)` / `Shutdown(ctx)`；`Startup` 必须阻塞到服务停止。 |
| `core.ServiceNode` | `packages/core` | 一个 `Application` 对应一个服务节点，包含服务名、版本、实例 ID 和多个 transport。 |
| `registry.Registrar` | `packages/registry` | 将 `ServiceNode` 注册到 Consul 等注册中心。 |
| `registry.Discovery` | `packages/registry` | 按服务名发现实例，或 watch 实例变化。 |
| `app.Application` | `packages/app` | 统一管理 Resource、Endpoint、Registrar 和 ServiceNode 的启动与关闭顺序。 |

`Application.Run` 的顺序固定：

```text
Resource.Setup()  FIFO
  -> Registrar.Register(ServiceNode)
  -> Endpoint.Startup()  并行阻塞
  -> Endpoint.Shutdown() 并行
  -> Registrar.Unregister(ServiceNode)
  -> Resource.Close()    LIFO
```

零 endpoint 的 `Application` 会打印 warning 并立即返回。接入 `Application` 的 endpoint 必须保证 `Startup` 在正常运行时阻塞，且 `Shutdown` 能解除阻塞。

## 模块总览

| 模块 | 内容 |
| --- | --- |
| `packages/core` | 零依赖核心抽象：`Resource`、`Endpoint`、`Transport`、`ServiceNode`。 |
| `packages/app` | 应用生命周期编排器，提供 `WithServiceNode`、`WithRegistrar`、`WithResource`、`WithEndpoint`。 |
| `packages/config` | Viper 配置加载器与 Cobra 命令集成，支持本地文件、远程 KV、环境变量和自动模块 flags。 |
| `packages/config/configitem` | 可组合的强类型基础设施配置：Application、HTTP、GRPC、Database、Redis、Storage、Logging、Telemetry 和消息中间件。 |
| `packages/log` | Context 强制日志接口；Zap 输出与基于 `cyub/ringbuffer` 的异步分发，支持文件轮转、动态 OpenTelemetry sink 和有界降级。 |
| `packages/metadata` | 不可变、分 scope 的上下文元数据，统一 HTTP/gRPC/消息 Header 与 OTel trace context 穿透。 |
| `packages/eventbus` | 进程内有序 EventBus Resource；队列数据直接存放在 `cyub/ringbuffer.MpscRingBuffer`。 |
| `packages/errs` | Kratos 风格统一错误、多语言 YAML Catalog、错误链/堆栈诊断与客户端/日志分流。 |
| `packages/generate` | 可安装的代码生成工具；`err_template_gen` 从 `errs.Define` 生成并校验多语言 YAML。 |
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

下游项目通常只在 `cmd/<process>/main.go` 里组装 `modular` 基础设施。业务代码放在 `internal/`，通过 proto 生成的接口暴露能力；切换 DB、Redis、Storage、HTTP/gRPC 或进程拓扑时，优先改 `cmd` 装配层。

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
	loggerManager, err := log.NewLoggerManager(&cfg.Logging, log.WithOutputConsole())
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

	node := core.NewServiceNode(cfg.Application.Name, cfg.Application.Version, httpSrv.Transport())

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

single 拓扑的进程配置会嵌套多个 svc，此时模块参数也带 svc 前缀，例如 `--User.HTTP.Port`、`--Billing.Redis.Host`。环境变量使用 `_` 表示层级，例如 `APP_STORAGE_OSS_ACCESSKEYID`；字段单词内部不插入下划线。

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

HTTP server 默认 `/health` 只表示进程存活。通过 `httpserver.WithReadiness(path, checkers...)` 注入 `health.Checker` 后，就绪接口返回 200/503，且 `Transport.HealthPath` 会指向 readiness 路径。流式响应可将 `WriteTimeout` 设为 `httpserver.NoWriteTimeout`。

HTTP client 是显式构造的 `*httpclient.Client`，主接口为 `Do(*http.Request)`。重试仅适用于可重放的幂等请求；POST/PATCH 需要 `Idempotency-Key` 或自定义 `RetryPolicy`。

## 推荐项目分层

v2 脚手架把基础设施框架和业务实现分开。第一阶段由 CLI 生成可编译的进程、配置、transport、Resource 和 wiring；第二阶段由 Agent 根据需求写 proto、端口和稳定错误码；第三阶段再实现业务并用单测保证行为。

```text
<project>/
  .modular/
    manifest.json            # 文件所有权、生成哈希、模板版本和最小生成来源
    profile.toml             # 当前项目的附加检查策略
    make/modular.mk          # 受管 Make 目标
    tool/                    # 项目内可迁移的脚手架及模板
  cmd/
    <process>/main.go        # 受管入口
    <process>/framework.gen.go
  config/
    <project>/               # single 拓扑进程配置，由 skill 聚合生成
      config.gen.go
      config.yaml
    <svc>/
      config.gen.go          # transport/Resource 字段，受管
      config.go              # 一次性模板，之后由用户维护
      config.yaml            # 受管配置片段；service 拓扑直接作为运行配置
  common/                    # protoc 生成物，不手写；目录结构镜像 proto/
    <svc>/
      <svc>.pb.go
      <svc>_grpc.pb.go
      <surface>.pb.go
      <surface>_grpc.pb.go
  internal/
    platform/wiring/
      framework.gen.go       # transport hook 和 Resource Provider 类型
      business.go            # 一次性 wiring 接缝，由 Agent/用户维护
    <svc>/
      api/                   # 入站适配器：HTTP/gRPC/event 映射到对应接口面
        <surface>/           # admin / management / platform / openapi ...
          grpc.go
          http.go
          event.go
          v1/                # 可选：只有 proto 接口面引入版本时才出现
      app/                   # 默认实现 pb XxxServiceServer，并编排用例：直接调用Repository(简单的MVC), 转到调用domain走复杂的业务逻辑
        <surface>/           # 与 api/<surface> 对齐
          adapter.go         # 业务相关接口在这里定义：可读写分离，简单的直接repository层实现(走dto), 复杂的依赖 domain 接口。
          server.go          # 该接口面的 pb server 实例和依赖注入, 简单的可以直接调用 repository/ 实现
          <method>.go        # 一个 pb 方法一个文件，例如 CreateUser -> create_user.go
          v1/                # 可选：版本化 pb server adapter
      domain/
        adapter.go           # 领域相关接口，仓储接口/端口：command/query 可拆分
        entity/              # 充血领域对象、聚合根，内聚的逻辑
        service/             # 领域逻辑？这里是什么领域逻辑
      repository/            # 出站适配器：DB/Redis/client/storage 等脏活累活 （内部的包随便拆分[都是基础设施]）
        app/                 # app 层简单的接口实现
        domain/              # domain 层业务逻辑的接口实现
        dto/                 # model 数据的操作的封装实现
        model/               # 数据模型
  proto/
    <svc>/
      <svc>.proto            # 按业务模块分包；默认不再细分 v1/v2
      <surface>.proto        # 可选：admin / management / platform 等接口面      
  go.mod    

```

约束：

- `service add` 必须显式选择 `--transport http|grpc`；未选择的 transport、domain、repository 和 event 层不会生成。
- `managed` 文件只有在当前哈希与 manifest 一致时才更新；`scaffold-once` 文件创建后归用户和 Agent 维护。
- `.modular/manifest.json` 不是架构决策文档，只记录脚手架重放所需的所有权、哈希、模板版本和最小来源，用于冲突检测、幂等同步、升级和安全删除。
- 跨领域调用走生成的 pb client，不导入其他领域的 `internal/`。
- `proto/` 和 `common/` 都按业务模块分包：`proto/<svc>/...` 生成到 `common/<svc>/...`，与 `internal/<svc>/...` 对齐；这里的 `<svc>` 是最外层业务模块名。
- 一个业务模块可以有多个接口面（surface），例如 `admin`、`management`、`platform`、`openapi`。接口面是外部契约维度，不是领域模型维度。
- `surface` 名称也是 Go 包名，使用 lower_snake_case，不使用连字符，默认不带版本后缀。
- 多接口面默认按 `proto/<svc>/<surface>.proto`、`common/<svc>/<surface>.pb.go`、`internal/<svc>/api/<surface>`、`internal/<svc>/app/<surface>` 对齐。单接口面可以继续使用 `proto/<svc>/<svc>.proto`。
- 当前默认不严格区分 `v1/v2`；未来如果某个接口面引入版本，优先放在 `proto/<svc>/<surface>/v1`，并镜像到 `common/<svc>/<surface>/v1`、`internal/<svc>/api/<surface>/v1`、`internal/<svc>/app/<surface>/v1`。
- `common/` 是生成目录，不手写 `.pb.go` 或 `_grpc.pb.go`。
- `api` 属于基础设施入站适配层，只做流量入口、路由/订阅、请求解析和映射，不放业务规则。
- `app/<surface>` 默认实现生成的 `XxxServiceServer`，同时编排用例流程。简单 CRUD/MVC 流程可以依赖本包 `adapter.go` 里定义的 `QueryRepository` / `CommandRepository`，由 `repository/app` 直接实现；复杂业务流程可以依赖 `internal/<svc>/domain` 里的领域定义和端口。
- `app/<surface>/server.go` 放该接口面的 server 类型、构造函数和依赖注入；每个 pb 方法放到独立文件，文件名与方法名一一对应（Go 文件名用 snake_case，例如 `CreateUser` -> `create_user.go`）。
- `service/` 不是默认层。只有当 pb 契约和内部用例模型明显分离、多版本 pb 需要复用同一组用例、或一个 pb service 需要组合多个 app 子模块时，才引入 `service` 作为额外适配层。
- 简单 CRUD 把端口放在 `app/<surface>`，不创建空 domain；只有不变量、聚合协调、策略或事务规则需要领域接缝时才创建 `domain`。
- `app/<surface>/adapter.go` 和 `domain/adapter.go` 是两类接口边界：前者服务简单用例，后者服务复杂领域模型。两者的实现都交给 `repository` 层。
- `repository` 是基础设施实现区：`repository/app` 实现 app 层简单接口，`repository/domain` 实现 domain 层复杂端口，`repository/dto` 和 `repository/model` 按需放 DTO、持久化模型和 ORM/BSON tag。
- `internal/` 的业务逻辑不直接依赖 `github.com/wplbyx/modular/packages/app.Application`。
- 项目自己的 svc `Config` 聚合类型放在 `config/<svc>`，并通过 `GetConfigFlagSpecsWithPrefix` 实现 `FlagProvider`。
- service 拓扑由 `cmd/<svc>` 使用 `NewRootCommand[config/<svc>.Config]`；single 拓扑由 `cmd/<project>` 使用生成的 `config/<project>.Config`，其中嵌套各 svc 配置。
- single 的 `config/<project>/config.gen.go|yaml` 是受管文件；业务配置以 `config/<svc>` 为来源，重新执行 scaffold/resource 命令会刷新进程聚合配置。
- `cmd` 可以依赖 `github.com/wplbyx/modular/packages/*`，负责把资源、endpoint 和业务实现接起来。
- 未实现的契约必须显式返回 Unimplemented 并携带 `modular:contract-unimplemented`，不能返回空成功响应。
- 框架阶段通过 `make scaffold-check`，契约阶段通过 `make contract-check`，业务完成后通过 `make verify`；覆盖率只报告，不设通用数值门槛。

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
使用 modular skill 初始化一个 single 拓扑项目，项目名叫 myapp
使用 modular skill 给当前项目添加只暴露 HTTP 的 user svc
使用 modular skill 为 user 设计简单 CRUD 契约，不要创建 domain 空壳
使用 modular skill 为订单聚合设计领域对象、端口和稳定错误码
使用 modular skill 给项目接入 redis resource
使用 modular skill 审计当前项目结构
使用 modular skill 把 single 拓扑迁移为每个 svc 一个进程
```

确定性命令：

| 命令 | 用途 |
| --- | --- |
| `init <project> --topology single|service` | 创建项目内工具、manifest、Make 目标和可编译框架。 |
| `service add/remove` | 管理 svc 配置和进程装配；添加时必须显式选择 transport。 |
| `transport add/remove` | 修改已有 svc 的 HTTP/gRPC 选择。 |
| `resource add/remove` | 管理 DB、Redis、Storage、Telemetry 和进程内 EventBus 资源装配。 |
| `sync` / `prune` | 幂等同步受管文件，或安全删除不再需要且未被修改的受管文件。 |
| `migrate topology` | 只迁移受管的 cmd/config 进程文件，保留业务 wiring 和业务包。 |
| `project upgrade` | 更新项目内工具和已发布的 modular 依赖版本。 |
| `doctor` / `verify` | 执行只读审计和 framework/contract/complete 阶段门禁。 |
| `gen` / `coverage` | 运行 buf 生成和无数值门槛的覆盖率报告。 |

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

- 仓库自身没有 `.proto`、`_pb.go`、buf/protoc 生成链路；proto-first 是下游业务项目的约定，`agent/modular` 会为下游项目生成骨架。
- `app` 不导入 `transport`，只接收 `core.Endpoint` 和 `core.Resource`。
- 请求边缘和需要稳定业务 reason 的错误使用 `packages/errs`；生命周期初始化/关闭错误仍使用 `fmt.Errorf("...: %w", err)` 和 `errors.Join`。
- 所有日志方法强制接收 `context.Context`；`log.Default()` 未安装时为 no-op。进程必须在配置加载后第二步创建 `LoggerManager`，再由 `cmd` 显式安装默认 logger。
- storage 当前只有 `disk` 和 `oss` 两类实现；OSS 使用 `alibabacloud-oss-go-sdk-v2`，不要引入 v1 SDK。
- `infra/cache/redis`、`infra/database`、`transport/client` 保留包级全局能力，但应用装配时应优先把返回实例作为依赖注入。
