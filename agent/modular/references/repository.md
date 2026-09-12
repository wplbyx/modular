# Adapter and repository placement

Read after routing to CRUD or domain work. The CLI creates no repository shell;
the Agent defines the smallest port after the use case is known.

## Simple app flow

For CRUD/query/mutation without rich domain behavior, put use-case ports under
`modules/<module>/internal/app` and implementations under
`modules/<module>/internal/repository/app`. DTO-style data is
acceptable at this seam.

## Domain flow

For aggregates, invariants, policies, or transaction coordination, put ports
under `modules/<module>/internal/domain`. Implement them in
`internal/repository/domain`; keep persistence structs and ORM tags under
`internal/repository/model`, outside domain entities.

## Rules

- Explain app-versus-domain placement before adding packages.
- Define the smallest interface needed by the use case and its tests.
- Repositories receive concrete `core.Provider[T]` dependencies; call
  `Value()` only while handling work after Resource Setup.
- A module owns its data writes. Cross-module code depends on the provider's
  public Go `contract`, never its `internal` implementation.
- A cross-module ACID workflow is owned by its initiating module. Define its
  narrow transaction runner beside the use case and implement it in project
  infrastructure; do not expose repositories or raw transaction handles.
- Generate DTO/model packages only when a real adapter needs them. Temporary
  adapters return explicit errors, never fake success values.
