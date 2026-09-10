# Resource workflow

Use for DB, Redis, Storage, Telemetry, or Local EventBus. Read
[infrastructure](../infra.md), [config](../config.md), and
[lifecycle](../lifecycle.md).

1. Select the owning Process. Omit `--process` only when exactly one exists.
2. Choose the library constructor: Bun, GORM+dialect, Mongo, Redis, Storage,
   OpenTelemetry, or EventBus.
3. Review `resource add ... --diff`. Confirm Process config has one resource
   field and typed `wiring.Platform` exposes its provider to hosted modules.
4. Keep Application lifecycle order and inject providers into repositories;
   resolve `Value()` only after Setup.
5. Run `make scaffold-check` and focused adapter tests. Use
   `resource remove ... --apply` only after reviewing dependents.

EventBus is a best-effort Local Notification Resource. It does not create
events or handlers and is not a reliable cross-Process integration mechanism.
