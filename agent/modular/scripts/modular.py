#!/usr/bin/env python3
"""Deterministic project scaffolder bundled with the modular skill.

The same file runs from an installed skill and from a generated project's
``.modular/tool`` directory.  Business contracts and business logic stay out
of this module; it owns framework files, their provenance, and verification.
"""

from __future__ import annotations

import argparse
import copy
import difflib
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable, Iterable

from _vendor import tomli


TOOL_VERSION = "4.0.0"
MANIFEST_SCHEMA = 1
MIN_V4_MODULAR_VERSION = (0, 4, 0)
DEFAULT_GO_VERSION = "1.26.0"
VALID_TRANSPORTS = {"http", "grpc"}
VALID_RESOURCES = {"db", "eventbus", "redis", "storage", "telemetry"}
VALID_DB_DRIVERS = {"bun", "gorm", "mongo"}
VALID_GORM_DIALECTS = {"postgres", "mysql", "sqlite", "clickhouse"}
OWNER_MANAGED = "managed"
OWNER_SCAFFOLD = "scaffold-once"
MANIFEST_PATH = Path(".modular/manifest.json")
PROFILE_PATH = Path(".modular/profile.toml")
ARCHITECTURE_PATH = Path(".modular/architecture.yaml")
PLACEHOLDER_MARKERS = (
    "modular:business-unwired",
    "modular:contract-unimplemented",
)
FORBIDDEN_PLACEHOLDERS = (
    "ExampleDTO",
    "FindExample",
    "SaveExample",
    "ExampleRequest",
    "ExampleResponse",
)


SCRIPT_DIR = Path(__file__).resolve().parent
SKILL_DIR = SCRIPT_DIR.parent if SCRIPT_DIR.name == "scripts" else SCRIPT_DIR
ASSETS_DIR = SKILL_DIR / "assets"
REFERENCES_DIR = SKILL_DIR / "references"
VENDOR_DIR = SCRIPT_DIR / "_vendor"


class ScaffoldError(RuntimeError):
    """Expected command failure with a user-facing message."""


def info(message: str) -> None:
    print(f"==> {message}")


def warn(message: str) -> None:
    print(f"warning: {message}", file=sys.stderr)


def sha256_text(content: str) -> str:
    return hashlib.sha256(content.encode("utf-8")).hexdigest()


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def normalize_identifier(value: str) -> str:
    text = re.sub(r"([a-z0-9])([A-Z])", r"\1_\2", value)
    text = re.sub(r"[^A-Za-z0-9_]+", "_", text)
    return re.sub(r"_+", "_", text).strip("_").lower()


def pascal_case(value: str) -> str:
    return "".join(part[:1].upper() + part[1:] for part in normalize_identifier(value).split("_") if part)


def lower_camel(value: str) -> str:
    value = pascal_case(value)
    return value[:1].lower() + value[1:] if value else ""


def env_prefix(value: str) -> str:
    return normalize_identifier(value).upper()


def validate_name(value: str, label: str) -> str:
    normalized = normalize_identifier(value)
    if not normalized or not re.fullmatch(r"[a-z][a-z0-9_]*", normalized):
        raise ScaffoldError(f"invalid {label}: {value!r}")
    return normalized


def run_command(
    command: list[str],
    *,
    cwd: Path,
    capture: bool = False,
    check: bool = True,
    env: dict[str, str] | None = None,
) -> subprocess.CompletedProcess[str]:
    completed = subprocess.run(
        command,
        cwd=cwd,
        text=True,
        capture_output=capture,
        env=env,
    )
    if check and completed.returncode != 0:
        details = completed.stderr.strip() if capture else ""
        suffix = f": {details}" if details else ""
        raise ScaffoldError(f"command failed ({' '.join(command)}){suffix}")
    return completed


def testing_mode() -> bool:
    return os.environ.get("MODULAR_SCAFFOLD_TESTING") == "1"


def parse_semver(value: str) -> tuple[int, int, int]:
    match = re.fullmatch(r"v(\d+)\.(\d+)\.(\d+)(?:[-+].*)?", value.strip())
    if match is None:
        raise ScaffoldError(f"invalid modular version: {value!r}")
    return tuple(int(part) for part in match.groups())  # type: ignore[return-value]


def resolve_modular_version(requested: str | None) -> str:
    override = os.environ.get("MODULAR_SCAFFOLD_TEST_VERSION") if testing_mode() else None
    if override:
        version = override
    else:
        query = requested or "latest"
        go = shutil.which("go")
        if go is None:
            raise ScaffoldError("`go` is required to resolve github.com/wplbyx/modular")
        completed = run_command(
            [go, "list", "-m", "-json", f"github.com/wplbyx/modular@{query}"],
            cwd=Path.cwd(),
            capture=True,
        )
        try:
            version = str(json.loads(completed.stdout)["Version"])
        except (KeyError, TypeError, json.JSONDecodeError) as error:
            raise ScaffoldError("go list did not return a modular version") from error
    if parse_semver(version) < MIN_V4_MODULAR_VERSION:
        minimum = "v" + ".".join(str(part) for part in MIN_V4_MODULAR_VERSION)
        raise ScaffoldError(f"modular {version} is unsupported; v0.4 scaffolds require {minimum} or newer")
    return version


def template_text(relative: str) -> str:
    path = ASSETS_DIR / relative
    if not path.is_file():
        raise ScaffoldError(f"missing template: {relative}")
    return path.read_text(encoding="utf-8")


def render_template(relative: str, values: dict[str, str]) -> str:
    content = template_text(relative)
    for key, value in values.items():
        content = content.replace("{{" + key + "}}", value)
    leftovers = sorted(set(re.findall(r"\{\{([A-Z0-9_]+)\}\}", content)))
    if leftovers:
        raise ScaffoldError(f"unresolved template tokens in {relative}: {', '.join(leftovers)}")
    return content


def read_module(project: Path) -> str:
    go_mod = project / "go.mod"
    if not go_mod.is_file():
        raise ScaffoldError(f"no go.mod in {project}")
    for line in go_mod.read_text(encoding="utf-8").splitlines():
        if line.startswith("module "):
            return line.split(None, 1)[1].strip()
    raise ScaffoldError(f"go.mod in {project} has no module directive")


def empty_v4_manifest(*, module: str, modular_version: str) -> dict[str, Any]:
    return {
        "schema": MANIFEST_SCHEMA,
        "tool_version": TOOL_VERSION,
        "project": {
            "module": module,
            "name": module.split("/")[-1],
            "model": "modular-monolith",
            "modular_version": modular_version,
        },
        "features": {},
        "files": {},
    }


def is_module_process(manifest: dict[str, Any]) -> bool:
    return manifest.get("project", {}).get("model") == "module-process"


def is_modular_monolith(manifest: dict[str, Any]) -> bool:
    return manifest.get("project", {}).get("model") == "modular-monolith"


def empty_architecture(project: str, transports: list[str]) -> dict[str, Any]:
    ports: dict[str, int] = {}
    if "http" in transports:
        ports["http"] = 18080
    if "grpc" in transports:
        ports["grpc"] = 19090
    return {
        "schema": 2,
        "application": {
            "name": project,
            "transports": sorted(set(transports)),
            "ports": ports,
            "resources": {},
        },
        "modules": {},
    }


def architecture_content(architecture: dict[str, Any]) -> str:
    # JSON is valid YAML 1.2 and keeps the repository-local tool dependency-free.
    return json.dumps(architecture, indent=2, sort_keys=True) + "\n"


def load_architecture(project: Path) -> dict[str, Any]:
    path = project / ARCHITECTURE_PATH
    if not path.is_file():
        raise ScaffoldError(f"missing {ARCHITECTURE_PATH}; initialize or migrate the project with modular v0.4")
    try:
        architecture = json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as error:
        raise ScaffoldError(
            f"invalid {ARCHITECTURE_PATH}: use JSON-compatible YAML ({error})"
        ) from error
    validate_architecture(architecture)
    return architecture


def validate_architecture(architecture: dict[str, Any]) -> None:
    if not isinstance(architecture, dict):
        raise ScaffoldError("architecture root must be an object")
    if architecture.get("schema") != 2:
        raise ScaffoldError(f"unsupported architecture schema {architecture.get('schema')!r}")
    required_root_fields = {"schema", "application", "modules"}
    if set(architecture) != required_root_fields:
        missing = sorted(required_root_fields - set(architecture))
        unsupported = sorted(set(architecture) - required_root_fields)
        details = []
        if missing:
            details.append(f"missing {', '.join(missing)}")
        if unsupported:
            details.append(f"unsupported {', '.join(unsupported)}")
        raise ScaffoldError(f"architecture fields are invalid: {'; '.join(details)}")
    application = architecture.get("application")
    modules = architecture.get("modules")
    if not isinstance(application, dict) or not isinstance(modules, dict):
        raise ScaffoldError("architecture application/modules have invalid types")
    required_application_fields = {"name", "transports", "ports", "resources"}
    if set(application) != required_application_fields:
        missing = sorted(required_application_fields - set(application))
        unsupported = sorted(set(application) - required_application_fields)
        details = []
        if missing:
            details.append(f"missing {', '.join(missing)}")
        if unsupported:
            details.append(f"unsupported {', '.join(unsupported)}")
        raise ScaffoldError(f"application fields are invalid: {'; '.join(details)}")
    name = application.get("name")
    if not isinstance(name, str) or validate_name(name, "application") != name:
        raise ScaffoldError(f"application name must be canonical lower_snake_case: {name!r}")
    transports = application.get("transports", [])
    ports = application.get("ports", {})
    resources = application.get("resources", {})
    if not isinstance(transports, list) or not all(isinstance(value, str) for value in transports):
        raise ScaffoldError("application transports must be a list of strings")
    if len(transports) != len(set(transports)) or not set(transports).issubset(VALID_TRANSPORTS):
        raise ScaffoldError("application has duplicate or unsupported transports")
    if not isinstance(ports, dict) or not isinstance(resources, dict):
        raise ScaffoldError("application ports/resources have invalid types")
    if set(ports) != set(transports):
        raise ScaffoldError("application ports must match its transports")
    for transport in transports:
        port = ports.get(transport)
        if not isinstance(port, int) or isinstance(port, bool) or not 0 <= port <= 65535:
            raise ScaffoldError(f"application has invalid {transport} port")
    validate_resources(resources, "application")
    validate_modules(modules)


