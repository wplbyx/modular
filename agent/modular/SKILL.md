---
name: modular
description: Scaffold, wire, audit, and evolve modular monolith Go projects built on github.com/wplbyx/modular. Use when initializing a project, adding bounded-context Business Modules, declaring module dependencies, wiring shared transports/resources, designing Go contracts, migrating v0.3 projects, or running scaffold/contract/business verification.
---

# Modular skill

This skill designs and maintains one deployable Application composed of
bounded-context Business Modules. `.modular/tool/modular.py` deterministically
owns framework files; the Agent owns module contracts and business behavior.

## Route first

| Intent | Read first | CLI action |
| --- | --- | --- |
| New project or Business Module | [init](references/workflows/init.md) | `init`, `module` |
| Simple CRUD contract | [crud](references/workflows/crud.md) | Agent edits + `contract-check` |
| Aggregate or transaction rules | [domain](references/workflows/domain.md) | Agent edits + `contract-check` |
| DB/Redis/Storage/Telemetry/EventBus | [resource](references/workflows/resource.md) | `resource add/remove` |
| v0.3 upgrade | [migration](references/workflows/migration.md) | `migrate v0.3-to-v0.4` |
| Convention or release audit | [audit](references/workflows/audit.md) | `self-check`, `doctor`, `verify` |

Read only the references selected by that workflow. Keep universal rules here
and project-specific policy in `.modular/profile.toml`.

## Architecture contract

- A project has one Application lifecycle and deployment unit. It owns shared
  transports, infrastructure, configuration, logging, health, and telemetry.
- A Business Module normally represents one bounded context. It owns cohesive
  business behavior, use cases, data writes, and its public Go contract.
- `.modular/architecture.yaml` declares the Application capabilities, modules,
  and an acyclic module dependency graph. The manifest records generated-file
  ownership and replay state only.
- Other modules import only `internal/modules/<provider>/contract`; imports
  across another module's implementation boundary are invalid.
- Module contracts use hand-written Go interfaces and Command/Query/Result
  types. Protobuf and other wire formats belong only to optional external
  adapters and are not scaffold concerns.
- Cross-module ACID transactions are allowed when one initiating module owns
  the workflow and calls only public contracts. Define the narrow transaction
  interface in that use case; do not add a framework-wide UoW.
- Local EventBus notifications are process-local. Use explicit durable delivery
  when the Application communicates with an external system that requires it.

## Three phases

1. **Framework** creates one compiling Application, shared resources and
   transports, typed `wiring.Platform`, and an `Assembly` seam. It creates no
   fake business packages or repositories.
2. **Contract** is Agent-led. Write narrow Go contracts, stable reasons, use-case
   inputs/outputs, and edge mappings. Temporary work may carry
   `modular:contract-unimplemented`.
3. **Business** implements use cases, domain rules, adapters, and focused tests.
   Remove scaffold markers before `make verify`.

## Ownership and lifecycle

Managed files are replaced only when their manifest hash is unchanged.
Scaffold-once files become user-owned after creation. A conflict is a stop
condition: inspect `--diff`, move extensions to the intended seam, or perform a
deliberate migration. Destructive commands preview by default and require
`--apply`.

Keep `cmd/<application>/framework.gen.go` managed and `policy.go`
scaffold-once. The Application loads config, installs logging, creates shared
resources, calls `WireApplication(platform)`, builds shared endpoints, then
runs the `app.Application` lifecycle.

## Completion gates

- `make scaffold-check`: framework doctor, placeholders, and build.
- `make contract-check`: Go contract doctor and build; explicitly marked
  unfinished contracts are allowed.
- `make verify`: format, vet, build, tests, race tests, test-presence checks,
  and no unfinished markers.

Do not report completion until the gate for the current phase passes. Keep
domain errors language-free and localize stable errors at Application edges.
Logger/EventBus queue data remains directly in
`github.com/cyub/ringbuffer.MpscRingBuffer`.

## Tool invocation

For a new v0.4 project, use the installed tool:

```bash
python3 scripts/modular.py init myapp --modular-version v0.4.0
```

For an existing project, use its local copy:

```bash
make scaffold-module MODULE=user
make scaffold-module MODULE=order DEPENDS_ON=user
make scaffold-resource RESOURCE=db DRIVER=bun
make scaffold-check
```
