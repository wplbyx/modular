# packages 代码审计与优化建议

审计日期：2026-07-09

## 0. 实施状态（2026-07-09）

已按本文件优先级完成一批修复并补测试：SSE/HTTP/gRPC Endpoint 生命周期，DiskStorage prefix 安全，Application 只关闭已成功 Setup 的 Resource，Logger 初始化失败不污染全局，Bulkhead panic/关闭竞态，Pub/Sub SubscriberEndpoint 深化，`errs` 状态保护，`util` URL/request 修复，K8s/Consul registry 取消与上下文语义，HTTP/gRPC client 小修，`app.yml` 配置可信度，HTTP bind 类型边界，command required/重复 flag 语义，Redis/GORM/Bun Resource wrapper，Storage 小接口与 OSS option 修正，Telemetry Setup 初始化，caching cache miss 语义，Memoizer 并发 miss 抑制，RateLimiter middleware 等待语义。

仍建议后续单独推进的较大重构：`resilience` 全部执行器统一为 `Executor` 签名、`WriteBehind` error sink/死信/重试策略、`RefreshAhead` 刷新任务纳入完整 wait group 与 per-key in-flight、`pool` future/error handler 与 `Close(ctx)`、Telemetry/Gin 彻底解耦。这些属于接口扩展或行为设计面较大的长期项，不宜混在本轮修 bug 的提交里继续扩大破坏面。

## 1. 审计范围与验证基线

本次只审计 `packages/` 下代码，不修改业务实现。仓库当前已有用户变更：

- `packages/video/video.go` 已删除
- `packages/video/video_test.go` 已删除

这两个删除不属于本次审计建议范围，本文件不要求恢复。

已执行的验证命令：

```bash
go test ./packages/...
go vet ./packages/...
go test -race ./packages/resilience ./packages/patterns/... ./packages/pool ./packages/transport/server/sse ./packages/infra/storage/filedisk ./packages/infra/cache/redis
gofmt -l packages
```

结果：

- `go test ./packages/...` 通过。
- `go vet ./packages/...` 通过。
- 指定高风险并发包的 `go test -race` 通过，但 `packages/resilience` 和 `packages/transport/server/sse` 没有测试文件。
- `gofmt -l packages` 发现格式漂移：
  - `packages/core/node.go`
  - `packages/core/node_test.go`
  - `packages/resilience/wrapper.go`
  - `packages/transport/pubsub/endpoint.go`
  - `packages/transport/pubsub/event.go`

## 2. 总体判断

`core` / `app` 的大方向是正确的。`Application` 只依赖 `core.Endpoint`、`core.Resource`、`registry.Registrar`，没有反向导入 `transport`，这一点符合仓库的积木式设计目标。真正需要优先处理的问题不是抽象方向，而是部分 adapter 没有严格遵守 interface 的隐含契约。

当前最重要的 interface 契约是：

- `core.Endpoint.Startup(ctx)` 必须阻塞到服务停止。
- `core.Endpoint.Shutdown(ctx)` 必须解除 `Startup` 阻塞。
- `core.Resource.Setup(ctx)` / `Close(ctx)` 是基础设施生命周期 seam。
- `ServiceNode` 是服务身份，`Endpoint.Name()` / `Resource.Name()` 只是日志标签。

主要风险集中在六类：

1. Endpoint 生命周期契约未完全落实，尤其是 SSE、HTTP 构造即监听、gRPC `Port=0` 注册。
2. Registry watch seam 过窄，Consul/K8s 行为不一致，取消、错误、关闭语义不清。
3. 配置样例与实际结构漂移，`app.yml` 已不可信。
4. Redis/GORM/Bun/HTTP client/worker pool 保留包级全局，和依赖注入理念冲突。
5. 并发模块缺测试，`resilience` 存在 race/panic 风险但没有 `_test.go`。
6. 一些 utility/package-level interface 偏浅，把错误处理、context、重试、生命周期策略留给调用方。

后续优化建议按 P0/P1/P2/P3 排序。P0 是可能导致泄漏、panic、错误注册、安全逃逸的问题；P1 是 interface 深化和契约收敛；P2 是配置、文档和兼容性；P3 是测试和长期质量门禁。

## 3. P0：优先修复的高风险问题

### 3.1 SSE Endpoint 违反 Startup/Shutdown 契约

涉及文件：

- `packages/transport/server/sse/server.go`

问题：