def validate_modules(modules: dict[str, Any]) -> None:
    for name, module in modules.items():
        if not isinstance(name, str) or validate_name(name, "module") != name:
            raise ScaffoldError(f"module name must be canonical lower_snake_case: {name!r}")
        if not isinstance(module, dict) or not isinstance(module.get("dependencies", []), list):
            raise ScaffoldError(f"module {name!r} has invalid dependencies")
        if set(module) != {"dependencies"}:
            raise ScaffoldError(f"module {name!r} contains unsupported fields")
        dependencies = module.get("dependencies", [])
        for dependency in dependencies:
            if not isinstance(dependency, str) or validate_name(dependency, "module dependency") != dependency:
                raise ScaffoldError(f"module {name!r} has invalid dependency {dependency!r}")
            if dependency not in modules:
                raise ScaffoldError(f"module {name!r} depends on unknown module {dependency!r}")
        if len(dependencies) != len(set(dependencies)):
            raise ScaffoldError(f"module {name!r} contains duplicate dependencies")
    visiting: set[str] = set()
    visited: set[str] = set()

    def visit(name: str) -> None:
        if name in visiting:
            raise ScaffoldError(f"module dependency cycle includes {name!r}")
        if name in visited:
            return
        visiting.add(name)
        for dependency in modules[name].get("dependencies", []):
            visit(str(dependency))
        visiting.remove(name)
        visited.add(name)

    for name in modules:
        visit(str(name))


def validate_resources(resources: dict[str, Any], owner: str) -> None:
    unknown_resources = sorted(set(resources) - VALID_RESOURCES)
    if unknown_resources:
        raise ScaffoldError(f"{owner} has unsupported resources: {', '.join(unknown_resources)}")
    for kind, definition in resources.items():
        if not isinstance(definition, dict):
            raise ScaffoldError(f"{owner} resource {kind!r} must be an object")
        if definition.get("kind", kind) != kind:
            raise ScaffoldError(f"{owner} resource {kind!r} has mismatched kind")
        if kind != "db":
            continue
        driver = definition.get("driver", "bun")
        dialect = definition.get("dialect", "postgres")
        if driver not in VALID_DB_DRIVERS:
            raise ScaffoldError(f"{owner} has unsupported database driver {driver!r}")
        if driver == "gorm" and dialect not in VALID_GORM_DIALECTS:
            raise ScaffoldError(f"{owner} has unsupported GORM dialect {dialect!r}")
        if driver == "bun" and dialect != "postgres":
            raise ScaffoldError(f"{owner} Bun database requires postgres dialect")


def load_v3_architecture(project: Path) -> dict[str, Any]:
    path = project / ARCHITECTURE_PATH
    if not path.is_file():
        raise ScaffoldError(f"missing {ARCHITECTURE_PATH}; the project is not a v0.3 module/process project")
    try:
        architecture = json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as error:
        raise ScaffoldError(f"invalid {ARCHITECTURE_PATH}: {error}") from error
    validate_v3_architecture(architecture)
    return architecture


def validate_v3_architecture(architecture: dict[str, Any]) -> None:
    if not isinstance(architecture, dict):
        raise ScaffoldError("v0.3 architecture root must be an object")
    if architecture.get("schema") != 1:
        raise ScaffoldError(f"unsupported architecture schema {architecture.get('schema')!r}")
    modules = architecture.get("modules")
    processes = architecture.get("processes")
    blockers = architecture.get("extraction_blockers", [])
    if not isinstance(modules, dict) or not isinstance(processes, dict) or not isinstance(blockers, list):
        raise ScaffoldError("architecture modules/processes/blockers have invalid types")
    if not processes:
        raise ScaffoldError("architecture must contain at least one process")
    for name, module in modules.items():
        if not isinstance(name, str) or validate_name(name, "module") != name:
            raise ScaffoldError(f"module name must be canonical lower_snake_case: {name!r}")
        if not isinstance(module, dict) or not isinstance(module.get("dependencies", []), list):
            raise ScaffoldError(f"module {name!r} has invalid dependencies")
        if module.get("extraction", "local") not in {"local", "extracted"}:
            raise ScaffoldError(f"module {name!r} has invalid extraction state")
        dependencies = module.get("dependencies", [])
        for dependency in dependencies:
            if not isinstance(dependency, str) or validate_name(dependency, "module dependency") != dependency:
                raise ScaffoldError(f"module {name!r} has invalid dependency {dependency!r}")
            if dependency not in modules:
                raise ScaffoldError(f"module {name!r} depends on unknown module {dependency!r}")
        if len(dependencies) != len(set(dependencies)):
            raise ScaffoldError(f"module {name!r} contains duplicate dependencies")
    visiting: set[str] = set()
    visited: set[str] = set()

    def visit(name: str) -> None:
        if name in visiting:
            raise ScaffoldError(f"module dependency cycle includes {name!r}")
        if name in visited:
            return
        visiting.add(name)
        for dependency in modules[name].get("dependencies", []):
            visit(str(dependency))
        visiting.remove(name)
        visited.add(name)

    for name in modules:
        visit(str(name))

    assignments: dict[str, str] = {}
    for name, process in processes.items():
        if not isinstance(name, str) or validate_name(name, "process") != name:
            raise ScaffoldError(f"process name must be canonical lower_snake_case: {name!r}")
        if not isinstance(process, dict):
            raise ScaffoldError(f"process {name!r} must be an object")
        process_modules = process.get("modules", [])
        process_transports = process.get("transports", [])
        ports = process.get("ports", {})
        resources = process.get("resources", {})
        if not isinstance(process_modules, list) or not isinstance(process_transports, list):
            raise ScaffoldError(f"process {name!r} has invalid modules/transports")
        if not all(isinstance(value, str) for value in process_modules + process_transports):
            raise ScaffoldError(f"process {name!r} modules/transports must be strings")
        if not isinstance(ports, dict) or not isinstance(resources, dict):
            raise ScaffoldError(f"process {name!r} has invalid ports/resources")
        transports = set(process_transports)
        if not transports.issubset(VALID_TRANSPORTS):
            raise ScaffoldError(f"process {name!r} has unsupported transports")
        for transport in transports:
            port = ports.get(transport)
            if not isinstance(port, int) or isinstance(port, bool) or not 0 <= port <= 65535:
                raise ScaffoldError(f"process {name!r} has invalid {transport} port")
        unknown_resources = sorted(set(resources) - VALID_RESOURCES)
        if unknown_resources:
            raise ScaffoldError(f"process {name!r} has unsupported resources: {', '.join(unknown_resources)}")
        for kind, definition in resources.items():
            if not isinstance(definition, dict):
                raise ScaffoldError(f"process {name!r} resource {kind!r} must be an object")
            if definition.get("kind", kind) != kind:
                raise ScaffoldError(f"process {name!r} resource {kind!r} has mismatched kind")
            if kind != "db":
                continue
            driver = definition.get("driver", "bun")
            dialect = definition.get("dialect", "postgres")
            if driver not in VALID_DB_DRIVERS:
                raise ScaffoldError(f"process {name!r} has unsupported database driver {driver!r}")
            if driver == "gorm" and dialect not in VALID_GORM_DIALECTS:
                raise ScaffoldError(f"process {name!r} has unsupported GORM dialect {dialect!r}")
            if driver == "bun" and dialect != "postgres":
                raise ScaffoldError(f"process {name!r} Bun database requires postgres dialect")
        for module in process_modules:
            if module not in modules:
                raise ScaffoldError(f"process {name!r} contains unknown module {module!r}")
            if module in assignments:
                raise ScaffoldError(
                    f"module {module!r} is assigned to both {assignments[module]!r} and {name!r}"
                )
            assignments[str(module)] = str(name)
    missing = sorted(set(modules) - set(assignments))
    if missing:
        raise ScaffoldError(f"modules are not assigned to a process: {', '.join(missing)}")
    for blocker in blockers:
        if not isinstance(blocker, dict) or not isinstance(blocker.get("modules"), list):
            raise ScaffoldError("architecture contains an invalid extraction blocker")
        if not blocker["modules"]:
            raise ScaffoldError("extraction blocker must reference at least one module")
        if blocker.get("kind") not in {"shared-transaction", "shared-data", "local-event", "streaming", "other"}:
            raise ScaffoldError(f"invalid extraction blocker kind {blocker.get('kind')!r}")
        if not str(blocker.get("reason", "")).strip():
            raise ScaffoldError("extraction blocker reason is empty")
        for module in blocker["modules"]:
            if module not in modules:
                raise ScaffoldError(f"extraction blocker references unknown module {module!r}")


def load_manifest(project: Path) -> dict[str, Any]:
    path = project / MANIFEST_PATH
    if not path.is_file():
        raise ScaffoldError(f"missing {MANIFEST_PATH}; initialize the project or migrate it with the installed tool")
    try:
        manifest = json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as error:
        raise ScaffoldError(f"invalid {MANIFEST_PATH}: {error}") from error
    if manifest.get("schema") != MANIFEST_SCHEMA:
        raise ScaffoldError(
            f"unsupported manifest schema {manifest.get('schema')!r}; run project upgrade with the installed skill"
        )
    if not isinstance(manifest.get("features"), dict) or not isinstance(manifest.get("files"), dict):
        raise ScaffoldError(f"invalid {MANIFEST_PATH}: features/files must be objects")
    return manifest


def manifest_content(manifest: dict[str, Any]) -> str:
    payload = copy.deepcopy(manifest)
    payload["tool_version"] = TOOL_VERSION
    return json.dumps(payload, indent=2, sort_keys=True) + "\n"


@dataclass(frozen=True)
class OutputFile:
    path: Path
    content: str
    owner: str
    template: str
    provenance: dict[str, Any] = field(default_factory=dict)


@dataclass(frozen=True)
class Change:
    path: Path
    action: str
    before: str | None
    after: str | None
    output: OutputFile | None = None


