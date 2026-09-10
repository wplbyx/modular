# Domain workflow

Use only when invariants, policies, aggregate coordination, or transaction
rules justify a domain model. Read [layering](../layering.md),
[repository placement](../repository.md), and [errors](../errors.md).

1. Name the aggregate and why app-layer ports are insufficient.
2. Add concepts under `internal/modules/<module>/internal/domain`; do not split
   a domain by transport surface.
3. Keep domain errors machine-readable. Map stable reasons and localization at
   the app/API edge.
4. Create entities only with known fields and invariants, not ID-only shells.
5. Keep persistence models/tags and provider adapters in repository packages.
6. Define a module-local UoW for atomic work. Declare any unavoidable
   cross-module transaction as an extraction blocker.
7. Add focused unit tests and finish with `make verify`.
