# Commands

Use the CLI for deterministic scaffolding:

```bash
python agent/modular/scripts/modular.py <command> ...
```

From inside the skill directory:

```bash
python scripts/modular.py <command> ...
```

Compatibility wrappers remain:

```bash
python scripts/init_project.py <project> [single|service]
python scripts/gen_proto.py --project-dir <project>
```

## Project Commands

- `init <project> [single|service] [--out DIR] [--go-version 1.26.0] [--modular-path PATH]`
  creates the project shell. It does not create top-level `config/config.go`.
- `service <svc> [--surface public] [--methods CreateX,ListX] [--method Method] [--gen auto|skip|required] [--project-dir DIR]`
  creates a svc module and rewrites HTTP+gRPC cmd wiring. Generated entrypoints use `config.NewRoot`.
- `surface <svc> <surface> [--methods ...] [--method ...] [--gen auto|skip|required] [--project-dir DIR]`
  adds another surface to an existing svc.
- `method <svc> <surface> <MethodName> [--gen auto|skip|required] [--project-dir DIR]`
  updates proto and creates an app method file.
- `resource <db|redis|storage|telemetry> [--driver bun|gorm|mongo] [--dialect postgres|mysql|sqlite|clickhouse] [--svc SVC] [--project-dir DIR]`
  updates `config/<svc>`, records resource metadata, and rewrites cmd.
- `repository recommend <svc> [surface] --aggregate X --feature "..." --query ... --command ... [--complexity auto|simple|domain] [--json] [--project-dir DIR]`
  recommends app-vs-domain placement, expands name-only ports into Go signatures, and prints the next scaffold command.
- `repository app <svc> <surface> --aggregate X --query ... --command ... [--force] [--project-dir DIR]`
  writes app-layer ports and `repository/app` methods.
- `repository domain <svc> --aggregate X --query ... --command ... [--force] [--project-dir DIR]`
  writes domain ports, domain entity, and `repository/domain` methods.
- `gen [--project-dir DIR] [-- buf args...]`
  runs `buf generate`; fails if `buf` is missing.
- `doctor [--project-dir DIR]`
  runs read-only convention checks.

## Generation Policy

- `--gen auto` runs buf when available and warns when unavailable.
- `--gen skip` is for early scaffolds and tests.
- `--gen required` fails if buf is unavailable.
- The CLI runs `gofmt -w` on generated Go files when `gofmt` is available.
- Single topology regenerates `config/<project>/config.go|yaml` from svc config fragments whenever cmd wiring is rebuilt.
- Service topology uses `config/<svc>/config.go|yaml` directly.
- Generated ServiceNode metadata comes from each server's `Transport()` method.

## Error Locale Generator

`err_template_gen` is a standalone Go tool, not a `modular.py` subcommand. Install it with:

```bash
go install github.com/wplbyx/modular/packages/generate/cmd/err_template_gen@latest
```

Generate or merge locale files with explicit package scope, output directory, and the complete managed language set:

```bash
err_template_gen --root . --packages ./internal/user/... --out ./config/user/locales --languages zh-CN,en-US
```

Run the identical command with `--check` in CI. For single topology, scan all included svc packages and write one process-level catalog under `config/<project>/locales`; for service topology, use `config/<svc>/locales`. Read [errors.md](errors.md) before defining messages or wiring the Catalog and Handler.

## Resource Mapping

- Bun/GORM/Mongo/Redis/Storage/Telemetry use library Resource types.
- GORM requires a dialect subpackage selected by `--dialect`; SQLite is pure Go.
- Repository roots receive the generated resources as typed `core.Provider[T]` constructor arguments.
- If multiple svc modules exist, pass `--svc`; otherwise the only svc is selected automatically.

## Safety

- `doctor` catches top-level `config/config.go|yaml`, missing process/svc configs, old repository layouts, `internal/infra`, hand-written `common/*.go`, cross-svc `internal` imports, and cmd entrypoints that bypass `config.NewRoot`.
- Repository scaffold commands overwrite only when `--force` is passed for existing files.
- There is no `switch` subcommand. Topology migration is an agent workflow that rewrites cmd and process-level config only.
- There is no `errors` subcommand. Error definition, locale generation, and Handler wiring are an agent workflow using `err_template_gen`.
