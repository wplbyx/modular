# CRUD contract workflow

Use for straightforward query and mutation behavior without rich domain rules.
Read [layering](../layering.md), [adapter placement](../repository.md), and
[errors](../errors.md).

1. Name the owning bounded context and use case in business language.
2. If another module calls it, define the smallest public Go interface and
   Query/Command/Result types in `modules/<provider>/contract`.
   Use [interface design](../interface-design.md) for operation granularity,
   contract guarantees, and the split between request validation and business
   authorization. Keep ordinary edits lightweight.
3. Put use cases and outbound ports in `modules/<module>/internal/app`; keep
   `internal/domain/doc.go` explaining why no domain model is needed.
4. Put HTTP DTOs, mappings, and handlers in `infrastructure/http`, and
   persistence models/repositories in `infrastructure/gorm`.
5. In module `bootstrap.go`, construct adapters and use cases and expose typed
   contract capabilities and necessary mounting hooks. Use cases implement
   public contract interfaces directly. Cmd supplies narrow
   dependencies, connects modules, and mounts their entrypoints.
6. Add stable reasons and public-entrypoint behavior tests. Pass `verify --phase contract`, then
   implement behavior and pass `verify --phase complete`.