def output(
    path: str | Path,
    content: str,
    *,
    owner: str = OWNER_MANAGED,
    template: str,
    provenance: dict[str, Any] | None = None,
) -> OutputFile:
    return OutputFile(Path(path), content, owner, template, provenance or {})


def runtime_outputs() -> list[OutputFile]:
    outputs = [
        output(
            ".modular/tool/modular.py",
            Path(__file__).read_text(encoding="utf-8"),
            template="runtime/modular.py",
        )
    ]
    for base, label in [
        (ASSETS_DIR, "assets"),
        (REFERENCES_DIR, "references"),
        (VENDOR_DIR, "_vendor"),
    ]:
        if not base.is_dir():
            continue
        for path in sorted(base.rglob("*")):
            if path.is_file() and "__pycache__" not in path.parts and path.suffix != ".pyc":
                relative = path.relative_to(base)
                outputs.append(
                    output(
                        Path(".modular/tool") / label / relative,
                        path.read_text(encoding="utf-8"),
                        template=f"runtime/{label}/{relative.as_posix()}",
                    )
                )
    return outputs


def unique_imports(imports: Iterable[str]) -> list[str]:
    return list(dict.fromkeys(imports))


def format_go_content(content: str, path: Path) -> str:
    if path.suffix != ".go":
        return content
    gofmt = shutil.which("gofmt")
    if gofmt is None:
        if testing_mode():
            return content
        raise ScaffoldError("`gofmt` is required to render Go scaffold files")
    completed = subprocess.run([gofmt], input=content, text=True, encoding="utf-8", capture_output=True)
    if completed.returncode != 0:
        raise ScaffoldError(f"gofmt rejected generated {path}: {completed.stderr.strip()}")
    return completed.stdout


def render_main_scaffold() -> str:
    return "// Code generated by modular scaffold. DO NOT EDIT.\n\npackage main\n\nfunc main() { execute() }\n"


def render_cmd_policy_scaffold() -> str:
    return (
        "// Code scaffolded by modular. This file is user-maintained.\n\n"
        "package main\n\n"
        "import (\n"
        '\tmodularlog "github.com/wplbyx/modular/packages/log"\n'
        '\tmodulartransport "github.com/wplbyx/modular/packages/transport"\n'
        ")\n\n"
        "func newTransportPolicy(application string, logger modularlog.Logger) *modulartransport.Policy {\n"
        "\treturn modulartransport.NewPolicy(application, modulartransport.WithLogger(logger))\n"
        "}\n"
    )


def render_module_config_scaffold(module: str) -> str:
    return (
        "// Code scaffolded by modular. This file is user-maintained.\n\n"
        f"package {module}\n\n"
        'import modularconfig "github.com/wplbyx/modular/packages/config"\n\n'
        "type Config struct{}\n\n"
        "func (Config) Flags(string) []modularconfig.FlagSpec { return nil }\n"
    )


def render_module_skeleton(module: str) -> dict[str, str]:
    return {
        f"modules/{module}/bootstrap.go": (
            "// Code scaffolded by modular. This file is user-maintained.\n\n"
            f"package {module}\n\n"
            "// Dependencies 是模块装配所需的显式依赖。\n"
            "// 按实际用例添加必要的 Provider 和其他模块的 contract，不传入全量资源容器。\n"
            "type Dependencies struct {}\n\n"
            "// Module 是提供给 cmd 的装配结果，不是兄弟模块的业务契约。\n"
            "// 按实际用例暴露 contract 能力和必要的挂载入口，保持内部实现私有。\n"
            "type Module struct {}\n\n"
            "// New 在模块内部组装 adapter、用例和领域对象。\n"
            "// 构造仅连接对象：保留 Provider，不提前调用 Value，不启动后台任务。\n"
            "// 迁移、订阅等运行行为由 cmd 汇总后交给 Application 生命周期安排。\n"
            "// 空骨架仅表示空模块装配成功；业务实现由实际需求驱动。\n"
            "func New(cfg Config, deps Dependencies) (*Module, error) {\n"
            "\treturn &Module{}, nil\n"
            "}\n"
        ),
        f"modules/{module}/contract/doc.go": "// Package contract contains the public module contract.\npackage contract\n",
        f"modules/{module}/internal/app/doc.go": "package app\n",
        f"modules/{module}/internal/domain/doc.go": "// Package domain is intentionally empty until domain invariants exist.\npackage domain\n",
        f"modules/{module}/infrastructure/http/doc.go": "package http\n",
        f"modules/{module}/infrastructure/gorm/doc.go": "package gorm\n",
        f"modules/{module}/infrastructure/eventbus/doc.go": "package eventbus\n",
    }

def application_resource_specs(application: dict[str, Any]) -> list[dict[str, str]]:
    resources = application.get("resources", {})
    specs: list[dict[str, str]] = []
    db = resources.get("db")
    if db:
        driver = str(db.get("driver", "bun"))
        if driver == "bun":
            specs.append({
                "field": "DB",
                "variable": "dbResource",
                "config": "Database",
                "type": "*bunresource.Resource",
                "type_import": 'bunresource "github.com/wplbyx/modular/packages/infra/database/bun"',
                "ctor_import": 'bunresource "github.com/wplbyx/modular/packages/infra/database/bun"',
                "ctor": "bunresource.NewResource(&cfg.Database)",
            })
        elif driver == "gorm":
            dialect = str(db.get("dialect", "postgres"))
            specs.append({
                "field": "DB",
                "variable": "dbResource",
                "config": "Database",
                "type": "*modulargorm.Resource",
                "type_import": 'modulargorm "github.com/wplbyx/modular/packages/infra/database/gorm"',
                "ctor_import": f'gormresource "github.com/wplbyx/modular/packages/infra/database/gorm/{dialect}"',
                "ctor": "gormresource.NewResource(&cfg.Database)",
            })
        else:
            specs.append({
                "field": "DB",
                "variable": "dbResource",
                "config": "Mongo",
                "type": "*mongoresource.Resource",
                "type_import": 'mongoresource "github.com/wplbyx/modular/packages/infra/database/mongo"',
                "ctor_import": 'mongoresource "github.com/wplbyx/modular/packages/infra/database/mongo"',
                "ctor": "mongoresource.NewResource(&cfg.Mongo)",
            })
    if "redis" in resources:
        specs.append({
            "field": "Redis",
            "variable": "redisResource",
            "config": "Redis",
            "type": "*redisresource.Resource",
            "type_import": 'redisresource "github.com/wplbyx/modular/packages/infra/cache/redis"',
            "ctor_import": 'redisresource "github.com/wplbyx/modular/packages/infra/cache/redis"',
            "ctor": "redisresource.NewResource(&cfg.Redis)",
        })
    if "storage" in resources:
        specs.append({
            "field": "Storage",
            "variable": "storageResource",
            "config": "Storage",
            "type": "*storageresource.Resource",
            "type_import": 'storageresource "github.com/wplbyx/modular/packages/infra/storage/resource"',
            "ctor_import": 'storageresource "github.com/wplbyx/modular/packages/infra/storage/resource"',
            "ctor": "storageresource.New(&cfg.Storage)",
        })
    if "telemetry" in resources:
        specs.append({
            "field": "Telemetry",
            "variable": "telemetryResource",
            "config": "Telemetry",
            "type": "*telemetry.OpenTelemetry",
            "type_import": '"github.com/wplbyx/modular/packages/telemetry"',
            "ctor_import": '"github.com/wplbyx/modular/packages/telemetry"',
            "ctor": "",
        })
    if "eventbus" in resources:
        specs.append({
            "field": "EventBus",
            "variable": "eventBusResource",
            "config": "EventBus",
            "type": "*eventbus.Bus",
            "type_import": '"github.com/wplbyx/modular/packages/eventbus"',
            "ctor_import": '"github.com/wplbyx/modular/packages/eventbus"',
            "ctor": "eventbus.New(cfg.EventBus, loggerManager.Logger())",
        })
    return specs


def render_application_config(module: str, application: dict[str, Any]) -> str:
    modules = list(application.get("modules", []))
    project_imports = [
        f'\t{lower_camel(name)}config "{module}/modules/{name}"'
        for name in modules
    ]
    import_lines = ['\t"github.com/wplbyx/modular/packages/config/configitem"']
    if project_imports:
        import_lines.extend(["", *project_imports])
    fields = [
        '\tApplication configitem.Application `mapstructure:"Application"`',
        '\tLogging configitem.Logging `mapstructure:"Logging"`',
    ]
    transports = set(application.get("transports", []))
    if "http" in transports:
        fields.append('\tHTTP configitem.HTTP `mapstructure:"HTTP"`')
    if "grpc" in transports:
        fields.append('\tGRPC configitem.GRPC `mapstructure:"GRPC"`')
    resources = application.get("resources", {})
    db = resources.get("db")
    if db and db.get("driver") == "mongo":
        fields.append('\tMongo configitem.Mongo `mapstructure:"Mongo"`')
    elif db:
        fields.append('\tDatabase configitem.Database `mapstructure:"Database"`')
    for kind, type_name in [
        ("redis", "Redis"),
        ("storage", "Storage"),
        ("telemetry", "Telemetry"),
        ("eventbus", "EventBus"),
    ]:
        if kind in resources:
            fields.append(f'\t{type_name} configitem.{type_name} `mapstructure:"{type_name}"`')
    fields.extend(
        f'\t{pascal_case(name)} {lower_camel(name)}config.Config `mapstructure:"{pascal_case(name)}"`'
        for name in modules
    )
    return (
        "// Code generated by modular scaffold. DO NOT EDIT.\n\n"
        "package config\n\n"
        "import (\n" + "\n".join(import_lines) + "\n)\n\n"
        "type Config struct {\n" + "\n".join(fields) + "\n}\n"
    )


