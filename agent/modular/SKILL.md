---
name: modular
description: Scaffold and wire Go applications built on the `modular` infrastructure library. Use this skill when a project imports the `modular` module (module path `modular`, Go 1.26+) and needs to initialize a project shell, add a service/domain module, attach infrastructure resources (DB/Redis/Storage/Telemetry), regenerate proto code, or switch between monolith and microservice topology. Also use it to enforce the modular conventions - proto-first interfaces, the cmd/common/internal layering, and the core.Resource/core.Endpoint framework lifecycle. Trigger when the user asks to scaffold, init, add service, add resource, generate proto, switch to single/microservice, or otherwise build upon the modular library.
---

# modular

Scaffold, wire, and operate Go services built on the `modular` infrastructure library (module `modular`, Go 1.26+). This skill is the usage and convention contract for downstream projects: it generates a runnable skeleton and enforces the layering and lifecycle rules the library expects.

## What this skill is for

A downstream project imports `modular/packages/*`, then uses this skill to generate a correct project structure and wire the library's pieces together. Two convention layers, kept strictly separate:

- **Framework layer (`cmd/`)**: the `core.Resource` / `core.Endpoint` interfaces make infrastructure components swappable. These live in `cmd/main.go` only.
- **Business layer (`proto` + `internal/`)**: proto is the only cross-module interface boundary. Business code depends on proto interfaces, never on another domain's internals.

The entire value is that `internal/` business code never changes when you swap DB vendors, flip monolith to microservice, or replace a transport - only `cmd/` and the proto-gen output change.

## Project layout (target)

Every project generated or maintained by this skill follows this tree:

```
<project>/
  go.mod                       # module <project>; requires modular
  buf.yaml buf.gen.yaml        # proto toolchain (protoc-gen-go + protoc-gen-go-grpc)
  Makefile                     # gen / build / run-<domain> / check targets
  proto/<domain>.proto
  common/                      # PURE protoc output, never hand-edited
    <domain>_pb.go             #   messages + gRPC stub
  internal/
    <domain>/
      api/
        http.go                # gin routes + http.HandlerFunc adapters (call pb service)
        grpc.go                # pb.Register<Domain>Server(rpcServer, serviceImpl)
        event.go               # pub/sub subscriber handlers (MessageHandler/EventHandler)
      service/                 # business logic, implements pb service interface
      repository/              # infra implementations of the service's Repo interfaces
      models/                  # bun ORM models
  cmd/
    <svc>/main.go              # orchestrator: build transports, resources, Application
  config/config.yaml
```

Rules baked into this layout - see [references/layering.md](references/layering.md):

- `common/` is generated. Never hand-edit it. Add nothing here.
- `internal/<domain>/api/` owns ALL transport bindings (http, grpc, event) for that domain.
- `service/` defines its OWN business repository interfaces; `repository/` implements them with infra (DB/Redis/client). Business stays infra-agnostic via interfaces.
- `cmd/` is thin: construct transport servers, call the domain's `api.RegisterXxx(...)`, pass the resulting `core.Endpoint`s to `app.NewApplication`.
- Cross-domain calls go through generated pb clients, never through `internal/` imports.

## Commands

The skill offers five commands. `init` and `gen` are script-driven (mechanical); `service`, `resource`, and `switch` are asset+rules driven (agent applies judgment):

1. **`init <project> [single|service]`** - create the project shell. Runs `scripts/init_project.py`. Produces `go.mod`, buf configs, `Makefile`, `proto/`, `common/`, `internal/`, `config/`, and a `cmd/` entry whose shape depends on topology: `single` = one shared `cmd/<project>/main.go` holding all domains; `service` = one `cmd/<svc>/main.go` per service. Default topology is `single`.
2. **`service <domain>`** - add a domain. Scaffold `proto/<domain>.proto` from [assets/domain/proto.tmpl](assets/domain/proto.tmpl), run `gen`, then create `internal/<domain>/{api,service,repository,models}` from templates, and wire it into `cmd/`. See [references/layering.md](references/layering.md) section "Adding a service".
3. **`resource <kind>`** - add an infrastructure resource adapter. `kind` in `db | redis | storage | telemetry`. Scaffold a `core.Resource` wrapper, wire it into `cmd/`'s `app.WithResource(...)` chain, and add its config block. See [references/infra.md](references/infra.md) for the exact constructors per kind.
4. **`gen`** - regenerate `common/` from `proto/`. Runs `scripts/gen_proto.py` (wraps `buf generate`). Re-run after any proto edit.
5. **`switch [single|service]`** - rewrite the `cmd/` layer to flip topology. `internal/` and `common/` are untouched. See [references/layering.md](references/layering.md) section "Topology switch".

