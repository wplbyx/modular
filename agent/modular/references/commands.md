# Commands

The project-local tool is copied into `.modular/tool/` during `init`; generated
Make targets call that copy. Run commands from the generated project root.

## Command paths

- `init`
- `module add`
- `module remove`
- `module depend add`
- `module depend remove`
- `module blocker add`
- `module extract`
- `process add`
- `process remove`
- `process attach`
- `transport add`
- `transport remove`
- `resource add`
- `resource remove`
- `sync`
- `doctor`
- `prune`
- `migrate v0.2-to-v0.3`
- `project upgrade`
- `verify`
- `gen`
- `coverage`
- `self-check`

The legacy `service add`, `service remove`, and `migrate topology` commands
remain available only for v0.2 projects.

`make scaffold-migrate TOPOLOGY=service APPLY=1` keeps the v0.2 topology path;
without `TOPOLOGY`, the target previews or applies `migrate v0.2-to-v0.3`.

## Initialize

New projects use the Module/Process model and require modular v0.3.0 or newer:

```bash
python3 scripts/modular.py init billing-demo
python3 scripts/modular.py init billing-demo --transport http --transport grpc \
  --modular-version v0.3.0
```

The default creates one Process named after the project with one HTTP server
and no Business Modules. `init --topology single|service` is the explicit v0.2
compatibility path. Initialization resolves a concrete published version and
never writes a local `replace` directive.

## Modules and dependencies

```bash
python3 .modular/tool/modular.py module add customer
python3 .modular/tool/modular.py module add order --depends-on customer
python3 .modular/tool/modular.py module depend add billing order
python3 .modular/tool/modular.py module depend remove billing order
python3 .modular/tool/modular.py module blocker add \
  --module order --module inventory \
  --kind shared-transaction --reason "reservation commits with order"
```

Every module is assigned to exactly one Process. `--process` is optional only
when the architecture has one Process. Dependency additions reject cycles.
Module addition creates business config, but no handlers, domain, repository,
or placeholder business implementation.

Removing a module is destructive and previews by default:

```bash
python3 .modular/tool/modular.py module remove customer
python3 .modular/tool/modular.py module remove customer --apply
```

## Processes

```bash
python3 .modular/tool/modular.py process add workers
python3 .modular/tool/modular.py process add billing --transport http --transport grpc
python3 .modular/tool/modular.py process attach billing order
python3 .modular/tool/modular.py transport add billing grpc
```

A Process owns one Application, identity, config, health manager, and at most
one server per enabled protocol. `process attach` changes deployment grouping
only after the same blocker/contract/remote-adapter checks used by extraction
pass. Use `process remove --apply` only after moving all of its modules.

## Resources

Resources belong to a Process in v0.3:

```bash
python3 .modular/tool/modular.py resource add db \
  --process billing --driver gorm --dialect postgres
python3 .modular/tool/modular.py resource add redis --process billing
python3 .modular/tool/modular.py resource add eventbus --process workers
```

With one Process, `--process` may be omitted. The v0.2 compatibility model uses
`--svc` instead. `transport remove` and `resource remove` preview unless
`--apply` is supplied.

## Extraction

```bash
python3 .modular/tool/modular.py module extract order \
  --to-process order_api --check
python3 .modular/tool/modular.py module extract order \
  --to-process order_api --apply
```

The check reports declared blockers, missing protobuf contracts, and missing
remote adapters for dependencies that would cross a Process boundary. Apply
only changes architecture and managed Process files after the check is clean.
It does not generate business adapters, outbox/inbox behavior, or registrar
policy.

## Migration and upgrade

Run v0.2 migration with the newly installed skill copy so the v0.3 templates
are available:

```bash
python3 <installed-skill>/scripts/modular.py migrate v0.2-to-v0.3 \
  --project-dir . --modular-version v0.3.0 --diff
python3 <installed-skill>/scripts/modular.py migrate v0.2-to-v0.3 \
  --project-dir . --modular-version v0.3.0 --apply
```

Customized `internal/platform/wiring/business.go` stops automatic migration.
So does a customized legacy `config/<svc>/config.go`; move its settings to
`config/modules/<svc>/config.go` first. Align conflicting per-service resources
before migrating a v0.2 single topology. Use `project upgrade --apply` for
dependency/tool upgrades that do not change the architecture model.

## Verification and safety

All mutating commands accept `--dry-run` and `--diff`. Removal, extraction, and
migration commands require `--apply`; their default mode is a preview.

- `make scaffold-check`: self-check, strict framework doctor, placeholder scan,
  and build.
- `make contract-check`: Buf lint/generate, contract doctor, and build.
- `make verify`: formatting, vet, build, tests, race tests, test-presence checks,
  and zero unfinished markers.

`.modular/architecture.yaml` is the user-maintained architecture source.
`.modular/manifest.json` records generated ownership and hashes. Managed files
are updated only from a known hash; scaffold-once files remain user-owned.
`prune --apply` deletes only unchanged managed files. Writes are staged and
rolled back when post-write verification fails.
