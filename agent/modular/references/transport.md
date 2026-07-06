# Transport

Servers and clients. Read when adding endpoints or event handlers. Source: `packages/transport/`.

## Table of contents

- [Servers (core.Endpoint)](#servers-coreendpoint)
- [Pub/Sub subscriber endpoint](#pubsub-subscriber-endpoint)
- [SSE (route-mounted endpoint)](#sse-route-mounted-endpoint)
- [Clients](#clients)
- [Middleware](#middleware)

## Servers (core.Endpoint)

All three servers implement `core.Endpoint`.

HTTP (`packages/transport/server/http`): `httpserver.NewServer(cfg *config.HTTP, opts ...ServerOption) (*Server, error)`. Construct-then-listen: it binds the port inside `NewServer`, so `Port=0` yields a real assigned port via the returned server. Gin engine is created internally with Recovery; pass middleware via `httpserver.WithMiddleware(...)` and mode via `WithMode(...)`. `RegisterRoute(funcs ...RegisterRouteFunc)` attaches business routes; `RegisterRouteFunc` is `func(*gin.Engine)`. A `/health` check is registered by default (`DefaultHealthPath = "/health"`); disable with `WithDisableHealth()` or move with `WithHealthPath`. `Startup` calls `http.Server.Serve` (blocks); `Shutdown` honors `config.HTTP.ShutdownTimeout` (default 5s).

gRPC (`packages/transport/server/rpc`): `rpcserver.NewServer(cfg *config.GRPC, register RegisterFunc, opts ...Option) (*Server, error)`. `RegisterFunc` is `func(grpc.ServiceRegistrar) error` - the cmd passes a closure that calls `pb.RegisterXxxServer(s, impl)`. Note `Option` here is `func(*Server) error` (returns error - the only such Option type in the library; handle its error). Options: `WithUnaryInterceptors(...)`, `WithStreamInterceptors(...)`, `WithMTLS(cert, key, clientCA)`. Health check is auto-registered (`grpc_health_v1`). `Startup` calls `grpcServer.Serve(listener)` (blocks); `Shutdown` does GracefulStop with `config.GRPC.ShutdownTimeout` then force-stops on timeout.

## Pub/Sub subscriber endpoint

`packages/transport/pubsub/endpoint.go`: `NewSubscriberEndpoint(name string, sub pubsub.Subscriber, topic string, handler pubsub.MessageHandler, opts ...SubscriberOption) *SubscriberEndpoint`. Returns a `core.Endpoint`. `Startup` optionally connects (via `WithConnect(fn)`), subscribes, then blocks on an internal context until `Shutdown` cancels it and closes the subscriber. Use `WithDisconnect(fn)` for symmetric teardown (e.g. MQTT).

Handlers: `pubsub.MessageHandler func(ctx, Message) error`. `pubsub.EventHandler func(ctx, Event) error`. Convert with `pubsub.AsMessageHandler(h)`. `pubsub.EventFromMessage(msg)` builds a `BaseEvent` from a `Message`.

Broker clients implementing `pubsub.Subscriber`/`Publisher`/`Client`: `kafka` (Consumer + Producer), `mqtt` (Client), `redis` (PubSub + Stream), `rocket` (push consumer + producer). Each has `NewConsumer`/`NewClient` + `With*` options. In `internal/<domain>/api/event.go`, return a `MessageHandler`; the cmd wraps it with `NewSubscriberEndpoint`.

Kafka needs no connect/disconnect. MQTT needs `WithConnect(mqttClient.Connect)` / `WithDisconnect(mqttClient.Disconnect)`.

## SSE (route-mounted endpoint)

`packages/transport/server/sse`: `sse.NewServer(bufferSize int) *Server`. Implements `core.Endpoint`, but `Startup` only marks started and blocks on context - it does NOT listen on a port. Mount its handler on an HTTP server's route: `httpServer.RegisterRoute(func(e *gin.Engine){ e.GET("/sse", sseServer.Connect()) })`. Publish with `sseServer.Publish(clientID, msg)` (non-blocking) or `Notify(msg)` (broadcast). Clients identify via `?client_id=` query param.

## Clients

HTTP (`packages/transport/client/http`): `httpclient.NewClient(cfg *Config) Client`. Interface: `Get`, `Post`, `PostMultipart`, `PostMultipartWithFile`, `Download`. `Config` struct (no functional options): `Timeout`, `MaxRetries`, `RetryDelay`, `MaxIdleConns`, `IdleConnTimeout`. Use `DefaultConfig()` for zero values.

gRPC (`packages/transport/client/rpc`): `rpcclient.GetClientConnection(ctx, opts ...ClientConfigOption) (*grpc.ClientConn, error)` and `rpcclient.UseClient(callback, opts...)` (auto-closes conn). Options configure endpoint, timeout, credentials, interceptors, balancer, tracing, metrics. For service discovery, dial a resolver target produced by the registry (see registry.md).

## Middleware

Gin middleware in `packages/transport/server/http/middleware/`: `cors`, `limiter`, `logger`, `request_id`, `telemetry` (wraps `telemetry.GinMiddleware`), `trace`. Attach via `httpserver.WithMiddleware(...)`. The HTTP server's constructor already adds Recovery and (if a zap logger is set via `WithLogger`) a zap gin logger.
