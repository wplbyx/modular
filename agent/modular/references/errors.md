# Errors and localization

Business packages own stable error reasons and their variable-slot contract. Define each message as a package-level value:

```go
var UserNotFound = errs.Define(
	"USER_NOT_FOUND",
	errs.Template("user %v not found", errs.Name("user_id")),
)

return errs.NotFound(
	UserNotFound.With(errs.Name("user_id"), id),
	errs.WithCause(cause),
	errs.WithField("user_id", id),
)
```

Only `%v` consumes a slot; `%%` emits a literal percent sign. Reasons use uppercase underscore form and names use lowercase snake_case. Do not construct definitions dynamically: `err_template_gen` statically reads constant reasons, patterns, and `errs.Name` values.

Generate one editable YAML file per BCP 47 locale:

```bash
err_template_gen \
  --root . \
  --packages ./internal/user/... \
  --out ./config/user/locales \
  --languages zh-CN,en-US
```

Product authors may change text and reorder `{{.name}}` slots. They must not add, remove, rename, or duplicate slots. Run the same command with `--check` in CI. Existing copy and comments are preserved; new reasons are appended; stale reasons and contract mismatches fail without writing files.

The cmd package loads `errs.Catalog`, constructs one `errs.Handler` with the process zap logger, and injects it with both `httpserver.WithErrorHandler` and `rpcserver.WithErrorHandler`. Missing runtime values render as `UNKNOWN` only in their positions and are logged. Causes, fields, chains, stacks, request IDs, and trace IDs never enter client responses.