def render_application_yaml(name: str, application: dict[str, Any]) -> str:
    lines = [
        "# Code generated by modular scaffold. DO NOT EDIT.",
        "Application:",
        f"  Name: {name}",
        "  Mode: dev",
        "  Version: v0.4.1",
        '  InstanceID: ""',
        "  Metadata: {}",
        "  ShutdownTimeout: 10s",
        "",
        "Logging:",
        "  Level: info",
        "  Output: [console]",
        "  Async:",
        "    Enabled: true",
        "    Capacity: 8192",
        "    ErrorTimeout: 50ms",
        "    FlushTimeout: 5s",
    ]
    ports = application.get("ports", {})
    if "http" in application.get("transports", []):
        lines.extend(["", "HTTP:", '  Host: "0.0.0.0"', f"  Port: {ports.get('http', 18080)}"])
    if "grpc" in application.get("transports", []):
        lines.extend(["", "GRPC:", '  Host: "0.0.0.0"', f"  Port: {ports.get('grpc', 19090)}"])
    resources = application.get("resources", {})
    db = resources.get("db")
    if db:
        driver = db.get("driver", "bun")
        dialect = db.get("dialect", "postgres")
        if driver == "mongo":
            lines.extend(["", "Mongo:", '  URI: "mongodb://127.0.0.1:27017"', "  Database: app"])
        else:
            dsn = {
                "postgres": "postgres://app:app@127.0.0.1:5432/app?sslmode=disable",
                "mysql": "app:app@tcp(127.0.0.1:3306)/app?charset=utf8mb4&parseTime=True&loc=Local",
                "sqlite": "app.db",
                "clickhouse": "tcp://127.0.0.1:9000?database=app&username=default&password=",
            }[dialect]
            lines.extend(["", "Database:", f'  DSN: "{dsn}"', "  MaxOpenConn: 25", "  MaxIdleConn: 5"])
    if "redis" in resources:
        lines.extend(["", "Redis:", '  Host: "127.0.0.1"', "  Port: 6379"])
    if "storage" in resources:
        lines.extend(["", "Storage:", "  Type: disk", "  Disk:", "    RootDir: storage/upload", "    BaseUrl: /upload"])
    if "telemetry" in resources:
        lines.extend(["", "Telemetry:", '  Tracer: ""', '  Metric: ""', '  Logger: ""'])
    if "eventbus" in resources:
        lines.extend(["", "EventBus:", f"  Name: {name}-events", "  Capacity: 8192"])
    for module in application.get("modules", []):
        lines.extend(["", f"{pascal_case(module)}: {{}}"])
    return "\n".join(lines) + "\n"


def render_v4_wiring(module_path: str, architecture: dict[str, Any]) -> str:
    modules = sorted(architecture["modules"])
    application = architecture["application"]
    resource_specs = application_resource_specs(application)
    transports = set(application.get("transports", []))
    imports = [
        '"github.com/wplbyx/modular/packages/core"',
        '"github.com/wplbyx/modular/packages/health"',
        'modularlog "github.com/wplbyx/modular/packages/log"',
        '"github.com/wplbyx/modular/packages/registry"',
    ]
    if "http" in transports:
        imports.append('httpserver "github.com/wplbyx/modular/packages/transport/server/http"')
    if "grpc" in transports:
        imports.append('rpcserver "github.com/wplbyx/modular/packages/transport/server/rpc"')
    imports.extend(
        spec["type_import"]
        for spec in resource_specs
    )
    project_imports = [
        f'{lower_camel(name)}config "{module_path}/modules/{name}"'
        for name in modules
    ]
    lines = [
        "// Code generated by modular scaffold. DO NOT EDIT.",
        "// 应用级装配：按 DAG 调用模块 New，传入配置、必要的 Provider 和其他模块的 contract。",
        "// 模块 bootstrap 组装内部对象；cmd 连接模块、汇总挂载入口并配置 Application 生命周期。",
        "",
        "package main",
        "",
        "import (",
        *["\t" + item for item in unique_imports(imports)],
        *([""] if project_imports else []),
        *["\t" + item for item in project_imports],
        ")",
        "",
        "type ModuleConfigs struct {",
        *[f"\t{pascal_case(name)} {lower_camel(name)}config.Config" for name in modules],
        "}",
        "",
        "type Resources struct {",
        *[f"\t{spec['field']} {spec['type']}" for spec in resource_specs],
        "}",
        "",
        "type Platform struct {",
        "\tLogger    modularlog.Logger",
        "\tHealth    *health.Manager",
        "\tModules   ModuleConfigs",
        "\tResources Resources",
        "}",
        "",
        "type Assembly struct {",
    ]
    if "http" in transports:
        lines.append("\tHTTP      []httpserver.RegisterRouteFunc")
    if "grpc" in transports:
        lines.append("\tGRPC      []rpcserver.RegisterFunc")
    lines.extend([
        "\tResources []core.Resource",
        "\tEndpoints []core.Endpoint",
        "\tChecks    []health.Checker",
        "\tRegistrar registry.Registrar",
        "}",
        "",
    ])
    if "http" in transports:
        lines.append("func (a *Assembly) AddHTTP(routes ...httpserver.RegisterRouteFunc) { a.HTTP = append(a.HTTP, routes...) }")
    if "grpc" in transports:
        lines.append("func (a *Assembly) AddGRPC(registers ...rpcserver.RegisterFunc) { a.GRPC = append(a.GRPC, registers...) }")
    lines.extend([
        "func (a *Assembly) AddResource(resources ...core.Resource) { a.Resources = append(a.Resources, resources...) }",
        "func (a *Assembly) AddEndpoint(endpoints ...core.Endpoint) { a.Endpoints = append(a.Endpoints, endpoints...) }",
        "func (a *Assembly) AddChecker(checkers ...health.Checker) { a.Checks = append(a.Checks, checkers...) }",
        "",
    ])
    return "\n".join(lines)


def render_v4_business_scaffold() -> str:
    return (
        "// Code scaffolded by modular. This file is user-maintained.\n\n"
        "package main\n\n"
        "func WireApplication(platform Platform) (Assembly, error) {\n"
        "\t// modular:business-unwired - assemble typed modules in dependency order.\n"
        "\t// 向模块 New 传入配置、必要的 Provider 和其他模块的 contract。\n"
        "\t// 模块 bootstrap 组装内部对象；此处连接模块并汇总挂载入口。\n"
        "\t_ = platform\n"
        "\treturn Assembly{}, nil\n"
        "}\n"
    )


def render_v4_framework(manifest: dict[str, Any], application_name: str, application: dict[str, Any]) -> str:
    module_path = str(manifest["project"]["module"])
    specs = application_resource_specs(application)
    standard_imports = ['"context"', '"errors"', '"fmt"', '"os"', '"os/signal"', '"syscall"']
    modular_imports = [
        '"github.com/wplbyx/modular/packages/app"',
        'modularconfig "github.com/wplbyx/modular/packages/config"',
        '"github.com/wplbyx/modular/packages/core"',
        '"github.com/wplbyx/modular/packages/health"',
        'modularlog "github.com/wplbyx/modular/packages/log"',
    ]
    transports = set(application.get("transports", []))
    if "http" in transports:
        modular_imports.append('httpserver "github.com/wplbyx/modular/packages/transport/server/http"')
    if "grpc" in transports:
        modular_imports.append('rpcserver "github.com/wplbyx/modular/packages/transport/server/rpc"')
    modular_imports.extend(spec["ctor_import"] for spec in specs)
    lines = [
        "// Code generated by modular scaffold. DO NOT EDIT.", "", "package main", "", "import (",
        *["\t" + item for item in standard_imports], "",
        *["\t" + item for item in unique_imports(modular_imports)], "",
        f'\tprojectconfig "{module_path}/config/{application_name}"',
                ")", "",
        "func execute() {",
        "\tctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)",
        "\tdefer cancel()", "",
        f"\tcommand := modularconfig.NewRootCommand[projectconfig.Config](modularconfig.CommandOptions[projectconfig.Config]{{",
        f'\t\tName: "{application_name}",', f'\t\tShort: "{application_name} application",',
        f'\t\tDefaultFile: "./config/{application_name}/config.yaml",', f'\t\tEnvPrefix: "{env_prefix(application_name)}",',
        "\t\tRun: run,", "\t})", "\tcommand.SetContext(ctx)",
        "\tcommand.SilenceErrors = true", "\tcommand.SilenceUsage = true",
        "\tif err := command.Execute(); err != nil {", '\t\tfmt.Fprintf(os.Stderr, "application exited: %v\\n", err)',
        "\t\tos.Exit(1)", "\t}", "}", "",
        "func run(ctx context.Context, cfg *projectconfig.Config) (runErr error) {",
        "\tloggerManager, err := modularlog.NewLoggerManager(&cfg.Logging)", "\tif err != nil {",
        '\t\treturn fmt.Errorf("create logger: %w", err)', "\t}",
        "\trestoreLogger := modularlog.SetDefault(loggerManager.Logger())", "\tdefer restoreLogger()",
        "\tdefer func() { runErr = errors.Join(runErr, loggerManager.Close(context.WithoutCancel(ctx))) }()",
        *(["\tpolicy := newTransportPolicy(cfg.Application.Name, loggerManager.Logger())"] if transports else []),
        "\thealthManager := health.NewManager()",
        "\tresources := make([]core.Resource, 0)", "\tendpoints := make([]core.Endpoint, 0)",
        "\ttransports := make([]core.Transport, 0)",
        "\tplatform := Platform{Logger: loggerManager.Logger(), Health: healthManager}",
        "\tplatform.Modules = ModuleConfigs{",
        *[f"\t\t{pascal_case(name)}: cfg.{pascal_case(name)}," for name in application.get("modules", [])],
        "\t}", "",
    ]
    for spec in specs:
        variable = spec["variable"]
        if spec["field"] == "Telemetry":
            lines.extend([
                f"\t{variable}, err := telemetry.NewOpenTelemetry(ctx, cfg.Application.Name, cfg.Application.Version, &cfg.Telemetry, telemetry.WithLoggerManager(loggerManager))",
                "\tif err != nil {", '\t\treturn fmt.Errorf("create telemetry: %w", err)', "\t}",
            ])
        elif spec["field"] == "EventBus":
            lines.extend([
                f"\t{variable}, err := {spec['ctor']}", "\tif err != nil {",
                '\t\treturn fmt.Errorf("create event bus: %w", err)', "\t}",
            ])
        else:
            lines.append(f"\t{variable} := {spec['ctor']}")
        lines.extend([
            f"\tplatform.Resources.{spec['field']} = {variable}",
            f"\tresources = append(resources, {variable})",
        ])
    lines.extend([
        "", "\tassembly, err := WireApplication(platform)", "\tif err != nil {",
        '\t\treturn fmt.Errorf("wire application: %w", err)', "\t}",
        "\tif err := healthManager.Register(assembly.Checks...); err != nil {",
        '\t\treturn fmt.Errorf("register module health checks: %w", err)', "\t}",
        "\tresources = append(resources, assembly.Resources...)",
        "\tendpoints = append(endpoints, assembly.Endpoints...)", "",
    ])
    if "http" in transports:
        lines.extend([
            "\thttpServer, err := httpserver.NewServer(&cfg.HTTP, httpserver.WithPolicy(policy), httpserver.WithHealthManager(\"\", healthManager))",
            "\tif err != nil {", '\t\treturn fmt.Errorf("create HTTP server: %w", err)', "\t}",
            "\thttpServer.RegisterRoute(assembly.HTTP...)", "\tendpoints = append(endpoints, httpServer)",
            "\ttransports = append(transports, httpServer.Transport())", "",
        ])
    if "grpc" in transports:
        lines.extend([
            "\tgrpcServer, err := rpcserver.NewServer(&cfg.GRPC, rpcserver.ChainRegister(assembly.GRPC...), rpcserver.WithPolicy(policy))",
            "\tif err != nil {", '\t\treturn fmt.Errorf("create gRPC server: %w", err)', "\t}",
            "\tendpoints = append(endpoints, grpcServer)", "\ttransports = append(transports, grpcServer.Transport())", "",
        ])
    lines.extend([
        "\tidentity, err := core.NewProcessIdentity(cfg.Application.Name, cfg.Application.Version, cfg.Application.InstanceID, cfg.Application.Metadata)",
        "\tif err != nil {", '\t\treturn fmt.Errorf("create process identity: %w", err)', "\t}",
        "\tnode := core.NewServiceNodeFromProcess(identity, transports...)",
        "\toptions := []app.Option{app.WithServiceNode(node), app.WithHealthManager(healthManager)}",
        "\tif assembly.Registrar != nil { options = append(options, app.WithRegistrar(assembly.Registrar)) }",
        "\tfor _, resource := range resources { options = append(options, app.WithResource(resource)) }",
        "\tfor _, endpoint := range endpoints { options = append(options, app.WithEndpoint(endpoint)) }",
        "\tapplication, err := app.NewApplication(ctx, &cfg.Application, loggerManager.Logger(), options...)",
        "\tif err != nil {", '\t\treturn fmt.Errorf("create application: %w", err)', "\t}",
        "\treturn application.Run()", "}", "",
    ])
    return "\n".join(lines) + "\nfunc main() { execute() }\n"


