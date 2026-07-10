---
name: modular
description: Scaffold, wire, audit, and recommend structure for Go applications built on `github.com/wplbyx/modular` (Go 1.26+). Use this skill when the user wants to initialize a modular project, add a svc/business module, add an admin/management/platform/openapi surface, add pb methods, attach DB/Redis/Storage/Telemetry resources, regenerate proto output, run modular convention checks, recommend app-layer vs domain-layer adapters, scaffold repository implementations, or switch between single-process and service topology. Enforce the README svc layout, proto-first boundaries, config-per-svc packages, HTTP+gRPC endpoint wiring, and core.Resource/core.Endpoint lifecycle rules.
---

# modular

This skill scaffolds downstream projects that import `github.com/wplbyx/modular/packages/*`. The README layout is the source of truth:

- The outer business-module namespace is **svc**: `proto/<svc>`, `common/<svc>`, `internal/<svc>`, `config/<svc>`.
- `domain` only means the complex domain package inside one svc: `internal/<svc>/domain`.
- Simple CRUD/MVC flows can define app-layer ports in `internal/<svc>/app/<surface>/adapter.go`.
- Complex domain behavior defines ports in `internal/<svc>/domain/adapter.go`.
- Repository implementations are infrastructure: `repository/app`, `repository/domain`, and optional `repository/dto` / `repository/model`.

## Target Layout

```text
<project>/
  cmd/
    <project>/main.go          # single topology: one process aggregating svc modules
    <svc>/main.go              # service topology: one process per svc
  config/
    <svc>/
      config.go
      config.yaml
      resources.json           # generated CLI metadata for resource wiring
  proto/
    <svc>/
      <svc>.proto
      <surface>.proto
  common/
    <svc>/                     # protoc output only
  internal/
    <svc>/
      api/<surface>/
      app/<surface>/
        adapter.go             # simple app QueryRepository / CommandRepository
        server.go
        <method>.go
      domain/
        adapter.go             # complex domain ports
        entity/
        service/               # only when real domain services exist
      repository/
        app/
        domain/
        dto/                   # generated only when needed
        model/                 # generated only when needed
```

## CLI Commands

Use the deterministic CLI for scaffolding:

```bash
python agent/modular/scripts/modular.py <command> ...
```

- `init <project> [single|service]` creates the project shell. Single topology creates `cmd/<project>/main.go`; service topology creates cmd entries when svc modules are added.
- `service <svc> [--surface public] [--methods CreateX,ListX]` creates `config/<svc>`, proto/common target paths, api/app/domain/repository scaffolds, and HTTP+gRPC cmd wiring.
- `surface <svc> <surface> [--methods ...]` adds a surface under the existing svc and rewrites cmd wiring.
- `method <svc> <surface> <MethodName>` updates the surface proto and creates `app/<surface>/<method>.go`.
- `resource <db|redis|storage|telemetry> [--driver bun|gorm|mongo] [--svc <svc>]` adds per-svc config/resource metadata and rewrites cmd wiring.
- `repository recommend <svc> [surface] --aggregate X --feature "..." --query ... --command ...` recommends app vs domain placement and prints the scaffold command.
- `repository app <svc> <surface> --aggregate X --query ... --command ...` writes app ports and `repository/app` methods.
- `repository domain <svc> --aggregate X --query ... --command ...` writes domain ports and `repository/domain` methods.
- `gen` runs `buf generate`.
- `doctor` runs read-only structure checks.

Read [references/commands.md](references/commands.md) before calling the CLI.

## Agent Workflows

- **`plan`**: Convert broad requirements into svc, surface, method, resource, adapter, repository, `gen`, and `doctor` steps.
- **`adapter recommend`**: Decide whether each interface belongs in `app/<surface>/adapter.go` or `domain/adapter.go`; explain why before scaffolding.
- **`repository recommend`**: After adapter placement is clear, draft exact QueryRepository / CommandRepository signatures and the scaffold command.
- **`crud-service`**: For simple CRUD, prefer app adapter + `repository app`. Only introduce domain ports when business rules justify it.
- **`switch [single|service]`**: Agent-driven cmd rewrite only. Do not change `proto/`, `common/`, or `internal/`.

## Hard Rules

- `common/` is generated only. Never hand-edit `.pb.go` or `_grpc.pb.go`.
- Cross-svc calls go through generated pb clients, never another svc's `internal/` package.
- `api/<surface>` is an inbound adapter only: HTTP/gRPC/event mapping, no business rules.
- `app/<surface>` implements generated pb server interfaces and coordinates use cases.
- App-layer adapters are for simple flows; domain adapters are for complex domain behavior.
- Repository code is infrastructure. Persistence tags belong in `repository/model`, not `domain/entity`.
- Resource lifecycle uses `Setup(ctx)` / `Close(ctx)`; endpoint lifecycle uses blocking `Startup(ctx)` / `Shutdown(ctx)`.
- Generated cmd wiring registers both HTTP and gRPC endpoints by default.
- Single topology has one `cmd/<project>/main.go` aggregating many svc modules; service topology has one `cmd/<svc>/main.go` per svc.

## References

- [references/layering.md](references/layering.md) - svc layout, app/domain adapter boundaries, topology.
- [references/commands.md](references/commands.md) - CLI command options and safety.
- [references/repository.md](references/repository.md) - adapter recommendation and repository scaffold rules.
- [references/lifecycle.md](references/lifecycle.md) - Resource/Endpoint lifecycle and Application assembly.
- [references/config.md](references/config.md) - config types and `config/<svc>` loading.
- [references/transport.md](references/transport.md) - HTTP, gRPC, SSE, pub/sub, clients.
- [references/registry.md](references/registry.md) - ServiceNode, registry, discovery, resolver.
- [references/infra.md](references/infra.md) - DB/Redis/Storage/Telemetry constructors and resources.

## Decision Tree

```text
New project?                    -> init <project> [single|service]
Add business module?            -> service <svc>; then gen
Add another interface surface?  -> surface <svc> <surface>; then gen
Add pb method?                  -> method <svc> <surface> <MethodName>; then map HTTP/event if needed
Simple persistence?             -> repository recommend -> repository app <svc> <surface>
Complex domain persistence?     -> repository recommend -> repository domain <svc>
Need infra?                     -> resource <kind> [--svc <svc>]
Need convention audit?          -> doctor
Proto changed?                  -> gen
Switch topology?                -> rewrite cmd only; read layering + registry
```
