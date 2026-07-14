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
| `packages/log` | Zap 日志封装，支持控制台、文件轮转、OpenTelemetry 输出；使用包级日志函数。 |
| `packages/errs` | 自定义错误封装，主要用于 resilience 相关能力。 |
| `packages/util` | AES/RSA/ECC、随机字符串、URL、HTTP 请求和 context 工具。 |
| `packages/transport/server/http` | 基于 Gin 的 HTTP endpoint，支持中间件、健康检查、TLS、h2c；构造时即监听端口。 |
| `packages/transport/server/rpc` | gRPC endpoint，支持健康检查、拦截器和 mTLS。 |
| `packages/transport/server/sse` | SSE 服务，可挂载到 HTTP 路由，作为 `core.Endpoint` 管理连接生命周期。 |
| `packages/transport/client` | HTTP / gRPC 客户端封装。保留全局单例能力，但应用装配时优先依赖注入。 |
| `packages/transport/pubsub` | 消息订阅 endpoint 抽象，以及 Kafka、MQTT、RocketMQ、Redis Pub/Sub、Redis Stream 适配。 |
| `packages/registry` | Consul 注册发现、K8s discovery、gRPC resolver；Consul 按 transport 注册服务记录。 |
| `packages/infra/database` | Bun / GORM / MongoDB 数据库连接能力；Bun、GORM 提供可直接注入 Application 的 Resource。 |
| `packages/infra/cache/redis` | go-redis 客户端、布隆过滤器、幂等工具。 |
| `packages/infra/storage` | 对象存储富接口与可选直传预签名接口，当前实现为本地磁盘 `filedisk` 和阿里云 OSS v2 `alioss`。 |
| `packages/telemetry` | OpenTelemetry trace、metric、log provider，作为 `core.Resource` 注入应用。 |
| `packages/resilience` | 熔断、重试、限流、隔板，以及 middleware chain 风格 wrapper。 |
| `packages/patterns` | 缓存模式（Cache-Aside、Write-Through、Write-Behind、Refresh-Ahead）和并发模式。 |
| `packages/pool` | `WorkerPool` 抽象与 ants 协程池实现。 |

## 典型使用方式

下游项目通常只在 `cmd/<process>/main.go` 里组装 `modular` 基础设施。业务代码放在 `internal/`，通过 proto 生成的接口暴露能力；切换 DB、Redis、Storage、HTTP/gRPC 或进程拓扑时，优先改 `cmd` 装配层。

配置入口推荐使用 `config.NewRoot[T]`。它会扫描业务聚合配置中实现 `FlagProvider` 的模块，注册 Cobra flags，并按以下优先级合并：

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
	httpserver "github.com/wplbyx/modular/packages/transport/server/http"

	projectconfig "<project>/config/user"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	command := modularconfig.NewRoot[projectconfig.Config](modularconfig.Options[projectconfig.Config]{
		AppName:     "user",
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
	logger, err := log.NewLoggerManager(&cfg.Logging, log.WithOutputConsole())
	if err != nil {
		return fmt.Errorf("create logger: %w", err)
	}
	defer logger.Close()

	httpSrv, err := httpserver.NewServer(&cfg.HTTP)
	if err != nil {
		return fmt.Errorf("create HTTP server: %w", err)
	}
	httpSrv.RegisterRoute(func(r *gin.Engine) {
		r.GET("/ping", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})
	})

	node := core.NewServiceNode(cfg.Name, cfg.Version, httpSrv.Transport())

	application, err := app.NewApplication(
		ctx,
		&cfg.Application,
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
  --http.port 18080
```

single 拓扑的进程配置会嵌套多个 svc，此时模块参数也带 svc 前缀，例如 `--user.http.port`、`--billing.redis.host`。

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

| 后端 | 连接构造函数 | Application Resource | DSN |
| --- | --- | --- | --- |
| Bun | `bun.NewBunConnection(cfg)` | `bun.NewResource(cfg)` | `database.DSNPostgres` |
| GORM | `gorm.NewGormConnection(cfg)` | `gorm.NewResource(cfg)` | `database.DSNSqlite`、`DSNMySQL`、`DSNPostgres`、`DSNClickhouse` |
| MongoDB | `mongo.NewMongoConnection(cfg)` | 当前需要项目侧 Resource 包装 | `database.DSNMongo` |

这些连接构造函数都会在连接后 ping，并保留包级全局实例用于兼容。应用装配优先注入 `bun.NewResource` / `gorm.NewResource`，或接收构造函数返回的实例，不直接依赖 `GetDB()` / `GetClient()`。Redis 同样提供 `redis.NewResource(cfg)`。

## 推荐项目分层

使用 `modular` 的业务项目建议采用以下结构：

```text
<project>/
  cmd/
    <svc>/main.go            # 只做配置加载、资源构造、endpoint 注册、Application 装配
  config/
    <project>/               # single 拓扑进程配置，由 skill 聚合生成
      config.go
      config.yaml
    <svc>/
      config.go              # svc Config 聚合类型，同时实现 FlagProvider
      config.yaml            # svc 配置片段；service 拓扑直接作为运行配置
      resources.json         # skill 的资源装配元数据
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
- 项目自己的 svc `Config` 聚合类型放在 `config/<svc>`，并通过 `GetConfigFlagSpecsWithPrefix` 实现 `FlagProvider`。
- service 拓扑由 `cmd/<svc>` 使用 `NewRoot[config/<svc>.Config]`；single 拓扑由 `cmd/<project>` 使用生成的 `config/<project>.Config`，其中嵌套各 svc 配置。
- single 的 `config/<project>/config.go|yaml` 是 skill 生成物；业务配置以 `config/<svc>` 为来源，重新执行 scaffold/resource 命令会刷新进程聚合配置。
- `cmd` 可以依赖 `github.com/wplbyx/modular/packages/*`，负责把资源、endpoint 和业务实现接起来。

## Agent 使用方式

仓库内提供了一个 Codex skill：`agent/modular`。技能列表里只会显示一个顶层 skill 名称 `modular`；`init`、`service`、`surface`、`method`、`resource`、`repository`、`doctor`、`gen` 是这个 skill 内部的命令语义，不是独立的子 skill。

可以这样让 Agent 使用它：

```text
使用 modular skill 初始化一个 single 拓扑项目，项目名叫 myapp
使用 modular skill 给当前项目添加 user service
使用 modular skill 给 user 服务添加 admin 接口面
使用 modular skill 给 user/admin 接口实现 CreateUser 方法骨架
使用 modular skill 给项目接入 redis resource
使用 modular skill 重新生成 proto
使用 modular skill 审计当前项目结构
使用 modular skill 规划从单体到微服务的 cmd/config 迁移
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

拓扑迁移是 Agent 工作流，不是 `modular.py` 子命令。迁移时只调整进程级 `cmd` 和配置聚合，不改写 `proto/`、`common/` 或 `internal/` 业务代码。

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