def render_v4_outputs(project: Path, manifest: dict[str, Any], architecture: dict[str, Any]) -> dict[Path, OutputFile]:
    validate_architecture(architecture)
    meta = manifest["project"]
    module_path = str(meta["module"])
    values = {
        "PROJECT": module_path,
        "GO_VERSION": DEFAULT_GO_VERSION,
        "MODULAR_VERSION": str(meta["modular_version"]),
    }
    application = {**architecture["application"], "modules": sorted(architecture["modules"])}
    name = str(application["name"])
    outputs: list[OutputFile] = [
        output("go.mod", render_template("project/go.mod.tmpl", values), owner=OWNER_SCAFFOLD, template="project/go.mod"),
        output("Makefile", template_text("project/Makefile.tmpl"), owner=OWNER_SCAFFOLD, template="project/Makefile"),
        output(".gitignore", template_text("project/gitignore.tmpl"), owner=OWNER_SCAFFOLD, template="project/gitignore"),
        output(PROFILE_PATH, template_text("project/profile.toml.tmpl"), owner=OWNER_SCAFFOLD, template="project/profile"),
        output(ARCHITECTURE_PATH, architecture_content(architecture), owner=OWNER_SCAFFOLD, template="architecture/source"),
        output(".modular/make/modular.mk", template_text("project/modular.mk.tmpl"), template="project/modular.mk"),
        output("modules/.gitkeep", "", template="directory/internal"),
        output(f"cmd/{name}/modules.go", render_v4_wiring(module_path, architecture), template="cmd/modules-v4"),
        output(f"cmd/{name}/resources.go", "package main\n\n// Shared resources are constructed in the generated application bootstrap.\n", owner=OWNER_SCAFFOLD, template="cmd/resources-v4"),
    ]
    outputs.extend(runtime_outputs())
    for module in sorted(architecture["modules"]):
        outputs.append(output(
            f"modules/{module}/config.go", render_module_config_scaffold(module), owner=OWNER_SCAFFOLD,
            template="config/module", provenance={"module": module},
        ))
        for path, content in render_module_skeleton(module).items():
            outputs.append(output(path, content, owner=OWNER_SCAFFOLD, template="module/skeleton", provenance={"module": module}))
    outputs.extend([
        output(f"config/{name}/config.gen.go", render_application_config(module_path, application), template="config/application-generated-v4", provenance={"application": name}),
        output(f"config/{name}/config.yaml", render_application_yaml(name, application), template="config/application-yaml-v4", provenance={"application": name}),
        output(f"cmd/{name}/policy.go", render_cmd_policy_scaffold(), owner=OWNER_SCAFFOLD, template="cmd/policy", provenance={"application": name}),
        output(f"cmd/{name}/main.go", render_v4_framework(manifest, name, application), template="cmd/main-v4", provenance={"application": name}),
    ])
    formatted = [
        OutputFile(item.path, format_go_content(item.content, item.path), item.owner, item.template, item.provenance)
        for item in outputs
    ]
    return {item.path: item for item in formatted}


def render_outputs(
    project: Path,
    manifest: dict[str, Any],
    architecture: dict[str, Any] | None = None,
) -> dict[Path, OutputFile]:
    require_v4(manifest)
    return render_v4_outputs(project, manifest, architecture or load_architecture(project))


def file_record(item: OutputFile, content_hash: str) -> dict[str, Any]:
    return {
        "owner": item.owner,
        "sha256": content_hash,
        "template": item.template,
        "template_version": TOOL_VERSION,
        "provenance": item.provenance,
    }


def build_changes(
    project: Path,
    manifest: dict[str, Any],
    desired: dict[Path, OutputFile],
    *,
    allow_delete: bool,
    force_scaffold: set[Path] | None = None,
    force_delete: set[Path] | None = None,
) -> tuple[list[Change], dict[str, Any]]:
    changes: list[Change] = []
    updated = copy.deepcopy(manifest)
    records: dict[str, Any] = updated.setdefault("files", {})
    old_records = copy.deepcopy(records)
    force_scaffold = force_scaffold or set()
    force_delete = force_delete or set()

    for relative, item in desired.items():
        key = relative.as_posix()
        target = project / relative
        previous = old_records.get(key)
        exists = target.is_file()
        current = target.read_text(encoding="utf-8") if exists else None

        if item.owner == OWNER_SCAFFOLD and relative in force_scaffold:
            if not exists:
                raise ScaffoldError(f"cannot update missing scaffold file: {key}")
            if current != item.content:
                changes.append(Change(relative, "update", current, item.content, item))
            records[key] = file_record(item, sha256_text(item.content))
            continue

        if item.owner == OWNER_SCAFFOLD and previous is not None:
            if not exists:
                warn(f"scaffold-once file is missing and will not be recreated: {key}")
            continue

        if item.owner == OWNER_SCAFFOLD and exists:
            records[key] = file_record(item, sha256_text(current or ""))
            continue

        if item.owner == OWNER_MANAGED and exists:
            current_hash = sha256_text(current or "")
            if previous is None:
                if current != item.content:
                    raise ScaffoldError(f"untracked file blocks managed output: {key}")
            elif previous.get("owner") != OWNER_MANAGED:
                raise ScaffoldError(f"ownership conflict for {key}: expected managed")
            elif current_hash != previous.get("sha256") and current != item.content:
                raise ScaffoldError(f"managed file was modified: {key}; move custom code to an extension file")

        if not exists:
            changes.append(Change(relative, "create", None, item.content, item))
        elif current != item.content:
            changes.append(Change(relative, "update", current, item.content, item))
        records[key] = file_record(item, sha256_text(item.content))

    desired_keys = {path.as_posix() for path in desired}
    for key, record in old_records.items():
        relative = Path(key)
        if key in desired_keys or (record.get("owner") != OWNER_MANAGED and relative not in force_delete):
            continue
        target = project / relative
        if not allow_delete:
            continue
        if target.is_file():
            current = target.read_text(encoding="utf-8")
            if sha256_text(current) != record.get("sha256"):
                ownership = "scaffold-once" if record.get("owner") == OWNER_SCAFFOLD else "managed"
                raise ScaffoldError(f"refusing to delete modified {ownership} file: {key}")
            changes.append(Change(relative, "delete", current, None, None))
        records.pop(key, None)

    updated["tool_version"] = TOOL_VERSION
    return changes, updated


def change_diff(change: Change) -> str:
    before = [] if change.before is None else change.before.splitlines(keepends=True)
    after = [] if change.after is None else change.after.splitlines(keepends=True)
    return "".join(
        difflib.unified_diff(
            before,
            after,
            fromfile=f"a/{change.path.as_posix()}",
            tofile=f"b/{change.path.as_posix()}",
        )
    )


def show_plan(changes: list[Change], manifest_before: str | None, manifest_after: str, *, show_diff: bool) -> None:
    if not changes and manifest_before == manifest_after:
        info("no changes")
        return
    for change in changes:
        print(f"{change.action:>6} {change.path.as_posix()}")
        if show_diff:
            diff = change_diff(change)
            if diff:
                print(diff, end="" if diff.endswith("\n") else "\n")
    if manifest_before != manifest_after:
        print(f"update {MANIFEST_PATH.as_posix()}")
        if show_diff:
            manifest_change = Change(MANIFEST_PATH, "update", manifest_before, manifest_after)
            diff = change_diff(manifest_change)
            if diff:
                print(diff, end="" if diff.endswith("\n") else "\n")


def ensure_target(project: Path, relative: Path) -> Path:
    root = project.resolve()
    target = (project / relative).resolve()
    if target != root and root not in target.parents:
        raise ScaffoldError(f"output escapes project root: {relative}")
    return target


