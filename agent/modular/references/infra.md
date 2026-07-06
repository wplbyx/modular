# Infrastructure Resources

Constructors and `core.Resource` wrappers for the `resource` command. Source: `packages/infra/`.

## Table of contents

- [How to wrap infra as a Resource](#how-to-wrap-infra-as-a-resource)
- [Database](#database)
- [Redis](#redis)
- [Storage](#storage)
- [Telemetry](#telemetry)

## How to wrap infra as a Resource

None of the infra packages ship a `core.Resource` directly (except telemetry). The pattern: in `cmd/` (or a small adapter), call the constructor, hold the returned instance, and expose a `core.Resource` whose `Setup`/`Close` manage it. Example shape:

    type dbResource struct {
        cfg *config.Database
        db  *bun.DB
    }
    func (r *dbResource) Name() string { return "database" }
    func (r *dbResource) Setup(ctx context.Context) error {
        db, err := bun.NewBunConnection(r.cfg)
        if err != nil { return fmt.Errorf("database setup: %w", err) }
        r.db = db
        return nil
    }
    func (r *dbResource) Close(ctx context.Context) error { return r.db.Close() }

Inject the returned `bun.DB` / `gorm.DB` / `redis.UniversalClient` / `storage.Storage` into repositories as a dependency - do NOT rely on the package-level globals (`GetDB()`, `GetClient()`) from business code.

## Database

Two adapters; pick one per project.

Bun (`packages/infra/database/bun`): `bun.NewBunConnection(cfg *config.Database) (*bun.DB, error)`. **Postgres only** (`DSNPostgres`). Pings on connect. Also sets a package global `globalDB` (ignore it; use the returned instance). Migrations: `bun.NewMigrationTool(db, migrationsFS embed.FS)` / `bun.NewBunMigration(migrationsFS)` (uses the global). Models implement `database.ModelIndexer` to declare indexes.

GORM (`packages/infra/database/gorm`): `gorm.NewGormConnection(cfg *config.Database) (*gorm.DB, error)`. Supports `DSNSqlite`, `DSNMySQL`, `DSNPostgres`, `DSNClickhouse`. `SkipDefaultTransaction: true` is set by default. Also pings and sets a package global.

Supported dialect constants live in `packages/infra/database/database.go`: `DSNSqlite`, `DSNMySQL`, `DSNPostgres`, `DSNClickhouse`. Set `config.Database.Dsn` to one of these.

## Redis

`packages/infra/cache/redis`: `redis.NewRedisClient(cfg *config.Redis) (redis.UniversalClient, error)`. Universal client: pass `Urls` for a cluster/sentinel, or `Host`+`Port` for a single node. Pings on connect. Extras: `redis` package also has `bloom.go` (Bloom filter via Lua scripts) and `idempotence.go`. Sets a package global `globalClient`.

## Storage

`packages/infra/storage`. The `Storage` interface is the contract. Two backends:

- Disk: `packages/infra/storage/filedisk`. `filedisk.NewDiskStorage(cfg *config.Storage) (*DiskStorage, error)`. Local filesystem.
- OSS: `packages/infra/storage/alioss`. `alioss.NewOSSStorage(cfg *config.Storage) (*OssStorage, error)`. **OSS v2 SDK only** (`alibabacloud-oss-go-sdk-v2`); v1 is forbidden.

`config.Storage.Type` is `oneof=disk oss`. The `storage.NewStorage(cfg)` factory in `storage.go` is **commented out** - callers MUST construct the backend directly (`filedisk.NewDiskStorage` / `alioss.NewOSSStorage`). `storage.ErrUnsupportedStorageType` is exported for reference. The interface supports single-file CRUD, batch upload/delete, prefix iteration, and multipart upload. IO options: `WithQuiet`, `WithContentType`, `WithVersionID`, `WithConcurrency`, `WithMeta`.

## Telemetry

`packages/telemetry`: `telemetry.NewOpenTelemetry(ctx, name, version, cfg *config.Telemetry) (*OpenTelemetry, error)`. This IS a `core.Resource` already (`Name()` + `Setup(ctx)` + `Close(ctx)`). Each of `Tracer`/`Metric`/`Logger` is an OTLP gRPC endpoint string; empty string skips that signal. `Close` flushes and shuts down all initialized providers. Register directly: `app.WithResource(otel)`.

Gin integration: `telemetry.GinMiddleware(serviceName) gin.HandlerFunc` adds tracing + metrics to an HTTP server. Attach via `httpserver.WithMiddleware(telemetry.GinMiddleware(cfg.Name))`. `telemetry.GetSpan(c)` / `telemetry.GetStartTime(c)` read from gin.Context.