- `Startup(ctx)` 只等待传入的 `ctx.Done()`。
- `Shutdown(ctx)` 只清理客户端，并不会取消 `Startup` 正在等待的 context。
- 如果 `Application.shutdownEndpoints` 调用 `SSE.Shutdown`，它不能保证解除 `Startup` 阻塞，违反 `core.Endpoint` interface 契约。
- 重复 `client_id` 连接时，只从 map 删除旧连接，没有关闭旧 `MsgChan`，旧 handler 会悬挂到客户端断开。

建议：

- 在 `Server` 内部持有 `startupCtx` 的 `cancel`，`Shutdown` 必须调用它。
- `Shutdown` 和重复登录都要安全关闭旧 client channel，避免 double close。
- 增加 `server/sse` 测试：
  - `Startup` 启动后，只调用 `Shutdown`，`Startup` 必须返回。
  - 重复 `client_id` 时旧连接必须退出。
  - `Publish` / `Notify` 在 `Shutdown` 后不能向已关闭 channel panic。

### 3.2 HTTP server 构造即监听后的 fd 泄漏

涉及文件：

- `packages/transport/server/http/server.go`
- `packages/transport/server/http/server_test.go`

问题：

- `NewServer` 构造时已经 `net.Listen`。
- 多个测试或调用方会 `NewServer` 后只调用 `Shutdown`，但如果从未 `Startup`，`http.Server.Shutdown` 不一定释放预创建的 listener。
- `Port=0` 的真实端口只能从私有字段 `listener` 拿，外部 `cmd` 层无法构建正确 `ServiceNode`。

建议：

- `Shutdown` 需要覆盖未启动状态，确保关闭 `listener`。
- 增加 `Addr()` 或 `Transport()` concrete helper，让外部可读取真实地址和端口。
- 不要把该 helper 加到 `core.Endpoint`，避免 `app` 需要知道 transport 细节。
- 增加测试：
  - `NewServer` 后直接 `Shutdown`，同端口可再次监听。
  - `Port=0` 可通过公开方法取到真实端口。

### 3.3 gRPC `Port=0` 与服务注册时序冲突

涉及文件：

- `packages/transport/server/rpc/server.go`
- `packages/app/application.go`

问题：

- HTTP server 是构造即监听，`Port=0` 构造后可拿真实端口。
- gRPC server 在 `Startup` 才 `net.Listen`。
- `Application.Run` 在 `Endpoint.Startup` 前先 `registrar.Register(node)`，如果 gRPC 配置 `Port=0`，注册中心会拿到 `:0`。

建议：

- 选一个策略并统一文档：
  - gRPC 也改为构造即监听，和 HTTP 保持一致。
  - 或明确禁止 gRPC endpoint 注册时使用 `Port=0`，配置校验提前报错。
- 如果改为构造即监听，同样提供 `Addr()` / `Transport()` helper。
- 增加测试覆盖 `Port=0` 的注册前真实端口可见性。

### 3.4 K8s registry watch 可能 panic

涉及文件：

- `packages/registry/registry_k8s.go`

问题：

- `Watch` 每次调用都会向共享 informer 添加 handler。
- 返回 channel 在 goroutine 退出时关闭，但 handler 没有移除。后续 informer 事件可能向已关闭 channel send，导致 panic。
- `endpointsToServiceNodes` 直接使用 `addr.TargetRef.Name`，手工 Endpoints 或 external endpoints 可能 `TargetRef == nil`。

建议：

- 使用 `AddEventHandler` 返回的 handle，在 watch 结束时移除 handler，或改为每个 registry 内部维护 watcher fanout。
- `sendUpdate` 需要检查 watcher 是否仍 active。
- `TargetRef == nil` 时使用 `addr.IP` 或 `serviceName + addr.IP + port` 生成稳定 ID。
- 增加 fake clientset 测试：
  - `ctx` 取消后再触发 endpoints update，不 panic。
  - `TargetRef == nil` 能正常转换。

### 3.5 Consul registry 忽略 context，Watch 错误被吞

涉及文件：

- `packages/registry/consul.go`

问题：

- `Register`、`Unregister`、`GetService` 没有把 `ctx` 传入 Consul SDK 的 write/query option。
- `Watch` 长轮询 `WaitTime=5m`，取消可能最多卡住 5 分钟。
- `Watch` 遇到错误时直接 `continue`，没有 backoff，也没有把错误暴露给 resolver/caller。

建议：

