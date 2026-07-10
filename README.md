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
| `packages/config` | 基于 Viper 的配置加载和强类型配置结构体，支持文件、环境变量、命令行和远程配置。 |
| `packages/log` | Zap 日志封装，支持控制台、文件轮转、OpenTelemetry 输出；使用包级日志函数。 |
| `packages/errs` | 自定义错误封装，主要用于 resilience 相关能力。 |
| `packages/util` | AES/RSA、随机字符串、URL、请求和 context 工具。 |
| `packages/transport/server/http` | 基于 Gin 的 HTTP endpoint，支持中间件、健康检查、TLS、h2c；构造时即监听端口。 |
| `packages/transport/server/rpc` | gRPC endpoint，支持健康检查、拦截器和 mTLS。 |
| `packages/transport/server/sse` | SSE 服务，可挂载到 HTTP 路由，作为 `core.Endpoint` 管理连接生命周期。 |
| `packages/transport/client` | HTTP / gRPC 客户端封装。保留全局单例能力，但应用装配时优先依赖注入。 |
| `packages/transport/pubsub` | 消息订阅 endpoint 抽象，以及 Kafka、MQTT、RocketMQ、Redis Pub/Sub、Redis Stream 适配。 |
| `packages/registry` | Consul 注册发现、K8s discovery、gRPC resolver；Consul 按 transport 注册服务记录。 |
| `packages/infra/database` | Bun / GORM / MongoDB 数据库连接能力，支持 sqlite、mysql、postgres、clickhouse、mongodb 等配置。 |
| `packages/infra/cache/redis` | go-redis 客户端、布隆过滤器、幂等工具。 |
| `packages/infra/storage` | 统一对象存储接口，当前实现为本地磁盘 `filedisk` 和阿里云 OSS v2 `alioss`。 |
| `packages/telemetry` | OpenTelemetry trace、metric、log provider，作为 `core.Resource` 注入应用。 |
| `packages/resilience` | 熔断、重试、限流、隔板，以及 middleware chain 风格 wrapper。 |
| `packages/patterns` | 缓存模式（Cache-Aside、Write-Through、Write-Behind、Refresh-Ahead）和并发模式。 |
| `packages/pool` | 标准协程池和 ants 协程池适配。 |
| `packages/command` | 基于 struct tag 的命令行参数解析。 |

## 典型使用方式

下游项目通常只在 `cmd/<svc>/main.go` 里直接组装 `modular` 的基础设施。业务代码放在 `internal/`，通过 proto 生成的接口暴露能力；切换 DB、Redis、Storage、HTTP/gRPC 或单体/微服务拓扑时，优先改 `cmd` 装配层。

一个最小 HTTP 应用大致如下：

```go
package main

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/wplbyx/modular/packages/app"
	"github.com/wplbyx/modular/packages/core"
	"github.com/wplbyx/modular/packages/log"
	httpserver "github.com/wplbyx/modular/packages/transport/server/http"

	projectconfig "<project>/config"
)

func main() {
	ctx := context.Background()

	cfg, err := projectconfig.Load("./config")
	if err != nil {
		panic(err)
	}

	logger, err := log.NewLoggerManager(&cfg.Logging, log.WithOutputConsole())
	if err != nil {
		panic(err)
	}
	defer logger.Close()

	httpSrv, err := httpserver.NewServer(&cfg.HTTP)
	if err != nil {
		panic(err)
	}
	httpSrv.RegisterRoute(func(r *gin.Engine) {
		r.GET("/ping", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})
	})

	node := core.NewServiceNode(
		cfg.Application.Name,
		cfg.Application.Version,
		core.Transport{
			Protocol:   "http",
			Address:    core.NormalizeHost(cfg.HTTP.Host),
			Port:       cfg.HTTP.Port,
			HealthPath: httpserver.DefaultHealthPath,
		},
	)

	application, err := app.NewApplication(
		ctx,
		&cfg.Application,
		app.WithServiceNode(node),
		app.WithEndpoint(httpSrv),
	)
	if err != nil {
		panic(err)
	}

	if err := application.Run(); err != nil {
		log.Errorf("application exited: %v", err)
	}
}
```

需要注册中心时，在 `cmd` 中构造 registrar 并注入：

```go
registrar, err := registry.NewConsulRegistry("127.0.0.1:8500")
if err != nil {
	panic(err)
}

application, err := app.NewApplication(
	ctx,
	&cfg.Application,
	app.WithServiceNode(node),
	app.WithRegistrar(registrar),
	app.WithEndpoint(httpSrv),
)
```

需要基础设施时，将其包装或直接构造成 `core.Resource` 后通过 `app.WithResource(...)` 注入。`Resource.Setup` 会在所有 endpoint 启动前执行，`Resource.Close` 会在 endpoint 停止后按反向顺序执行。

### 数据库连接

`packages/infra/database` 当前提供三类连接适配：

| 后端 | 构造函数 | DSN | 说明 |
| --- | --- | --- | --- |
| Bun | `bun.NewBunConnection(cfg *config.Database)` | `database.DSNPostgres` | 仅支持 Postgres，适合需要 Bun ORM 和迁移工具的项目。 |
| GORM | `gorm.NewGormConnection(cfg *config.Database)` | `DSNSqlite`、`DSNMySQL`、`DSNPostgres`、`DSNClickhouse` | 默认 `SkipDefaultTransaction: true`，适合常规关系型数据库。 |
| MongoDB | `mongo.NewMongoConnection(cfg *config.Database)` | `database.DSNMongo` | 使用 `go.mongodb.org/mongo-driver/v2`，支持 `Urls`、单节点 `Host`+`Port`、`ReplicaSet`、`MaxPoolSize`。 |

