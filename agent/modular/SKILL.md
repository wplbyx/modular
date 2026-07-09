---
name: modular
description: Scaffold and wire Go applications built on the `modular` infrastructure library. Use this skill when a project imports the `modular` module (module path `modular`, Go 1.26+) and needs to initialize a project shell, add a service/domain module, add an interface surface such as admin/management/platform, add a pb method implementation file, attach infrastructure resources (DB/Redis/Storage/Telemetry), regenerate proto code, or switch between monolith and microservice topology. Also use it to enforce the modular conventions - proto-first interfaces, the cmd/common/internal layering, app-surface method files, and the core.Resource/core.Endpoint framework lifecycle. Trigger when the user asks to scaffold, init, add service, add surface, implement a method, add resource, generate proto, switch to single/microservice, or otherwise build upon the modular library.
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
  proto/
    <domain>/
      <domain>.proto          # domain-scoped proto package; no v1/v2 split by default
      <surface>.proto         # optional interface surface: admin / management / platform
  common/                      # PURE protoc output, never hand-edited
    <domain>/                  # mirrors proto/<domain>/
      <domain>.pb.go           #   messages
      <domain>_grpc.pb.go      #   gRPC stub
      <surface>.pb.go          #   optional surface messages
      <surface>_grpc.pb.go     #   optional surface gRPC stub
  internal/
    <domain>/
      api/
        <surface>/             # admin / management / platform / openapi ...
          http.go              # inbound HTTP adapter for this interface surface
          grpc.go              # pb.Register<Surface>Server(rpcServer, pb server impl)
          event.go             # inbound event adapters for this surface
          v1/                  # optional only when the surface contract is versioned
      app/                     # pb server implementation + application orchestration
        <surface>/             # mirrors api/<surface>
          server.go            # dependencies + generated pb XxxServiceServer implementation type
          <method>.go          # one rpc method per file; CreateUser -> create_user.go
          v1/                  # optional version-specific pb adapter
      domain/
        repository.go          # command/query repository interfaces (ports)
        entity/                # rich domain entities / aggregates
        model/                 # optional read/simple domain models, not persistence tags
      repository/              # outbound infra adapters implementing domain ports
      service/                 # optional adapter/facade for complex protocol/use-case splits
  cmd/
    <svc>/main.go              # orchestrator: build transports, resources, Application
  config/
    config.go                  # project Config aggregate + Load helper
    config.yaml
