# Audit workflow

Use for convention checks or release verification. Read [commands](../commands.md),
[layering](../layering.md), and [lifecycle](../lifecycle.md).

1. Run the repository-local tool and inspect its manifest ownership state.
2. Confirm architecture schema 2 declares one Application and an acyclic
   bounded-context module graph.
3. Confirm cross-module imports target only declared provider `contract`
   packages; no proto/common shortcut or implementation import is accepted.
4. Confirm the Application has one identity/lifecycle, shared Resources, and at
   most one HTTP/gRPC server.
5. Confirm module data writes stay owned and any cross-module transaction has a
   clear initiating use case and project-owned transaction interface.
6. Run `make contract-check` or `make verify` for the current phase.
