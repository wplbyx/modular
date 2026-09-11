# Initialization workflow

Use for a new modular monolith or Business Module. Read [commands](../commands.md),
[layering](../layering.md), [config](../config.md), and
[lifecycle](../lifecycle.md).

1. Confirm the project name and optional external HTTP/gRPC transports.
2. Initialize one Application with modular v0.4.0 or newer; write a concrete
   dependency without a local replace.
3. Review the dry-run/diff and apply.
4. Add Business Modules named for bounded contexts with explicit dependencies.
5. Add Application-owned Resources only when a real use case needs them.
6. Run `make scaffold-check`.

Do not create proto/Buf files, handlers, repositories, domain shells, or fake
contracts during framework initialization. The CLI creates compiling framework
seams; the Agent creates business code from known requirements.