```

Rules baked into this layout - see [references/layering.md](references/layering.md):

- `common/` is generated. Never hand-edit it. Add nothing here.
- `proto/` and `common/` are domain-partitioned: place files under `proto/<domain>/...` and generate into `common/<domain>/...`. Keep this aligned with `internal/<domain>/...`.
- Split a domain by interface surface when public contracts differ: `admin`, `management`, `platform`, `openapi`, etc. Default multi-surface layout is `proto/<domain>/<surface>.proto`, `common/<domain>/<surface>.pb.go`, `internal/<domain>/api/<surface>`, and `internal/<domain>/app/<surface>`.
- Surface names are Go package names: use lower_snake_case, no hyphens, no version suffix by default.
- Do not introduce `v1/v2` directories by default. If a surface contract is versioned, mirror that surface version through `proto/<domain>/<surface>/v1`, `common/<domain>/<surface>/v1`, `internal/<domain>/api/<surface>/v1`, and `internal/<domain>/app/<surface>/v1` deliberately.
- `internal/<domain>/api/<surface>/` owns inbound adapters (http, grpc, event) for that interface surface. It maps traffic into the generated pb server interface or app methods; it does not own business rules.
- `app/<surface>/` is the default pb server implementation package. It implements the generated `XxxServiceServer`, orchestrates use cases, and coordinates domain entities, repository ports, transactions, and cross-repository workflows.
- Keep one rpc method per app file: `server.go` holds dependencies and constructors; `CreateUser` lives in `create_user.go`, `ListOrders` in `list_orders.go`. This gives the agent and humans a stable target for method-level edits.
- `service/` is optional, not default. Add it only when pb contracts and internal use cases intentionally diverge, multiple pb versions share one use-case core, or a pb service is a facade over multiple app surface packages.
- `domain/` owns business abstractions: repository interfaces, rich entities/aggregates under `domain/entity`, and optional read/simple domain models under `domain/model`. Do not put DB/ORM/BSON tags in `domain/model`.
- `domain/` and `repository/` normally do not split by surface or version. Split them only when the domain concepts or persistence adapters genuinely differ.
- `repository/` implements the domain repository interfaces with infra (DB/Redis/client/storage). Business stays infra-agnostic via interfaces.
- Project-specific config aggregation lives in the project `config` package next to `config.yaml`. `cmd` calls that package's `Load(...)` helper instead of defining anonymous config structs inline.
- `cmd/` is thin wiring: construct resources, repositories, app surface servers, transport servers, and pass endpoints/resources to `app.NewApplication`.
- Cross-domain calls go through generated pb clients, never through `internal/` imports.

## Commands

The skill offers seven commands. `init` and `gen` are script-driven (mechanical); `service`, `surface`, `method`, `resource`, and `switch` are asset+rules driven (agent applies judgment):

1. **`init <project> [single|service]`** - create the project shell. Runs `scripts/init_project.py`. Produces `go.mod`, buf configs, `Makefile`, `proto/`, `common/`, `internal/`, `config/`, and a `cmd/` entry whose shape depends on topology: `single` = one shared `cmd/<project>/main.go` holding all domains; `service` = one `cmd/<svc>/main.go` per service. Default topology is `single`.
2. **`service <domain>`** - add a domain. Scaffold a default surface using [assets/domain/proto.tmpl](assets/domain/proto.tmpl), run `gen` to create `common/<domain>/...`, then create `internal/<domain>/{api/<surface>,app/<surface>,domain,repository}` from templates, and wire it into `cmd/`. Default surface is `public` for single-contract domains unless the user names one. See [references/layering.md](references/layering.md) section "Adding a service".
3. **`surface <domain> <surface>`** - add an interface surface such as `admin`, `management`, or `platform` to an existing domain. Create `proto/<domain>/<surface>.proto`, `internal/<domain>/api/<surface>`, and `internal/<domain>/app/<surface>`; keep `domain/` and `repository/` shared unless they genuinely diverge.
4. **`method <domain> <surface> <MethodName>`** - add a method skeleton to a surface. Update the relevant proto service, run `gen`, then create `internal/<domain>/app/<surface>/<method>.go` from [assets/domain/app_method.go.tmpl](assets/domain/app_method.go.tmpl). File names are snake_case versions of method names.
5. **`resource <kind>`** - add an infrastructure resource adapter. `kind` in `db | redis | storage | telemetry`; for `db`, choose Bun, GORM, or MongoDB based on the project's `config.Database.Dsn`. Scaffold a `core.Resource` wrapper, wire it into `cmd/`'s `app.WithResource(...)` chain, and add its config block. See [references/infra.md](references/infra.md) for the exact constructors per kind.
6. **`gen`** - regenerate `common/` from `proto/`. Runs `scripts/gen_proto.py` (wraps `buf generate`). Re-run after any proto edit.
7. **`switch [single|service]`** - rewrite the `cmd/` layer to flip topology. `internal/` and `common/` are untouched. See [references/layering.md](references/layering.md) section "Topology switch".

## Hard rules (enforce on every change)

These are non-negotiable; they are what makes the library's guarantees hold.

- **proto is the only cross-module boundary.** One domain's `internal/` MUST NOT import another domain's `internal/`. Use generated pb clients.
- **`common/` is generated.** Never hand-edit `.pb.go` or `_grpc.pb.go`. To change an interface, edit the `.proto` and run `gen`.
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

- [references/layering.md](references/layering.md) - cmd/common/internal contracts, adding a service, surface, method, and topology switch. **Read before `service`, `surface`, `method`, or `switch`.**
- [references/lifecycle.md](references/lifecycle.md) - Resource/Endpoint contracts, `Application.Run` order, shutdown budget. **Read when wiring `cmd/` or debugging startup/shutdown.**
- [references/config.md](references/config.md) - `config.*` types, `InitConfigure`, env/file/remote/flag loaders, validation. **Read when adding config.**
- [references/transport.md](references/transport.md) - http/rpc/sse servers (all `core.Endpoint`), pubsub endpoint, clients, middleware, "construct-then-listen". **Read when adding endpoints or event handlers.**
- [references/registry.md](references/registry.md) - ServiceNode, Registrar/Discovery, Consul registry, K8s discovery, gRPC resolver (`consul` scheme). **Read when wiring service discovery or single-to-micro switch.**
- [references/infra.md](references/infra.md) - database (bun/gorm/mongo), cache/redis, storage (disk/oss), telemetry constructors and resource wrappers. **Read for `resource` command.**

## Workflow decision tree

```
New project?                       -> init <project> [single|service]
  then add first service?          -> service <domain>; then gen
  then need DB/Redis/Storage/OTel? -> resource <kind>
Existing project, new service?     -> service <domain>; then gen
Proto changed?                     -> gen
Flip monolith <-> microservice?    -> switch [single|service]
Add another interface surface?     -> surface <domain> <surface>; then gen
Add HTTP/gRPC method?              -> method <domain> <surface> <MethodName>; then map in api/<surface>/ if HTTP/event is needed
Wire a transport into cmd?         -> see references/lifecycle.md + transport.md
```
