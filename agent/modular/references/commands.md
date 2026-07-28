# Commands

The project-local tool is copied into `.modular/tool/` during `init` and is
the same deterministic implementation used by the installed skill. The
Makefile in every generated project calls this local copy.

Documented command paths:

- `init`
- `project upgrade`
- `service add`
- `service remove`
- `transport add`
- `transport remove`
- `resource add`
- `resource remove`
- `sync`
- `doctor`
- `prune`
- `migrate topology`
- `verify`
- `gen`
- `coverage`
- `self-check`

## New project

```bash
python scripts/modular.py init billing-demo --topology single
python scripts/modular.py init billing-demo --topology service --modular-version v0.2.0
```

`init` resolves `github.com/wplbyx/modular@latest` when no tag is provided.
It writes the resolved version to `go.mod`, never creates a local `replace`,
and refuses versions older than `v0.2.0` before creating the project.

## Tool and dependency upgrade

Run `project upgrade` with the newly installed skill copy, not the older
project-local tool, so the new runtime and templates can replace unchanged
managed files:

```bash
python <installed-skill>/scripts/modular.py project upgrade \
  --project-dir . --modular-version v0.2.1 --diff
python <installed-skill>/scripts/modular.py project upgrade \
  --project-dir . --modular-version v0.2.1 --apply
```

## Framework phase

Run these commands from a generated project, or use the equivalent Makefile
targets:

```bash
python .modular/tool/modular.py service add user --transport http --transport grpc
python .modular/tool/modular.py resource add db --svc user --driver bun
python .modular/tool/modular.py sync
python .modular/tool/modular.py verify --phase framework
```

The framework phase creates config, cmd, endpoint/resource wiring and a
business registration seam. It does not create proto methods, domain models,
repository implementations, or Example placeholders.

All mutating commands accept `--dry-run` and `--diff`. `--diff` never writes.
`service remove`, `transport remove`, `resource remove`, `prune`, and
`migrate topology` require `--apply` for writes.

## Contract and business phases

The Agent writes proto, app/domain ports, error reason definitions, and API
mapping from the contract templates after it has made the architecture
decision. The CLI does not infer fields or method signatures.

```bash
make contract-check
make verify
```

`contract-check` allows only explicitly marked `Unimplemented` contract
methods. `verify` rejects those markers, requires tests in configured business
packages, runs build/vet/test/race, and never imposes a numeric coverage gate.

## Makefile targets

Every project carries these targets: `scaffold-service`, `scaffold-resource`,
`scaffold-sync`, `scaffold-diff`, `scaffold-doctor`, `scaffold-prune`,
`scaffold-migrate`, `gen`, `build`, `test`, `test-race`, `coverage`,
`scaffold-check`, `contract-check`, and `verify`.

## Safety

`.modular/manifest.json` records ownership, the last generated hash, template
version, and minimal provenance. Managed files are updated only when their
hash is unchanged. Scaffold-once files are never overwritten. `prune` only
deletes unchanged managed files and requires `--apply`.

All writes are staged and rolled back if a post-write verification fails.
`doctor --strict` checks structure, ownership, profile rules, generated common
files, and cross-svc dependency direction without changing project files.
