# Layering

A Business Module is a bounded-context code and data-write boundary. The
Application is the one deployment, configuration, transport, and lifecycle
boundary.

## Framework paths

- `.modular/architecture.yaml`: Application capabilities, modules, and module
  dependencies.
- `.modular/manifest.json`: generated ownership and replay hashes only.
- `modules/<module>/config.go`: scaffold-once business configuration.
- `config/<application>/config.gen.go|config.yaml`: managed aggregate config.
- `cmd/<application>/main.go|`: managed bootstrap.
- `cmd/<application>/policy.go`: scaffold-once transport policy.
- `cmd/<application>`: typed Platform and Assembly.
- `cmd/<application>/business.go`: scaffold-once composition root.

## Business paths

- `modules/<module>/contract`: public Go interfaces and their
  Command/Query/Result types.
- `modules/<module>/module.go`: optional module constructor and
  exported capabilities.
- `modules/<module>/internal/api/<surface>`: inbound adapters.
- `modules/<module>/internal/app`: use cases and simple ports.
- `modules/<module>/internal/domain`: real aggregates and policies.
- `modules/<module>/internal/repository`: outbound adapters.

Sibling modules import only a provider's `contract`. The extra `internal`
directory lets the Go compiler reject implementation imports from siblings;
the doctor also checks that every contract import is declared in architecture.

Do not generate a fixed layer tree. CRUD may remain in app. Add domain only
for invariants, policies, aggregate coordination, or transaction rules. Module
construction never depends on `app.Application` or a runtime container.

## Application assembly

One Application shares its logger, health manager, transport policy, resources,
and at most one HTTP and one gRPC server. `wiring.Platform.Resources` exposes
typed lifecycle resources to the composition root. Modules receive only the
narrow providers or interfaces they actually need.

Cross-module workflows belong to the initiating module. They may share an ACID
transaction when all participants use the same transactional resource, but
must call public contracts and must not expose another module's repository or
tables. Long-lived workflows with their own language and invariants should
become a Business Module, not a generic technical workflow layer.
