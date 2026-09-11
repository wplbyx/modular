# Adopt a pure modular monolith

## Status

Accepted for v0.4; supersedes ADR 0001 for new projects.

## Decision

modular v0.4 models one deployable Application composed of bounded-context
Business Modules. A Business Module owns cohesive behavior, use cases, its
public Go contract, and data writes. The Application owns transport,
configuration, infrastructure, observability, and lifecycle.

Modules collaborate through hand-written Go contracts and form an acyclic
dependency graph. They do not use protobuf-generated local Ports, Remote
Adapters, Process assignment, extraction blockers, or transparent topology
switching. Protobuf remains available only to projects that independently
choose gRPC as an external transport.

Cross-module ACID transactions are a valid monolith capability. The initiating
module owns the workflow, calls only public contracts, and defines the smallest
transaction interface required by that use case. The framework does not expose
a universal UoW or repositories across module boundaries.

## Consequences

- Module boundaries optimize business locality and dependency direction rather
  than hypothetical extraction.
- New projects contain one Application command/configuration tree and share
  infrastructure resources.
- Internal contracts stay idiomatic Go and can be refactored atomically.
- A future microservice extraction is a separate architecture migration that
  must design wire contracts and distributed consistency explicitly.
- v0.3 remains the historical module/process release; compatible single-Process
  projects can migrate to v0.4, while multi-Process projects require a deliberate
  consolidation or project split.