- Consul SDK 调用使用带 context 的 options。
- `Watch` 遇错要有 backoff，避免 tight loop。
- 明确 `Discovery.Watch` 是否传递错误：
  - 保持 `<-chan []*ServiceNode`，adapter 内部处理重连和日志。
  - 或升级为事件流，例如 `WatchEvent{Nodes []*ServiceNode, Err error}`。
- gRPC resolver 需要按新的 watch contract 处理错误和空地址。

### 3.6 DiskStorage PrefixIterator 路径逃逸

涉及文件：

- `packages/infra/storage/filedisk/disk.go`

问题：

- `GenKeyToFilePath` 有路径穿越防护。
- 但 `PrefixIterator` 直接 `filepath.Join(s.rootDir, filepath.FromSlash(prefix))`，没有经过 `GenKeyToFilePath`。
- `../` prefix 可能遍历 root 外路径并泄露元信息。

建议：

- `PrefixIterator` 和 `DeleteByPrefix` 的 prefix 入口统一走安全路径校验。
- 空 prefix 可以作为遍历 root 的显式特例处理，不能绕过安全校验。
- 增加测试：
  - `PrefixIterator("../")` 返回错误。
  - `DeleteByPrefix("../")` 返回错误。
  - `PrefixIterator("")` 仍按预期遍历 root。

### 3.7 Bulkhead 并发关闭竞态与 panic 后计数错误

涉及文件：

- `packages/resilience/bulkhead.go`

问题：

- `Execute` 先检查 `closed`，再向 `queue` 发送 task。
- `Close` 可能在检查和发送之间关闭 channel，导致 send on closed channel panic。
- task 执行没有 `defer`/`recover`，如果 `fn()` panic，`running` 不会回落。

建议：

- 使用 semaphore 或明确的 worker 生命周期模型替代裸 close channel。
- `Close(ctx)` 等待 worker 退出，并和提交任务处于同一个锁域或状态机下。
- task wrapper 中使用 `defer` 确保 `running--`。
- 是否 recover panic 要形成明确策略：返回 error 或继续 panic，但计数必须恢复。
- 新增 `packages/resilience` 测试，优先覆盖 close race、panic recovery、running 计数。

### 3.8 Logger 全局初始化失败后可能污染状态

涉及文件：

- `packages/log/logger.go`

问题：

- `NewLoggerManager` 在检查 `cores` 非空前就赋值全局 `lm`。
- 如果返回 `logger config output is empty`，全局函数里 `lm != nil` 但 `lm.logger == nil`，调用 `Info/Error/...` 会 panic。

建议：

- 使用局部 `manager` 完整构建成功后再赋值全局 `lm`。
- 所有包级日志函数统一走 `GetLogger()` 或显式判空。
- 增加测试：
  - 初始化失败后调用 `log.Info` 不 panic。
  - 成功初始化后全局 logger 可用。

## 4. P1：interface 深化与契约收敛

### 4.1 为所有 Endpoint adapter 增加契约测试

涉及包：

- `packages/transport/server/http`
- `packages/transport/server/rpc`
- `packages/transport/server/sse`
- `packages/transport/pubsub`

建议定义一组共享测试思路：

- `Startup` 在正常运行中必须阻塞。
- `Shutdown` 必须让 `Startup` 返回。
- `Startup` 提前返回 nil 或非 context 错误时，`Application.Run` 应触发整体 shutdown。
- 重复 `Shutdown` 应该是幂等或至少不 panic。

不建议把测试 helper 放进 `core` 以增加依赖。可以在各包本地测试里复制小 helper，或新建内部测试包。

### 4.2 Application 只关闭已成功 Setup 的 Resource

涉及文件：

- `packages/app/application.go`

问题：

- `setupResources` 如果第 N 个 resource 初始化失败，`triggerShutdown` 会调用 `closeResources`，当前逻辑会倒序关闭所有资源，包括未成功 Setup 的资源。
- 如果某些 Resource 的 `Close` 假设 `Setup` 成功，可能出现误报或 panic。

建议：

- `Application` 记录已成功 Setup 的 resources。
- `closeResources` 只关闭已成功 Setup 的 resources。
- `Close(ctx)` 与 `Run` 的 shutdown once 语义收敛。当前 `Run` 内部有局部 `shutdownOnce`，但手动 `Close` 不共享它。
- 增加测试：
  - 第二个 resource Setup 失败，只关闭第一个。
  - `Run` 与手动 `Close` 并发时 shutdown 只执行一次。

### 4.3 SubscriberEndpoint 应成为更深的 module

涉及文件：

