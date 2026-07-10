# Infrastructure Resources

Constructors and `core.Resource` wiring for the `resource` command. Source: `packages/infra/`.

## Table of contents

- [How to wrap infra as a Resource](#how-to-wrap-infra-as-a-resource)
- [Database](#database)
- [Redis](#redis)
- [Storage](#storage)
- [Telemetry](#telemetry)

## How to wrap infra as a Resource

Redis, Bun, GORM, and telemetry now ship `core.Resource` implementations. Prefer them in `cmd/` so Application owns setup/close ordering. Mongo and storage currently need project-side wrappers if they should participate in the Application lifecycle.

Inject the returned `bun.DB` / `gorm.DB` / `mongo.Client` / `redis.UniversalClient` / `storage.Storage` into repositories as a dependency - do NOT rely on the package-level globals (`GetDB()`, `GetClient()`) from business code.

## Database

Three adapters; pick one per project.

Bun (`packages/infra/database/bun`): `bun.NewBunConnection(cfg *config.Database) (*bun.DB, error)`. **Postgres only** (`DSNPostgres`). Pings on connect. Also sets a package global `globalDB` (ignore it; use the returned instance). For Application wiring use `bun.NewResource(cfg)`; after setup read the connection with `resource.DB()`. The CLI command is `resource db --driver bun`. Migrations: `bun.NewMigrationTool(db, migrationsFS embed.FS)` / `bun.NewBunMigration(migrationsFS)` (uses the global). Models implement `database.ModelIndexer` to declare indexes.

GORM (`packages/infra/database/gorm`): `gorm.NewGormConnection(cfg *config.Database) (*gorm.DB, error)`. Supports `DSNSqlite`, `DSNMySQL`, `DSNPostgres`, `DSNClickhouse`. `SkipDefaultTransaction: true` is set by default. Also pings and sets a package global. For Application wiring use `gorm.NewResource(cfg)`; after setup read the connection with `resource.DB()`. The CLI command is `resource db --driver gorm`.

MongoDB (`packages/infra/database/mongo`): `mongo.NewMongoConnection(cfg *config.Database) (*mongo.Client, error)`. Supports `DSNMongo`. Use `Urls` for host lists or a single MongoDB URI, or `Host`+`Port` for a single node. Optional Mongo fields: `ReplicaSet`, `MaxPoolSize`. Pings on connect and sets a package global. There is no library Resource yet; `resource db --driver mongo` generates `internal/<svc>/repository/mongo_resource.go`.

Supported dialect constants live in `packages/infra/database/database.go`: `DSNSqlite`, `DSNMySQL`, `DSNPostgres`, `DSNClickhouse`, `DSNMongo`. Set `config.Database.Dsn` to one of these.

## Redis

`packages/infra/cache/redis`: `redis.NewRedisClient(cfg *config.Redis) (redis.UniversalClient, error)`. Universal client: pass `Urls` for a cluster/sentinel, or `Host`+`Port` for a single node. Pings on connect. For Application wiring use `redis.NewResource(cfg)`; after setup read the client with `resource.Client()`. The CLI command is `resource redis`. Extras: `redis` package also has `bloom.go` (Bloom filter via Lua scripts) and `idempotence.go`. Sets a package global `globalClient` for compatibility.

## Storage

`packages/infra/storage`. The `Storage` interface is the contract. Two backends:

- Disk: `packages/infra/storage/filedisk`. `filedisk.NewDiskStorage(cfg *config.Storage) (*DiskStorage, error)`. Local filesystem.
- OSS: `packages/infra/storage/alioss`. `alioss.NewOSSStorage(cfg *config.Storage) (*OssStorage, error)`. **OSS v2 SDK only** (`alibabacloud-oss-go-sdk-v2`); v1 is forbidden.

`config.Storage.Type` is `oneof=disk oss`. The `storage.NewStorage(cfg)` factory in `storage.go` is **commented out** - callers MUST construct the backend directly (`filedisk.NewDiskStorage` / `alioss.NewOSSStorage`). The CLI command `resource storage` generates `internal/<svc>/repository/storage_resource.go`, a project-side `StorageResource` wrapper that does this switch and exposes `Storage() storage.Storage`. `storage.ErrUnsupportedStorageType` is exported for reference. The composite `storage.Storage` interface supports single-file CRUD, batch upload/delete, prefix iteration, and multipart upload. Smaller interfaces are available when repositories need less: `ObjectStore`, `BatchStore`, `PrefixStore`, `MultipartStore`. IO options: `WithQuiet`, `WithContentType`, `WithVersionID`, `WithConcurrency`, `WithMeta`; nil options are ignored. Disk validates prefix paths against traversal. OSS sorts multipart completion parts and preserves http/https endpoint scheme when building fallback URLs.

## Telemetry

`packages/telemetry`: `telemetry.NewOpenTelemetry(ctx, name, version, cfg *config.Telemetry) (*OpenTelemetry, error)`. This IS a `core.Resource` already (`Name()` + `Setup(ctx)` + `Close(ctx)`). The constructor stores config only; `Setup(ctx)` creates providers and installs globals, and cleans up partially-created providers on failure. Each of `Tracer`/`Metric`/`Logger` is an OTLP gRPC endpoint string; empty string skips that signal. `Close` flushes, shuts down initialized providers, and clears provider fields. Register directly: `app.WithResource(otel)`. The CLI command is `resource telemetry`.

Gin integration: `telemetry.GinMiddleware(serviceName) gin.HandlerFunc` adds tracing + metrics to an HTTP server. Attach via `httpserver.WithMiddleware(telemetry.GinMiddleware(cfg.Name))`. `telemetry.GetSpan(c)` / `telemetry.GetStartTime(c)` read from gin.Context.
