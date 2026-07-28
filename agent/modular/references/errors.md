# Errors and localization

Use `packages/errs` for stable errors that cross a request boundary. Keep application lifecycle, Resource Setup/Close, and shutdown aggregation as ordinary wrapped Go errors with `%w` and `errors.Join`.

## Define the contract

Business packages own stable reasons and variable-slot contracts. Declare messages as package-level values so `err_template_gen` can discover them statically:

```go
const userID errs.Name = "user_id"

var UserNotFound = errs.Define(
	"USER_NOT_FOUND",
	errs.Template("user %v not found", userID),
)

return errs.NotFound(
	UserNotFound.With(userID, id),
	errs.WithCause(cause),
	errs.WithField("user_id", id),
)
```

Rules:

- Reasons match `[A-Z][A-Z0-9_]*`; names match `[a-z][a-z0-9_]*` and are unique within one definition.
- Only `%v` consumes a Name. `%%` emits a literal percent sign. Other fmt verbs are invalid.
- The number of `%v` verbs must equal the number of Names. Invalid declarations panic during package initialization and are reported earlier by the generator.
- Reasons, patterns, and Names must be string constants. Keep `errs.Template` directly inside `errs.Define`; dynamic factories are not discoverable.
- Template values are client-visible. Put secrets, SQL, internal identifiers, and operational context in `WithCause` or `WithField`, not in `Message.With`.

Choose the HTTP constructor that describes the public outcome: `BadRequest`, `Unauthorized`, `Forbidden`, `NotFound`, `Conflict`, `TooManyRequests`, `InternalServer`, `ServiceUnavailable`, or `GatewayTimeout`.

## Generate locale YAML

Install the standalone generator once:

```bash
go install github.com/wplbyx/modular/packages/generate/cmd/err_template_gen@latest
```

For service topology, keep the catalog with that process configuration and scan only its business packages:

```bash
err_template_gen \
  --root . \
  --packages ./internal/user/... \
  --out ./config/user/locales \
  --languages zh-CN,en-US
```

For single topology, generate one process catalog containing every included svc reason:

```bash
err_template_gen \
  --root . \
  --packages ./internal/... \
  --out ./config/my-project/locales \
  --languages zh-CN,en-US
```

The output stays flat and product-editable:

```yaml
# slots: user_id
USER_NOT_FOUND: "用户 {{.user_id}} 不存在"
```

Product authors may change all text and reorder slots. They must not add, remove, rename, or duplicate `{{.name}}` slots. List every managed locale in `--languages`; an unlisted YAML locale in the output directory is treated as unmanaged drift.

Normal generation preserves valid product copy and comments, then appends new reasons. Conflicting source definitions, invalid slots, stale YAML reasons, or malformed templates fail validation before any locale file is written. Removing a source reason therefore requires removing its YAML entries deliberately.

Use the same arguments in CI with `--check`:

```bash
err_template_gen \
  --root . \
  --packages ./internal/... \
  --out ./config/my-project/locales \
  --languages zh-CN,en-US \
  --check
```

## Wire one Handler per process

Configuration is loaded first; initialize logging second and pass its explicit
`log.Logger` to the Handler and process policy.

```go
loggerManager, err := log.NewLoggerManager(&cfg.Logging, log.WithOutputConsole())
if err != nil {
	return fmt.Errorf("create logger: %w", err)
}
restoreLogger := log.SetDefault(loggerManager.Logger())
defer restoreLogger()
defer loggerManager.Close(context.WithoutCancel(ctx))

policy := transport.NewPolicy(cfg.Name, transport.WithLogger(loggerManager.Logger()))

catalog, err := errs.LoadCatalog(
	os.DirFS("."),
	"config/my-project/locales",
	"zh-CN",
)
if err != nil {
	return fmt.Errorf("load error catalog: %w", err)
}
errorHandler, err := errs.NewHandler(catalog, loggerManager.Logger())
if err != nil {
	return fmt.Errorf("create error handler: %w", err)
}

httpServer, err := httpserver.NewServer(
	&cfg.HTTP,
	httpserver.WithPolicy(policy),
	httpserver.WithErrorHandler(errorHandler),
)
grpcServer, err := rpcserver.NewServer(
	&cfg.GRPC,
	register,
	rpcserver.WithPolicy(policy),
	rpcserver.WithErrorHandler(errorHandler),
)
```

Use the locale directory for the current process topology. A single process with multiple svc modules still has one Catalog and one Handler; do not load locale files from business packages.

HTTP adapters return errors through `httpserver.Wrap` or `c.Error`. HTTP reads `Accept-Language`; gRPC reads `accept-language` metadata. The framework selects the locale and returns only `code`, `reason`, and localized `message`.

If a required runtime value is missing, only that slot renders as `UNKNOWN`; undeclared values are ignored. Both conditions are logged as template diagnostics. Causes, fields, error chains, stacks, request IDs, trace IDs, and span IDs go to the logger and never enter client responses.
