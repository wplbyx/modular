# Migration workflow

Topology migration changes only process-level cmd/config ownership. Proto,
common generated output, and business packages are preserved.
Read [commands](../commands.md), [layering](../layering.md), and
[config](../config.md) before reviewing the migration diff.

1. Run `make scaffold-diff ARGS="migrate topology --to service"`.
2. Confirm that the diff contains only managed framework files and manifest
   provenance. Modified business or scaffold-once files must stop the command.
3. Apply with `migrate topology --to ... --apply` and run `make scaffold-check`.
4. For a v1 project without a manifest, first perform a read-only audit and
   classify old generated files. Do not silently delete modified Example or
   repository files; use `prune` only after their ownership is established.
5. Upgrade the project-local tool through the installed skill before using
   newer Make targets.
