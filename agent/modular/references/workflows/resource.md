# Resource workflow

Use when adding DB, Redis, Storage, Telemetry, EventBus, or an ID generator. Read
[commands](../commands.md), [config](../config.md),
[infrastructure](../infra.md), and [lifecycle](../lifecycle.md).

1. Identify the real module use case and narrow dependency it needs.
   When defining a port or choosing delivery semantics, read
   [outbound needs and delivery guarantees](../interface-design.md#define-outbound-needs-and-delivery-guarantees).
2. Select the Application-owned Resource and driver/dialect. UUIDv7 is the
   coordination-free default for business IDs.
3. Review `resource add ... --diff`, then apply.
4. Keep shared Resource construction in the managed region of
   `cmd/<application>/resources.go`.
5. In `modules.go`, pass the necessary typed Provider through the module's
   Dependencies. Module bootstrap constructs its infrastructure adapter and
   injects it into the consuming use case. Use explicit replacement injection
   only when a real variation requires it; see [layering](../layering.md).
6. Call `Value()` only while handling work after Application Setup.
7. Return module migration callbacks through the assembly interface so cmd can
   aggregate them and the migration Resource runs
   after the database and before endpoints.
8. Run `verify --phase framework`, then add adapter behavior and tests.

EventBus is a best-effort process-local notification Resource. It does not
replace durable delivery to external systems.