def atomic_write(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(path.name + ".modular-tmp")
    with temporary.open("w", encoding="utf-8", newline="\n") as handle:
        handle.write(content)
    os.replace(temporary, path)


def gofmt_changed(project: Path, changes: list[Change]) -> None:
    gofmt = shutil.which("gofmt")
    if gofmt is None:
        if not testing_mode():
            raise ScaffoldError("`gofmt` is required to apply Go scaffold files")
        return
    files = [str(project / change.path) for change in changes if change.after is not None and change.path.suffix == ".go"]
    if files:
        run_command([gofmt, "-w", *files], cwd=project)


def refresh_manifest_hashes(project: Path, manifest: dict[str, Any]) -> None:
    for key, record in manifest.get("files", {}).items():
        if record.get("owner") != OWNER_MANAGED:
            continue
        path = project / Path(key)
        if path.is_file():
            record["sha256"] = sha256_file(path)


def restore_transaction(
    project: Path,
    backups: dict[Path, Path | None],
    *,
    project_existed: bool,
) -> None:
    if not project_existed:
        if project.exists():
            shutil.rmtree(project)
        return
    for relative, backup in backups.items():
        target = project / relative
        if backup is None:
            if target.is_file():
                target.unlink()
            continue
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(backup, target)


def apply_transaction(
    project: Path,
    changes: list[Change],
    manifest: dict[str, Any],
    *,
    verify: Callable[[Path], None] | None,
) -> None:
    project_existed = project.exists()
    project.parent.mkdir(parents=True, exist_ok=True)
    if not project_existed:
        project.mkdir()

    with tempfile.TemporaryDirectory(prefix="modular-scaffold-backup-") as directory:
        backup_root = Path(directory)
        backup_paths = {change.path for change in changes}
        backup_paths.update({MANIFEST_PATH, Path("go.mod"), Path("go.sum")})
        backups: dict[Path, Path | None] = {}
        for relative in backup_paths:
            target = project / relative
            if target.is_file():
                backup = backup_root / relative
                backup.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(target, backup)
                backups[relative] = backup
            else:
                backups[relative] = None
        try:
            for change in changes:
                target = ensure_target(project, change.path)
                if change.action == "delete":
                    if target.is_file():
                        target.unlink()
                else:
                    atomic_write(target, change.after or "")
            gofmt_changed(project, changes)
            refresh_manifest_hashes(project, manifest)
            atomic_write(project / MANIFEST_PATH, manifest_content(manifest))
            if verify is not None:
                verify(project)
        except BaseException:
            restore_transaction(project, backups, project_existed=project_existed)
            raise


def render_and_apply(
    project: Path,
    manifest: dict[str, Any],
    *,
    architecture: dict[str, Any] | None = None,
    dry_run: bool,
    diff: bool,
    allow_delete: bool = False,
    verify: Callable[[Path], None] | None = None,
    overrides: dict[Path, OutputFile] | None = None,
    force_scaffold: set[Path] | None = None,
    force_delete: set[Path] | None = None,
) -> int:
    desired = render_outputs(project, manifest, architecture)
    if overrides:
        desired.update(overrides)
    changes, updated = build_changes(
        project,
        manifest,
        desired,
        allow_delete=allow_delete,
        force_scaffold=force_scaffold,
        force_delete=force_delete,
    )
    before_path = project / MANIFEST_PATH
    before = before_path.read_text(encoding="utf-8") if before_path.is_file() else None
    after = manifest_content(updated)
    if dry_run or diff:
        show_plan(changes, before, after, show_diff=diff)
        return 0
    if not changes and before == after:
        info("no changes")
        return 0
    apply_transaction(project, changes, updated, verify=verify)
    for change in changes:
        info(f"{change.action} {change.path.as_posix()}")
    return 0


def project_profile(project: Path) -> dict[str, Any]:
    path = project / PROFILE_PATH
    if not path.is_file():
        return {}
    try:
        with path.open("rb") as handle:
            value = tomli.load(handle)
    except (OSError, tomli.TOMLDecodeError) as error:
        raise ScaffoldError(f"invalid {PROFILE_PATH}: {error}") from error
    if not isinstance(value, dict):
        raise ScaffoldError(f"invalid {PROFILE_PATH}: root must be a table")
    return value


def profile_business_globs(project: Path) -> list[str]:
    profile = project_profile(project)
    checks = profile.get("checks", {})
    if not isinstance(checks, dict):
        return []
    values = checks.get("business_globs", [])
    return [str(value) for value in values] if isinstance(values, list) else []


def all_go_files(project: Path) -> Iterable[Path]:
    ignored = {".git", ".modular"}
    for path in project.rglob("*.go"):
        if any(part in ignored for part in path.parts):
            continue
        yield path


def placeholder_check(project: Path, *, allow_contract: bool, allow_unwired: bool) -> None:
    failures: list[str] = []
    for path in project.rglob("*"):
        if not path.is_file() or ".git" in path.parts or ".modular" in path.parts:
            continue
        if path.suffix not in {".go", ".yaml", ".toml", ".md"}:
            continue
        try:
            text = path.read_text(encoding="utf-8")
        except UnicodeDecodeError:
            continue
        for marker in FORBIDDEN_PLACEHOLDERS:
            if marker in text:
                failures.append(f"{path.relative_to(project)} contains forbidden placeholder {marker}")
        if not allow_contract and "modular:contract-unimplemented" in text:
            failures.append(f"{path.relative_to(project)} still contains contract Unimplemented marker")
        if not allow_unwired and "modular:business-unwired" in text:
            failures.append(f"{path.relative_to(project)} still contains unwired business marker")
    if failures:
        raise ScaffoldError("placeholder check failed:\n" + "\n".join(f"- {item}" for item in failures))


def doctor_check(project: Path, *, phase: str, strict: bool) -> list[str]:
    errors: list[str] = []
    warnings: list[str] = []
    try:
        manifest = load_manifest(project)
        module = read_module(project)
    except ScaffoldError as error:
        raise ScaffoldError(str(error)) from error
    for required in ["go.mod", "Makefile", ".modular/tool/modular.py"]:
        if not (project / required).exists():
            errors.append(f"missing {required}")
    if not is_modular_monolith(manifest):
        require_v4(manifest)
    doctor_v4(project, module, phase, errors)
    if strict:
        errors.extend(warnings)
        warnings = []
    for warning in warnings:
        warn(warning)
    if errors:
        raise ScaffoldError("doctor failed:\n" + "\n".join(f"- {item}" for item in errors))
    return warnings


def doctor_v4(
    project: Path,
    module_path: str,
    phase: str,
    errors: list[str],
) -> None:
    try:
        architecture = load_architecture(project)
    except ScaffoldError as error:
        errors.append(str(error))
        return
    for module in architecture["modules"]:
        if not (project / f"modules/{module}/config.go").is_file():
            errors.append(f"missing module config for {module}")
    application = str(architecture["application"]["name"])
    framework = project / f"cmd/{application}/main.go"
    for required in [
        framework,
        project / f"cmd/{application}/policy.go",
        project / f"config/{application}/config.gen.go",
        project / f"config/{application}/config.yaml",
    ]:
        if not required.is_file():
            errors.append(f"missing application file: {required.relative_to(project)}")
    if framework.is_file():
        check_bootstrap_contract(framework, errors)

    check_module_imports(project, module_path, architecture, errors)
    if phase == "complete":
        patterns = profile_business_globs(project)
        business_files = [
            path for path in all_go_files(project)
            if any(path.relative_to(project).match(pattern) for pattern in patterns)
        ]
        packages = {path.parent for path in business_files if not path.name.endswith("_test.go")}
        for package in packages:
            if not list(package.glob("*_test.go")):
                errors.append(f"business package has no tests: {package.relative_to(project)}")


def check_module_imports(
    project: Path,
    module_path: str,
    architecture: dict[str, Any],
    errors: list[str],
) -> None:
    modules = architecture["modules"]
    prefix = f"{module_path}/modules/"
    import_pattern = re.compile(
        r'^\s*(?:import\s+)?(?:[A-Za-z_.][A-Za-z0-9_.]*\s+)?"([^"]+)"',
        re.MULTILINE,
    )
    for path in all_go_files(project):
        relative = path.relative_to(project)
        parts = relative.parts
        if len(parts) < 3 or parts[:1] != ("modules",):
            continue
        owner = parts[1]
        if owner not in modules:
            errors.append(f"{relative} belongs to undeclared module {owner!r}")
            continue
        declared = set(modules[owner].get("dependencies", []))
        imports = import_pattern.findall(path.read_text(encoding="utf-8"))
        for imported in imports:
            provider = ""
            contract_import = False
            if imported.startswith(prefix):
                remainder = imported[len(prefix):]
                provider, _, child = remainder.partition("/")
                contract_import = child == "contract" or child.startswith("contract/")
            if not provider or provider == owner:
                continue
            if provider not in modules:
                errors.append(f"{relative} imports unknown module {provider!r}")
            elif provider not in declared:
                errors.append(f"{relative} imports undeclared dependency {owner} -> {provider}")
            elif not contract_import:
                errors.append(f"{relative} crosses into {provider} outside its contract")


def check_bootstrap_contract(path: Path, errors: list[str]) -> None:
    content = path.read_text(encoding="utf-8")
    # Existing user-owned helpers remain valid when upgrading the library.
    logger_call = "modularlog.NewLoggerManager(&cfg.Logging)"
    if logger_call not in content and "newLoggerManager(ctx, &cfg.Logging)" in content:
        logger_call = "newLoggerManager(ctx, &cfg.Logging)"
    required = [
        "config.NewRootCommand",
        logger_call,
        "modularlog.SetDefault(loggerManager.Logger())",
        "app.NewApplication(ctx, &cfg.Application, loggerManager.Logger(), options...)",
    ]
    uses_transport = "httpserver." in content or "rpcserver." in content
    if uses_transport:
        required.insert(3, "newTransportPolicy(cfg.Application.Name, loggerManager.Logger())")
    for fragment in required:
        if fragment not in content:
            errors.append(f"{path.relative_to(path.parents[2])} is missing bootstrap contract: {fragment}")
    ordering = required[1:-1]
    positions = [content.find(fragment) for fragment in ordering]
    if -1 not in positions and positions != sorted(positions):
        errors.append(f"{path.relative_to(path.parents[2])} violates config -> logger -> transport policy ordering")


def run_go(project: Path, args: list[str]) -> None:
    go = shutil.which("go")
    if go is None:
        raise ScaffoldError("`go` is required for project verification")
    run_command([go, *args], cwd=project)


def verify_framework(project: Path) -> None:
    if testing_mode():
        doctor_check(project, phase="framework", strict=True)
        placeholder_check(project, allow_contract=True, allow_unwired=True)
        return
    doctor_check(project, phase="framework", strict=True)
    placeholder_check(project, allow_contract=True, allow_unwired=True)
    run_go(project, ["build", "./..."])


def verify_contract(project: Path) -> None:
    doctor_check(project, phase="contract", strict=True)
    placeholder_check(project, allow_contract=True, allow_unwired=False)
    if not testing_mode():
        run_go(project, ["build", "./..."])


def verify_complete(project: Path) -> None:
    doctor_check(project, phase="complete", strict=True)
    placeholder_check(project, allow_contract=False, allow_unwired=False)
    if testing_mode():
        return
    gofmt = shutil.which("gofmt")
    if gofmt is not None:
        result = subprocess.run([gofmt, "-l", *[str(path) for path in all_go_files(project)]], cwd=project, text=True, capture_output=True)
        if result.stdout.strip():
            raise ScaffoldError("gofmt check failed:\n" + result.stdout.strip())
    run_go(project, ["build", "./..."])
    run_go(project, ["vet", "./..."])
    run_go(project, ["test", "./..."])
    run_go(project, ["test", "-race", "./..."])
    run_go(project, ["test", "./...", "-coverprofile=coverage.out"])
    run_go(project, ["tool", "cover", "-func=coverage.out"])


def parser_command_paths(parser: argparse.ArgumentParser) -> list[str]:
    paths: list[str] = []

    def walk(current: argparse.ArgumentParser, prefix: tuple[str, ...]) -> None:
        subparser_actions = [
            action
            for action in current._actions
            if isinstance(action, argparse._SubParsersAction)
        ]
        if not subparser_actions:
            if prefix:
                paths.append(" ".join(prefix))
            return
        for action in subparser_actions:
            for name, child in action.choices.items():
                walk(child, (*prefix, name))

    walk(parser, ())
    return sorted(paths)


def self_check(root: Path) -> None:
    script = root / "scripts/modular.py" if (root / "scripts/modular.py").is_file() else root / "modular.py"
    if not script.is_file():
        raise ScaffoldError(f"self-check cannot find CLI at {script}")
    vendor = script.parent / "_vendor" / "tomli"
    for path in [
        vendor / "__init__.py",
        vendor / "_parser.py",
        vendor / "_re.py",
        vendor / "_types.py",
        vendor / "LICENSE",
    ]:
        if not path.is_file():
            raise ScaffoldError(f"skill is missing vendored runtime file {path.relative_to(root)}")
    if tomli.loads("version = 1").get("version") != 1:
        raise ScaffoldError("vendored tomli failed its parse self-check")
    source = script.read_text(encoding="utf-8")
    hardcoded_parent = "parents[" + "3]"
    hardcoded_repo = "/".join(("agent", "modular"))
    if hardcoded_parent in source or hardcoded_repo in source:
        raise ScaffoldError("CLI contains an installation-path assumption")
    tests = root / "tests"
    if tests.is_dir():
        for path in tests.rglob("*.py"):
            test_source = path.read_text(encoding="utf-8")
            if hardcoded_parent in test_source or hardcoded_repo in test_source:
                raise ScaffoldError(f"test contains an installation-path assumption: {path.relative_to(root)}")
    assets = root / "assets"
    references = root / "references"
    if not assets.is_dir():
        raise ScaffoldError("skill is missing assets/")
    for path in [references / "commands.md", assets / "templates.json"]:
        if not path.is_file():
            raise ScaffoldError(f"skill is missing {path.relative_to(root)}")
    markdown_files = list(references.rglob("*.md"))
    skill_markdown = root / "SKILL.md"
    if skill_markdown.is_file():
        markdown_files.append(skill_markdown)
    markdown_link = re.compile(r"\[[^\]]*\]\(([^)#?]+\.md)(?:#[^)]+)?\)")
    for path in markdown_files:
        for relative in markdown_link.findall(path.read_text(encoding="utf-8")):
            target = (path.parent / relative).resolve()
            if not target.is_file():
                raise ScaffoldError(
                    f"broken reference in {path.relative_to(root)}: {relative}"
                )
    try:
        catalog = json.loads((assets / "templates.json").read_text(encoding="utf-8"))
    except json.JSONDecodeError as error:
        raise ScaffoldError(f"invalid assets/templates.json: {error}") from error
    fixture = {str(key): "fixture" for key in catalog.get("tokens", [])}
    catalog_templates = catalog.get("templates", {})
    if not isinstance(catalog_templates, dict):
        raise ScaffoldError("assets/templates.json templates must be an object")
    template_paths = {
        path.relative_to(assets).as_posix(): path
        for path in assets.rglob("*.tmpl")
    }
    missing_catalog = sorted(set(template_paths) - set(catalog_templates))
    missing_files = sorted(set(catalog_templates) - set(template_paths))
    if missing_catalog:
        raise ScaffoldError(f"templates are not registered: {', '.join(missing_catalog)}")
    if missing_files:
        raise ScaffoldError(f"registered templates are missing: {', '.join(missing_files)}")
    for relative, path in template_paths.items():
        metadata = catalog_templates.get(relative)
        if not isinstance(metadata, dict) or metadata.get("owner") not in {OWNER_MANAGED, OWNER_SCAFFOLD}:
            raise ScaffoldError(f"template has no valid owner: {relative}")
        if metadata.get("phase") not in {"framework", "contract", "business"}:
            raise ScaffoldError(f"template has no valid phase: {relative}")
        render_template(path.relative_to(assets).as_posix(), fixture)
    parser = build_parser()
    actual_commands = parser_command_paths(parser)
    if actual_commands != sorted(COMMAND_PATHS):
        raise ScaffoldError(
            "COMMAND_PATHS does not match the parser: "
            f"expected {sorted(COMMAND_PATHS)!r}, got {actual_commands!r}"
        )
    documented = (references / "commands.md").read_text(encoding="utf-8")
    for command in COMMAND_PATHS:
        if f"`{command}`" not in documented:
            raise ScaffoldError(f"command documentation is missing `{command}`")
    parser.parse_args(["self-check"])
    info(f"self-check passed for {root}")


def mutation_flags(args: argparse.Namespace) -> tuple[bool, bool]:
    return bool(getattr(args, "dry_run", False)), bool(getattr(args, "diff", False))


def require_v4(manifest: dict[str, Any]) -> None:
    if is_modular_monolith(manifest):
        return
    if is_module_process(manifest):
        raise ScaffoldError("project uses the v0.3 module/process model")
    raise ScaffoldError("project uses a legacy model; migrate it with modular v0.3 before upgrading to v0.4")


def v4_render_and_apply(
    project: Path,
    manifest: dict[str, Any],
    architecture: dict[str, Any],
    args: argparse.Namespace,
    *,
    allow_delete: bool = False,
) -> int:
    validate_architecture(architecture)
    dry_run = bool(getattr(args, "dry_run", False)) or (
        bool(getattr(args, "apply", False)) is False and allow_delete
    )
    return render_and_apply(
        project,
        manifest,
        architecture=architecture,
        dry_run=dry_run,
        diff=bool(getattr(args, "diff", False)),
        allow_delete=allow_delete,
        verify=verify_framework if not dry_run and not bool(getattr(args, "diff", False)) else None,
        force_scaffold={ARCHITECTURE_PATH},
    )


def command_init(args: argparse.Namespace) -> int:
    project = validate_name(args.project, "project")
    root = (Path(args.out).resolve() / project).resolve()
    if root.exists():
        raise ScaffoldError(f"target already exists: {root}")
    version = resolve_modular_version(args.modular_version)
    dry_run, diff = mutation_flags(args)
    if parse_semver(version) < MIN_V4_MODULAR_VERSION:
        raise ScaffoldError(f"modular-monolith scaffolds require modular v0.4.0 or newer; got {version}")
    transports = list(args.transport or ["http"])
    manifest = empty_v4_manifest(module=project, modular_version=version)
    architecture = empty_architecture(project, transports)
    result = render_and_apply(
        root,
        manifest,
        architecture=architecture,
        dry_run=dry_run,
        diff=diff,
        verify=verify_framework,
    )
    if not dry_run and not diff:
        info(f"initialized {project} with github.com/wplbyx/modular {version}")
    return result


def command_module_add(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    manifest = load_manifest(project)
    require_v4(manifest)
    architecture = load_architecture(project)
    name = validate_name(args.module, "module")
    if name in architecture["modules"]:
        raise ScaffoldError(f"module {name!r} already exists")
    dependencies = sorted(set(args.depends_on or []))
    for dependency in dependencies:
        validate_name(dependency, "module dependency")
        if dependency not in architecture["modules"]:
            raise ScaffoldError(f"module {name!r} depends on unknown module {dependency!r}")
    architecture["modules"][name] = {
        "dependencies": dependencies,
    }
    return v4_render_and_apply(project, manifest, architecture, args)


def command_module_remove(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    manifest = load_manifest(project)
    require_v4(manifest)
    architecture = load_architecture(project)
    name = validate_name(args.module, "module")
    if name not in architecture["modules"]:
        raise ScaffoldError(f"module {name!r} does not exist")
    dependents = sorted(
        module for module, definition in architecture["modules"].items()
        if name in definition.get("dependencies", [])
    )
    if dependents:
        raise ScaffoldError(f"module {name!r} is required by: {', '.join(dependents)}")
    architecture["modules"].pop(name)
    return v4_render_and_apply(project, manifest, architecture, args, allow_delete=True)


def command_module_depend(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    manifest = load_manifest(project)
    require_v4(manifest)
    architecture = load_architecture(project)
    name = validate_name(args.module, "module")
    dependency = validate_name(args.dependency, "module dependency")
    if name not in architecture["modules"] or dependency not in architecture["modules"]:
        raise ScaffoldError("both module and dependency must exist")
    if name == dependency:
        raise ScaffoldError("a module cannot depend on itself")
    dependencies = set(architecture["modules"][name].get("dependencies", []))
    if args.dependency_action == "add":
        dependencies.add(dependency)
    else:
        dependencies.discard(dependency)
    architecture["modules"][name]["dependencies"] = sorted(dependencies)
    return v4_render_and_apply(project, manifest, architecture, args)


def command_transport_add(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    manifest = load_manifest(project)
    require_v4(manifest)
    architecture = load_architecture(project)
    application = architecture["application"]
    transports = set(application.get("transports", []))
    transports.add(args.kind)
    application["transports"] = sorted(transports)
    application.setdefault("ports", {}).setdefault(args.kind, 18080 if args.kind == "http" else 19090)
    return v4_render_and_apply(project, manifest, architecture, args)


def command_transport_remove(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    manifest = load_manifest(project)
    require_v4(manifest)
    architecture = load_architecture(project)
    application = architecture["application"]
    if args.kind not in application.get("transports", []):
        raise ScaffoldError(f"transport {args.kind!r} is not enabled")
    application["transports"] = sorted(set(application.get("transports", [])) - {args.kind})
    application.get("ports", {}).pop(args.kind, None)
    return v4_render_and_apply(project, manifest, architecture, args, allow_delete=True)


def command_resource_add(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    manifest = load_manifest(project)
    require_v4(manifest)
    architecture = load_architecture(project)
    kind = str(args.kind)
    feature: dict[str, Any] = {"kind": kind}
    if kind == "db":
        if args.driver == "bun" and args.dialect != "postgres":
            raise ScaffoldError("Bun database requires --dialect postgres")
        feature["driver"] = args.driver
        if args.driver in {"bun", "gorm"}:
            feature["dialect"] = args.dialect
    architecture["application"]["resources"][kind] = feature
    return v4_render_and_apply(project, manifest, architecture, args)


def command_resource_remove(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    manifest = load_manifest(project)
    require_v4(manifest)
    architecture = load_architecture(project)
    resources = architecture["application"]["resources"]
    if args.kind not in resources:
        raise ScaffoldError(f"resource {args.kind!r} is not attached to the application")
    resources.pop(args.kind)
    return v4_render_and_apply(project, manifest, architecture, args, allow_delete=True)


def command_sync(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    manifest = load_manifest(project)
    require_v4(manifest)
    dry_run, diff = mutation_flags(args)
    architecture = load_architecture(project)
    return render_and_apply(
        project,
        manifest,
        architecture=architecture,
        dry_run=dry_run,
        diff=diff,
        verify=verify_framework,
    )


def command_prune(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    manifest = load_manifest(project)
    require_v4(manifest)
    dry_run = not args.apply or args.dry_run
    architecture = load_architecture(project)
    return render_and_apply(
        project,
        manifest,
        architecture=architecture,
        dry_run=dry_run,
        diff=args.diff,
        allow_delete=True,
        verify=verify_framework if args.apply else None,
    )


def command_project_upgrade(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    manifest = load_manifest(project)
    require_v4(manifest)
    version = resolve_modular_version(args.modular_version)
    if parse_semver(version) < MIN_V4_MODULAR_VERSION:
        raise ScaffoldError(f"v0.4 projects require modular v0.4.0 or newer; got {version}")
    manifest["project"]["modular_version"] = version
    go_mod_path = project / "go.mod"
    go_mod = go_mod_path.read_text(encoding="utf-8")
    updated_go_mod, replacements = re.subn(
        r"(github\.com/wplbyx/modular\s+)v\d+\.\d+\.\d+(?:[-+][^\s]+)?",
        rf"\g<1>{version}",
        go_mod,
        count=1,
    )
    if replacements != 1:
        raise ScaffoldError("go.mod does not contain a concrete github.com/wplbyx/modular version")
    go_mod_output = output(
        "go.mod",
        updated_go_mod,
        owner=OWNER_SCAFFOLD,
        template="project/go.mod",
        provenance={"upgrade": version},
    )

    dry_run = not args.apply or args.dry_run
    return render_and_apply(
        project,
        manifest,
        dry_run=dry_run,
        diff=args.diff,
        allow_delete=True,
        verify=verify_framework if args.apply else None,
        overrides={Path("go.mod"): go_mod_output},
        force_scaffold={Path("go.mod")},
    )


def command_doctor(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    doctor_check(project, phase=args.phase, strict=args.strict)
    placeholder_check(
        project,
        allow_contract=args.phase != "complete",
        allow_unwired=args.phase == "framework",
    )
    info("doctor passed")
    return 0


def command_verify(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    if args.phase == "framework":
        verify_framework(project)
    elif args.phase == "contract":
        verify_contract(project)
    else:
        verify_complete(project)
    info(f"{args.phase} verification passed")
    return 0


def command_coverage(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    if not testing_mode():
        run_go(project, ["test", "./...", "-coverprofile=coverage.out"])
        run_go(project, ["tool", "cover", "-func=coverage.out"])
    return 0


def command_self_check(_: argparse.Namespace) -> int:
    self_check(SKILL_DIR)
    return 0


def add_mutation_options(parser: argparse.ArgumentParser, *, project: bool = True) -> None:
    if project:
        parser.add_argument("--project-dir", default=".")
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--diff", action="store_true")


COMMAND_PATHS = [
    "init",
    "module add",
    "module depend add",
    "module depend remove",
    "module remove",
    "project upgrade",
    "transport add",
    "transport remove",
    "resource add",
    "resource remove",
    "sync",
    "doctor",
    "prune",
    "verify",
    "coverage",
    "self-check",
]


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="modular-monolith scaffold v4")
    sub = parser.add_subparsers(dest="command", required=True)

    command = sub.add_parser("init", help="create a project with repository-local scaffold tooling")
    command.add_argument("project")
    command.add_argument("--transport", action="append", choices=sorted(VALID_TRANSPORTS))
    command.add_argument("--modular-version", default=None, help="published tag; defaults to remote latest")
    command.add_argument("--out", default=".")
    add_mutation_options(command, project=False)
    command.set_defaults(func=command_init)

    project_parser = sub.add_parser("project", help="project-level tool and dependency upgrades")
    project_sub = project_parser.add_subparsers(dest="project_command", required=True)
    command = project_sub.add_parser("upgrade")
    command.add_argument("--modular-version", default=None)
    command.add_argument("--apply", action="store_true")
    add_mutation_options(command)
    command.set_defaults(func=command_project_upgrade)

    module = sub.add_parser("module", help="manage bounded-context business modules")
    module_sub = module.add_subparsers(dest="module_command", required=True)
    command = module_sub.add_parser("add")
    command.add_argument("module")
    command.add_argument("--depends-on", action="append", default=[])
    add_mutation_options(command)
    command.set_defaults(func=command_module_add)
    command = module_sub.add_parser("remove")
    command.add_argument("module")
    command.add_argument("--apply", action="store_true")
    add_mutation_options(command)
    command.set_defaults(func=command_module_remove)
    depend = module_sub.add_parser("depend")
    depend_sub = depend.add_subparsers(dest="dependency_action", required=True)
    for action in ("add", "remove"):
        command = depend_sub.add_parser(action)
        command.add_argument("module")
        command.add_argument("dependency")
        add_mutation_options(command)
        command.set_defaults(func=command_module_depend)
    transport = sub.add_parser("transport", help="change the Application transport selection")
    transport_sub = transport.add_subparsers(dest="transport_command", required=True)
    command = transport_sub.add_parser("add")
    command.add_argument("kind", choices=sorted(VALID_TRANSPORTS))
    add_mutation_options(command)
    command.set_defaults(func=command_transport_add)
    command = transport_sub.add_parser("remove")
    command.add_argument("kind", choices=sorted(VALID_TRANSPORTS))
    command.add_argument("--apply", action="store_true")
    add_mutation_options(command)
    command.set_defaults(func=command_transport_remove)

    resource = sub.add_parser("resource", help="manage library-owned infrastructure resources")
    resource_sub = resource.add_subparsers(dest="resource_command", required=True)
    command = resource_sub.add_parser("add")
    command.add_argument("kind", choices=sorted(VALID_RESOURCES))
    command.add_argument("--driver", default="bun", choices=sorted(VALID_DB_DRIVERS))
    command.add_argument("--dialect", default="postgres", choices=sorted(VALID_GORM_DIALECTS))
    add_mutation_options(command)
    command.set_defaults(func=command_resource_add)
    command = resource_sub.add_parser("remove")
    command.add_argument("kind", choices=sorted(VALID_RESOURCES))
    command.add_argument("--apply", action="store_true")
    add_mutation_options(command)
    command.set_defaults(func=command_resource_remove)

    command = sub.add_parser("sync", help="replay managed files from manifest provenance")
    add_mutation_options(command)
    command.set_defaults(func=command_sync)

    command = sub.add_parser("prune", help="remove obsolete unchanged managed files")
    command.add_argument("--apply", action="store_true")
    add_mutation_options(command)
    command.set_defaults(func=command_prune)

    command = sub.add_parser("doctor", help="read-only structure and ownership checks")
    command.add_argument("--phase", choices=["framework", "contract", "complete"], default="framework")
    command.add_argument("--strict", action="store_true")
    command.add_argument("--project-dir", default=".")
    command.set_defaults(func=command_doctor)

    command = sub.add_parser("verify", help="run a phase completion gate")
    command.add_argument("--phase", choices=["framework", "contract", "complete"], required=True)
    command.add_argument("--project-dir", default=".")
    command.set_defaults(func=command_verify)

    command = sub.add_parser("coverage", help="generate a coverage report without a numeric gate")
    command.add_argument("--project-dir", default=".")
    command.set_defaults(func=command_coverage)

    command = sub.add_parser("self-check", help="validate the installed skill/runtime package")
    command.set_defaults(func=command_self_check)
    return parser


def main(argv: list[str] | None = None) -> int:
    try:
        args = build_parser().parse_args(argv)
        return int(args.func(args))
    except ScaffoldError as error:
        print(f"error: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