- `packages/transport/pubsub/endpoint.go`
- `packages/transport/pubsub/pubsub.go`

问题：

- `SubscriberEndpoint` 对 `Subscribe` 的隐含要求是“订阅后立即返回，由底层 goroutine 消费”。
- 这个行为不在 `Subscriber` interface 中声明，adapter 作者容易写成阻塞式 Subscribe，导致 Endpoint 的 `Startup` 语义混乱。
- `SubscriberEndpoint` 结构体里有 `opts []SubscribeOption`，但 `NewSubscriberEndpoint` 没有对外注入路径。
- `Connector` / `Disconnector` 已定义，但调用方仍要手写 `WithConnect(sc.Connect)`、`WithDisconnect(sc.Disconnect)`。

建议：

- 明确 `Subscriber.Subscribe` 必须是非阻塞订阅；或拆出 `AsyncSubscriber` / `LifecycleSubscriber`。
- `NewSubscriberEndpoint` 自动识别 `sub` 是否实现 `Connector` / `Disconnector`。
- 增加 `WithSubscribeOptions(...SubscribeOption)`，让 QoS、QueueName 等能真正传入。
- `Shutdown` 使用 `errors.Join` 聚合 disconnect/close 错误，而不是字符串拼接。

### 4.4 Pub/Sub 统一 interface 的协议差异需要显式化

涉及包：

- `packages/transport/pubsub/kafka`
- `packages/transport/pubsub/mqtt`
- `packages/transport/pubsub/redis`
- `packages/transport/pubsub/rocket`

问题：

- `PublishOptions` 包含 `QoS`、`Retained`、`Key`、`Headers`。
- MQTT、Kafka、Redis Stream、RocketMQ 对这些字段的支持不同，部分 adapter 会忽略字段。
- 调用方必须了解协议差异，interface 的 leverage 不够。

建议：

- 在 `pubsub` 文档中写清每个 adapter 支持/忽略的 option 矩阵。
- 对忽略字段采用明确策略：
  - 静默忽略，但文档固定。
  - 或提供 strict mode，遇到不支持 option 返回错误。
- Redis Stream 的 `XACK` 失败不能只有日志，至少提供 error hook/metric 或 pending reclaim 策略。
- MQTT handler 当前使用 `context.Background()`，应绑定订阅生命周期 context，避免取消后 handler 继续无界运行。

### 4.5 Registry resolver scheme 与 discovery seam 不一致

涉及文件：

- `packages/registry/resolver.go`

问题：

- `NewGRPCResolverBuilder(discovery Discovery)` 看起来可以接任意 discovery。
- 但 `Scheme()` 固定返回 `"consul"`，`BuildConsulTarget` 也固定 `consul:///svc`。
- 如果传入 K8s discovery，命名和行为不一致。

建议：

- 将 scheme 参数化，例如 `NewGRPCResolverBuilder("consul", discovery)`。
- 或明确这个 resolver 只服务 Consul，并为 K8s 单独提供 builder。
- `BuildTarget(scheme, serviceName)` 做通用 helper，`BuildConsulTarget` 保留兼容。

### 4.6 HTTP client interface 过宽，重试策略过隐式

涉及文件：

- `packages/transport/client/http/client.go`

问题：

- `Client` 同时包含 JSON POST、multipart、文件路径 multipart、download、GET。
- `doRequestWithRetry` 对所有 `>=400` 和所有方法重试，包括非幂等 POST。
- 全局 `defaultClient` 无锁，`Init` / `GetClient` 并发存在数据竞争风险。

建议：

- 拆出小 interface：`Requester`、`Downloader`、`MultipartPoster`。
- 重试策略显式化，至少区分：
  - 幂等方法默认可重试。
  - POST 默认不重试，除非调用方显式允许。
  - 只重试网络错误和 5xx，不重试 4xx。
- 全局 client 如果保留，应使用 `sync.Once` / `atomic.Value` / mutex。

### 4.7 gRPC client 是 SDK pass-through，需要明确连接语义

涉及文件：

- `packages/transport/client/rpc/client.go`

问题：

- `GetClientConnection` 直接暴露 `*grpc.ClientConn`，`WithClientOptions` 也直接透传 `grpc.DialOption`，module depth 偏浅。
- `UseClient` 固定使用 `context.Background()`，调用方无法控制建连等待的父 context。
- 默认 `balancerName` 是 `round_robin`，但未说明何时需要 resolver 支持；和 `registry` 的 scheme 绑定关系不明显。
- `enableTracing` / `enableMetrics` 字段可配置，但当前没有实际接入 interceptor 或 provider，属于无效 interface。

