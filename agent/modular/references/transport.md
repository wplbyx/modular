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

HTTP (`packages/transport/server/http`): `httpserver.NewServer(cfg *configitem.HTTP, opts ...ServerOption) (*Server, error)`. Construct-then-listen: it binds the port inside `NewServer`, so `Port=0` yields a real assigned port via `server.Addr()` or `server.Transport()`. `/health` remains a minimal liveness endpoint. Add readiness with `WithReadiness(path, checkers...)`; it returns 200/503 and becomes `Transport.HealthPath`. Use `httpserver.NoWriteTimeout` for streaming responses. Inject an `*errs.Handler` with `WithErrorHandler` to enable localized JSON errors and centralized panic/diagnostic handling. Business handlers may use `httpserver.Wrap(func(*gin.Context) error)` or `c.Error(err)`. `Startup` blocks in Serve; `Shutdown` releases the pre-bound listener even if Startup was never called.

gRPC (`packages/transport/server/rpc`): `rpcserver.NewServer(cfg *configitem.GRPC, register RegisterFunc, opts ...Option) (*Server, error)`. Construct-then-listen now matches HTTP: it binds in `NewServer`, so `Port=0` is visible before service registration via `server.Addr()` / `server.Transport()`. `RegisterFunc` is `func(grpc.ServiceRegistrar) error` - the cmd passes a closure that calls `pb.RegisterXxxServer(s, impl)`. Note `Option` here is `func(*Server) error` (returns error - the only such Option type in the library; handle its error). Options: `WithUnaryInterceptors(...)`, `WithStreamInterceptors(...)`, `WithMTLS(cert, key, clientCA)`, and `WithErrorHandler(...)`. The error handler installs outer unary/stream interceptors, reads `accept-language` metadata, returns localized gRPC status plus `ErrorInfo.reason`, logs full diagnostics, and recovers panics. Health check is auto-registered (`grpc_health_v1`). `Startup` calls `grpcServer.Serve(listener)` (blocks); `Shutdown` does GracefulStop with `configitem.GRPC.ShutdownTimeout` then force-stops on timeout and releases the listener.

## Pub/Sub subscriber endpoint

`packages/transport/pubsub/endpoint.go`: `NewSubscriberEndpoint(name string, sub pubsub.Subscriber, topic string, handler pubsub.MessageHandler, opts ...SubscriberOption) *SubscriberEndpoint`. Returns a `core.Endpoint`. `Startup` auto-detects optional `Connector` / `Disconnector` implementations on the subscriber, subscribes, then blocks on an internal context until `Shutdown` cancels it and closes the subscriber. Override auto-detected hooks with `WithConnect(fn)` / `WithDisconnect(fn)`. Use `WithSubscribeOptions(...SubscribeOption)` to forward QoS, queue name, and similar subscription options into `Subscriber.Subscribe`. Shutdown errors are aggregated with `errors.Join`.

Handlers: `pubsub.MessageHandler func(ctx, Message) error`. `pubsub.EventHandler func(ctx, Event) error`. Convert with `pubsub.AsMessageHandler(h)`. `pubsub.EventFromMessage(msg)` builds a `BaseEvent` from a `Message`.

Broker clients implementing `pubsub.Subscriber`/`Publisher`/`Client`: `kafka` (Consumer + Producer), `mqtt` (Client), `redis` (PubSub + Stream), `rocket` (push consumer + producer). Each has `NewConsumer`/`NewClient` + `With*` options. In `internal/<svc>/api/<surface>/event.go`, return a `MessageHandler`; the cmd wraps it with `NewSubscriberEndpoint`.

Kafka needs no connect/disconnect. MQTT/Redis clients that implement `Connect(ctx)` / `Disconnect(ctx)` are auto-detected; pass explicit hooks only when overriding that behavior.

## SSE (route-mounted endpoint)

`packages/transport/server/sse`: `sse.NewServer(bufferSize int) *Server`. Implements `core.Endpoint`, but `Startup` only marks started and blocks - it does NOT listen on a port. `Shutdown` cancels the internal startup context, closes clients, and unblocks `Startup`. Mount its handler on an HTTP server's route: `httpServer.RegisterRoute(func(e *gin.Engine){ e.GET("/sse", sseServer.Connect()) })`. Publish with `sseServer.Publish(clientID, msg)` (non-blocking) or `Notify(msg)` (broadcast). Reusing the same `client_id` closes the old connection channel before replacing it. Clients identify via `?client_id=` query param.

## Clients

HTTP (`packages/transport/client/http`): `httpclient.NewClient(cfg)` returns a concrete `*Client`; inject it explicitly. `Do(*http.Request)` is the primary interface and follows net/http response-body ownership. Convenience methods include Get, Post, multipart, and atomic Download. Retries require a replayable request and apply by default only to idempotent methods plus 408/425/429/5xx or temporary network failures. POST/PATCH also require `Idempotency-Key`, unless `RetryPolicy` explicitly authorizes them. Retry-After, exponential backoff, context cancellation, and intermediate response-body closure are handled internally.

gRPC (`packages/transport/client/rpc`): `rpcclient.GetClientConnection(ctx, opts ...ClientConfigOption) (*grpc.ClientConn, error)` waits until the connection reaches Ready or the context/timeout fails, closing the connection on failure. `rpcclient.UseClient(callback, opts...)` uses `context.Background()`; `rpcclient.UseClientContext(ctx, callback, opts...)` lets callers control the parent context. Both reject nil callbacks and auto-close the connection. Options configure endpoint, timeout, credentials, interceptors, balancer, tracing, metrics. For service discovery, dial a resolver target produced by the registry (see registry.md).

## Middleware

Gin middleware in `packages/transport/server/http/middleware/`: `cors`, `limiter`, `logger`, `request_id`, `telemetry` (wraps `telemetry.GinMiddleware`), `trace`. Attach via `httpserver.WithMiddleware(...)`. The HTTP server's constructor already adds Recovery and (if a zap logger is set via `WithLogger`) a zap gin logger.
