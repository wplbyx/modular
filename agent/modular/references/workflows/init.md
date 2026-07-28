# Init workflow

Use this workflow when the user wants a new modular project or a new topology.
Read [commands](../commands.md), [config](../config.md), and
[lifecycle](../lifecycle.md) before changing framework wiring.

1. Confirm the project name and explicit `single` or `service` topology.
2. Run `self-check` on the installed skill before invoking the CLI.
3. Resolve the remote modular version. The minimum supported version is
   `v0.2.0`; never invent a local `replace` path.
4. Run `init` with `--dry-run --diff`, review the framework paths, then apply.
5. Run `make scaffold-check`. The first phase must compile and may contain only
   the intentional unwired business seam.

Do not add a svc without an explicit transport selection. Keep business
contract creation for the contract workflow.
