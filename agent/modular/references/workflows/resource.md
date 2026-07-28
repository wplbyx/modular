# Resource workflow

Use this workflow for DB, Redis, Storage, or Telemetry wiring.
Read [infrastructure](../infra.md), [config](../config.md), and
[lifecycle](../lifecycle.md) before selecting a Resource adapter.

1. Run `resource add` with an explicit svc when more than one svc exists.
2. Choose the library Resource constructor: Bun, GORM plus dialect, Mongo,
   Redis, storage resource, or OpenTelemetry.
3. Keep resources in Application lifecycle order and expose typed providers to
   the business wiring seam. Never call `Provider.Value()` before Setup.
4. Review `--diff`, then run `make scaffold-check` and the relevant unit tests.
5. Remove resources only with `--apply`; modified managed files must produce a
   conflict rather than being overwritten.
