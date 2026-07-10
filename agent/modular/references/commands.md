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
  creates a svc module and rewrites HTTP+gRPC cmd wiring.
- `surface <svc> <surface> [--methods ...] [--method ...] [--gen auto|skip|required] [--project-dir DIR]`
  adds another surface to an existing svc.
- `method <svc> <surface> <MethodName> [--gen auto|skip|required] [--project-dir DIR]`
  updates proto and creates an app method file.
- `resource <db|redis|storage|telemetry> [--driver bun|gorm|mongo] [--svc SVC] [--project-dir DIR]`
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

## Resource Mapping

- Bun/GORM/Redis/Telemetry use library Resource types.
- Mongo and Storage generate project-side wrappers under `internal/<svc>/repository`.
- If multiple svc modules exist, pass `--svc`; otherwise the only svc is selected automatically.

## Safety

- `doctor` catches top-level `config/config.go|yaml`, old `domain/repository.go`, old `repository/repository.go`, `internal/infra`, hand-written `common/*.go`, and cross-svc `internal` imports.
- Repository scaffold commands overwrite only when `--force` is passed for existing files.