建议：

- 增加 `UseClientContext(ctx, callback, options...)`，保留 `UseClient` 兼容。
- 明确 `GetClientConnection` 是“建连并等待 Ready”，不是 lazy dial；timeout 语义写进注释和测试。
- 如果 tracing/metrics 尚未实现，先移除 option 或标记为未实现，避免调用方以为生效。
- registry resolver 与 balancer 的示例应放在同一处文档，说明 `consul:///svc`、`round_robin`、grpc transport filter 的组合方式。
- 增加测试：
  - 空 endpoint 报错。
  - context timeout 会关闭 conn 并返回 ctx error。
  - option 返回 error 时直接中断。

### 4.8 HTTP bind 需要限定反射绑定能力

涉及文件：

- `packages/transport/server/http/bind/binding.go`
- `packages/transport/server/http/bind/binding_test.go`

判断：

- `QueryBinding` 用 json tag 绑定 query，是一个有价值的小 module，因为它适配了项目 DTO 普遍使用 json tag 的现实。
- 当前测试覆盖了基础类型、slice、pointer 和 validation，基础路径健康。

问题：

- `RegisterCustomValidator` 参数名和内部变量同名，实际没有注册任何 custom validator，容易让调用方误解。
- `setValue` 不支持 `time.Duration`、`time.Time`、自定义 `encoding.TextUnmarshaler`，但错误只说 unsupported kind，interface 能力边界需要明确。
- 空字符串会被忽略，调用方无法区分“未传字段”和“传了空值”；对 required/omitempty 的行为需要文档化。

建议：

- 删除或实现 `RegisterCustomValidator`。如果保留，应接收 name/tag 和 validator func，真正注册到 Gin validator。
- 支持 `encoding.TextUnmarshaler`，这样 `time.Duration`、自定义 enum 等可以自然绑定。
- 明确空字符串策略；如果需要“显式清空”语义，增加 option 或专门 helper。
- 增加测试：
  - unsupported field type 的错误信息包含字段名。
  - `time.Duration` 或 TextUnmarshaler 类型。
  - 空字符串对 pointer/string 的行为。

### 4.9 command 包的 flag interface 已有深度，但隐性约束要收敛

涉及文件：

- `packages/command/command.go`
- `packages/command/command_test.go`

判断：

- `ParseCommands(args, object)` 把 struct tag 到 `pflag` 的反射绑定封装起来，是一个相对深的 module。
- 测试已覆盖嵌套 struct、required、unsupported type、常见标量和 string slice。

问题：

- `NewParseCommands` 直接读 `os.Args[1:]`，测试和嵌入式调用更适合使用 `ParseCommands`。
- `FlagSet` 名称使用 `os.Args[0]`，`ParseCommands(args, object)` 仍有进程级依赖。
- required + default 的语义不够清楚：有默认值但用户未传时是否算 required 满足，需要明确。
- 重复 flag 名、重复 short name、mapstructure 复杂 tag 的错误路径需要测试。

建议：

- 文档推荐业务代码优先使用 `ParseCommands(args, object)`，`NewParseCommands` 仅作为 CLI 入口便利函数。
- 允许调用方传入 `*pflag.FlagSet` 或配置 output writer，减少 stderr 副作用。
- 明确 required 语义：required 表示用户必须显式传入，还是最终值非零即可。
- 增加测试：
  - required + default。
  - 重复 flag/short。
  - `mapstructure:"Foo,omitempty"` 命名。
  - stderr/output 不污染测试日志。

### 4.10 Redis/GORM/Bun 增加 Resource adapter

涉及文件：

- `packages/infra/cache/redis/client.go`
- `packages/infra/database/gorm/connect.go`
- `packages/infra/database/bun/connect.go`

问题：

- 当前都是构造即连接、Ping，并写包级全局。
- 没有统一 `core.Resource` 生命周期。
- 没有统一 `Close`。

建议：

- 增加可注入 Resource wrapper：
  - `Setup(ctx)` 建连、Ping。
  - `Close(ctx)` 关闭底层连接。
  - `Name()` 返回 `"redis"`、`"gorm"`、`"bun"` 等。
- 构造函数返回实例，包级 `GetDB` / `GetClient` 保留兼容，但新代码优先依赖注入。
- 增加测试：
  - nil config 报错。
  - Setup 失败不污染全局。
  - Close 可重复调用。

## 5. P2：配置、文档和语义收敛

