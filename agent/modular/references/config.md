# Config

The config layer. Read when adding config. Source of truth: `packages/config/`.

## Table of contents

- [Strong types](#strong-types)
- [Loading](#loading)
- [Combining types in a project](#combining-types-in-a-project)
- [Validation and watching](#validation-and-watching)

## Strong types

All infrastructure config items live in `packages/config/configitem` and use typed structs with `mapstructure` tags. Use these, never bare `map[string]any`:

- `configitem.Application`: `Name` (required), `Mode` (required, oneof dev|test|prod), `Version` (required), `ShutdownTimeout`.
- `configitem.HTTP`: `Host` (required), `Port` (required, 1000-65535), `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, `ShutdownTimeout`, `EnableTLS`, `TLSKeyFile`, `TLSCertFile`.
- `configitem.GRPC`: `Host` (required), `Port` (required, 1000-65535), `Timeout`, `ShutdownTimeout`, `EnableTLS`, `TLSKeyFile`, `TLSCertFile`.
- `configitem.Database`: `Dsn` (required, oneof sqlite|mysql|postgres|clickhouse|mongodb), `Urls`, `Host`, `Port`, `Path` (sqlite), `Database`, `Username`, `Password`, `MaxOpenConn`, `MaxIdleConn`, `MaxPoolSize` (MongoDB), `ReplicaSet` (MongoDB), `ConnMaxLifetime`, `ConnMaxIdleTime`, `EnableTLS`.
- `configitem.Redis`: `Urls`, `Host`, `Port`, `Username`, `Password`, `Database`, `PoolSize`, `MinIdleConn`, `DialTimeout`, `ReadTimeout`, `WriteTimeout`, `MaxRetries`, `MinRetryBackoff`, `MaxRetryBackoff`.
- `configitem.Storage`: `Type` (required, oneof disk|oss), `PublicBaseURL`, `Disk *DiskStorageConfig`, `OSS *OSSStorageConfig`.
- `configitem.Telemetry`: `Logger`, `Metric`, `Tracer`.
- `configitem.Logging`: `Level`, `Output`, `File`, `OTel`.
- Broker config items are also available for `Kafka`, `MQTT`, `RabbitMQ`, and `RocketMQ`. Only include modules the svc actually uses.

## Loading

`config.InitConfigure(target, options...)` unmarshals into the target via Viper, with a `time.Duration` decode hook, then runs `validator` on the struct. Options:

- `config.WithConfigFile(path, ignoreNotFound)` - read one exact file path (e.g. `"./config/user/config.yaml"`). It does not search directories or infer an extension. When `ignoreNotFound` is true, only a missing file is ignored; malformed files, permission failures, and other errors are still returned.
- `config.WithEnvPrefix(prefix, replaces...)` - bind env vars matching `<PREFIX>_KEY`, lowercased with `_` -> `.`.
- `config.WithRemoteProvider(provider, endpoint, path)` - configure a Viper remote provider directly. The content format is inferred from the key extension and defaults to YAML.
- `config.WithRemoteURL(url)` - configure etcd or Consul through a single URL. `etcd://10.0.0.1:2379/config/myapp` maps to the modern `etcd3` provider; `consul://10.0.0.1:8500/config/myapp` maps to Consul. Add `?format=json` when the remote value is not YAML and the key has no extension.

For Cobra applications, prefer `config.NewRoot[T](config.Options[T]{...})`. It registers only the `configitem` modules selected by `T`, plus the shared source flags:

```text
--config, -c <path>   local config file
--remote <url>        etcd or Consul remote config
```

`Options.DefaultFile` and `Options.DefaultRemote` set their defaults. The flags can be used together. Precedence is explicit config flag > environment > local file > remote KV > `FlagSpec` default.

A missing `DefaultFile` is tolerated, while a path explicitly supplied through `--config` must exist. If remote loading fails and a local file was successfully loaded, the loader logs a warning and continues with the local file. Without a valid local file, the remote error is returned. When both sources are present their canonical formats must match; otherwise the remote source is skipped with a warning. Unknown remote keys are ignored when unmarshalling into the strongly typed aggregate.

At least one option is required or `NewConfigureLoader` errors.

`packages/config/app.yml` is kept as a loadable sample and uses the real PascalCase `mapstructure` keys under lowercase top-level sections (`application`, `http`, `grpc`, `storage`, etc.). Use it as a shape reference, not as production credentials.

`config.GetConfigFlagSpecsWithPrefix[T](prefix)` lets a project Config implement `FlagProvider` while preserving a parent prefix. Generated svc configs use it so a single-process aggregate exposes flags such as `--user.http.port`.

## Combining types in a project

A project defines one aggregate per svc in `config/<svc>/config.go`, next to `config/<svc>/config.yaml`. The svc config package owns the strong type and its `FlagProvider` implementation; generated `cmd/` entrypoints import the appropriate process config and let `config.NewRoot` perform loading instead of defining anonymous structs or calling a svc-specific loader in `main.go`.

    package config

    import (
        modularconfig "github.com/wplbyx/modular/packages/config"
        "github.com/wplbyx/modular/packages/config/configitem"
    )

    type Config struct {
        configitem.Application `mapstructure:"application,squash"`
        HTTP      configitem.HTTP      `mapstructure:"http"`
        GRPC      configitem.GRPC      `mapstructure:"grpc"`
        Database  configitem.Database  `mapstructure:"database"`
        Redis     configitem.Redis     `mapstructure:"redis"`
        Storage   configitem.Storage   `mapstructure:"storage"`
        Telemetry configitem.Telemetry `mapstructure:"telemetry"`
        Logging   configitem.Logging   `mapstructure:"logging"`
    }

    func (Config) Flags(prefix string) []modularconfig.FlagSpec {
        return modularconfig.GetConfigFlagSpecsWithPrefix[Config](prefix)
    }

Use `,squash` on the embedded `Application` so callers can access `cfg.Name` / `cfg.Version` while the serialized values remain under the `application` key.

An optional `Load` helper may be retained for tests or non-Cobra embedding, but generated application entrypoints use `NewRoot` so local files, remote KV, environment variables, and CLI overrides follow one loading path.

## Process Config

- Service topology: `cmd/<svc>` runs `NewRoot[config/<svc>.Config]` with `config/<svc>/config.yaml`.
- Single topology: the skill generates `config/<project>/Config` containing the process `Application` plus one nested field per svc. Its YAML is generated from the `config/<svc>/config.yaml` fragments.
- Treat `config/<project>/config.go|yaml` as generated output. Edit svc fragments, then rerun a scaffold/resource command to rebuild the process aggregate.
- Remote configuration for a single process uses the same nested shape (`user.http`, `billing.redis`, etc.).
- A single-topology svc may not have the same name as the project because both would own `config/<project>`.

## Validation and watching

`InitConfigure` runs `validator.Struct` automatically; `validate:"required,oneof=..."` tags are enforced. For manual validation use `config.ValidateNode(obj)`.

`ConfigureLoader.Watch(callback)` watches the local file for changes (Viper WatchConfig). `WatchRemoteConfig(ctx, callback)` polls a remote provider every 5s until ctx is cancelled.

## Note on generated tags

`packages/config/` has `//go:generate gomodifytags ...` directives. Do NOT rely on running that tooling in downstream projects; the library structs already carry `mapstructure` tags. Only regenerate if editing the library's own config structs.
