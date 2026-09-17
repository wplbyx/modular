# Commands

The installed CLI initializes projects. Every generated project carries the
same deterministic tool at `.modular/tool/modular.py`.

## Command surface

- `init`
- `project upgrade`
- `module add`
- `module remove`
- `module depend add`
- `module depend remove`
- `transport add`
- `transport remove`
- `resource add`
- `resource remove`
- `sync`
- `prune`
- `doctor`
- `verify`
- `coverage`
- `self-check`

## Initialization

```bash
python3 scripts/modular.py init myapp --modular-version v0.4.1
python3 scripts/modular.py init myapp --transport http --transport grpc
```

Initialization creates one Application with HTTP by default. It creates no
proto, generated wire contract, fake Business Module, repository, or domain
package.

## Modules and dependencies

```bash
python3 .modular/tool/modular.py module add customer
python3 .modular/tool/modular.py module add order --depends-on customer
python3 .modular/tool/modular.py module depend add billing order
python3 .modular/tool/modular.py module depend remove billing order
```

Modules represent bounded contexts and form an acyclic dependency graph.
Adding a module creates its configuration and typed bootstrap assembly shell,
alongside contract/internal package placeholders. Bootstrap exposes
`New(cfg Config, deps Dependencies) (*Module, error)`; Agent-led work supplies
actual dependencies, contract capabilities, and implementations from known use
cases. Cmd connects modules; bootstrap assembles internals. See
[layering](layering.md) before defining either interface. Existing scaffold-once
constructors and configs are preserved by sync, including older signatures.

Module removal is destructive and previews by default:

```bash
python3 .modular/tool/modular.py module remove customer
python3 .modular/tool/modular.py module remove customer --apply
```

## Application capabilities

```bash
python3 .modular/tool/modular.py transport add grpc
python3 .modular/tool/modular.py transport remove grpc --apply
python3 .modular/tool/modular.py resource add db --driver gorm --dialect postgres
python3 .modular/tool/modular.py resource add redis
python3 .modular/tool/modular.py resource remove redis --apply
```

Transports and Resources belong to the one Application. Removing either
previews managed-file changes unless `--apply` is supplied.

## Maintenance

```bash
  --modular-version v0.4.1 --diff
  --modular-version v0.4.1 --apply
python3 .modular/tool/modular.py sync
python3 .modular/tool/modular.py prune --apply
```

wiring outside modular management, rejects old modular protobuf generator
automatically.

All mutating commands accept `--dry-run` and `--diff`. `sync` replays managed
files without replacing scaffold-once extensions. `prune` removes only obsolete
files whose hashes still match the manifest.

## Gates

- `make scaffold-check`: generated framework and build.
- `make contract-check`: module Go contracts and build.
- `make verify`: formatting, vet, build, tests, race tests, and no unfinished
  markers.
- `doctor --strict --phase framework|contract|complete`: read-only audit.
