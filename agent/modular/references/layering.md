# Layering

The README svc layout is authoritative. Read this before `service`, `surface`,
`method`, `adapter recommend`, repository scaffolding, or topology changes.

## Layers

- `proto/<svc>/...` - source interface contracts for one business module.
- `common/<svc>/...` - generated protobuf output only, mirroring `proto/<svc>`.
- `config/<svc>/...` - per-svc config aggregate and YAML.
- `internal/<svc>/api/<surface>/...` - inbound adapters for HTTP/gRPC/event.
- `internal/<svc>/app/<surface>/...` - generated pb server implementation and simple app-layer use-case ports.
- `internal/<svc>/domain/...` - complex domain model, domain ports, entities, and real domain services.
- `internal/<svc>/repository/...` - infrastructure implementations for app/domain ports.
- `cmd/<project>/main.go` or `cmd/<svc>/main.go` - Application wiring only.

## Ownership

`app/<surface>/adapter.go` defines simple app-layer ports. Use it for CRUD/MVC-style flows where the pb method can call a repository implementation directly without a rich domain model. The default names are `QueryRepository` and `CommandRepository`.

`domain/adapter.go` defines complex domain ports. Use it when the app flow coordinates aggregates, invariants, policies, transactions, or domain services. App packages may depend on the `domain` package.

`domain/service` is not a default pass-through layer. Add it only when behavior spans entities/aggregates and does not naturally belong to one entity.

`repository/app` implements app-layer ports. `repository/domain` implements domain-layer ports. `repository/dto` and `repository/model` are generated only when needed; persistence tags stay in repository models, never in domain entities.

## Adding A Svc

`service <svc>`:

1. Creates `config/<svc>/config.go|yaml`.
2. Creates `proto/<svc>/<svc>.proto` for `public`, or `proto/<svc>/<surface>.proto` for a named surface.
3. Creates `internal/<svc>/api/<surface>`.
4. Creates `internal/<svc>/app/<surface>/adapter.go|server.go`.
5. Creates `internal/<svc>/domain/adapter.go`.
6. Creates `internal/<svc>/repository/app` with a minimal repository root.
7. Rewrites cmd wiring with HTTP and gRPC endpoints.

Multiple surfaces in the same svc share one Go package under `common/<svc>`. Avoid generic message names that collide across surfaces; prefer `<MethodName>Request` / `<MethodName>Response`.

## Adding A Surface

`surface <svc> <surface>` creates the surface proto, api package, app adapter, app server, and rewrites cmd. It does not split `domain` or repository directories by surface.

## Adding A Method

`method <svc> <surface> <MethodName>` updates the surface proto and creates `internal/<svc>/app/<surface>/<method>.go`. Method files are the behavioral target; keep `server.go` focused on dependencies and construction.

## Topology

Single topology uses one `cmd/<project>/main.go` that loads each `config/<svc>` and aggregates all endpoints/resources into one `Application`.

Service topology uses `cmd/<svc>/main.go` per svc. Each process loads `config/<svc>` and owns one `Application`.

Switching topology rewrites only `cmd/`; it must not rewrite `proto/`, `common/`, or `internal/`.
