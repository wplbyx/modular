# Layering

The directory layout and ownership rules. Read before `service` or `switch`.

## Table of contents

- [The three layers](#the-three-layers)
- [Per-directory ownership](#per-directory-ownership)
- [Adding a service](#adding-a-service)
- [Topology switch](#topology-switch)

## The three layers

Layer table (path | knows about | changed when):

- `common/` - generated transport. Only protoc output. Changed when proto changes (run `gen`).
- `internal/<domain>/{api,service,repository,models}` - business. Knows proto + own Repo interfaces. Changed when business logic changes.
- `cmd/<svc>/main.go` - orchestrator. Knows `core` + transport constructors + `app`. Changed when swapping infra or flipping topology.

The boundary rule: `internal/` imports `common/` (proto types). `cmd/` imports `core`, transport constructors (`httpserver`, `rpcserver`), and the domain `api` registration entry points. `cmd/` never reaches into `internal/<domain>/service` directly - it calls `api.RegisterXxx` which returns or registers a `core.Endpoint`.

## Per-directory ownership

`common/` - generated `_pb.go` only. Messages + the gRPC server stub (the `XxxServiceServer` interface) + the gRPC client (`NewXxxServiceClient`). Never hand-edit. The package name is whatever `go_package` declares in the proto; keep it stable so imports don't churn.

`internal/<domain>/api/` - ALL transport bindings for the domain:

- `grpc.go` exposes a function the cmd calls to register the pb service on a `*grpc.Server`: `func RegisterGRPC(s grpc.ServiceRegistrar, svc common.<Domain>ServiceServer)`. The domain owns its own gRPC binding in both topologies.
- `http.go` exposes `func HTTPRoutes(...) httpserver.RegisterRouteFunc` returning a gin route registration. Handlers here are thin adapters: parse request, call the pb service method, write JSON. They wrap pb service calls into `http.HandlerFunc`, exactly like the user's reference pattern (a `RouteRegister` that groups routes and maps each to a handler closure).
- `event.go` exposes handler constructors for pub/sub: returns a `pubsub.MessageHandler` or `pubsub.EventHandler`. The cmd wraps these in `pubsub.NewSubscriberEndpoint(...)` to make a `core.Endpoint`.

`internal/<domain>/service/` - the business logic. Implements the pb `<Domain>ServiceServer` interface. Defines its OWN repository interfaces (e.g. `type OrderRepo interface { ... }`) and consumes them as fields. No infra imports here - only proto types, the repo interface, and stdlib.

`internal/<domain>/repository/` - the concrete implementations of the service's repo interfaces. Imports infra: `database/bun` or `database/gorm`, `cache/redis`, `client/*`, `storage`. This is where infra-specific code lives. Swap a DB vendor here without touching `service/`.

`internal/<domain>/models/` - ORM structs (bun struct tags). `database.ModelIndexer` can be implemented for migration tooling.

`cmd/<svc>/main.go` - thin orchestrator. Build transport servers, build resources, build the pb service impl (passing repos wired to infra), call `api.RegisterGRPC` / `api.HTTPRoutes` / event endpoints, then `app.NewApplication(...)`.

## Adding a service

When the user runs `service <domain>`:

1. Create `proto/<domain>.proto` from assets/domain/proto.tmpl. Set `go_package` so output lands in `common/`. Use `package <domain>;` and a service `<Domain>Service` with rpc methods.
2. Run `gen` (scripts/gen_proto.py) to produce `common/<domain>_pb.go`.
3. Create `internal/<domain>/api/{grpc.go,http.go,event.go}` from templates. `grpc.go` registers the pb server; `http.go` is a stub `HTTPRoutes` returning an empty `RegisterRouteFunc` to fill in; `event.go` is a stub returning a no-op handler.
4. Create `internal/<domain>/service/service.go` implementing the pb service interface with placeholder methods.
5. Create `internal/<domain>/repository/` and `models/` only if the domain needs persistence. Skip empty dirs.
6. Wire into `cmd/`: in `single` topology, add the domain's endpoints to the shared `main.go`; in `service` topology, the domain already owns a `cmd/<domain>/main.go` - or create it if missing.

Cross-domain dependency: domain A needs to call domain B? Add `common`'s `New<DomainB>ServiceClient` to A's service as a field. In single topology, wire it to a local connection (`127.0.0.1`); in micro topology, wire it to a resolver address. The proto interface is identical either way.

## Topology switch

`switch [single|service]` rewrites ONLY `cmd/`. `internal/`, `common/`, `proto/` are untouched - that is the whole point.

- **single to service**: split the shared `cmd/<project>/main.go` into one `cmd/<domain>/main.go` per domain. Each main builds its own `Application` + `ServiceNode` + (if needed) `Registrar`. Cross-domain calls that were in-process now go through pb clients pointing at a resolver.
- **service to single**: merge per-domain mains into one `cmd/<project>/main.go` registering all endpoints in one `Application`. Cross-domain pb clients point at `127.0.0.1:<port>`. No `Registrar` needed.

See registry.md for resolver wiring and lifecycle.md for the `Application` assembly order.
