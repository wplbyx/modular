# Adapter and repository placement

Read after routing to CRUD or domain work. The CLI creates no repository shell;
the Agent defines the smallest port after the use case is known.

## Simple app flow

For CRUD/query/mutation without rich domain behavior, put use-case ports under
`internal/modules/<module>/internal/app` and implementations under
`internal/modules/<module>/internal/repository/app`. DTO-style data is
acceptable at this seam.

## Domain flow

For aggregates, invariants, policies, or transaction coordination, put ports
under `internal/modules/<module>/internal/domain`. Implement them in
`internal/repository/domain`; keep persistence structs and ORM tags under
`internal/repository/model`, outside domain entities.

## Rules

- Explain app-versus-domain placement before adding packages.
- Define the smallest interface needed by the use case and its tests.
- Repositories receive concrete `core.Provider[T]` dependencies; call
  `Value()` only while handling work after Resource Setup.
- A module owns its UoW and data writes. Cross-module shared transactions must
  be declared as extraction blockers.
- Cross-module code depends on the provider's generated Port or public
  `contract`, never its `internal` implementation.
- A consumer-side remote adapter lives at
  `internal/modules/<consumer>/internal/adapters/remote/<provider>` and
  normalizes remote errors through the generated adapter.
- Generate DTO/model packages only when a real adapter needs them. Temporary
  adapters return explicit errors, never fake success values.
