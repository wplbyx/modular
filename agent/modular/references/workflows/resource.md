# Resource workflow

Use when adding DB, Redis, Storage, Telemetry, or EventBus. Read
[commands](../commands.md), [config](../config.md), [infrastructure](../infra.md),
and [lifecycle](../lifecycle.md).

1. Identify the real module use case and the narrow dependency it needs.
2. Select the Application-owned Resource and driver/dialect.
3. Review `resource add ... --diff` and apply.
4. Keep generated Resource construction in managed bootstrap code.
5. Inject the typed Provider or a narrower project adapter into the owning
   module in `WireApplication`; never use package globals.
6. Call `Value()` only while handling work after Application Setup.
7. Run `make scaffold-check`, then add repository behavior and tests.

EventBus is a best-effort process-local notification Resource. It does not
replace durable delivery to external systems.