### 5.1 更新 app.yml，恢复配置可信度

涉及文件：

- `packages/config/app.yml`
- `packages/config/config_*.go`

问题：

- 示例配置仍包含 `storage.type: local`、`s3`、`minio`，但当前只允许 `disk|oss`。
- YAML 多处使用 snake_case，例如 `read_timeout`，但结构体 mapstructure tag 是 PascalCase，例如 `ReadTimeout`。
- `database.driver` 和结构体 `Dsn` 不一致。

建议：

- 将 `app.yml` 改为当前真实字段：
  - `storage.Type: disk|oss`
  - `storage.Disk.RootDir`
  - `storage.Disk.BaseUrl`
  - `storage.OSS.*`
  - `http.ReadTimeout`
  - `grpc.ShutdownTimeout`
- 删除或迁移 s3/minio/ftp 旧示例。
- 增加配置加载测试：用 `app.yml` 直接 `InitConfigure`，确保样例可被当前结构体解析并通过 validator。

### 5.2 对齐 database 支持矩阵

涉及文件：

- `packages/config/config_database.go`
- `packages/infra/database/database.go`
- `packages/infra/database/bun/connect.go`
- `packages/infra/database/gorm/connect.go`

问题：

- `database.DSNSqlite/MySQL/Postgres/Clickhouse` 有四种常量。
- GORM 支持 clickhouse。
- `config.Database` validator 只允许 `sqlite mysql postgres`。
- Bun 实际只支持 Postgres。
- `Urls`、`EnableTLS` 等配置字段存在，但部分实现没有使用。

建议：

- 明确矩阵：
  - GORM: sqlite/mysql/postgres/clickhouse。
  - Bun: postgres only。
- validator、文档、示例按矩阵更新。
- 未实现字段要么实现，要么注释标记“当前未使用”，避免调用方误以为生效。

### 5.3 Storage interface 保留 composite，同时增加小 interface

涉及文件：

- `packages/infra/storage/storage.go`
- `packages/infra/storage/filedisk/disk.go`
- `packages/infra/storage/alioss/oss.go`

问题：

- `Storage` interface 同时包含基础 CRUD、批量、前缀遍历、分片上传。
- 调用方如果只需要 `Upload/Download`，也被迫依赖全部能力。
- disk/oss 对 `VersionID`、`ContentType`、`Meta`、`partSize` 的支持程度不同。

建议：

- 保留当前 `Storage` 作为 composite interface，避免破坏调用方。
- 新增小 interface：
  - `ObjectStore`: `GetUrl/Exists/Upload/Delete/Download/GetMeta`
  - `BatchStore`: `BatchUpload/BatchDelete/DeleteByPrefix`
  - `PrefixStore`: `PrefixIterator`
  - `MultipartStore`: multipart 全套方法
- 文档说明 disk/oss 的 option 支持矩阵。
- OSS `applyIOOptions` 和 pubsub option 一样要跳过 nil option。
- OSS multipart complete 建议按 PartNumber 排序，和 disk 保持一致。
- `DisableSSL` 时 fallback URL 不应被 `joinEndpointPath` 强行拼回 `https://`。

### 5.4 Telemetry 与 HTTP 框架解耦

涉及文件：

- `packages/telemetry/telemetry.go`
- `packages/telemetry/gin.go`
- `packages/transport/server/http/middleware/telemetry.go`

问题：

- `telemetry` 通用包直接依赖 Gin。
- `NewOpenTelemetry` 构造时就创建 provider 并设置全局。
- `Setup` 基本空操作，不符合 `core.Resource` 的直觉。
- 如果创建部分 provider 后失败，已设置的全局 provider 没有清理。

建议：

- `OpenTelemetry` 保存配置，真实初始化移到 `Setup(ctx)`。
- 初始化任一 provider 失败时，关闭已创建 provider。
- Gin middleware 移到 `transport/server/http/middleware` 或直接复用 `otelgin`。
- `Close(ctx)` 保持 Lp -> Mp -> Tp 的 shutdown 顺序。

### 5.5 errors 包需要保护内部状态

涉及文件：

- `packages/errs/custom_error.go`

问题：

- `Fields()` 直接返回内部 map，调用方可修改错误内部状态。
- `Is` 只按 code 比较，`code=0` 可能造成意外匹配。
- `Wrap` 包装已有 `CustomError` 时保留 `origin.cause`，可能丢掉中间错误链语义。

建议：

