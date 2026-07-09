# Lifecycle

The framework-layer lifecycle contract. Read when wiring `cmd/` or debugging startup/shutdown.

## Table of contents

- [The two interfaces](#the-two-interfaces)
- [Application.Run order](#applicationrun-order)
- [Assembly in cmd/main.go](#assembly-in-cmdmaingo)
- [Shutdown](#shutdown)
- [Signals and zero-endpoint](#signals-and-zero-endpoint)

## The two interfaces

Both live in `packages/core/adapter.go`.

`Resource` (infrastructure: DB, Redis, storage, telemetry):

- `Name() string` - log label only.
- `Setup(ctx context.Context) error` - non-blocking; returns when ready.
- `Close(ctx context.Context) error` - non-blocking; tears down.

`Endpoint` (transport: HTTP, gRPC, SSE, pub/sub subscriber):

- `Name() string` - log label only.
- `Startup(ctx context.Context) error` - MUST BLOCK until the service is no longer running. Returning (nil or error) signals exit.
- `Shutdown(ctx context.Context) error` - the ONLY thing that may unblock `Startup`.

`Startup` must not return early on its own. `Application.Run` treats ANY `Startup` return as an exit signal. `Shutdown` is the mechanism that brings `Startup` down (e.g. `http.Server.Shutdown`, `grpc.Server.GracefulStop`, cancelling the subscriber context).

## Application.Run order

From `packages/app/application.go`:

1. `Resource.Setup` for each resource, FIFO (registration order). First failure stops and triggers cleanup of only the resources whose `Setup` already succeeded.
2. `registrar.Register(node)` if both a Registrar and ServiceNode are set (pass-through; app does not interpret registration details).
3. All `Endpoint.Startup` run in parallel via errgroup, each blocking.
4. Run state: waits until any endpoint exits or the context is cancelled.
5. On exit: `Endpoint.Shutdown` (parallel) then `Unregister(node)` then `Resource.Close` (LIFO, reverse registration order).

Shutdown is guarded by an Application-level `sync.Once`, shared by `Run` and manual `Close(ctx)`. It runs entirely within one `shutdownTimeout` budget when triggered by `Run` (default 10s; configurable via `config.Application.ShutdownTimeout`).

## Assembly in cmd/main.go

The orchestrator builds and injects in this shape (option order does not affect execution - resources are always FIFO up / LIFO down, endpoints always last):

    application, err := app.NewApplication(ctx, cfg,
        app.WithResource(db),
        app.WithResource(cache),
        app.WithEndpoint(httpServer),
        app.WithServiceNode(node),
        // app.WithRegistrar(consul),  // only when registering
    )
    application.Run()

The four `With...` options: `WithResource(core.Resource)`, `WithEndpoint(core.Endpoint)`, `WithServiceNode(*core.ServiceNode)`, `WithRegistrar(registry.Registrar)`.

A real cmd builds transports (which are already `core.Endpoint`), resources, the pb service impl, then registers:

- HTTP: `httpserver.NewServer(cfg)` returns an Endpoint; `server.RegisterRoute(api.HTTPRoutes(...))` to attach domain routes.
- gRPC: `rpcserver.NewServer(cfg, api.RegisterGRPC, opts...)` - the register callback wires `pb.RegisterXxxServer`.
- SSE: `sse.NewServer(bufSize)` is an Endpoint; mount its `Connect()` handler on the HTTP server's routes.
- Pub/sub: wrap a `pubsub.MessageHandler` from `api/event.go` with `pubsub.NewSubscriberEndpoint(name, subscriber, topic, handler, opts...)`.

## Shutdown

Graceful shutdown on `SIGINT`/`SIGTERM`: build the root context with `signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)`. `Application.Run`'s errgroup cancels on context cancellation and triggers shutdown once. `Application.Close(ctx)` is the manual trigger if needed.

Each endpoint's `Shutdown` honors its own timeout: HTTP server uses `config.HTTP.ShutdownTimeout` (default 5s), gRPC uses `config.GRPC.ShutdownTimeout` (default 5s, then force-stop).

## Signals and zero-endpoint

- An Application with zero endpoints logs a warning and `Run` returns `nil` immediately. It does NOT block. Always register at least one endpoint for a long-running service.
- `Shutdown` is idempotent (sync.Once). Calling `Run` shutdown and `Application.Close(ctx)` concurrently is safe; endpoints/resources are closed once.
- `errors.Join` aggregates shutdown errors; `Run` returns `errors.Join(runErr, shutdownErr)`.
