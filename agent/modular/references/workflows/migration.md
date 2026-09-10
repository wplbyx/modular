# Migration and extraction workflow

Use for v0.2 migration or moving a Business Module to another Process. Read
[commands](../commands.md), [layering](../layering.md),
[registry](../registry.md), and [config](../config.md).

## v0.2 to v0.3

1. Use the newly installed v0.3 tool and run `migrate v0.2-to-v0.3 --diff`.
2. Confirm each old svc becomes a Business Module. A single topology maps to
   one Process; service topology maps each svc to a Process.
3. Resolve conflicting per-service copies of a Resource before applying. A
   customized business wiring file stops automatic migration and must be
   adapted manually to `Platform`/`Contribution`. Move customized
   `config/<svc>/config.go` settings to `config/modules/<svc>/config.go`; the
   migration removes only unchanged legacy extensions.
4. Confirm the migration preserves non-modular `go.mod` requirements. It
   upgrades unchanged Buf/gitignore scaffolds. Customized copies are preserved
   when they contain the required plugin/ignore entry; otherwise add the entry
   named by the migration error and retry.
5. Apply with a concrete v0.3 version, inspect `.modular/architecture.yaml`,
   then run `make scaffold-check`.

## Module extraction

1. Name the operational reason: independent scaling, release cadence, fault
   isolation, compliance, or ownership. Do not extract only to imitate a
   microservice layout.
2. Declare actual shared transactions/data/local-event/streaming blockers.
3. Add stable protobuf contracts and consumer-owned Remote Adapters for every
   dependency that will cross the target Process boundary.
4. Replace local notification assumptions with durable outbox/inbox delivery
   where required; specify idempotency, retries, and reconciliation.
5. Run `module extract <module> --to-process <process> --check`. Apply only when
   it is clean, then configure discovery and run end-to-end tests.

Extraction changes managed grouping, not business semantics. Proto, generated
common output, module internals, and scaffold-once wiring remain owned by the
project.