- `Fields()` 返回 copy。
- `Is` 对 `code=0` 做特殊处理，避免零值匹配。
- `Wrap` 保留完整错误链，或者文档明确当前行为。
- 增加测试覆盖外部修改 fields、code=0、链路保留。

### 5.6 util/request.go 应降级或重写为可注入请求器

涉及文件：

- `packages/util/request.go`
- `packages/util/url.go`

问题：

- `DoRequest` 没有 context。
- 内部创建 `http.Client`，无法注入 timeout/transport。
- 不检查 HTTP status。
- `meta` 或 `callback` 为 nil 会 panic。
- `BuildURL` 直接拼 `?`，已有 query 时会生成错误 URL。

建议：

- 新 interface 形态：
  - `DoRequest(ctx context.Context, client *http.Client, meta Meta, param url.Values, body any) ([]byte, error)`
  - 或直接让调用方传 `*http.Request`。
- 检查 nil、status code、response body read error。
- `BuildURL` 使用 `url.Parse` 合并 query。
- 补 `httptest` 测试。

## 6. P3：并发模式、缓存模式、worker pool 的长期优化

### 6.1 resilience 统一 Executor interface

涉及文件：

- `packages/resilience/adapter.go`
- `packages/resilience/wrapper.go`
- `packages/resilience/retry.go`
- `packages/resilience/rate_limiter.go`
- `packages/resilience/circuit_breaker.go`
- `packages/resilience/bulkhead.go`

问题：

- `Executor` 是 `func(ctx context.Context) error`。
- 但 `CircuitBreaker.Execute`、`Bulkhead.Execute`、`Retry.Execute` 接收 `func() error`，ctx 要靠闭包捕获，隐性 interface 偏大。
- `RateLimiter.Take` 只等一次 `1/rate`，不是“阻塞直到拿到 token 或 ctx 超时”。
- `CircuitBreaker.currentState()` 在 open 超时后只返回 half-open，不落盘状态，`State()` 可见状态和内部计数容易不一致。

建议：

- 所有 resilience module 统一接收 `Executor`。
- `RateLimiter` 优先改用 `golang.org/x/time/rate`，或实现循环等待直到 token/ctx。
- middleware 中限流建议用 `Take(ctx)`，而不是 `Allow(ctx)`，让取消语义生效。
- circuit breaker 在 open 过期时显式迁移到 half-open 状态。
- 增加测试：
  - retryable / non-retryable。
  - half-open 成功关闭、失败重新 open。
  - rate limiter ctx timeout。
  - middleware 顺序。
  - bulkhead close race。

### 6.2 caching 模式需要明确 cache miss 和 backend error

涉及文件：

- `packages/patterns/caching/adapter.go`
- `packages/patterns/caching/cache_aside.go`
- `packages/patterns/caching/write_through.go`
- `packages/patterns/caching/write_behind.go`
- `packages/patterns/caching/refresh_ahead.go`

问题：

- `KVCache.Get(ctx, key) (string, error)` 无法区分 miss 和后端故障。
- 当前 `CacheAside` / `WriteThrough` 把任意 `Get` error 当 miss，Redis 故障会放大到源站。
- loader/writer 多数不接 context。
- `RefreshAhead` 每次刷新直接起 goroutine，无 per-key in-flight 去重，Stop 不等待刷新任务。
- `WriteBehind` writer 错误被吞，Stop/enqueue 语义需要更严格定义。

建议：

- 定义 `ErrCacheMiss`，或将 interface 改为 `Get(ctx, key) (value string, ok bool, err error)`。
- loader/writer 改成 context-aware。
- RefreshAhead 复用 `SingleFlight` 或 per-key in-flight 标记。
- 后台任务纳入 `wg`，`Stop` 后禁止新刷新。
- WriteBehind 增加 error callback、重试/死信、Flush 返回错误。

### 6.3 Memoizer 与 SingleFlight 可以形成更深 module

涉及文件：

- `packages/patterns/concurrency/memoize.go`
- `packages/patterns/concurrency/singleflight.go`

判断：

- `SingleFlight` 当前较深，panic、waiter cancellation、Forget 语义比较清楚。
- `Memoizer` 只做 TTL cache，`GetOrLoad` 没有抑制并发 miss。

建议：

- 新增 `LoadingMemoizer` 或给 `Memoizer` 可选注入 `SingleFlight`。
- 同一 key 并发 miss 只执行一次 loader。
- 测试多个 goroutine 同时 `GetOrLoad`，loader 只调用一次。

### 6.4 worker pool 应明确 fire-and-forget 语义

