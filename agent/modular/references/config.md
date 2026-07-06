# Config

The config layer. Read when adding config. Source of truth: `packages/config/`.

## Table of contents

- [Strong types](#strong-types)
- [Loading](#loading)
- [Combining types in a project](#combining-types-in-a-project)
- [Validation and watching](#validation-and-watching)

## Strong types

All config is typed structs with `mapstructure` tags (pascal-case). Use these, never bare `map[string]any`:

- `config.Application`: `Name` (required), `Mode` (required, oneof dev|test|prod), `Version` (required), `ShutdownTimeout`.
- `config.HTTP`: `Host` (required), `Port` (required, 1000-65535), `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, `ShutdownTimeout`, `EnableTLS`, `TLSKeyFile`, `TLSCertFile`.
- `config.GRPC`: `Host` (required), `Port` (required, 1000-65535), `Timeout`, `ShutdownTimeout`, `EnableTLS`, `TLSKeyFile`, `TLSCertFile`.
- `config.Database`: `Dsn` (required, oneof sqlite|mysql|postgres|clickhouse), `Urls`, `Host`, `Port`, `Path` (sqlite), `Database`, `Username`, `Password`, `MaxOpenConn`, `MaxIdleConn`, `ConnMaxLifetime`, `ConnMaxIdleTime`, `EnableTLS`.
- `config.Redis`: `Urls`, `Host`, `Port`, `Username`, `Password`, `Database`, `PoolSize`, `MinIdleConn`, `DialTimeout`, `ReadTimeout`, `WriteTimeout`, `MaxRetries`, `MinRetryBackoff`, `MaxRetryBackoff`.
- `config.Storage`: `Type` (required, oneof disk|oss), `PublicBaseURL`, `Disk *DiskStorageConfig`, `OSS *OSSStorageConfig`. `DiskStorageConfig`: `RootDir`, `BaseUrl`. `OSSStorageConfig`: `AccessKeyID`, `AccessKeySecret`, `SecurityToken`, `Region`, `Bucket`, `Endpoint`, `BaseDir`, `DisableSSL`, `UseCName`, `Timeout`, `MaxRetries`.
- `config.Telemetry`: `Logger`, `Metric`, `Tracer` (each an OTLP gRPC endpoint string; empty disables that signal).
- `config.Logging`: `Level` (required, oneof debug|info|warn|error), `Output []string`, `File FileConfig`, `OTel OTelConfig`. `FileConfig`: `Filename`, `MaxSize`, `MaxBackups`, `MaxAge`, `Compress`, `SplitRange`. `OTelConfig`: `Endpoint`, `Insecure`.
- `config.CustomConfig` is a ready-made aggregate embedding `Application`, `Database`, `Redis`, `HTTP`. Prefer building a project-specific aggregate (see below).

## Loading

`config.InitConfigure(target, options...)` unmarshals into the target via Viper, with a `time.Duration` decode hook, then runs `validator` on the struct. Options:

- `config.WithConfigFile(filename, filetype, paths...)` - read a file (e.g. "config", "yaml", "./config"). A missing file is not fatal (ConfigFileNotFoundError is tolerated); a malformed one is.
- `config.WithEnvPrefix(prefix, replaces...)` - load env vars matching `<PREFIX>_KEY`, lowercased with `_` -> `.`. Automatic env is intentionally disabled (Viper quirks); env is read manually.
- `config.WithCommandLine(flagSet *pflag.FlagSet)` - bind pflag flags. Pass nil to use a default flag set.
- `config.WithRemoteProvider(provider, endpoint, path)` - etcd/consul/firestore remote config; reads via `ReadRemoteConfig`.

At least one option is required or `NewConfigureLoader` errors.

## Combining types in a project

A project defines its own aggregate. Embed the library types and add domain config:

    type Config struct {
        config.Application `mapstructure:"application,squash"`
        HTTP    config.HTTP     `mapstructure:"http"`
        GRPC    config.GRPC     `mapstructure:"grpc"`
        Database config.Database `mapstructure:"database"`
        Redis   config.Redis    `mapstructure:"redis"`
        Storage config.Storage  `mapstructure:"storage"`
        Telemetry config.Telemetry `mapstructure:"telemetry"`
        Logging config.Logging  `mapstructure:"logging"`
        Domains DomainConfigs   `mapstructure:"domains"`
    }

Use `,squash` on the embedded `Application` so its fields sit at top level under the `application` key (matches `config.CustomConfig`'s flat layout).

## Validation and watching

`InitConfigure` runs `validator.Struct` automatically; `validate:"required,oneof=..."` tags are enforced. For manual validation use `config.ValidateNode(obj)`.

`ConfigureLoader.Watch(callback)` watches the local file for changes (Viper WatchConfig). `WatchRemoteConfig(ctx, callback)` polls a remote provider every 5s until ctx is cancelled.

## Note on generated tags

`packages/config/` has `//go:generate gomodifytags ...` directives. Do NOT rely on running that tooling in downstream projects; the library structs already carry `mapstructure` tags. Only regenerate if editing the library's own config structs.
