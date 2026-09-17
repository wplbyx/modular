# Lifecycle

Read when wiring the Application or diagnosing startup/shutdown. Source:
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
  -> Registrar.Register(Application ServiceNode)
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
context, which supports worker-only Applications.

Run-triggered shutdown uses one timeout budget, defaulting to 10 seconds or
`configitem.Application.ShutdownTimeout`. Unregister happens before server
shutdown so discovery stops directing new work before connections drain.

## Application composition root

The `main` package in `cmd/<application>` performs one visible bootstrap:

1. `main.go` creates a signal context and `config.NewRootCommand[Config]`.
2. The run callback creates `LoggerManager`, installs its logger, loads the one
   error catalog, and creates the shared transport policy and health manager.
3. `resources.go` constructs shared Resources. Their order places the database
   before the aggregated module-migration Resource and dependent Resources.
4. `modules.go` calls module constructors in DAG order, passing configuration,
   necessary Providers, and provider contracts. Each module bootstrap constructs
   its private adapters and use cases. Cmd collects routes, subscriptions, checks,
   migrations, and any additional endpoints from their typed assembly results.
5. cmd constructs one server per enabled protocol, mounts module registrations,
   then builds `ProcessIdentity`, `ServiceNode`, and `app.Application`.

Application does not own the logger; cmd closes it after Run. HTTP readiness
uses `httpserver.WithHealthManager`; gRPC exposes its standard health service.
Build ServiceNode transports from constructed servers so pre-bound `Port=0`
values are preserved.

Module construction only assembles objects; migration, message consumption,
and background tasks run through Application-managed lifecycle. See
[layering](layering.md) for the distinction between assembly and business contracts.

Use `signal.NotifyContext` for `SIGINT`/`SIGTERM`. HTTP and gRPC also apply their
own graceful-stop timeouts before forced closure.
