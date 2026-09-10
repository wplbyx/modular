# Layering

Read this after the workflow router selects module, contract, resource, or
extraction work. A Business Module is a code/data-write boundary; a Process is
a deployment and lifecycle boundary.

## Framework paths

- `.modular/architecture.yaml`: user-maintained modules, dependencies,
  assignments, Process capabilities, and extraction blockers.
- `.modular/manifest.json`: generated ownership and replay hashes only.
- `config/modules/<module>/config.go`: scaffold-once business configuration.
- `config/<process>/config.gen.go|config.yaml`: managed Process aggregate.
- `cmd/<process>/main.go|framework.gen.go`: managed Process bootstrap.
- `cmd/<process>/policy.go`: scaffold-once transport policy.
- `internal/platform/wiring/framework.gen.go`: typed `Platform`, module configs,
  per-Process resource groups, and `Contribution` types.
- `internal/platform/wiring/business.go`: scaffold-once composition root.

`module add` creates module configuration and updates Process aggregates. It
does not create proto, API, app, domain, repository, event, or fake business
packages. `transport` and `resource` commands target Processes.

## Business paths

- `proto/<module>/<surface>.proto`: source contracts.
- `common/<module>/...`: Buf-generated protobuf, gRPC, Port, and Remote Adapter
  output only.
- `internal/modules/<module>/module.go`: public module constructor/capabilities.
- `internal/modules/<module>/contract`: narrow hand-written contracts when a
  generated Port is not the right abstraction.
- `internal/modules/<module>/internal/api/<surface>`: inbound adapters.
- `internal/modules/<module>/internal/app`: use cases and simple ports.
- `internal/modules/<module>/internal/domain`: real aggregates and policies.
- `internal/modules/<module>/internal/repository`: outbound adapters.
- `internal/modules/<consumer>/internal/adapters/remote/<provider>`:
  cross-Process adapter owned and encapsulated by the consumer.

Sibling modules import only a provider's `contract` or `common/<provider>`.
The extra `internal` directory lets the Go compiler reject implementation
imports from siblings. Dependencies must also be declared in architecture.

For CRUD, keep ports in app and omit domain. Add domain only for real
invariants, policies, aggregate coordination, or transaction rules. Module
construction never depends on `app.Application` or a runtime DI container.

## Process grouping

One Process hosts any number of modules and shares one logger, health manager,
transport policy, Resource set, and server per protocol. Typed resources are
available as `Platform.Resources.<Process>.<Resource>`, so different Processes
may choose different database adapters. `process attach` and
`module extract --check` validate blockers, contracts, and remote adapters
before creating a Process boundary.

The v0.2 `internal/<svc>`, per-service transport, and `single|service` topology
layout is compatibility input for `migrate v0.2-to-v0.3`; do not use it for new
projects.
