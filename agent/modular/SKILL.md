---
name: modular
description: Scaffold, wire, audit, and evolve modular-monolith-first Go projects built on github.com/wplbyx/modular. Use when initializing a project, adding Business Modules or Processes, declaring module dependencies or extraction blockers, wiring process transports/resources, designing contracts and adapters, checking extraction readiness, migrating v0.2 projects, or running scaffold/contract/business verification. Route architectural decisions through the Agent and deterministic framework files through the repository-local CLI.
---

# Modular skill

This skill has two cooperating interfaces:

- The skill guides Business Module boundaries, contracts, and extraction.
- `.modular/tool/modular.py` deterministically owns framework files and is
  called by the generated Makefile.

## Route first

| Intent | Read first | CLI action |
| --- | --- | --- |
| New project or module grouping | [init](references/workflows/init.md) | `init`, `module`, `process` |
| Simple CRUD contract | [crud](references/workflows/crud.md) | Agent edits + `contract-check` |
| Aggregate or transaction rules | [domain](references/workflows/domain.md) | Agent edits + `contract-check` |
| DB/Redis/Storage/Telemetry/EventBus | [resource](references/workflows/resource.md) | `resource add/remove` |
| Extraction or v0.2 upgrade | [migration](references/workflows/migration.md) | `module extract`, `migrate` |
| Convention or release audit | [audit](references/workflows/audit.md) | `self-check`, `doctor`, `verify` |

Read only the technical references selected by that workflow. Keep universal
rules here and project-specific policy in `.modular/profile.toml`.

## Architecture contract

- A Business Module owns cohesive behavior and data writes. A Process owns
  deployment, one Application lifecycle, shared infrastructure, and at most one
  HTTP and one gRPC server.
- `.modular/architecture.yaml` declares modules, dependencies, Process
  assignment, capabilities, and extraction blockers. The manifest records only
  generation ownership and replay state.
- Other modules import only `internal/modules/<provider>/contract` or
  `common/<provider>`. Imports across another module's implementation boundary
  are invalid even while both modules share a Process.
- Protobuf defines external and stable cross-module contracts. Generated unary
  `XxxServicePort` has no `grpc.CallOption`; local wiring injects an
  implementation and remote wiring injects its standard gRPC client adapter.
- Transactions stop at a module by default. A necessary shared transaction is
  a declared `shared-transaction` blocker, not hidden portability.
- EventBus carries best-effort Local Notifications. A Process boundary needs a
  durable Integration Event adapter with outbox/inbox, retry, and idempotency.

## Three phases

1. **Framework** creates compiling Process config, shared transports/resources,
   and typed `wiring.Platform`/`Contribution` seams. It creates no fake business
   packages or repositories.
2. **Contract** is Agent-led. Write complete protobuf fields, generated or
   hand-written module ports, stable reasons, and API mappings. Temporary
   methods carry `modular:contract-unimplemented` and return Unimplemented.
3. **Business** implements use cases, domain rules, adapters, and focused tests.
   Remove scaffold markers before `make verify`.

## Ownership and lifecycle

Managed files are replaced only when their manifest hash is unchanged.
Scaffold-once files become user-owned after creation. A conflict is a stop
condition: inspect `--diff`, move extensions to the intended seam, or perform a
deliberate migration. Mutating commands support `--dry-run` and `--diff`;
destructive commands require `--apply`.

Keep `cmd/<process>/framework.gen.go` managed and `policy.go` scaffold-once.
Each Process loads config, creates and installs its logger, builds one transport
policy and health manager, constructs Resources, wires modules, then constructs
shared Endpoints and Application. Application sets up Resources, starts and
waits for Endpoints, registers the Process node, then marks readiness. Shutdown
marks draining, unregisters, stops Endpoints, and closes Resources.

## Completion gates

- `make scaffold-check`: strict framework doctor, placeholders, and build.
- `make contract-check`: Buf lint/generate, contract doctor, and build;
  explicitly marked Unimplemented seams are allowed.
- `make verify`: format, vet, build, tests, race tests, test-presence checks,
  and no unfinished markers.

Do not report completion until the gate for the current phase passes. Keep
domain errors language-free and localize stable error messages at each Process
edge. Logger/EventBus queue data remains directly in
`github.com/cyub/ringbuffer.MpscRingBuffer`.

## Tool invocation

For a new v0.3 project, use the installed tool:

```bash
python3 scripts/modular.py init myapp --modular-version v0.3.0
```

For an existing project, use its local copy:

```bash
make scaffold-module MODULE=user
make scaffold-module MODULE=order DEPENDS_ON=user
make scaffold-resource RESOURCE=db PROCESS=myapp DRIVER=bun
make scaffold-check
```

Initialization resolves a published version and writes no local `replace`.
The legacy `--topology` flag exists only to create v0.2-compatible projects.
