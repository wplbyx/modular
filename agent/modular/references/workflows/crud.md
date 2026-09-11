# CRUD contract workflow

Use for straightforward query and mutation behavior without rich domain rules.
Read [layering](../layering.md), [repository placement](../repository.md), and
[errors](../errors.md).

1. Name the owning bounded context and the use case in business language.
2. If another module calls it, define the smallest public Go interface and
   Command/Query/Result types under the provider's `contract` package.
3. Keep simple use cases and their outbound ports under the module's app layer;
   do not generate an empty domain layer.
4. Map HTTP/gRPC/message DTOs in inbound adapters instead of exposing them to
   module contracts.
5. Add stable reasons, focused tests, and explicit wiring.
6. Run `make contract-check`, implement behavior, then run `make verify`.
