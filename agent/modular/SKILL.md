---
name: modular
description: Scaffold, wire, audit, and evolve Go projects built on github.com/wplbyx/modular. Use this skill whenever a user asks to initialize a modular project, add framework transport or Resource wiring, define a CRUD or domain contract, attach app/domain adapters, manage project Make commands, migrate topology, inspect generated ownership, or run scaffold/contract/business verification. Route the task before editing: the CLI handles deterministic framework files while the Agent owns architecture decisions and business code.
---

# modular skill

This skill has two cooperating interfaces:

- The installed skill is the architecture guide and Agent workflow.
- `.modular/tool/modular.py` is the repository-local deterministic scaffolder;
  the generated Makefile calls the same tool so commands travel with the code.

## Route first

Classify the request before changing files:

| Intent | Read first | Deterministic action |
| --- | --- | --- |
| New project/topology | [init](references/workflows/init.md) | `init` |
| Simple CRUD contract | [crud](references/workflows/crud.md) | Agent templates + `contract-check` |
| Invariants/aggregate domain | [domain](references/workflows/domain.md) | Agent domain templates + `contract-check` |
| DB/Redis/Storage/Telemetry/EventBus | [resource](references/workflows/resource.md) | `resource add/remove` |
| Topology/tool upgrade | [migration](references/workflows/migration.md) | `migrate` or project upgrade |
| Convention/release audit | [audit](references/workflows/audit.md) | `self-check`, `doctor`, `verify` |

Read the technical reference named by the workflow only after this routing
step. Keep universal modular rules separate from `.modular/profile.toml`
project policy.

## Three phases

1. **Framework** creates a compiling topology, config, cmd, explicitly selected
   HTTP/gRPC endpoints, Resources, Make targets, and a typed business wiring
   seam. Every process loads config first, creates its context-required logger
   second, then builds a cmd-owned transport policy. It does not create
   business packages or fake repositories.
2. **Contract** is Agent-led. Write complete proto fields, app/domain ports,
   stable reasons, and API mappings using the contract templates. A temporary
   method must carry `modular:contract-unimplemented` and return an explicit
   Unimplemented result.
3. **Business** implements entities, use cases, adapters, and tests. Remove
   both scaffold markers before `make verify`; every configured business
   package needs tests. Coverage is reported but has no universal numeric gate.

## Ownership rules

`managed` files such as `.gen.go`, `.modular/tool`, Make fragments, and process
config aggregates may be updated only when their manifest hash is unchanged.
`scaffold-once` files are user/Agent maintained after creation. `common/` is
external buf output. Unregistered files are never overwritten. A conflict is a
stop condition; use `--diff`, move custom code to an extension seam, or make a
deliberate migration decision.

All mutating commands support `--dry-run` and `--diff`. Destructive operations
require `--apply`. The tool stages files and rolls the transaction back when a
post-write doctor, placeholder, buf, or build check fails.

Keep `cmd/<process>/framework.gen.go` managed and
`cmd/<process>/policy.go` scaffold-once. The latter is the explicit project
seam for logging outputs, Metadata allowlists, tracing, access logs, and Aegis
BBR/SRE protection. Never hide these decisions in business packages.

## Completion gates

- `make scaffold-check`: self-check, strict framework doctor, placeholder scan,
  and `go build ./...`.
- `make contract-check`: buf lint/generate, contract doctor, build; marked
  Unimplemented seams are allowed.
- `make verify`: gofmt, vet, build, tests, race tests, test-presence checks, and
  zero business/unwired markers.

Never report a scaffold as complete until its appropriate gate passes. Keep
domain errors language-free and perform client localization at the API edge
with the process-level error Handler.

Never implement or copy a RingMPSC algorithm. Logger/EventBus queue data must
remain directly in `github.com/cyub/ringbuffer.MpscRingBuffer`; auxiliary
channels may signal wakeups only.

## Tool invocation

For a new project use the installed copy:

```bash
python scripts/modular.py init <project> --topology single
```

For an existing project prefer the repository-local interface:

```bash
make scaffold-service SVC=user TRANSPORTS="http grpc"
make scaffold-resource SVC=user RESOURCE=db DRIVER=bun
make scaffold-check
```

The init command resolves a remote published modular version (latest by
default, explicit tag when supplied), writes a concrete Go dependency, and
never creates a local path replacement. v2 requires modular `v0.2.0` or newer.
