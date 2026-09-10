# CRUD contract workflow

Use for simple CRUD or MVC-style behavior. Read [layering](../layering.md),
[repository placement](../repository.md), and [errors](../errors.md).

1. Put the stable interface in `proto/<module>/<surface>.proto` and simple
   use-case ports under `internal/modules/<module>/internal/app`.
2. Keep domain absent when it would only pass data through.
3. Complete request/response fields and repository signatures before applying
   contract templates.
4. Let `protoc-gen-go-modular` generate unary Ports and Remote Adapters. API
   mappings may temporarily carry `modular:contract-unimplemented`, but must
   return an explicit Unimplemented result.
5. Run `make contract-check`, implement behavior and tests, then `make verify`.
