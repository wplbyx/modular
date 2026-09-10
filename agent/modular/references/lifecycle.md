# Lifecycle

Read when wiring a Process or diagnosing startup/shutdown. Source:
`packages/core`, `packages/app`, and `packages/health`.

## Contracts

`core.Resource` represents supporting infrastructure:

- `Setup(ctx)` completes when usable.
- `Close(ctx)` releases it.
- `Name()` is a log/check label, not identity.

Use `core.ManagedResource[T]` for infrastructure exposing a typed value. It
implements `core.Resource`, `core.Provider[T]`, and structurally
`health.Checker`. It runs callbacks outside its state mutex, permits a failed
Setup to retry, and caches the first completed Close result.

`core.Endpoint` represents an inbound transport:

- `Startup(ctx)` blocks until the endpoint stops; any early nil/error return is
  an Application exit signal.
- `Shutdown(ctx)` is the normal mechanism that releases Startup.
- `core.ReadyEndpoint.Ready(ctx)` optionally reports when Startup can accept
  work. Custom Endpoints without it are considered ready once Startup begins.

Every callback must honor context cancellation. A Go caller cannot forcibly
stop a callback that ignores its context.

## Application order

```text
Resource.Setup FIFO
  -> Endpoint.Startup concurrently
  -> ReadyEndpoint.Ready concurrently
  -> Registrar.Register(Process ServiceNode)
  -> health.Manager Ready
  -> wait for cancellation or Endpoint exit
  -> health.Manager Draining
  -> Registrar.Unregister
  -> Endpoint.Shutdown concurrently
  -> Resource.Close LIFO
```

Only successfully set-up Resources and started Endpoints are cleaned up. Run
and manual Close share one `sync.Once`. Application is single-use; Close before
Run moves it to stopped without invoking dependencies. A Registrar requires a
ServiceNode. A zero-Endpoint Application sets up Resources and waits for its
context, which supports worker-only Processes.

Run-triggered shutdown uses one timeout budget, defaulting to 10 seconds or
`configitem.Application.ShutdownTimeout`. Unregister happens before server
shutdown so discovery stops directing new work before connections drain.

## Process assembly

Generated bootstrap order is fixed:

1. `config.NewRootCommand` loads the Process config.
2. `newLoggerManager` creates and installs the context-required logger.
3. `newTransportPolicy` and `health.NewManager` create Process policy/state.
4. cmd constructs shared Resources and a typed `wiring.Platform`.
5. `WireBusiness(process, platform)` returns module `Contribution` values.
6. cmd builds one HTTP and/or one gRPC server and registers all contributions.
7. cmd creates `core.ProcessIdentity`, `core.ServiceNode`, and Application.

Application does not own the logger. The composition root closes it after Run.
HTTP readiness should use `httpserver.WithHealthManager`; gRPC exposes its
standard health service. Build ServiceNode transports from each server's
`Transport()` so pre-bound `Port=0` values are preserved.

Use `signal.NotifyContext` for `SIGINT`/`SIGTERM`. HTTP and gRPC also apply their
own graceful-stop timeouts before forced closure.
