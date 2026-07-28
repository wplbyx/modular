# CRUD contract workflow

Use this workflow for simple CRUD or MVC-style features.
Read [layering](../layering.md), [repository placement](../repository.md), and
[errors](../errors.md) before writing the contract.

1. Keep the interface in `proto/<svc>/<surface>.proto` and the use-case ports
   in `internal/<svc>/app/<surface>`.
2. Do not create `internal/<svc>/domain` merely to hold pass-through code.
3. Write complete request/response fields and repository port signatures before
   applying the contract templates.
4. Generate API mappings and app method seams with a dedicated
   `modular:contract-unimplemented` marker. The marker must return an explicit
   gRPC/HTTP Unimplemented outcome, never an empty success response.
5. Run `make contract-check`, then implement the use case and repository with
   tests before running `make verify`.
