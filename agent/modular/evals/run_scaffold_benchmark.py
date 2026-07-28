#!/usr/bin/env python3
"""Run objective old-vs-v2 scaffold regression scenarios.

This complements the Agent prompts in evals.json. It deliberately measures only
deterministic scaffold behavior; independent Agent runs remain the authority for
architecture judgment and business-contract quality.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Callable


SKILL_ROOT = Path(__file__).resolve().parents[1]
REPO_ROOT = SKILL_ROOT.parents[1]
CURRENT_CLI = SKILL_ROOT / "scripts" / "modular.py"
FORBIDDEN = ("ExampleDTO", "FindExample", "SaveExample", "ExampleRequest", "ExampleResponse")


@dataclass
class RunResult:
    facts: dict[str, bool | int | str]
    commands: list[str]
    errors: list[str]
    project: Path


@dataclass
class Scenario:
    eval_id: int
    name: str
    prompt: str
    expectations: list[str]
    run_new: Callable[[Path, list[str], list[str]], RunResult]
    run_old: Callable[[Path, Path, list[str], list[str]], RunResult]


def run(
    command: list[str],
    *,
    cwd: Path,
    commands: list[str],
    errors: list[str],
    env: dict[str, str] | None = None,
    check: bool = True,
    timeout: int = 300,
) -> subprocess.CompletedProcess[str]:
    commands.append(" ".join(command))
    completed = subprocess.run(
        command,
        cwd=cwd,
        env=env,
        text=True,
        capture_output=True,
        timeout=timeout,
    )
    if completed.returncode != 0:
        detail = completed.stderr.strip() or completed.stdout.strip()
        errors.append(detail or f"exit code {completed.returncode}")
        if check:
            raise RuntimeError(f"command failed: {' '.join(command)}\n{detail}")
    return completed


def testing_env() -> dict[str, str]:
    env = os.environ.copy()
    env["MODULAR_SCAFFOLD_TESTING"] = "1"
    env["MODULAR_SCAFFOLD_TEST_VERSION"] = "v0.2.0"
    return env


def new_project(root: Path, name: str, commands: list[str], errors: list[str]) -> Path:
    out = root / "out"
    run(
        [sys.executable, str(CURRENT_CLI), "init", name, "--topology", "single", "--out", str(out)],
        cwd=root,
        commands=commands,
        errors=errors,
        env=testing_env(),
    )
    return out / name


def old_project(root: Path, baseline: Path, name: str, commands: list[str], errors: list[str]) -> Path:
    out = root / "out"
    cli = baseline / "scripts" / "modular.py"
    run(
        [
            sys.executable,
            str(cli),
            "init",
            name,
            "single",
            "--out",
            str(out),
            "--modular-path",
            REPO_ROOT.as_posix(),
        ],
        cwd=root,
        commands=commands,
        errors=errors,
    )
    return out / name


def project_files(project: Path) -> list[Path]:
    return sorted(
        path.relative_to(project)
        for path in project.rglob("*")
        if path.is_file() and ".modular/tool" not in path.as_posix()
    )


def placeholder_count(project: Path) -> int:
    count = 0
    for path in project.rglob("*"):
        if not path.is_file() or ".modular/tool" in path.as_posix():
            continue
        try:
            text = path.read_text(encoding="utf-8")
        except (UnicodeDecodeError, OSError):
            continue
        count += sum(text.count(marker) for marker in FORBIDDEN)
    return count


def build_project(project: Path, commands: list[str], errors: list[str], *, version: str | None) -> bool:
    env = os.environ.copy()
    if version is None:
        env["GOWORK"] = "off"
    else:
        work = project.parent / "go.work"
        if work.exists():
            work.unlink()
        work_env = os.environ.copy()
        work_env["GOWORK"] = "off"
        run(["go", "work", "init", str(project)], cwd=project.parent, commands=commands, errors=errors, env=work_env)
        edit_env = os.environ.copy()
        edit_env["GOWORK"] = str(work)
        run(
            ["go", "work", "edit", "-replace", f"github.com/wplbyx/modular@{version}={REPO_ROOT.as_posix()}"],
            cwd=project.parent,
            commands=commands,
            errors=errors,
            env=edit_env,
        )
        env["GOWORK"] = str(work)
    completed = run(
        ["go", "test", "./..."],
        cwd=project,
        commands=commands,
        errors=errors,
        env=env,
        check=False,
        timeout=600,
    )
    return completed.returncode == 0


def common_facts(project: Path) -> dict[str, bool | int | str]:
    go_mod = (project / "go.mod").read_text(encoding="utf-8")
    files = project_files(project)
    return {
        "completed": True,
        "file_count": len(files),
        "placeholder_count": placeholder_count(project),
        "has_local_replace": "replace github.com/wplbyx/modular" in go_mod,
        "has_manifest": (project / ".modular/manifest.json").is_file(),
        "has_local_tool": (project / ".modular/tool/modular.py").is_file(),
        "has_domain_shell": any(path.as_posix().startswith("internal/user/domain/") for path in files),
        "has_repository_shell": any(path.as_posix().startswith("internal/user/repository/") for path in files),
    }


def failed_facts() -> dict[str, bool | int | str]:
    return {
        "completed": False,
        "file_count": 0,
        "placeholder_count": 0,
        "has_local_replace": False,
        "has_manifest": False,
        "has_local_tool": False,
        "has_domain_shell": False,
        "has_repository_shell": False,
        "http_only": False,
        "all_resources_wired": False,
        "typed_providers": False,
        "sync_idempotent": False,
        "migration_available": False,
        "preview_no_write": False,
        "business_preserved": False,
        "topology_service": False,
        "build_passed": False,
    }


def run_new_framework(root: Path, commands: list[str], errors: list[str]) -> RunResult:
    project = new_project(root, "frameworkdemo", commands, errors)
    cli = project / ".modular/tool/modular.py"
    run(
        [sys.executable, str(cli), "service", "add", "user", "--transport", "http", "--project-dir", str(project)],
        cwd=root,
        commands=commands,
        errors=errors,
        env=testing_env(),
    )
    facts = common_facts(project)
    framework = (project / "cmd/frameworkdemo/framework.gen.go").read_text(encoding="utf-8")
    facts.update(
        {
            "http_only": "httpserver.NewServer" in framework and "rpcserver.NewServer" not in framework,
            "build_passed": build_project(project, commands, errors, version="v0.2.0"),
        }
    )
    return RunResult(facts, commands, errors, project)


def run_old_framework(root: Path, baseline: Path, commands: list[str], errors: list[str]) -> RunResult:
    project = old_project(root, baseline, "frameworkdemo", commands, errors)
    cli = baseline / "scripts/modular.py"
    run(
        [sys.executable, str(cli), "service", "user", "--gen", "skip", "--project-dir", str(project)],
        cwd=root,
        commands=commands,
        errors=errors,
    )
    facts = common_facts(project)
    main = (project / "cmd/frameworkdemo/main.go").read_text(encoding="utf-8")
    facts.update(
        {
            "http_only": "httpserver.NewServer" in main and "rpcserver.NewServer" not in main,
            "build_passed": build_project(project, commands, errors, version=None),
        }
    )
    return RunResult(facts, commands, errors, project)


def run_new_resources(root: Path, commands: list[str], errors: list[str]) -> RunResult:
    project = new_project(root, "resourcedemo", commands, errors)
    cli = project / ".modular/tool/modular.py"
    base = [sys.executable, str(cli)]
    run(base + ["service", "add", "user", "--transport", "http", "--project-dir", str(project)], cwd=root, commands=commands, errors=errors, env=testing_env())
    for resource in ["db", "redis", "storage", "telemetry"]:
        args = base + ["resource", "add", resource, "--svc", "user", "--project-dir", str(project)]
        if resource == "db":
            args.extend(["--driver", "bun"])
        run(args, cwd=root, commands=commands, errors=errors, env=testing_env())
    sync = run(base + ["sync", "--project-dir", str(project)], cwd=root, commands=commands, errors=errors, env=testing_env())
    facts = common_facts(project)
    cmd = (project / "cmd/resourcedemo/framework.gen.go").read_text(encoding="utf-8")
    wiring = (project / "internal/platform/wiring/framework.gen.go").read_text(encoding="utf-8")
    facts.update(
        {
            "all_resources_wired": all(token in cmd for token in ["bunresource.NewResource", "redisresource.NewResource", "storageresource.New", "telemetry.NewOpenTelemetry"]),
            "typed_providers": all(token in wiring for token in ["UserDB", "*bunresource.Resource", "UserRedis", "*redisresource.Resource", "UserStorage", "*storageresource.Resource"]),
            "sync_idempotent": "no changes" in sync.stdout,
            "build_passed": build_project(project, commands, errors, version="v0.2.0"),
        }
    )
    return RunResult(facts, commands, errors, project)


def run_old_resources(root: Path, baseline: Path, commands: list[str], errors: list[str]) -> RunResult:
    project = old_project(root, baseline, "resourcedemo", commands, errors)
    cli = baseline / "scripts/modular.py"
    run([sys.executable, str(cli), "service", "user", "--gen", "skip", "--project-dir", str(project)], cwd=root, commands=commands, errors=errors)
    for resource in ["db", "redis", "storage", "telemetry"]:
        args = [sys.executable, str(cli), "resource", resource, "--svc", "user", "--project-dir", str(project)]
        if resource == "db":
            args.extend(["--driver", "bun"])
        run(args, cwd=root, commands=commands, errors=errors)
    facts = common_facts(project)
    main = (project / "cmd/resourcedemo/main.go").read_text(encoding="utf-8")
    facts.update(
        {
            "all_resources_wired": all(token in main for token in ["bunresource.NewResource", "redisresource.NewResource", "storageresource.New", "telemetry.NewOpenTelemetry"]),
            "typed_providers": "core.Provider[*bun.DB]" in "\n".join(path.read_text(encoding="utf-8") for path in project.rglob("*.go")),
            "sync_idempotent": False,
            "build_passed": build_project(project, commands, errors, version=None),
        }
    )
    return RunResult(facts, commands, errors, project)


def run_new_migration(root: Path, commands: list[str], errors: list[str]) -> RunResult:
    project = new_project(root, "migrationdemo", commands, errors)
    cli = project / ".modular/tool/modular.py"
    base = [sys.executable, str(cli)]
    for svc in ["user", "billing"]:
        run(base + ["service", "add", svc, "--transport", "http", "--project-dir", str(project)], cwd=root, commands=commands, errors=errors, env=testing_env())
    business = project / "internal/platform/wiring/business.go"
    business.write_text(business.read_text(encoding="utf-8") + "\n// user-owned wiring\n", encoding="utf-8")
    before = hashlib.sha256(business.read_bytes()).hexdigest()
    preview = run(base + ["migrate", "topology", "--to", "service", "--diff", "--project-dir", str(project)], cwd=root, commands=commands, errors=errors, env=testing_env())
    preview_preserved = (project / "cmd/migrationdemo/main.go").is_file()
    run(base + ["migrate", "topology", "--to", "service", "--apply", "--project-dir", str(project)], cwd=root, commands=commands, errors=errors, env=testing_env())
    after = hashlib.sha256(business.read_bytes()).hexdigest()
    manifest = json.loads((project / ".modular/manifest.json").read_text(encoding="utf-8"))
    facts = common_facts(project)
    facts.update(
        {
            "migration_available": True,
            "preview_no_write": preview_preserved and "delete cmd/migrationdemo/main.go" in preview.stdout,
            "business_preserved": before == after,
            "topology_service": manifest["project"]["topology"] == "service" and (project / "cmd/user/framework.gen.go").is_file() and (project / "cmd/billing/framework.gen.go").is_file(),
            "build_passed": build_project(project, commands, errors, version="v0.2.0"),
        }
    )
    return RunResult(facts, commands, errors, project)


def run_old_migration(root: Path, baseline: Path, commands: list[str], errors: list[str]) -> RunResult:
    project = old_project(root, baseline, "migrationdemo", commands, errors)
    cli = baseline / "scripts/modular.py"
    for svc in ["user", "billing"]:
        run([sys.executable, str(cli), "service", svc, "--gen", "skip", "--project-dir", str(project)], cwd=root, commands=commands, errors=errors)
    completed = run([sys.executable, str(cli), "migrate", "topology", "--to", "service", "--project-dir", str(project)], cwd=root, commands=commands, errors=errors, check=False)
    facts = common_facts(project)
    facts.update(
        {
            "migration_available": completed.returncode == 0,
            "preview_no_write": False,
            "business_preserved": False,
            "topology_service": False,
            "build_passed": build_project(project, commands, errors, version=None),
        }
    )
    return RunResult(facts, commands, errors, project)


SCENARIOS = [
    Scenario(
        1,
        "framework-http-svc",
        "Initialize a single-process project and add one HTTP-only user svc without business shells.",
        [
            "The project uses a concrete remote dependency without a local replace.",
            "The selected framework is HTTP-only.",
            "No default domain or repository shell is generated.",
            "No forbidden Example placeholder remains.",
            "The generated framework builds and includes a manifest plus project-local tool.",
        ],
        run_new_framework,
        run_old_framework,
    ),
    Scenario(
        3,
        "resource-wiring",
        "Attach Bun, Redis, Storage, and Telemetry without inventing repository business code.",
        [
            "All selected library Resources are wired into the generated cmd.",
            "Typed Providers are exposed for later business adapters.",
            "No default repository shell or forbidden Example placeholder is generated.",
            "A repeated sync is idempotent.",
            "The generated framework builds.",
        ],
        run_new_resources,
        run_old_resources,
    ),
    Scenario(
        4,
        "topology-migration",
        "Preview and apply single-to-service topology migration while preserving user business wiring.",
        [
            "A deterministic topology migration command is available.",
            "The diff preview does not write files.",
            "User-owned business wiring is preserved byte-for-byte.",
            "The manifest and managed cmd files switch to service topology.",
            "The migrated framework builds.",
        ],
        run_new_migration,
        run_old_migration,
    ),
]


def verdicts(scenario: Scenario, facts: dict[str, bool | int | str]) -> list[dict[str, object]]:
    completed = bool(facts["completed"])
    if scenario.eval_id == 1:
        checks = [
            completed and not bool(facts["has_local_replace"]),
            completed and bool(facts["http_only"]),
            completed and not bool(facts["has_domain_shell"]) and not bool(facts["has_repository_shell"]),
            completed and int(facts["placeholder_count"]) == 0,
            completed and bool(facts["build_passed"]) and bool(facts["has_manifest"]) and bool(facts["has_local_tool"]),
        ]
    elif scenario.eval_id == 3:
        checks = [
            completed and bool(facts["all_resources_wired"]),
            completed and bool(facts["typed_providers"]),
            completed and not bool(facts["has_repository_shell"]) and int(facts["placeholder_count"]) == 0,
            completed and bool(facts["sync_idempotent"]),
            completed and bool(facts["build_passed"]),
        ]
    else:
        checks = [
            completed and bool(facts["migration_available"]),
            completed and bool(facts["preview_no_write"]),
            completed and bool(facts["business_preserved"]),
            completed and bool(facts["topology_service"]),
            completed and bool(facts["build_passed"]),
        ]
    return [
        {
            "text": text,
            "passed": passed,
            "evidence": f"facts={json.dumps(facts, sort_keys=True)}",
        }
        for text, passed in zip(scenario.expectations, checks, strict=True)
    ]


def write_run(run_dir: Path, scenario: Scenario, result: RunResult, duration: float) -> None:
    outputs = run_dir / "outputs"
    outputs.mkdir(parents=True, exist_ok=True)
    tree = "\n".join(path.as_posix() for path in project_files(result.project))
    report = (
        f"# {scenario.name}\n\n"
        f"## Facts\n\n```json\n{json.dumps(result.facts, indent=2, sort_keys=True)}\n```\n\n"
        f"## Project files\n\n```text\n{tree}\n```\n"
    )
    (outputs / "report.md").write_text(report, encoding="utf-8")
    transcript = "## Eval Prompt\n\n" + scenario.prompt + "\n\n## Commands\n\n" + "\n".join(f"- `{item}`" for item in result.commands)
    if result.errors:
        transcript += "\n\n## Command errors\n\n" + "\n\n".join(result.errors)
    (run_dir / "transcript.md").write_text(transcript + "\n", encoding="utf-8")
    expectations = verdicts(scenario, result.facts)
    passed = sum(1 for item in expectations if item["passed"])
    metrics = {
        "tool_calls": {"shell": len(result.commands)},
        "total_tool_calls": len(result.commands),
        "total_steps": len(result.commands),
        "files_created": ["report.md"],
        "errors_encountered": len(result.errors),
        "output_chars": len(report),
        "transcript_chars": len(transcript),
    }
    (outputs / "metrics.json").write_text(json.dumps(metrics, indent=2) + "\n", encoding="utf-8")
    timing = {"total_tokens": 0, "duration_ms": round(duration * 1000), "total_duration_seconds": round(duration, 3)}
    (run_dir / "timing.json").write_text(json.dumps(timing, indent=2) + "\n", encoding="utf-8")
    grading = {
        "expectations": expectations,
        "summary": {"passed": passed, "failed": len(expectations) - passed, "total": len(expectations), "pass_rate": passed / len(expectations)},
        "execution_metrics": metrics,
        "timing": {"executor_duration_seconds": round(duration, 3), "total_duration_seconds": round(duration, 3)},
        "claims": [],
        "user_notes_summary": {"uncertainties": [], "needs_review": [], "workarounds": []},
        "eval_feedback": {"suggestions": [], "overall": "Objective scaffold assertions are programmatically checked."},
    }
    (run_dir / "grading.json").write_text(json.dumps(grading, indent=2) + "\n", encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--workspace", type=Path, required=True)
    parser.add_argument("--baseline", type=Path, required=True)
    args = parser.parse_args()
    workspace = args.workspace.resolve()
    baseline = args.baseline.resolve()
    if not (baseline / "scripts/modular.py").is_file():
        parser.error(f"baseline skill is missing scripts/modular.py: {baseline}")
    workspace.mkdir(parents=True, exist_ok=True)

    for scenario in SCENARIOS:
        eval_dir = workspace / f"eval-{scenario.eval_id}-{scenario.name}"
        eval_dir.mkdir(parents=True, exist_ok=True)
        metadata = {
            "eval_id": scenario.eval_id,
            "eval_name": scenario.name,
            "prompt": scenario.prompt,
            "assertions": scenario.expectations,
        }
        (eval_dir / "eval_metadata.json").write_text(json.dumps(metadata, indent=2) + "\n", encoding="utf-8")
        for configuration in ["new_skill", "old_skill"]:
            run_dir = eval_dir / configuration / "run-1"
            if run_dir.exists():
                shutil.rmtree(run_dir)
            run_dir.mkdir(parents=True)
            commands: list[str] = []
            errors: list[str] = []
            started = time.perf_counter()
            try:
                if configuration == "new_skill":
                    result = scenario.run_new(run_dir, commands, errors)
                else:
                    result = scenario.run_old(run_dir, baseline, commands, errors)
            except Exception as error:
                errors.append(str(error))
                project = run_dir / "out"
                candidates = [path for path in project.iterdir()] if project.is_dir() else []
                result = RunResult(failed_facts(), commands, errors, candidates[0] if candidates else run_dir)
            duration = time.perf_counter() - started
            write_run(run_dir, scenario, result, duration)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
