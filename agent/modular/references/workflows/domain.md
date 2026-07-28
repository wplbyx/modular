# Domain workflow

Use this workflow only when invariants, policies, aggregate coordination,
transaction rules, or domain services justify a domain seam.
Read [layering](../layering.md), [repository placement](../repository.md), and
[errors](../errors.md) before choosing the domain interface.

1. Explain why app-layer ports are insufficient and name the aggregate boundary.
2. Create `internal/<svc>/domain` only for the chosen concepts. Do not split it
   by API surface.
3. Keep domain errors machine-readable and free of localized human text.
   Map stable reasons to `errs.Message` at the app/API seam and localize there.
4. Use the entity template only after the Agent has complete fields and
   invariants. Do not generate an anemic `ID`-only entity.
5. Put persistence tags and provider adapters under `repository/model` and
   `repository/domain`, never inside domain entities.
6. Add focused unit tests with the entity and service implementation; finish
   with `make verify`.
