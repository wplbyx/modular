# Layering

The README svc layout is authoritative. Read this after the workflow router
selects CRUD, domain, resource, or migration work.

## Framework paths

- `config/<svc>/config.gen.go` is managed transport/Resource configuration.
- `config/<svc>/config.go` is a scaffold-once extension owned by the user.
- `config/<svc>/config.yaml` is a managed svc configuration fragment.
- `config/<project>/...` is the managed single-topology process aggregate.
- `cmd/<process>/main.go|framework.gen.go` is managed Application wiring.
- `cmd/<process>/policy.go` is scaffold-once process policy owned by the user.
- `internal/platform/wiring/framework.gen.go` is the managed hook/provider seam.
- `internal/platform/wiring/business.go` is scaffold-once business registration.

`service add <svc> --transport ...` creates only these framework paths. It does
not create proto, API, app, domain, repository, event, or Example shells.

## Business paths

- `proto/<svc>/<surface>.proto` contains source interface contracts.
- `common/<svc>/...` contains buf output only.
- `internal/<svc>/api/<surface>/...` contains selected inbound adapters.
- `internal/<svc>/app/<surface>/...` contains use cases and simple ports.
- `internal/<svc>/domain/...` exists only for real domain concepts and rules.
- `internal/<svc>/repository/...` contains adapters for selected ports.

For simple CRUD, place ports in `app/<surface>` and omit domain. Create a
domain module only when aggregates, invariants, policies, transactions, or
domain services provide real leverage. Do not split domain by API surface.

## Topology

Single topology has one `cmd/<project>` process and one generated process config
containing nested svc configs. Service topology has one `cmd/<svc>` per svc and
loads `config/<svc>.Config` directly.

Both topologies use the same bootstrap contract: `config.NewRoot` loads config,
then `newLoggerManager` creates logging, then `newTransportPolicy` defines
Metadata/tracing/access/protection, and only then does cmd construct Resources,
Endpoints, and `Application`. `cmd` is the only composition root.

Use `migrate topology --to single|service --apply`. It changes only managed
process cmd/config files. Proto, common output, business packages, and
`internal/platform/wiring/business.go` must remain unchanged.
