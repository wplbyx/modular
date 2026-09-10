# Skill evaluation

`evals.json` contains the Agent-level scenarios. Run those with the
skill-creator with-skill/old-skill flow when independent Agent execution is
available.

`run_scaffold_benchmark.py` is the deterministic companion. It compares the
current scaffold with a frozen old skill snapshot and checks file count,
placeholder residue, dependency mode, build result, idempotency, typed Process
resources, v0.2 migration safety, and Module/Process bootstrap behavior.

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
