# Skill evaluation

`evals.json` contains the Agent-level acceptance scenarios for the v0.4
self-contained-module layout. Together they exercise the `shop` scaffold, its
CRUD/domain/event-consumer module shapes, module import boundaries, managed
regions, complete-phase verification, and v0.3 migration. Run them with the
skill-creator with-skill/old-skill flow when independent Agent execution is
available.

`run_scaffold_benchmark.py` is the deterministic companion. It compares the
current scaffold with a frozen old skill snapshot using only observable CLI and
filesystem behavior. It checks the `cmd/{main,resources,modules}` composition
root, explicit Go module path, locale/config layout, SQLite/EventBus/UUIDv7
resources, module DAG and import enforcement, region preservation and sync
idempotency, documentation-only package verification, and v0.3-to-v0.4
migration safety. Business-model quality remains an Agent-level judgment.

```bash
python3 agent/modular/evals/run_scaffold_benchmark.py \
  --baseline agent/modular-workspace/skill-snapshot \
  --workspace agent/modular-workspace/iteration-1
```

Then aggregate and render with the skill-creator tools:

```bash
python3 -m scripts.aggregate_benchmark <workspace>/iteration-1 --skill-name modular
python3 eval-viewer/generate_review.py <workspace>/iteration-1 \
  --skill-name modular --benchmark <workspace>/iteration-1/benchmark.json \
  --static <workspace>/iteration-1/review.html
```
