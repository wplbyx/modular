# Infrastructure Resources

Constructors and `core.Resource` wiring for the `resource` command. Source: `packages/infra/`.

## Managed resources

Bun, GORM, MongoDB, Redis, and Storage use `core.ManagedResource[T]`. Each result implements:

- `core.Resource` for Application lifecycle ownership.
- `core.Provider[T]` for typed repository injection.
- `health.Checker` structurally through `Name` and `Check`.

Do not call `Value()` before Application has completed Setup. Store the Provider in the repository and resolve it when handling a use case. There are no DB, Mongo, Redis, Storage, or HTTP-client package globals.

## Database

SQL uses `configitem.Database` with an explicit `DSN` and pool settings. Dialect selection belongs in cmd.

- Bun/PostgreSQL: `bun.NewResource(&cfg.Database)`.
- GORM/PostgreSQL: `gorm/postgres.NewResource(&cfg.Database)`.
- GORM/MySQL: `gorm/mysql.NewResource(&cfg.Database)`.
- GORM/ClickHouse: `gorm/clickhouse.NewResource(&cfg.Database)`.
- GORM/SQLite: `gorm/sqlite.NewResource(&cfg.Database)`; this is a pure Go driver.
- MongoDB: `mongo.NewResource(&cfg.Mongo)` using the separate `configitem.Mongo` type.

The CLI form is:

```bash
python agent/modular/scripts/modular.py resource db \
  --driver gorm --dialect postgres --svc user
```

Bun supports PostgreSQL only. Migrations use `bun.NewMigrationTool(db, migrationsFS)` with an explicit DB. Startup migrations or warmups can be modeled with `core.NewFuncResource` and the same typed Provider.

## Redis

Use `redis.NewResource(&cfg.Redis)`. It provides `redis.UniversalClient`, pings during Setup and readiness checks, and closes during Application shutdown.

## Storage

Use `storageresource.New(&cfg.Storage)` from `packages/infra/storage/resource`. This composition package selects only disk or OSS and avoids the root-package import cycle. OSS uses the v2 SDK. Repositories may depend on `core.Provider[storage.Storage]` or expose a narrower storage interface internally.

## Telemetry

`telemetry.NewOpenTelemetry` already implements `core.Resource`. Construct it with the service name/version, register it with Application, and do not pass it to repositories unless they genuinely need that dependency.
