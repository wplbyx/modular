# Config

The config layer. Read when adding config. Source of truth: `packages/config/`.

## Table of contents

- [Strong types](#strong-types)
- [Loading](#loading)
- [Combining types in a project](#combining-types-in-a-project)
- [Validation and watching](#validation-and-watching)

## Strong types

Infrastructure config items live in `packages/config/configitem` and use typed structs with PascalCase `mapstructure` tags. Use these rather than bare `map[string]any`:

- `configitem.Application`: `Name`, `Mode`, `Version`, `ShutdownTimeout`.
- `configitem.HTTP`: `Host`, `Port` (0 or 1000-65535), timeouts, and TLS fields. TLS requires both certificate and key files.
- `configitem.GRPC`: `Host`, `Port`, timeouts, and TLS fields.
- `configitem.Database`: explicit SQL `DSN` and pool settings. The cmd selects the SQL dialect adapter.
- `configitem.Mongo`: exactly one of `URI` or `Hosts`; URI starts with `mongodb://` or `mongodb+srv://`.
- `configitem.Redis`: `Urls` or the `Host` and `Port` fallback, plus credentials and pool settings.
- `configitem.Storage`: selected `disk` or `oss` backend and its required constructor fields.
- `configitem.Logging`, `Telemetry`, `EventBus`, `Kafka`, `MQTT`, `RabbitMQ`, and `RocketMQ`. Only include modules the process uses.

## Loading

`config.InitConfigure(target, options...)` accepts a non-nil struct pointer. It applies every selected module's `FlagSpec` defaults, unmarshals through Viper's standard duration and string-slice hooks, then runs field and structure-level validation. No source option is required.

- `config.WithConfigFile(path, ignoreNotFound)` reads one exact path. `ignoreNotFound` ignores only a missing file.
- `config.WithConfigFS(fsys, path)` reads one exact file from an `fs.FS`, including `go:embed`.
- `config.WithEnvPrefix(prefix, replaces...)` binds matching environment variables; prefix matching is case-insensitive and `_` separates levels.
- `config.WithRemoteProvider(provider, endpoint, path)` configures a Viper remote provider directly.
- `config.WithRemoteURL(url)` configures etcd or Consul with `etcd://` or `consul://`; add `?format=json` when needed.
- `config.WithStrictDecode()` rejects keys absent from the target. The default filters unknown keys and relies on final validation for completeness.

For Cobra applications, use `config.NewRootCommand[T](config.CommandOptions[T]{...})`. It registers only selected config modules plus `--config/-c` and `--remote`. `CommandOptions.StrictDecode` enables strict decoding. Precedence is CLI > environment > local file > remote KV > `FlagSpec` default.

A missing default file is tolerated; an explicitly supplied `--config` path must exist. Remote failure falls back only to an already loaded local file. Local and remote canonical formats must match when used together.

Canonical paths use PascalCase at every typed level:

```text
YAML: Application.Name, Storage.OSS.AccessKeyID
CLI:  --Application.Name, --Storage.OSS.AccessKeyID
ENV:  APP_APPLICATION_NAME, APP_STORAGE_OSS_ACCESSKEYID
```

Environment `_` is reserved for hierarchy. Do not turn `AccessKeyID` into `ACCESS_KEY_ID`. Viper still decodes file keys case-insensitively, so lowercase YAML is accepted, but generators, samples, flags, and documentation emit PascalCase only. `packages/config/app.yml` is the loadable shape reference.

`config.GetConfigFlagSpecsWithPrefix[T](prefix)` lets a composed project config implement `FlagProvider`. A single-process aggregate therefore exposes paths such as `--User.HTTP.Port`.

## Combining types in a project

A project composes a managed `Generated` type with a scaffold-once `Config` extension:

```go
package config

import (
    modularconfig "github.com/wplbyx/modular/packages/config"
    "github.com/wplbyx/modular/packages/config/configitem"
)

type Generated struct {
    Application configitem.Application `mapstructure:"Application"`
    Logging     configitem.Logging     `mapstructure:"Logging"`
    HTTP        configitem.HTTP        `mapstructure:"HTTP"`
    Database    configitem.Database    `mapstructure:"Database"`
}

type Config struct {
    Generated `mapstructure:",squash"`
}

func (Config) Flags(prefix string) []modularconfig.FlagSpec {
    return modularconfig.GetConfigFlagSpecsWithPrefix[Generated](prefix)
}
```

`Application` is a named field. Use `cfg.Application.Name` and `cfg.Application.Version`. Do not put `,squash` on `Application`: squash removes that nesting during mapstructure decode, so values under `Application` do not reach the field. The outer generated wrapper remains squashed because it intentionally adds no configuration level.

- Service topology runs `NewRootCommand[config/<svc>.Config]`; that config contains process `Application` and `Logging`.
- Single topology generates a process config with process `Application`, process `Logging`, and one PascalCase nested field per service.
- Treat generated `config.gen.go` and YAML as managed output. Remote configuration uses the same nested shape.

## Validation and watching

`config.Validate` enforces `validate` tags and built-in Storage, Mongo, Redis, and HTTP cross-field rules. It returns `*config.ValidationError` containing `[]config.Violation` with canonical paths, rule names, and parameters. Error strings never include actual configuration values. `ValidateNode` is a deprecated compatibility wrapper.

`ConfigureLoader.Watch(callback)` watches a local file. `WatchRemoteConfig(ctx, callback)` polls a remote provider every five seconds until cancellation.

`packages/config/` has `//go:generate gomodifytags ...` directives. Run them only when editing the library's config structs; downstream projects do not need that tool.
