# Init workflow

Use for a new modular project, Business Module, or Process grouping. Read
[commands](../commands.md), [config](../config.md), and
[lifecycle](../lifecycle.md).

1. Confirm the project name; default to one Process and HTTP unless the user
   explicitly needs other transports. New projects do not choose a topology.
2. Run the installed tool's `self-check`.
3. Resolve modular v0.3.0 or newer; write a concrete remote dependency without
   a local `replace`.
4. Review `init --dry-run --diff`, apply, then add Business Modules with
   explicit dependencies and Process assignment where ambiguous.
5. Verify one shared server per protocol and the config -> logger -> policy and
   health -> Resource -> module wiring -> Endpoint -> Application order.
6. Run `make scaffold-check`. Framework completion may retain only the
   intentional `modular:business-unwired` marker.

Do not generate handlers, repositories, domain shells, or fake contracts while
initializing the framework. `init --topology` is reserved for v0.2 compatibility.