## Hard rules (enforce on every change)

These are non-negotiable; they are what makes the library's guarantees hold.

- **proto is the only cross-module boundary.** One domain's `internal/` MUST NOT import another domain's `internal/`. Use generated pb clients.
- **`common/` is generated.** Never hand-edit `_pb.go`. To change an interface, edit the `.proto` and run `gen`.
- **Lifecycle method names are fixed.** Resource = `Setup(ctx) error` / `Close(ctx) error`. Endpoint = `Startup(ctx) error` (MUST block until shutdown) / `Shutdown(ctx) error`. Do not invent new lifecycle verbs.
- **`app` never imports `transport`.** Endpoints are always injected as `core.Endpoint`. Resources as `core.Resource`.
- **Prefer dependency injection over the global singletons.** `database`, `cache/redis`, `client/{http,rpc}` ship package-level `Init/GetX`, but inject the returned instance instead of relying on the global. Storage has no global - construct `filedisk.NewDiskStorage` / `alioss.NewOSSStorage` directly.
- **Endpoint `Startup` blocks.** `Application.Run` treats ANY return (nil or error) as an exit signal. Only `Shutdown` may unblock it. A zero-endpoint Application returns immediately with a warning - always register at least one endpoint.
- **Errors:** use `fmt.Errorf("...: %w", err)` + `errors.Join` for aggregation everywhere except `resilience`, which uses `errs`.
- **Logging:** call package-level `log.Infof` / `log.Error` / `log.Warn` / `log.Warnf` / `log.Errorf` / `log.Debug` / `log.Debugf`. There is no logger-in-context pattern. `log.GetLogger()` returns the zap logger.
- **Config:** use `config.*` strong types (`config.Application`, `config.HTTP`, `config.GRPC`, `config.Database`, `config.Redis`, `config.Storage`, `config.Telemetry`, `config.Logging`). Never pass bare `map[string]any`. Load via `config.InitConfigure` (Viper). See [references/config.md](references/config.md).
- **Comments:** match the existing file's language (core/older code is Chinese; newer transport code is English). New code follows the file it's added to.
- **ServiceNode identity:** one Application = one `core.ServiceNode`, built from config via `core.NewServiceNode(name, version, transports...)`. `Endpoint.Name()` / `Resource.Name()` are log labels only, not service identity.

## References (load on demand)

Read each only when the task needs it; do not load all upfront.

- [references/layering.md](references/layering.md) - cmd/common/internal contracts, adding a service, topology switch. **Read before `service` or `switch`.**
- [references/lifecycle.md](references/lifecycle.md) - Resource/Endpoint contracts, `Application.Run` order, shutdown budget. **Read when wiring `cmd/` or debugging startup/shutdown.**
- [references/config.md](references/config.md) - `config.*` types, `InitConfigure`, env/file/remote/flag loaders, validation. **Read when adding config.**
- [references/transport.md](references/transport.md) - http/rpc/sse servers (all `core.Endpoint`), pubsub endpoint, clients, middleware, "construct-then-listen". **Read when adding endpoints or event handlers.**
- [references/registry.md](references/registry.md) - ServiceNode, Registrar/Discovery, Consul registry, K8s discovery, gRPC resolver (`consul` scheme). **Read when wiring service discovery or single-to-micro switch.**
- [references/infra.md](references/infra.md) - database (bun/gorm), cache/redis, storage (disk/oss), telemetry constructors and resource wrappers. **Read for `resource` command.**

## Workflow decision tree

```
New project?                       -> init <project> [single|service]
  then add first service?          -> service <domain>; then gen
  then need DB/Redis/Storage/OTel? -> resource <kind>
Existing project, new service?     -> service <domain>; then gen
Proto changed?                     -> gen
Flip monolith <-> microservice?    -> switch [single|service]
Add HTTP route / gRPC method?      -> edit proto + internal/<domain>/api/ ; gen
Wire a transport into cmd?         -> see references/lifecycle.md + transport.md
```
