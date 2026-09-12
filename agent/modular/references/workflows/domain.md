# Domain workflow

Use only when invariants, policies, aggregate coordination, or transaction
rules justify a domain model. Read [layering](../layering.md),
[repository placement](../repository.md), and [errors](../errors.md).

1. Name the aggregate and why app-layer behavior is insufficient.
2. Add concepts under `modules/<module>/internal/domain`; do not split
   a domain by transport surface.
3. Keep domain errors machine-readable and localize them at Application edges.
4. Create entities only with known fields and invariants, not ID-only shells.
5. Keep persistence models, ORM tags, and adapters outside domain entities.
6. Define the smallest transaction interface in the initiating use case.
7. For a cross-module ACID workflow, call only public contracts and keep one
   clear initiating module; never export repositories or database handles.
8. Add focused unit tests and finish with `make verify`.
