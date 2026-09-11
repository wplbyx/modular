# Config

Read when adding Application or Business Module configuration. Source:
`packages/config`.

## Ownership

- `config/modules/<module>/config.go` is scaffold-once and contains only
  business-owned settings plus its `Flags` method.
- `config/<application>/config.gen.go` and `config.yaml` are managed aggregates.
- Transport and infrastructure settings exist once at Application level. Module
  configs are nested by PascalCase module name.

Example shape:

```go
type Config struct {
	Application configitem.Application `mapstructure:"Application"`
	Logging     configitem.Logging     `mapstructure:"Logging"`
	HTTP        configitem.HTTP        `mapstructure:"HTTP"`
	Database    configitem.Database    `mapstructure:"Database"`
	Order       orderconfig.Config     `mapstructure:"Order"`
}
```

`Application` remains named; do not squash it. Generated cmd uses
`cfg.Application.Name`, `Version`, `InstanceID`, and `Metadata` as runtime
identity. InstanceID is exposed as a CLI/env setting; Metadata is normally
loaded from YAML or remote configuration. A module implements `Flags(prefix)`
with `config.GetConfigFlagSpecsWithPrefix` when it adds fields.

## Loading

`config.InitConfigure(target, options...)` requires a non-nil struct pointer,
applies selected `FlagSpec` defaults, decodes Viper values, and validates.
`config.NewRootCommand[T]` adds typed CLI flags and invokes a callback after
loading. Precedence is CLI > environment > local file > remote KV > defaults.

- `WithConfigFile(path, ignoreNotFound)` reads one exact local file.
- `WithConfigFS(fsys, path)` supports `go:embed`.
- `WithEnvPrefix(prefix, replaces...)` maps `_` to hierarchy.
- `WithRemoteProvider` or `WithRemoteURL` loads etcd/Consul data.
- `WithStrictDecode` rejects unknown keys; default decoding filters them.

Canonical keys use PascalCase at every typed level:

```text
YAML: Application.Name, Order.PaymentTimeout
CLI:  --Application.Name, --Order.PaymentTimeout
ENV:  APP_APPLICATION_NAME, APP_ORDER_PAYMENTTIMEOUT
```

Environment underscores separate levels, not words inside a Go field name.
Local and remote config must use the same shape. Missing default files are
tolerated; an explicitly supplied file must exist.

Use the narrowest `configitem` type for Application resources: `Application`,
`Logging`, `HTTP`, `GRPC`, `Database`, `Mongo`, `Redis`, `Storage`, `Telemetry`,
or `EventBus`. `config.Validate` reports canonical paths without exposing
values. Only library config-struct changes require
`go generate ./packages/config/...` and external `gomodifytags`.
