# Adopt self-contained Business Module directories

## Status

Accepted for modular v0.4.

## Decision

Generated applications place bounded contexts at `modules/<module>`. Each
module exposes its constructor, Config, and outward `contract`; keeps use cases,
outbound ports, DTOs, and domain types in a module-level `internal`; and owns its
HTTP, persistence, and event adapters in `infrastructure` under the same module.
The only composition root is `cmd/<application>/{main,resources,modules}.go`.

## Considered options

The former `internal/modules` plus centralized wiring layout made all application
code private but doubled `internal` nesting and separated adapters from their
bounded contexts. A root-level centralized infrastructure tree could expose
adapters to cmd only by making module ports public, expanding the public API and
reintroducing horizontal technical layers. Root-level module directories
without a `modules` umbrella would lose a stable discovery boundary for doctor.

## Consequences

Go now enforces private module implementation access while cmd can still build
module-owned adapters. Siblings share only provider contracts and the declared
DAG remains a convention checked by doctor. Application projects are
technically importable from another repository, which is acceptable because
they are deployment units rather than libraries. Truly shared project-specific
adapters may later justify an explicit `platform` or `pkg` package; none is
generated preemptively.
