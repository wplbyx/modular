# Layering

The directory layout and ownership rules. Read before `service` or `switch`.

## Table of contents

- [The three layers](#the-three-layers)
- [Per-directory ownership](#per-directory-ownership)
- [Adding a service](#adding-a-service)
- [Adding a surface](#adding-a-surface)
- [Adding a method](#adding-a-method)
- [Topology switch](#topology-switch)

## The three layers

Layer table (path | knows about | changed when):

- `proto/<domain>/...` - domain interface source. Changed when that domain's public contract changes.
- `common/<domain>/...` - generated transport. Only protoc output. Changed when proto changes (run `gen`).
- `internal/<domain>/{api,app,domain,repository}` - domain slice. Inbound adapters, app-surface pb server implementations, domain abstractions, and infra adapters stay separated. `service/` is optional for complex adapter/facade cases.
- `cmd/<svc>/main.go` - orchestrator. Knows `core` + transport constructors + `app`. Changed when swapping infra or flipping topology.

The boundary rule: `internal/<domain>/...` imports its generated `common/<domain>` package for proto types. Cross-domain calls import another domain's generated common package and use its client, never another domain's `internal/`. `cmd/` may import `api`, `app`, and `repository` from the same domain for wiring only; it should not contain business decisions.

## Per-directory ownership

`proto/<domain>/` - source proto files for one business module. Default single-contract domains may use `proto/<domain>/<domain>.proto`. Multi-surface domains use files such as `proto/<domain>/admin.proto`, `proto/<domain>/management.proto`, and `proto/<domain>/platform.proto`. The surface is an external interface contract, not a domain-model split. Surface names must be valid Go package names: lower_snake_case, no hyphens. Do not introduce `v1/v2` directories by default; if a surface is versioned, mirror `proto/<domain>/<surface>/v1` through `common/<domain>/<surface>/v1`, `internal/<domain>/api/<surface>/v1`, and `internal/<domain>/app/<surface>/v1`.

`common/<domain>/` - generated `.pb.go` / `_grpc.pb.go` only, mirroring `proto/<domain>/...`. Messages + the gRPC server stub (the `XxxServiceServer` interface) + the gRPC client (`NewXxxServiceClient`). Never hand-edit. The package name is whatever `go_package` declares in the proto; keep it stable so imports don't churn.

`internal/<domain>/api/<surface>/` - inbound adapter code for one interface surface such as `admin`, `management`, `platform`, or `openapi`. It owns HTTP/gRPC/event registration and maps traffic into the generated pb server interface or app-surface methods. It is infrastructure-facing and should stay thin:

- `grpc.go` exposes a function the cmd calls to register the pb service on a `*grpc.Server`: `func RegisterGRPC(s grpc.ServiceRegistrar, svc common.<Surface>ServiceServer)`. The domain owns its own gRPC binding in both topologies.
- `http.go` exposes `func HTTPRoutes(svc common.<Surface>ServiceServer) httpserver.RegisterRouteFunc` returning a gin route registration. Handlers parse HTTP, call the pb server interface, and write JSON.
- `event.go` exposes handler constructors for pub/sub. Event handlers usually call `app` use cases directly because events do not always map 1:1 to pb RPC methods.

`internal/<domain>/app/<surface>/` - default generated pb server implementation and application orchestration for one interface surface. It implements `common.<Surface>ServiceServer`, coordinates command/query flows, transaction boundaries, cross-repository workflows, idempotency, and calls into domain entities. It depends on `domain/` interfaces and models, not on infra implementations.

Inside `app/<surface>/`, use `server.go` for the server type, constructor, dependency fields, and compile-time interface assertion. Put each rpc method in its own file named after the method in snake_case: `CreateUser` -> `create_user.go`, `ListOrders` -> `list_orders.go`. This is both a human navigation rule and an agent target rule: method-level edits should touch the corresponding method file.

`internal/<domain>/service/` - optional. Introduce it only when the pb contract and internal use-case core intentionally diverge, multiple pb versions need to share one use-case implementation, or one pb service is a facade over multiple app surface packages. Do not scaffold it as a pass-through layer.

`internal/<domain>/domain/` - pure domain abstractions:

- `repository.go` defines repository ports. Split large ports into command/query interfaces when it improves locality: command repositories mutate aggregates; query repositories return read models.
- `entity/` contains rich entities and aggregate roots used by command workflows.
- `model/` is optional. Use it for read/query models or simple domain data shapes when keeping them near repository ports is practical. Treat these as domain-facing models, not persistence records: do not put ORM/BSON tags or database-specific fields here.

`internal/<domain>/repository/` - outbound infra adapters implementing `domain/` repository interfaces. Imports infra: `database/bun`, `database/gorm`, `database/mongo`, `cache/redis`, `client/*`, `storage`. This is where dirty work lives: SQL, Mongo filters, Redis keys, external client mapping, persistence records, and migrations. Swap a DB vendor here without touching `app/`, `api/`, or `domain/`.

`cmd/<svc>/main.go` - thin orchestrator. Build resources, repository adapters, app surface servers, and transport servers; call the surface api package's `RegisterGRPC` / `HTTPRoutes` / event endpoints; then `app.NewApplication(...)`. Keep business decisions out of cmd.

## Adding a service

When the user runs `service <domain>`:

1. Choose the initial interface surface. Use `public` for a single-contract domain unless the user names a surface such as `admin`, `management`, or `platform`.
2. Create the proto from assets/domain/proto.tmpl. For a single-contract domain this can be `proto/<domain>/<domain>.proto`; for a named surface use `proto/<domain>/<surface>.proto`. Set `go_package` to `<project>/common/<domain>` so output lands in the domain's generated package. Use `package <domain>;` and a service name that includes the surface when needed, such as `<Domain>AdminService`.
3. Run `gen` (scripts/gen_proto.py) to produce generated files under `common/<domain>/...`.
4. Create `internal/<domain>/api/<surface>/{grpc.go,http.go,event.go}` from templates. `grpc.go` registers a pb server interface; `http.go` maps HTTP into that same pb server; `event.go` maps pub/sub into app surface methods or shared use cases.
5. Create `internal/<domain>/app/<surface>/server.go` from the server template. This package implements the generated pb service interface directly by default.
6. Create one app method file per rpc method from the method template. For example, `CreateUser` creates `internal/<domain>/app/<surface>/create_user.go`.
7. Create `internal/<domain>/domain/repository.go`, `internal/<domain>/domain/entity/`, and optionally `internal/<domain>/domain/model/`.
8. Create `internal/<domain>/repository/` only when the domain needs persistence or outbound infra. Persistence-specific records live here, not in `domain/model`.
9. Wire into `cmd/`: build repository adapters, app surface servers, and endpoints. In `single` topology, add endpoints to the shared `main.go`; in `service` topology, the domain owns a `cmd/<domain>/main.go` - or create it if missing.

Cross-domain dependency: domain A needs to call domain B? Use domain B's generated `common` client, but inject it into A's app surface package or an outbound adapter interface rather than importing B's `internal/`. In single topology, wire it to a local connection (`127.0.0.1`); in micro topology, wire it to a resolver address. The proto interface is identical either way.

## Adding a surface

When the user runs `surface <domain> <surface>`:

1. Add `proto/<domain>/<surface>.proto` from assets/domain/proto.tmpl. Use a service name that includes the surface, such as `UserAdminService`.
2. Run `gen` so `common/<domain>/...` gains the generated service interface and client.
3. Add `internal/<domain>/api/<surface>/` from the api templates.
4. Add `internal/<domain>/app/<surface>/server.go` from the server template.
5. Do not split `domain/` or `repository/` just because a new surface exists. Split them only when the domain concepts or outbound adapter implementations actually differ.
6. Wire the new surface in `cmd/` by constructing the app surface server and registering its gRPC/HTTP/event adapters.

## Adding a method

When the user runs `method <domain> <surface> <MethodName>`:

1. Find the surface proto file. Prefer `proto/<domain>/<surface>.proto`; if the domain intentionally uses a single proto file, use `proto/<domain>/<domain>.proto`.
2. Add the rpc declaration and request/response messages if they do not already exist.
3. Run `gen` so the generated pb interface includes the method.
4. Create or update `internal/<domain>/app/<surface>/<method>.go`, where `<method>` is the snake_case form of `<MethodName>`.
5. Keep `server.go` focused on dependencies and construction. Do not pile method bodies into `server.go`.
6. If HTTP or event exposure is required, add the thin adapter in `api/<surface>/http.go` or `api/<surface>/event.go`; the app method remains the behavioral target.

## Topology switch

`switch [single|service]` rewrites ONLY `cmd/`. `internal/`, `common/`, `proto/` are untouched - that is the whole point.

- **single to service**: split the shared `cmd/<project>/main.go` into one `cmd/<domain>/main.go` per domain. Each main builds its own `Application` + `ServiceNode` + (if needed) `Registrar`. Cross-domain calls that were in-process now go through pb clients pointing at a resolver.
- **service to single**: merge per-domain mains into one `cmd/<project>/main.go` registering all endpoints in one `Application`. Cross-domain pb clients point at `127.0.0.1:<port>`. No `Registrar` needed.

See registry.md for resolver wiring and lifecycle.md for the `Application` assembly order.