这些构造函数都会在连接后 ping，并保留包级全局实例用于兼容；业务代码仍应优先接收构造函数返回的实例，而不是直接依赖 `GetDB()` / `GetClient()`。

## 推荐项目分层

使用 `modular` 的业务项目建议采用以下结构：

```text
<project>/
  cmd/
    <svc>/main.go            # 只做配置加载、资源构造、endpoint 注册、Application 装配
  config/
    <svc>/
      config.go              # 项目自己的 Config 聚合类型和 Load 函数
      config.yaml
  common/                    # protoc 生成物，不手写；目录结构镜像 proto/
    <svc>/
      <svc>.pb.go
      <svc>_grpc.pb.go
      <surface>.pb.go
      <surface>_grpc.pb.go
  internal/
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
- `domain` 是每个 `<svc>` 内部的复杂领域层，定义领域端口、充血实体/聚合和领域服务；`domain/service` 只在真实跨实体/聚合领域行为出现时引入，不作为默认空层。
- `app/<surface>/adapter.go` 和 `domain/adapter.go` 是两类接口边界：前者服务简单用例，后者服务复杂领域模型。两者的实现都交给 `repository` 层。
- `repository` 是基础设施实现区：`repository/app` 实现 app 层简单接口，`repository/domain` 实现 domain 层复杂端口，`repository/dto` 和 `repository/model` 按需放 DTO、持久化模型和 ORM/BSON tag。
- `internal/` 的业务逻辑不直接依赖 `github.com/wplbyx/modular/packages/app.Application`。
- 项目自己的 `Config` 聚合类型按业务模块放在 `config/<svc>` 包里，和 `config.yaml` 同目录；`cmd` 只调用对应的 `project/config/<svc>.Load(...)`，不在入口里匿名定义配置结构。
- `cmd` 可以依赖 `github.com/wplbyx/modular/packages/*`，负责把资源、endpoint 和业务实现接起来。

## Agent 使用方式

仓库内提供了一个 Codex skill：`agent/modular`。技能列表里只会显示一个顶层 skill 名称 `modular`；`init`、`service`、`surface`、`method`、`resource`、`gen`、`switch` 是这个 skill 内部的命令语义，不是独立的 `modular:init` 或 `modular:service` 子 skill。

可以这样让 Agent 使用它：

```text
使用 modular skill 初始化一个 single 拓扑项目，项目名叫 myapp
使用 modular skill 给当前项目添加 user service
使用 modular skill 给 user 服务添加 admin 接口面
使用 modular skill 给 user/admin 接口实现 CreateUser 方法骨架
使用 modular skill 给项目接入 redis resource
使用 modular skill 重新生成 proto
使用 modular skill 把当前项目从单体切到微服务拓扑
```

内部命令语义：

| 命令 | 用途 |
| --- | --- |
| `init <project> [single|service]` | 创建下游项目骨架，包含 `go.mod`、buf 配置、`Makefile`、`proto/`、`common/`、`internal/`、`cmd/`、`config/`。 |
| `service <svc>` | 添加业务模块：创建 `config/<svc>`、默认 proto、`internal/<svc>` 的 api/app/domain/repository，并接入 `cmd`。 |
| `surface <svc> <surface>` | 为业务模块添加接口面，例如 `admin`、`management`、`platform`，生成 `proto/<svc>/<surface>.proto`、`api/<surface>`、`app/<surface>`。 |
| `method <svc> <surface> <MethodName>` | 为某个接口面添加 pb 方法骨架，生成或更新 proto rpc，并创建 `app/<surface>/<method>.go` 基础实现文件。 |
| `resource <kind>` | 添加基础设施资源，`kind` 为 `db`、`redis`、`storage`、`telemetry`；`db` 可按项目选择 Bun、GORM 或 MongoDB。 |
| `repository recommend <svc> [surface]` | 根据需求推荐 app/domain adapter 放置，展开 repository 接口签名并输出下一步 scaffold 命令。 |
| `repository app <svc> <surface>` | 为简单 app 用例生成 `app/<surface>/adapter.go` 和 `repository/app` 实现。 |
| `repository domain <svc>` | 为复杂领域模型生成 `domain/adapter.go` 和 `repository/domain` 实现。 |
| `doctor` | 检查旧结构残留、生成目录误写、跨 svc `internal` 引用等约束。 |
| `gen` | 从 `proto/` 重新生成 `common/`。 |
| `switch [single|service]` | 只重写 `cmd` 装配层，在单体和微服务拓扑之间切换。 |

Agent 处理这些任务时会按需读取 `agent/modular/references/`：

- 加服务、接口面、方法骨架或切拓扑：读取 `references/layering.md`。
- 接基础设施资源：读取 `references/infra.md`。
- 修改 `cmd` 生命周期：读取 `references/lifecycle.md`。
- 增加 endpoint 或事件入口：读取 `references/transport.md`。
- 接服务注册发现：读取 `references/registry.md`。
- 调整配置：读取 `references/config.md`。

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
- `packages/errs` 当前主要由 `packages/resilience` 使用；其他包主流写法是 `fmt.Errorf("...: %w", err)` 和 `errors.Join`。
- 日志是包级全局 logger，不走 context；未初始化时 `log.GetLogger()` 返回 `zap.NewNop()`。
- storage 当前只有 `disk` 和 `oss` 两类实现；OSS 使用 `alibabacloud-oss-go-sdk-v2`，不要引入 v1 SDK。
- `infra/cache/redis`、`infra/database`、`transport/client` 保留包级全局能力，但应用装配时应优先把返回实例作为依赖注入。