涉及文件：

- `packages/pool/worker.go`
- `packages/pool/worker_ants.go`

问题：

- `WorkerPool.Submit` 返回的 error 只覆盖入队失败，不覆盖任务执行失败。
- 任务错误只写日志。
- 包级全局 `wp` 无同步保护，`NewAntsWorkerPool` 重复调用会覆盖全局。
- `Close()` 没有 context，长任务会让关闭无限等待。

建议：

- 弱化或移除包级全局，优先实例注入。
- 如果保留 fire-and-forget，文档明确 Submit 不返回执行错误。
- 提供 error sink 或 future/handle：
  - `Submit(ctx, task) (Handle, error)`
  - `Handle.Wait(ctx) error`
  - 或 `WithErrorHandler(func(error))`
- `Close(ctx)` 支持取消等待。
- 增加 `Close` 与 `Submit` 并发测试。

## 7. 测试补齐清单

优先测试：

1. `packages/transport/server/sse`
   - Startup/Shutdown 契约。
   - 重复 client id。
   - Shutdown 后 Publish/Notify。
2. `packages/transport/server/http`
   - New 后未 Startup 直接 Shutdown 释放 listener。
   - Port=0 真实端口公开。
3. `packages/registry`
   - K8s watch 取消后事件不 panic。
   - TargetRef nil。
   - Consul Watch 取消与错误 backoff。
4. `packages/infra/storage/filedisk`
   - PrefixIterator/DeleteByPrefix 路径逃逸。
5. `packages/resilience`
   - bulkhead close race。
   - panic 后 running 计数。
   - rate limiter Take 等待语义。
   - circuit breaker half-open 状态。
6. `packages/log`
   - logger 初始化失败不污染全局。
7. `packages/config`
   - `app.yml` 示例可加载、可验证。
8. `packages/patterns/caching`
   - cache miss 与 backend error 区分。
   - write-behind Stop/Flush/writer error。
   - refresh-ahead in-flight 去重。
9. `packages/util`
   - DoRequest nil/context/status。
   - BuildURL 合并已有 query。
10. `packages/transport/client/rpc`
    - 建连 timeout 与 Ready 等待语义。
    - tracing/metrics option 是否真实生效。
11. `packages/transport/server/http/bind`
    - json tag query binding 的类型边界。
    - custom validator 注册行为。
12. `packages/command`
    - required/default 语义。
    - 重复 flag/short name。

## 8. 建议实施顺序

第一批：修运行时风险。

1. SSE Endpoint 契约。
2. HTTP listener 释放与 `Addr()` helper。
3. K8s registry panic。
4. DiskStorage prefix 安全。
5. Logger 全局初始化。
6. Bulkhead close race。

第二批：修 interface 和生命周期。

1. Endpoint 契约测试。
2. gRPC Port=0 策略。
3. Registry Watch contract。
4. SubscriberEndpoint 深化。
5. Redis/GORM/Bun Resource adapter。

第三批：修配置和文档可信度。

1. 更新 `app.yml`。
2. 对齐 database 支持矩阵。
3. Storage option 语义矩阵。
4. Pub/Sub option 支持矩阵。

第四批：长期质量。

1. resilience 测试全覆盖。
2. caching 模式重构。
3. pool error/close 语义。
4. util request/url 重写。
5. errs 状态保护。

## 9. 每批完成前的质量门禁

每次提交前至少运行：

```bash
gofmt -l packages
go test ./packages/...
go vet ./packages/...
```

涉及并发或后台 goroutine 的改动，额外运行：

```bash
go test -race ./packages/resilience ./packages/patterns/... ./packages/pool ./packages/infra/cache/redis
```

涉及 transport lifecycle 的改动，额外运行：

```bash
go test ./packages/app ./packages/transport/...
```

涉及配置或基础设施的改动，额外运行：

```bash
go test ./packages/config/... ./packages/infra/... ./packages/log ./packages/telemetry
```

## 10. 结论

这个仓库的核心 seam 方向是可继续发展的：`Application` 负责编排，transport/infra 通过 adapter 注入，业务层可以不绑定框架。当前优化的重点不是推翻架构，而是把已经写进 interface 的生命周期和错误语义真正落实到每个 adapter。

优先级最高的是 P0：它们会导致阻塞无法退出、fd 泄漏、panic、安全路径逃逸或全局状态污染。P0 修完后，再推进 registry watch contract、resource adapter、配置样例可信度和 resilience/caching 的深 module 化。
