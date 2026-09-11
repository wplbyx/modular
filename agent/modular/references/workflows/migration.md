# v0.3 to v0.4 migration workflow

Use when upgrading a module/process project to the pure modular-monolith model.
Read [commands](../commands.md), [layering](../layering.md), and
[config](../config.md).

1. Use the newly installed v0.4 tool and run
   `migrate v0.3-to-v0.4 --diff`.
2. Confirm the v0.3 architecture contains exactly one Process. For multiple
   Processes, deliberately consolidate them in v0.3 or split them into separate
   projects; the migration never guesses.
3. Remove generated `*_modular.pb.go` files and every executable/build-tool
   reference to `protoc-gen-go-modular`. If customized `buf.gen.yaml` still
   references that plugin, remove the entry first.
4. If business wiring was customized, change
   `WireBusiness(process, platform)` to `WireApplication(platform)` and adapt
   `Contribution` to `Assembly` before retrying.
5. Apply the migration. An already adapted `WireApplication` is preserved as
   user-owned wiring. Unchanged default Buf files are removed; customized
   proto/Buf files remain as user-owned external transport assets.
6. Inspect schema 2 architecture and run `make scaffold-check`.

The migration lifts the sole Process's transports, ports, and Resources into
the Application, removes extraction state and blockers, and preserves module
dependency declarations. It never changes business packages automatically.
