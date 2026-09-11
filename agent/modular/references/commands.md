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
- `migrate v0.3-to-v0.4`
- `doctor`
- `verify`
- `coverage`
- `self-check`

## Initialization

```bash
python3 scripts/modular.py init myapp --modular-version v0.4.0
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
Adding a module creates only its business configuration seam. Agent-led work
adds `internal/modules/<module>/contract` and the minimum implementation
packages justified by real use cases.

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

## Migration and maintenance

```bash
python3 .modular/tool/modular.py migrate v0.3-to-v0.4 \
  --modular-version v0.4.0 --diff
python3 .modular/tool/modular.py migrate v0.3-to-v0.4 \
  --modular-version v0.4.0 --apply
python3 .modular/tool/modular.py sync
python3 .modular/tool/modular.py prune --apply
```

The v0.4 migration accepts only a v0.3 architecture containing one Process.
It preserves customized proto/Buf assets and already adapted `WireApplication`
wiring outside modular management, rejects old modular protobuf generator
output or executable references, and never combines multiple Processes
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
