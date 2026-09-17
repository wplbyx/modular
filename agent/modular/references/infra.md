# Infrastructure Resources

Read when adding an Application Resource or a module-owned adapter. Source:
`packages/infra`, `packages/eventbus`, `packages/telemetry`, and
`packages/idgen`.

## Shared Resources

The project creates shared Resources in `cmd/<application>/resources.go` by
calling modular library constructors. Do not add a project-level `platform`,
`shared`, or `infrastructure` package merely to wrap those constructors.
`cmd/<application>/modules.go` passes the necessary providers to each module's
Dependencies. Module bootstrap constructs its private infrastructure adapters;
see [layering](layering.md) for the two assembly interfaces.

Bun, GORM, MongoDB, Redis, Storage, and ID generators use
`core.ManagedResource[T]`. Each result implements:

- `core.Resource` for Application lifecycle ownership.
- `core.Provider[T]` for typed module adapter injection.
- `health.Checker` structurally through `Name` and `Check` when applicable.

Retain the Provider in a repository and call `Value()` only while handling work
after Application Setup. There are no DB, Mongo, Redis, Storage, HTTP-client, or
ID-generator package globals.

## Database and migrations

SQL uses `configitem.Database` with an explicit DSN and pool settings. Dialect
selection belongs in `cmd/<application>/resources.go`.

- Bun/PostgreSQL: `bun.NewResource(&cfg.Database)`.
- GORM/PostgreSQL: `gorm/postgres.NewResource(&cfg.Database)`.
- GORM/MySQL: `gorm/mysql.NewResource(&cfg.Database)`.
- GORM/ClickHouse: `gorm/clickhouse.NewResource(&cfg.Database)`.
- GORM/SQLite: `gorm/sqlite.NewResource(&cfg.Database)`; this is pure Go.
- MongoDB: `mongo.NewResource(&cfg.Mongo)` using `configitem.Mongo`.

Each module's GORM adapter may expose `Migrate(ctx) error`. Module bootstrap exposes
those callbacks through its assembly result; cmd aggregates them into one `core.FuncResource`
ordered immediately after the database. Do not put project migrations in
`.modular/architecture.yaml` or call them before the DB Resource is set up.

## Other resources

- Redis: `redis.NewResource(&cfg.Redis)`.
- Storage: `storageresource.New(&cfg.Storage)` from
  `packages/infra/storage/resource`; only disk and OSS v2 are supported.
- EventBus: one process-local `eventbus.Bus` Resource, injected into publishers
  and subscriber adapters.
- UUIDv7: `idresource.NewUUIDv7(name)` supplies
  `core.Provider[idgen.Generator]` without coordination. Add it with
  `resource add idgen --driver uuidv7`.
- Telemetry: `telemetry.NewOpenTelemetry` is registered once as a Resource and
  normally is not injected into repositories.

EventBus is best-effort process-local notification. Publishing after a database
commit does not make delivery durable; use an explicit Outbox when external
delivery guarantees are part of the requirements.
