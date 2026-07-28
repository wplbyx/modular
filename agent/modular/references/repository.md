# Adapter And Repository Placement

Use this reference after routing a request to the CRUD or domain workflow. The
CLI does not infer repository signatures; the Agent owns this architecture
decision and writes the selected contract templates directly.

## App placement

Choose `internal/<svc>/app/<surface>/ports.go` when the flow is simple:

- CRUD or query/mutation with no rich domain behavior.
- The use case can call a repository adapter directly.
- DTO-style data is acceptable at the app seam.

Put the adapter under `internal/<svc>/repository/app` only after its interface
is known. Do not create a repository shell merely because a svc exists.

## Domain placement

Choose `internal/<svc>/domain/ports.go` when aggregates, invariants, policies,
transactions, or cross-entity behavior matter. Put persistence adapters under
`internal/<svc>/repository/domain` and persistence models/tags under
`repository/model`, never on domain entities.

## Rules

- Explain app-vs-domain placement before writing files.
- Give each interface the smallest surface that supports the use case and its tests.
- Accept generated `core.Provider[T]` dependencies in repository constructors.
- Do not call `Provider.Value()` before Application completes Resource Setup.
- Generate DTO/model packages only when the chosen adapter needs them.
- Cross-svc dependencies use generated pb clients, not another svc's `internal`.
- Unimplemented adapters return an explicit error; never generate fake success data.
