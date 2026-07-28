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

HTTP (`packages/transport/server/http`): `httpserver.NewServer(cfg *configitem.HTTP, opts ...ServerOption) (*Server, error)`. Construct-then-listen: it binds the port inside `NewServer`, so `Port=0` yields a real assigned port via `server.Addr()` or `server.Transport()`. Inject the process policy with `WithPolicy(policy)`. `/health` remains a minimal liveness endpoint. Add readiness with `WithReadiness(path, checkers...)`; it returns 200/503 and becomes `Transport.HealthPath`. Use `httpserver.NoWriteTimeout` for streaming responses. Inject an `*errs.Handler` with `WithErrorHandler` to enable localized JSON errors and centralized panic/diagnostic handling. Business handlers may use `httpserver.Wrap(func(*gin.Context) error)` or `c.Error(err)`. `Startup` blocks in Serve; `Shutdown` releases the pre-bound listener even if Startup was never called.

gRPC (`packages/transport/server/rpc`): `rpcserver.NewServer(cfg *configitem.GRPC, register RegisterFunc, opts ...Option) (*Server, error)`. Construct-then-listen now matches HTTP: it binds in `NewServer`, so `Port=0` is visible before service registration via `server.Addr()` / `server.Transport()`. `RegisterFunc` is `func(grpc.ServiceRegistrar) error`; combine multiple callbacks with `rpcserver.ChainRegister`. Note `Option` here is `func(*Server) error` (returns error - the only such Option type in the library; handle its error). Options include `WithPolicy`, `WithUnaryInterceptors`, `WithStreamInterceptors`, `WithMTLS`, and `WithErrorHandler`. `otelgrpc` stats instrumentation is installed when policy tracing is enabled. Health check is auto-registered (`grpc_health_v1`).

## Pub/Sub subscriber endpoint

`packages/transport/pubsub/endpoint.go`: `NewSubscriberEndpoint(name string, sub pubsub.Subscriber, topic string, handler pubsub.MessageHandler, opts ...SubscriberOption) *SubscriberEndpoint`. Returns a `core.Endpoint`. `Startup` auto-detects optional `Connector` / `Disconnector` implementations on the subscriber, subscribes, then blocks on an internal context until `Shutdown` cancels it and closes the subscriber. Override auto-detected hooks with `WithConnect(fn)` / `WithDisconnect(fn)`. Use `WithSubscribeOptions(...SubscribeOption)` to forward QoS, queue name, and similar subscription options into `Subscriber.Subscribe`. Shutdown errors are aggregated with `errors.Join`.

Handlers: `pubsub.MessageHandler func(ctx, Message) error`. `pubsub.EventHandler func(ctx, Event) error`. Convert with `pubsub.AsMessageHandler(h)`. `pubsub.EventFromMessage(msg)` builds a `BaseEvent` from a `Message`.

Header-capable publishers call `ResolvePublishOptions` to inject globally scoped
Metadata and trace context; subscribers restore them before the handler. Kafka,
Redis Stream, and RocketMQ support this. MQTT v3 and Redis Pub/Sub channels do
not expose a header carrier and therefore start a new local request context.

Broker clients implementing `pubsub.Subscriber`/`Publisher`/`Client`: `kafka` (Consumer + Producer), `mqtt` (Client), `redis` (PubSub + Stream), `rocket` (push consumer + producer). Each has `NewConsumer`/`NewClient` + `With*` options. In `internal/<svc>/api/<surface>/event.go`, return a `MessageHandler`; the cmd wraps it with `NewSubscriberEndpoint`.

Kafka needs no connect/disconnect. MQTT/Redis clients that implement `Connect(ctx)` / `Disconnect(ctx)` are auto-detected; pass explicit hooks only when overriding that behavior.

## SSE (route-mounted endpoint)

`packages/transport/server/sse`: `sse.NewServer(bufferSize int) *Server`. Implements `core.Endpoint`, but `Startup` only marks started and blocks - it does NOT listen on a port. `Shutdown` cancels the internal startup context, closes clients, and unblocks `Startup`. Mount its handler on an HTTP server's route: `httpServer.RegisterRoute(func(e *gin.Engine){ e.GET("/sse", sseServer.Connect()) })`. Publish with `sseServer.Publish(clientID, msg)` (non-blocking) or `Notify(msg)` (broadcast). Reusing the same `client_id` closes the old connection channel before replacing it. Clients identify via `?client_id=` query param.

## Clients

HTTP (`packages/transport/client/http`): `httpclient.NewClient(cfg)` returns a concrete `*Client`; inject it explicitly. `Config.Policy` controls Metadata, OTel client spans, access logging, and adaptive protection. `Do(*http.Request)` is the primary interface and follows net/http response-body ownership. Retries require a replayable request and apply by default only to idempotent methods plus 408/425/429/5xx or temporary network failures. Each attempt re-enters propagation and protection.

gRPC (`packages/transport/client/rpc`): `rpcclient.GetClientConnection(ctx, opts ...ClientConfigOption) (*grpc.ClientConn, error)` waits until the connection reaches Ready or the context/timeout fails, closing the connection on failure. Use `rpcclient.WithPolicy(policy)` so client Metadata, access logs, protection, and `otelgrpc` stats share process policy. `WithEnableTracing` and `WithClientMetrics` now install real stats instrumentation.

## Middleware

`transport.NewPolicy` enables the common chain by default:

```text
Recovery/Error -> Metadata/RequestID -> OpenTelemetry -> AccessLog
-> Aegis BBR/SRE Protection -> user middleware -> handler
```

The scaffold-once `cmd/<process>/policy.go` owns replacements and opt-outs.
`WithMiddleware` adds business-specific Gin middleware after the common chain;
custom gRPC interceptors are likewise appended after the common interceptors.
All log calls require the request Context. Sensitive Metadata is denied unless
the policy propagator explicitly allowlists it.
