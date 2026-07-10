# Adapter And Repository Recommendation

Use this before generating ports from a natural-language feature request.

The CLI has a deterministic helper for shell use:

```bash
python agent/modular/scripts/modular.py repository recommend <svc> [surface] \
  --aggregate User \
  --feature "CRUD user profile management" \
  --query FindUser \
  --command SaveUser
```

Treat its output as a first pass: it prints the recommended placement, expanded
Go signatures, and the exact scaffold command. The agent should still adjust the
recommendation when the user's domain context is clearer than the heuristic.

## Placement

Choose `app/<surface>/adapter.go` when the flow is simple:

- CRUD or query/mutation with no rich domain behavior.
- The pb method can call a repository implementation directly.
- DTO-style data is acceptable at the app boundary.

Choose `domain/adapter.go` when the flow is complex:

- Aggregates, invariants, policies, or transactions matter.
- Multiple repositories or entities are coordinated.
- App should depend on domain definitions and domain services.

## App Scaffold

After confirmation, use:

```bash
python agent/modular/scripts/modular.py repository app <svc> <surface> \
  --aggregate User \
  --query "FindUser(ctx context.Context, id string) (UserDTO, error)" \
  --command "SaveUser(ctx context.Context, item UserDTO) error"
```

Name-only defaults:

- `Find/Get/LoadX` -> `(ctx context.Context, id string) (XDTO, error)`
- `ListX` -> `(ctx context.Context) ([]XDTO, error)`
- `Delete/RemoveX` -> `(ctx context.Context, id string) error`
- other commands -> `(ctx context.Context, item XDTO) error`

## Domain Scaffold

After confirmation, use:

```bash
python agent/modular/scripts/modular.py repository domain <svc> \
  --aggregate User \
  --query "FindUser(ctx context.Context, id string) (*entity.User, error)" \
  --command "SaveUser(ctx context.Context, item *entity.User) error"
```

Name-only defaults:

- `Find/Get/LoadX` -> `(ctx context.Context, id string) (*entity.X, error)`
- `ListX` -> `(ctx context.Context) ([]*entity.X, error)`
- `Delete/RemoveX` -> `(ctx context.Context, id string) error`
- other commands -> `(ctx context.Context, item *entity.X) error`

## Rules

- Explain app-vs-domain placement before scaffolding.
- Keep persistence tags out of `domain/entity`.
- Put app implementations in `repository/app`.
- Put domain implementations in `repository/domain`.
- Generate `repository/dto` and `repository/model` only when the chosen design actually needs them.
- Cross-svc dependencies go through generated pb clients, not another svc's `internal`.
