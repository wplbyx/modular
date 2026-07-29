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


TOOL_VERSION = "2.1.1"
MANIFEST_SCHEMA = 1
MIN_MODULAR_VERSION = (0, 2, 0)
DEFAULT_GO_VERSION = "1.26.0"
VALID_TOPOLOGIES = {"single", "service"}
VALID_TRANSPORTS = {"http", "grpc"}
VALID_RESOURCES = {"db", "eventbus", "redis", "storage", "telemetry"}
VALID_DB_DRIVERS = {"bun", "gorm", "mongo"}
VALID_GORM_DIALECTS = {"postgres", "mysql", "sqlite", "clickhouse"}
OWNER_MANAGED = "managed"
OWNER_SCAFFOLD = "scaffold-once"
MANIFEST_PATH = Path(".modular/manifest.json")
PROFILE_PATH = Path(".modular/profile.toml")
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
) -> subprocess.CompletedProcess[str]:
    completed = subprocess.run(
        command,
        cwd=cwd,
        text=True,
        capture_output=capture,
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
    if parse_semver(version) < MIN_MODULAR_VERSION:
        minimum = "v" + ".".join(str(part) for part in MIN_MODULAR_VERSION)
        raise ScaffoldError(f"modular {version} is unsupported; v2 scaffolds require {minimum} or newer")
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


def project_name(project: Path) -> str:
    return read_module(project).split("/")[-1]


def empty_manifest(*, module: str, topology: str, modular_version: str) -> dict[str, Any]:
    return {
        "schema": MANIFEST_SCHEMA,
        "tool_version": TOOL_VERSION,
        "project": {
            "module": module,
            "name": module.split("/")[-1],
            "topology": topology,
            "modular_version": modular_version,
        },
        "features": {},
        "files": {},
    }


def load_manifest(project: Path) -> dict[str, Any]:
    path = project / MANIFEST_PATH
    if not path.is_file():
        raise ScaffoldError(f"missing {MANIFEST_PATH}; run the v2 project migration first")
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


def services_from(manifest: dict[str, Any]) -> dict[str, dict[str, Any]]:
    services: dict[str, dict[str, Any]] = {}
    for key, feature in manifest["features"].items():
        if key.startswith("service:") and isinstance(feature, dict):
            services[key.split(":", 1)[1]] = feature
    return dict(sorted(services.items()))


def resources_for(manifest: dict[str, Any], svc: str) -> dict[str, dict[str, Any]]:
    resources: dict[str, dict[str, Any]] = {}
    prefix = f"resource:{svc}:"
    for key, feature in manifest["features"].items():
        if key.startswith(prefix) and isinstance(feature, dict):
            resources[key[len(prefix):]] = feature
    return dict(sorted(resources.items()))


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
    completed = subprocess.run([gofmt], input=content, text=True, capture_output=True)
    if completed.returncode != 0:
        raise ScaffoldError(f"gofmt rejected generated {path}: {completed.stderr.strip()}")
    return completed.stdout


def render_svc_generated_config(
    svc: str,
    feature: dict[str, Any],
    resources: dict[str, dict[str, Any]],
    *,
    include_logging: bool,
) -> str:
    fields = ["\tApplication configitem.Application `mapstructure:\"Application\"`"]
    if include_logging:
        fields.append("\tLogging configitem.Logging `mapstructure:\"Logging\"`")
    transports = set(feature.get("transports", []))
    if "http" in transports:
        fields.append("\tHTTP configitem.HTTP `mapstructure:\"HTTP\"`")
    if "grpc" in transports:
        fields.append("\tGRPC configitem.GRPC `mapstructure:\"GRPC\"`")
    db = resources.get("db")
    if db and db.get("driver") == "mongo":
        fields.append("\tMongo configitem.Mongo `mapstructure:\"Mongo\"`")
    elif db:
        fields.append("\tDatabase configitem.Database `mapstructure:\"Database\"`")
    if "redis" in resources:
        fields.append("\tRedis configitem.Redis `mapstructure:\"Redis\"`")
    if "storage" in resources:
        fields.append("\tStorage configitem.Storage `mapstructure:\"Storage\"`")
    if "telemetry" in resources:
        fields.append("\tTelemetry configitem.Telemetry `mapstructure:\"Telemetry\"`")
    if "eventbus" in resources:
        fields.append("\tEventBus configitem.EventBus `mapstructure:\"EventBus\"`")
    return (
        "// Code generated by modular scaffold. DO NOT EDIT.\n\n"
        "package config\n\n"
        'import "github.com/wplbyx/modular/packages/config/configitem"\n\n'
        "type Generated struct {\n"
        + "\n".join(fields)
        + "\n}\n"
    )


def render_svc_config_scaffold() -> str:
    return (
        "// Code scaffolded by modular. This file is user-maintained.\n\n"
        "package config\n\n"
        'import modularconfig "github.com/wplbyx/modular/packages/config"\n\n'
        "type Config struct {\n"
        "\tGenerated `mapstructure:\",squash\"`\n"
        "}\n\n"
        "func (Config) Flags(prefix string) []modularconfig.FlagSpec {\n"
        "\treturn modularconfig.GetConfigFlagSpecsWithPrefix[Generated](prefix)\n"
        "}\n"
    )


def render_svc_yaml(
    svc: str,
    feature: dict[str, Any],
    resources: dict[str, dict[str, Any]],
    *,
    include_logging: bool,
) -> str:
    lines = [
        "# Code generated by modular scaffold. DO NOT EDIT.",
        "Application:",
        f"  Name: {svc}",
        "  Mode: dev",
        "  Version: v0.1.0",
        "  ShutdownTimeout: 10s",
    ]
    if include_logging:
        lines.extend([
            "",
            "Logging:",
            "  Level: info",
            "  Output: [console]",
            "  Async:",
            "    Enabled: true",
            "    Capacity: 8192",
            "    ErrorTimeout: 50ms",
            "    FlushTimeout: 5s",
        ])
    ports = feature.get("ports", {})
    if "http" in feature.get("transports", []):
        lines.extend(["", "HTTP:", '  Host: "0.0.0.0"', f"  Port: {ports.get('http', 18080)}"])
    if "grpc" in feature.get("transports", []):
        lines.extend(["", "GRPC:", '  Host: "0.0.0.0"', f"  Port: {ports.get('grpc', 19090)}"])
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
        lines.extend(["", "EventBus:", f"  Name: {svc}-events", "  Capacity: 8192"])
    return "\n".join(lines) + "\n"


def render_process_config(module: str, process: str, services: dict[str, dict[str, Any]]) -> str:
    imports = [f'\t{lower_camel(svc)}config "{module}/config/{svc}"' for svc in services]
    fields = [f'\t{pascal_case(svc)} {lower_camel(svc)}config.Config `mapstructure:"{pascal_case(svc)}"`' for svc in services]
    import_block = "\n".join(imports)
    if import_block:
        import_block = "\n\n" + import_block
    field_block = "\n".join(fields)
    if field_block:
        field_block = "\n" + field_block
    return (
        "// Code generated by modular scaffold. DO NOT EDIT.\n\n"
        "package config\n\n"
        "import (\n"
        '\t"github.com/wplbyx/modular/packages/config/configitem"'
        + import_block
        + "\n)\n\n"
        "type Config struct {\n"
        '\tApplication configitem.Application `mapstructure:"Application"`\n'
        '\tLogging configitem.Logging `mapstructure:"Logging"`'
        + field_block
        + "\n}\n"
    )


def render_process_yaml(process: str, services: dict[str, dict[str, Any]], manifest: dict[str, Any]) -> str:
    lines = [
        "# Code generated by modular scaffold. DO NOT EDIT.",
        "Application:",
        f"  Name: {process}",
        "  Mode: dev",
        "  Version: v0.1.0",
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
    for svc, feature in services.items():
        body = render_svc_yaml(
            svc,
            feature,
            resources_for(manifest, svc),
            include_logging=False,
        ).splitlines()[1:]
        lines.extend(["", f"{pascal_case(svc)}:", *["  " + line if line else "" for line in body]])
    return "\n".join(lines) + "\n"


def render_main_scaffold() -> str:
    return "// Code generated by modular scaffold. DO NOT EDIT.\n\npackage main\n\nfunc main() { execute() }\n"


def render_cmd_policy_scaffold() -> str:
    return (
        "// Code scaffolded by modular. This file is user-maintained.\n\n"
        "package main\n\n"
        "import (\n"
        '\t"context"\n'
        '\t"fmt"\n'
        '\t"strings"\n\n'
        '\t"github.com/wplbyx/modular/packages/config/configitem"\n'
        '\tmodularlog "github.com/wplbyx/modular/packages/log"\n'
        '\tmodulartransport "github.com/wplbyx/modular/packages/transport"\n'
        ")\n\n"
        "func newLoggerManager(ctx context.Context, cfg *configitem.Logging) (*modularlog.LoggerManager, error) {\n"
        "\toutputs := cfg.Output\n"
        "\tif len(outputs) == 0 {\n"
        '\t\toutputs = []string{"console"}\n'
        "\t}\n"
        "\toptions := make([]modularlog.LoggerManagerOption, 0, len(outputs))\n"
        "\tbootstrapSinks := 0\n"
        "\tfor _, output := range outputs {\n"
        "\t\tswitch strings.ToLower(output) {\n"
        '\t\tcase "console":\n'
        "\t\t\toptions = append(options, modularlog.WithOutputConsole())\n"
        "\t\t\tbootstrapSinks++\n"
        '\t\tcase "file":\n'
        "\t\t\toptions = append(options, modularlog.WithOutputFiles(ctx))\n"
        "\t\t\tbootstrapSinks++\n"
        '\t\tcase "telemetry":\n'
        "\t\t\t// Telemetry attaches its sink during Resource.Setup.\n"
        "\t\tdefault:\n"
        '\t\t\treturn nil, fmt.Errorf("unsupported logging output %q", output)\n'
        "\t\t}\n"
        "\t}\n"
        "\tif bootstrapSinks == 0 {\n"
        '\t\treturn nil, fmt.Errorf("logging requires a console or file bootstrap output")\n'
        "\t}\n"
        "\treturn modularlog.NewLoggerManager(cfg, options...)\n"
        "}\n\n"
        "func newTransportPolicy(process string, logger modularlog.Logger) *modulartransport.Policy {\n"
        "\treturn modulartransport.NewPolicy(process, modulartransport.WithLogger(logger))\n"
        "}\n"
    )


def render_business_scaffold() -> str:
    return (
        "// Code scaffolded by modular. This file is user-maintained.\n\n"
        "package wiring\n\n"
        "func WireBusiness(process string, hooks *BusinessHooks, providers Providers) error {\n"
        "\t// modular:business-unwired - remove this marker after contracts are registered.\n"
        "\t_ = process\n"
        "\t_ = hooks\n"
        "\t_ = providers\n"
        "\treturn nil\n"
        "}\n"
    )


def provider_specs(manifest: dict[str, Any], services: dict[str, dict[str, Any]]) -> list[dict[str, str]]:
    specs: list[dict[str, str]] = []
    for svc in services:
        prefix = pascal_case(svc)
        variable = lower_camel(svc)
        resources = resources_for(manifest, svc)
        db = resources.get("db")
        if db:
            driver = str(db.get("driver", "bun"))
            if driver == "bun":
                specs.append({
                    "field": prefix + "DB",
                    "variable": variable + "DBResource",
                    "type": "*bunresource.Resource",
                    "type_import": 'bunresource "github.com/wplbyx/modular/packages/infra/database/bun"',
                    "ctor_import": 'bunresource "github.com/wplbyx/modular/packages/infra/database/bun"',
                    "ctor": f"bunresource.NewResource(&{variable}Cfg.Database)",
                    "svc": svc,
                })
            elif driver == "gorm":
                dialect = str(db.get("dialect", "postgres"))
                specs.append({
                    "field": prefix + "DB",
                    "variable": variable + "DBResource",
                    "type": "*modulargorm.Resource",
                    "type_import": 'modulargorm "github.com/wplbyx/modular/packages/infra/database/gorm"',
                    "ctor_import": f'gormresource "github.com/wplbyx/modular/packages/infra/database/gorm/{dialect}"',
                    "ctor": f"gormresource.NewResource(&{variable}Cfg.Database)",
                    "svc": svc,
                })
            else:
                specs.append({
                    "field": prefix + "DB",
                    "variable": variable + "DBResource",
                    "type": "*mongoresource.Resource",
                    "type_import": 'mongoresource "github.com/wplbyx/modular/packages/infra/database/mongo"',
                    "ctor_import": 'mongoresource "github.com/wplbyx/modular/packages/infra/database/mongo"',
                    "ctor": f"mongoresource.NewResource(&{variable}Cfg.Mongo)",
                    "svc": svc,
                })
        if "redis" in resources:
            specs.append({
                "field": prefix + "Redis",
                "variable": variable + "RedisResource",
                "type": "*redisresource.Resource",
                "type_import": 'redisresource "github.com/wplbyx/modular/packages/infra/cache/redis"',
                "ctor_import": 'redisresource "github.com/wplbyx/modular/packages/infra/cache/redis"',
                "ctor": f"redisresource.NewResource(&{variable}Cfg.Redis)",
                "svc": svc,
            })
        if "storage" in resources:
            specs.append({
                "field": prefix + "Storage",
                "variable": variable + "StorageResource",
                "type": "*storageresource.Resource",
                "type_import": 'storageresource "github.com/wplbyx/modular/packages/infra/storage/resource"',
                "ctor_import": 'storageresource "github.com/wplbyx/modular/packages/infra/storage/resource"',
                "ctor": f"storageresource.New(&{variable}Cfg.Storage)",
                "svc": svc,
            })
        if "telemetry" in resources:
            specs.append({
                "field": prefix + "Telemetry",
                "variable": variable + "TelemetryResource",
                "type": "*telemetry.OpenTelemetry",
                "type_import": '"github.com/wplbyx/modular/packages/telemetry"',
                "ctor_import": '"github.com/wplbyx/modular/packages/telemetry"',
                "ctor": "",
                "svc": svc,
            })
        if "eventbus" in resources:
            specs.append({
                "field": prefix + "EventBus",
                "variable": variable + "EventBusResource",
                "type": "*eventbus.Bus",
                "type_import": '"github.com/wplbyx/modular/packages/eventbus"',
                "ctor_import": '"github.com/wplbyx/modular/packages/eventbus"',
                "ctor": f"eventbus.New({variable}Cfg.EventBus, loggerManager.Logger())",
                "svc": svc,
            })
    return specs


def render_wiring_framework(manifest: dict[str, Any], services: dict[str, dict[str, Any]]) -> str:
    providers = provider_specs(manifest, services)
    transports = {
        transport
        for feature in services.values()
        for transport in feature.get("transports", [])
    }
    imports: list[str] = []
    if any("core." in spec["type"] for spec in providers):
        imports.append('"github.com/wplbyx/modular/packages/core"')
    if "http" in transports:
        imports.append('httpserver "github.com/wplbyx/modular/packages/transport/server/http"')
    if "grpc" in transports:
        imports.append('rpcserver "github.com/wplbyx/modular/packages/transport/server/rpc"')
    imports.extend(spec["type_import"] for spec in providers)
    lines = [
        "// Code generated by modular scaffold. DO NOT EDIT.",
        "",
        "package wiring",
        "",
    ]
    if imports:
        lines.extend(["import (", *["\t" + item for item in unique_imports(imports)], ")", ""])
    lines.append("type BusinessHooks struct {")
    if "http" in transports:
        lines.append("\thttp map[string][]httpserver.RegisterRouteFunc")
    if "grpc" in transports:
        lines.append("\tgrpc map[string][]rpcserver.RegisterFunc")
    lines.extend([
        "}",
        "",
        "func NewBusinessHooks() *BusinessHooks {",
        "\treturn &BusinessHooks{",
    ])
    if "http" in transports:
        lines.append("\t\thttp: make(map[string][]httpserver.RegisterRouteFunc),")
    if "grpc" in transports:
        lines.append("\t\tgrpc: make(map[string][]rpcserver.RegisterFunc),")
    lines.extend(["\t}", "}", ""])
    if "http" in transports:
        lines.extend([
            "func (h *BusinessHooks) AddHTTP(svc string, routes ...httpserver.RegisterRouteFunc) {",
            "\th.http[svc] = append(h.http[svc], routes...)",
            "}",
            "",
            "func (h *BusinessHooks) HTTP(svc string) []httpserver.RegisterRouteFunc {",
            "\treturn append([]httpserver.RegisterRouteFunc(nil), h.http[svc]...)",
            "}",
            "",
        ])
    if "grpc" in transports:
        lines.extend([
            "func (h *BusinessHooks) AddGRPC(svc string, registers ...rpcserver.RegisterFunc) {",
            "\th.grpc[svc] = append(h.grpc[svc], registers...)",
            "}",
            "",
            "func (h *BusinessHooks) RegisterGRPC(svc string) rpcserver.RegisterFunc {",
            "\treturn rpcserver.ChainRegister(h.grpc[svc]...)",
            "}",
            "",
        ])
    lines.append("type Providers struct {")
    lines.extend(f"\t{spec['field']} {spec['type']}" for spec in providers)
    lines.extend(["}", ""])
    return "\n".join(lines)


def render_framework(
    manifest: dict[str, Any],
    process: str,
    services: dict[str, dict[str, Any]],
    *,
    aggregate: bool,
) -> str:
    module = str(manifest["project"]["module"])
    config_name = process
    providers = provider_specs(manifest, services)
    standard_imports = [
        '"context"',
        '"errors"',
        '"fmt"',
        '"os"',
        '"os/signal"',
        '"syscall"',
    ]
    modular_imports = [
        '"github.com/wplbyx/modular/packages/app"',
        'modularconfig "github.com/wplbyx/modular/packages/config"',
        '"github.com/wplbyx/modular/packages/core"',
        'modularlog "github.com/wplbyx/modular/packages/log"',
    ]
    project_imports = [
        f'projectconfig "{module}/config/{config_name}"',
        f'wiring "{module}/internal/platform/wiring"',
    ]
    transports = {
        transport
        for feature in services.values()
        for transport in feature.get("transports", [])
    }
    if "http" in transports:
        modular_imports.append('httpserver "github.com/wplbyx/modular/packages/transport/server/http"')
    if "grpc" in transports:
        modular_imports.append('rpcserver "github.com/wplbyx/modular/packages/transport/server/rpc"')
    modular_imports.extend(spec["ctor_import"] for spec in providers)
    modular_imports = unique_imports(modular_imports)

    lines = [
        "// Code generated by modular scaffold. DO NOT EDIT.",
        "",
        "package main",
        "",
        "import (",
        *["\t" + item for item in standard_imports],
        "",
        *["\t" + item for item in modular_imports],
        "",
        *["\t" + item for item in project_imports],
        ")",
        "",
    ]
    lines.extend([
        "func execute() {",
        "\tctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)",
        "\tdefer cancel()",
        "",
        "\tcommand := modularconfig.NewRootCommand[projectconfig.Config](modularconfig.CommandOptions[projectconfig.Config]{",
        f'\t\tName: "{process}",',
        f'\t\tShort: "{process} service",',
        f'\t\tDefaultFile: "./config/{config_name}/config.yaml",',
        f'\t\tEnvPrefix: "{env_prefix(process)}",',
        "\t\tRun: run,",
        "\t})",
        "\tcommand.SetContext(ctx)",
        "\tcommand.SilenceErrors = true",
        "\tcommand.SilenceUsage = true",
        "\tif err := command.Execute(); err != nil {",
        '\t\tfmt.Fprintf(os.Stderr, "application exited: %v\\n", err)',
        "\t\tos.Exit(1)",
        "\t}",
        "}",
        "",
        "func run(ctx context.Context, cfg *projectconfig.Config) (runErr error) {",
        "\t// Configuration is loaded by config.NewRootCommand before this callback.",
        "\tloggerManager, err := newLoggerManager(ctx, &cfg.Logging)",
        "\tif err != nil {",
        '\t\treturn fmt.Errorf("create logger: %w", err)',
        "\t}",
        "\trestoreLogger := modularlog.SetDefault(loggerManager.Logger())",
        "\tdefer restoreLogger()",
        "\tdefer func() {",
        "\t\trunErr = errors.Join(runErr, loggerManager.Close(context.WithoutCancel(ctx)))",
        "\t}()",
        "\tpolicy := newTransportPolicy(cfg.Application.Name, loggerManager.Logger())",
        "",
        "\tendpoints := make([]core.Endpoint, 0)",
        "\tresources := make([]core.Resource, 0)",
        "\ttransports := make([]core.Transport, 0)",
        "\tproviders := wiring.Providers{}",
        "",
    ])

    for svc in services:
        cfg_var = lower_camel(svc) + "Cfg"
        cfg_expr = f"&cfg.{pascal_case(svc)}" if aggregate else "cfg"
        lines.extend([f"\t{cfg_var} := {cfg_expr}", ""])
        for spec in [item for item in providers if item["svc"] == svc]:
            variable = spec["variable"]
            if spec["field"].endswith("Telemetry"):
                lines.extend([
                    f"\t{variable}, err := telemetry.NewOpenTelemetry(ctx, {cfg_var}.Application.Name, {cfg_var}.Application.Version, &{cfg_var}.Telemetry, telemetry.WithLoggerManager(loggerManager))",
                    "\tif err != nil {",
                    f'\t\treturn fmt.Errorf("create {svc} telemetry: %w", err)',
                    "\t}",
                ])
            elif spec["field"].endswith("EventBus"):
                lines.extend([
                    f"\t{variable}, err := {spec['ctor']}",
                    "\tif err != nil {",
                    f'\t\treturn fmt.Errorf("create {svc} event bus: %w", err)',
                    "\t}",
                ])
            else:
                lines.append(f"\t{variable} := {spec['ctor']}")
            lines.extend([
                f"\tproviders.{spec['field']} = {variable}",
                f"\tresources = append(resources, {variable})",
            ])
        if any(item["svc"] == svc for item in providers):
            lines.append("")

    lines.extend([
        "\thooks := wiring.NewBusinessHooks()",
        f'\tif err := wiring.WireBusiness("{process}", hooks, providers); err != nil {{',
        '\t\treturn fmt.Errorf("wire business contracts: %w", err)',
        "\t}",
        "",
    ])

    for svc, feature in services.items():
        cfg_var = lower_camel(svc) + "Cfg"
        if "http" in feature.get("transports", []):
            server = lower_camel(svc) + "HTTPServer"
            lines.extend([
                f"\t{server}, err := httpserver.NewServer(&{cfg_var}.HTTP, httpserver.WithPolicy(policy))",
                "\tif err != nil {",
                f'\t\treturn fmt.Errorf("create {svc} http server: %w", err)',
                "\t}",
                f"\t{server}.RegisterRoute(hooks.HTTP(\"{svc}\")...)",
                f"\tendpoints = append(endpoints, {server})",
                f"\ttransports = append(transports, {server}.Transport())",
                "",
            ])
        if "grpc" in feature.get("transports", []):
            server = lower_camel(svc) + "GRPCServer"
            lines.extend([
                f"\t{server}, err := rpcserver.NewServer(&{cfg_var}.GRPC, hooks.RegisterGRPC(\"{svc}\"), rpcserver.WithPolicy(policy))",
                "\tif err != nil {",
                f'\t\treturn fmt.Errorf("create {svc} grpc server: %w", err)',
                "\t}",
                f"\tendpoints = append(endpoints, {server})",
                f"\ttransports = append(transports, {server}.Transport())",
                "",
            ])

    lines.extend([
        "\tnode := core.NewServiceNode(cfg.Application.Name, cfg.Application.Version, transports...)",
        "\toptions := []app.Option{app.WithServiceNode(node)}",
        "\tfor _, endpoint := range endpoints {",
        "\t\toptions = append(options, app.WithEndpoint(endpoint))",
        "\t}",
        "\tfor _, resource := range resources {",
        "\t\toptions = append(options, app.WithResource(resource))",
        "\t}",
        "\tapplication, err := app.NewApplication(ctx, &cfg.Application, loggerManager.Logger(), options...)",
        "\tif err != nil {",
        '\t\treturn fmt.Errorf("create application: %w", err)',
        "\t}",
        "\treturn application.Run()",
        "}",
        "",
    ])
    return "\n".join(lines)


def render_outputs(project: Path, manifest: dict[str, Any]) -> dict[Path, OutputFile]:
    meta = manifest["project"]
    module = str(meta["module"])
    name = str(meta["name"])
    topology = str(meta["topology"])
    services = services_from(manifest)
    values = {
        "PROJECT": module,
        "GO_VERSION": DEFAULT_GO_VERSION,
        "MODULAR_VERSION": str(meta["modular_version"]),
    }
    outputs: list[OutputFile] = [
        output("go.mod", render_template("project/go.mod.tmpl", values), owner=OWNER_SCAFFOLD, template="project/go.mod"),
        output("buf.yaml", template_text("project/buf.yaml.tmpl"), owner=OWNER_SCAFFOLD, template="project/buf.yaml"),
        output("buf.gen.yaml", template_text("project/buf.gen.yaml.tmpl"), owner=OWNER_SCAFFOLD, template="project/buf.gen.yaml"),
        output("Makefile", template_text("project/Makefile.tmpl"), owner=OWNER_SCAFFOLD, template="project/Makefile"),
        output(".gitignore", template_text("project/gitignore.tmpl"), owner=OWNER_SCAFFOLD, template="project/gitignore"),
        output(PROFILE_PATH, template_text("project/profile.toml.tmpl"), owner=OWNER_SCAFFOLD, template="project/profile"),
        output(".modular/make/modular.mk", template_text("project/modular.mk.tmpl"), template="project/modular.mk"),
        output("proto/.gitkeep", "", template="directory/proto"),
        output("common/.gitkeep", "", template="directory/common"),
        output("internal/.gitkeep", "", template="directory/internal"),
    ]
    outputs.extend(runtime_outputs())

    outputs.extend([
        output(
            "internal/platform/wiring/framework.gen.go",
            render_wiring_framework(manifest, services),
            template="wiring/framework",
        ),
        output(
            "internal/platform/wiring/business.go",
            render_business_scaffold(),
            owner=OWNER_SCAFFOLD,
            template="wiring/business",
        ),
    ])

    for svc, feature in services.items():
        resources = resources_for(manifest, svc)
        provenance = {"feature": f"service:{svc}"}
        outputs.extend([
            output(
                f"config/{svc}/config.gen.go",
                render_svc_generated_config(
                    svc,
                    feature,
                    resources,
                    include_logging=topology == "service",
                ),
                template="config/svc-generated",
                provenance=provenance,
            ),
            output(
                f"config/{svc}/config.go",
                render_svc_config_scaffold(),
                owner=OWNER_SCAFFOLD,
                template="config/svc-extension",
                provenance=provenance,
            ),
            output(
                f"config/{svc}/config.yaml",
                render_svc_yaml(
                    svc,
                    feature,
                    resources,
                    include_logging=topology == "service",
                ),
                template="config/svc-yaml",
                provenance=provenance,
            ),
        ])

    if topology == "single":
        process = name
        outputs.extend([
            output(
                f"config/{process}/config.gen.go",
                render_process_config(module, process, services),
                template="config/process-generated",
            ),
            output(
                f"config/{process}/config.yaml",
                render_process_yaml(process, services, manifest),
                template="config/process-yaml",
            ),
            output(f"cmd/{process}/main.go", render_main_scaffold(), template="cmd/main"),
            output(
                f"cmd/{process}/policy.go",
                render_cmd_policy_scaffold(),
                owner=OWNER_SCAFFOLD,
                template="cmd/policy",
            ),
            output(
                f"cmd/{process}/framework.gen.go",
                render_framework(manifest, process, services, aggregate=True),
                template="cmd/framework",
            ),
        ])
    else:
        for svc, feature in services.items():
            one = {svc: feature}
            outputs.extend([
                output(f"cmd/{svc}/main.go", render_main_scaffold(), template="cmd/main"),
                output(
                    f"cmd/{svc}/policy.go",
                    render_cmd_policy_scaffold(),
                    owner=OWNER_SCAFFOLD,
                    template="cmd/policy",
                ),
                output(
                    f"cmd/{svc}/framework.gen.go",
                    render_framework(manifest, svc, one, aggregate=False),
                    template="cmd/framework",
                ),
            ])
    formatted = [
        OutputFile(item.path, format_go_content(item.content, item.path), item.owner, item.template, item.provenance)
        for item in outputs
    ]
    return {item.path: item for item in formatted}


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
) -> tuple[list[Change], dict[str, Any]]:
    changes: list[Change] = []
    updated = copy.deepcopy(manifest)
    records: dict[str, Any] = updated.setdefault("files", {})
    old_records = copy.deepcopy(records)
    force_scaffold = force_scaffold or set()

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
        if key in desired_keys or record.get("owner") != OWNER_MANAGED:
            continue
        target = project / Path(key)
        if not allow_delete:
            continue
        if target.is_file():
            current = target.read_text(encoding="utf-8")
            if sha256_text(current) != record.get("sha256"):
                raise ScaffoldError(f"refusing to delete modified managed file: {key}")
            changes.append(Change(Path(key), "delete", current, None, None))
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
    dry_run: bool,
    diff: bool,
    allow_delete: bool = False,
    verify: Callable[[Path], None] | None = None,
    overrides: dict[Path, OutputFile] | None = None,
    force_scaffold: set[Path] | None = None,
) -> int:
    desired = render_outputs(project, manifest)
    if overrides:
        desired.update(overrides)
    changes, updated = build_changes(
        project,
        manifest,
        desired,
        allow_delete=allow_delete,
        force_scaffold=force_scaffold,
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
    ignored = {".git", ".modular", "common"}
    for path in project.rglob("*.go"):
        if any(part in ignored for part in path.parts):
            continue
        yield path


def placeholder_check(project: Path, *, allow_contract: bool, allow_unwired: bool) -> None:
    failures: list[str] = []
    for path in project.rglob("*"):
        if not path.is_file() or ".git" in path.parts or ".modular" in path.parts:
            continue
        if path.suffix not in {".go", ".proto", ".yaml", ".toml", ".md"}:
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
    for required in ["go.mod", "buf.yaml", "buf.gen.yaml", "Makefile", ".modular/tool/modular.py"]:
        if not (project / required).exists():
            errors.append(f"missing {required}")
    topology = manifest["project"].get("topology")
    services = services_from(manifest)
    if topology not in VALID_TOPOLOGIES:
        errors.append(f"invalid topology in manifest: {topology}")
    for svc, feature in services.items():
        if not (project / f"config/{svc}/config.gen.go").is_file():
            errors.append(f"missing generated config for svc {svc}")
        if not (project / f"config/{svc}/config.yaml").is_file():
            errors.append(f"missing config/{svc}/config.yaml")
        transports = set(feature.get("transports", []))
        if not transports.issubset(VALID_TRANSPORTS) or not transports:
            errors.append(f"svc {svc} has invalid or empty transport selection")
    if topology == "single":
        process = str(manifest["project"]["name"])
        main = project / f"cmd/{process}/framework.gen.go"
        policy = project / f"cmd/{process}/policy.go"
        if not main.is_file():
            errors.append(f"missing cmd/{process}/framework.gen.go")
        if not policy.is_file():
            errors.append(f"missing cmd/{process}/policy.go")
        if not (project / f"config/{process}/config.gen.go").is_file():
            errors.append(f"missing config/{process}/config.gen.go")
        if main.is_file():
            check_bootstrap_contract(main, errors)
        process_config = project / f"config/{process}/config.gen.go"
        if process_config.is_file() and "configitem.Logging" not in process_config.read_text(encoding="utf-8"):
            errors.append(f"config/{process}/config.gen.go does not declare process Logging")
    else:
        for svc in services:
            main = project / f"cmd/{svc}/framework.gen.go"
            policy = project / f"cmd/{svc}/policy.go"
            if not main.is_file():
                errors.append(f"missing cmd/{svc}/framework.gen.go")
            else:
                check_bootstrap_contract(main, errors)
            if not policy.is_file():
                errors.append(f"missing cmd/{svc}/policy.go")
            service_config = project / f"config/{svc}/config.gen.go"
            if service_config.is_file() and "configitem.Logging" not in service_config.read_text(encoding="utf-8"):
                errors.append(f"config/{svc}/config.gen.go does not declare process Logging")

    for path in (project / "common").rglob("*.go") if (project / "common").exists() else []:
        if not path.name.endswith((".pb.go", "_grpc.pb.go")):
            errors.append(f"common contains hand-written Go file: {path.relative_to(project)}")
    for path in all_go_files(project):
        relative = path.relative_to(project)
        text = path.read_text(encoding="utf-8")
        parts = relative.parts
        if len(parts) >= 3 and parts[0] == "internal":
            own = parts[1]
            for svc in services:
                if svc != own and f'"{module}/internal/{svc}/' in text:
                    errors.append(f"{relative} imports another svc internal package: {svc}")
        for symbol in ("log.GetLogger(", "log.Infof(", "log.Warnf(", "log.Errorf(", "log.Debugf("):
            if symbol in text:
                errors.append(f"{relative} uses removed logger API {symbol[:-1]}")
    if phase == "complete":
        business_files = [path for path in all_go_files(project) if any(path.match(pattern) for pattern in profile_business_globs(project))]
        packages: set[Path] = {path.parent for path in business_files if not path.name.endswith("_test.go")}
        for package in packages:
            if not list(package.glob("*_test.go")):
                errors.append(f"business package has no tests: {package.relative_to(project)}")
    if strict:
        errors.extend(warnings)
        warnings = []
    for warning in warnings:
        warn(warning)
    if errors:
        raise ScaffoldError("doctor failed:\n" + "\n".join(f"- {item}" for item in errors))
    return warnings


def check_bootstrap_contract(path: Path, errors: list[str]) -> None:
    content = path.read_text(encoding="utf-8")
    required = [
        "config.NewRootCommand",
        "newLoggerManager(ctx, &cfg.Logging)",
        "modularlog.SetDefault(loggerManager.Logger())",
        "newTransportPolicy(cfg.Application.Name, loggerManager.Logger())",
        "app.NewApplication(ctx, &cfg.Application, loggerManager.Logger(), options...)",
    ]
    for fragment in required:
        if fragment not in content:
            errors.append(f"{path.relative_to(path.parents[2])} is missing bootstrap contract: {fragment}")
    positions = [content.find(fragment) for fragment in required[1:4]]
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


def snapshot_common(project: Path) -> tuple[Path, bool]:
    temporary = Path(tempfile.mkdtemp(prefix="modular-common-backup-"))
    common = project / "common"
    existed = common.exists()
    if existed:
        shutil.copytree(common, temporary / "common")
    return temporary, existed


def restore_common(project: Path, temporary: Path, existed: bool) -> None:
    common = project / "common"
    if common.exists():
        shutil.rmtree(common)
    if existed:
        shutil.copytree(temporary / "common", common)
    shutil.rmtree(temporary, ignore_errors=True)


def verify_contract(project: Path) -> None:
    temporary, existed = snapshot_common(project)
    try:
        if not testing_mode():
            buf = shutil.which("buf")
            if buf is None:
                raise ScaffoldError("`buf` is required for contract verification")
            run_command([buf, "lint"], cwd=project)
            run_command([buf, "generate"], cwd=project)
        doctor_check(project, phase="contract", strict=True)
        placeholder_check(project, allow_contract=True, allow_unwired=False)
        if not testing_mode():
            run_go(project, ["build", "./..."])
    except BaseException:
        restore_common(project, temporary, existed)
        raise
    shutil.rmtree(temporary, ignore_errors=True)


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


def resolve_service(project: Path, requested: str | None) -> str:
    if requested:
        svc = validate_name(requested, "svc")
        if f"service:{svc}" not in load_manifest(project)["features"]:
            raise ScaffoldError(f"svc {svc!r} does not exist")
        return svc
    services = services_from(load_manifest(project))
    if len(services) == 1:
        return next(iter(services))
    if not services:
        raise ScaffoldError("no svc exists; run service add <svc> first")
    raise ScaffoldError("multiple svc modules exist; pass --svc")


def mutation_flags(args: argparse.Namespace) -> tuple[bool, bool]:
    return bool(getattr(args, "dry_run", False)), bool(getattr(args, "diff", False))


def next_port(manifest: dict[str, Any], transport: str) -> int:
    base = 18080 if transport == "http" else 19090
    used = {
        int(feature.get("ports", {}).get(transport, 0))
        for feature in services_from(manifest).values()
        if feature.get("ports", {}).get(transport)
    }
    port = base
    while port in used:
        port += 1
    return port


def command_init(args: argparse.Namespace) -> int:
    project = validate_name(args.project, "project")
    topology = str(args.topology)
    if topology not in VALID_TOPOLOGIES:
        raise ScaffoldError(f"invalid topology: {topology}")
    root = (Path(args.out).resolve() / project).resolve()
    if root.exists():
        raise ScaffoldError(f"target already exists: {root}")
    version = resolve_modular_version(args.modular_version)
    manifest = empty_manifest(module=project, topology=topology, modular_version=version)
    dry_run, diff = mutation_flags(args)
    result = render_and_apply(root, manifest, dry_run=dry_run, diff=diff, verify=verify_framework)
    if not dry_run and not diff:
        info(f"initialized {project} with github.com/wplbyx/modular {version}")
    return result


def command_service_add(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    manifest = load_manifest(project)
    svc = validate_name(args.svc, "svc")
    transports = [str(value) for value in args.transport]
    if not transports:
        raise ScaffoldError("service add requires at least one explicit --transport")
    invalid = sorted(set(transports) - VALID_TRANSPORTS)
    if invalid:
        raise ScaffoldError(f"unsupported transports: {', '.join(invalid)}")
    if manifest["project"]["topology"] == "single" and svc.casefold() == str(manifest["project"]["name"]).casefold():
        raise ScaffoldError(f"svc {svc!r} conflicts with the single-process config directory")
    key = f"service:{svc}"
    current = copy.deepcopy(manifest["features"].get(key, {}))
    selected = sorted(set(current.get("transports", [])) | set(transports))
    ports = dict(current.get("ports", {}))
    for transport in selected:
        ports.setdefault(transport, next_port(manifest, transport))
    manifest["features"][key] = {
        "kind": "service",
        "svc": svc,
        "transports": selected,
        "ports": ports,
    }
    dry_run, diff = mutation_flags(args)
    return render_and_apply(project, manifest, dry_run=dry_run, diff=diff, verify=verify_framework)


def command_service_remove(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    manifest = load_manifest(project)
    svc = validate_name(args.svc, "svc")
    key = f"service:{svc}"
    if key not in manifest["features"]:
        raise ScaffoldError(f"svc {svc!r} does not exist")
    dependents = [key for key in manifest["features"] if key.startswith(f"resource:{svc}:")]
    if dependents:
        raise ScaffoldError(f"remove resources for {svc!r} before removing the svc")
    manifest["features"].pop(key)
    dry_run = not args.apply or args.dry_run
    return render_and_apply(project, manifest, dry_run=dry_run, diff=args.diff, allow_delete=True, verify=verify_framework if args.apply else None)


def command_transport_add(args: argparse.Namespace) -> int:
    args.transport = [args.kind]
    return command_service_add(args)


def command_transport_remove(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    manifest = load_manifest(project)
    svc = validate_name(args.svc, "svc")
    key = f"service:{svc}"
    feature = copy.deepcopy(manifest["features"].get(key))
    if not isinstance(feature, dict):
        raise ScaffoldError(f"svc {svc!r} does not exist")
    selected = set(feature.get("transports", []))
    selected.discard(args.kind)
    if not selected:
        raise ScaffoldError("a svc must retain at least one transport; remove the svc instead")
    feature["transports"] = sorted(selected)
    feature.get("ports", {}).pop(args.kind, None)
    manifest["features"][key] = feature
    dry_run = not args.apply or args.dry_run
    return render_and_apply(project, manifest, dry_run=dry_run, diff=args.diff, allow_delete=True, verify=verify_framework if args.apply else None)


def command_resource_add(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    manifest = load_manifest(project)
    svc = resolve_service(project, args.svc)
    kind = str(args.kind)
    feature: dict[str, Any] = {"kind": "resource", "svc": svc, "resource": kind}
    if kind == "db":
        if args.driver not in VALID_DB_DRIVERS:
            raise ScaffoldError(f"unsupported database driver: {args.driver}")
        feature["driver"] = args.driver
        if args.driver == "gorm":
            if args.dialect not in VALID_GORM_DIALECTS:
                raise ScaffoldError(f"unsupported GORM dialect: {args.dialect}")
            feature["dialect"] = args.dialect
        elif args.driver in {"bun"}:
            feature["dialect"] = args.dialect
    manifest["features"][f"resource:{svc}:{kind}"] = feature
    dry_run, diff = mutation_flags(args)
    return render_and_apply(project, manifest, dry_run=dry_run, diff=diff, verify=verify_framework)


def command_resource_remove(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    manifest = load_manifest(project)
    svc = resolve_service(project, args.svc)
    key = f"resource:{svc}:{args.kind}"
    if key not in manifest["features"]:
        raise ScaffoldError(f"resource {args.kind!r} is not attached to svc {svc!r}")
    manifest["features"].pop(key)
    dry_run = not args.apply or args.dry_run
    return render_and_apply(project, manifest, dry_run=dry_run, diff=args.diff, allow_delete=True, verify=verify_framework if args.apply else None)


def command_sync(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    manifest = load_manifest(project)
    dry_run, diff = mutation_flags(args)
    return render_and_apply(project, manifest, dry_run=dry_run, diff=diff, verify=verify_framework)


def command_prune(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    manifest = load_manifest(project)
    dry_run = not args.apply or args.dry_run
    return render_and_apply(
        project,
        manifest,
        dry_run=dry_run,
        diff=args.diff,
        allow_delete=True,
        verify=verify_framework if args.apply else None,
    )


def command_migrate_topology(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    manifest = load_manifest(project)
    target = str(args.to)
    if target not in VALID_TOPOLOGIES:
        raise ScaffoldError(f"invalid topology: {target}")
    manifest["project"]["topology"] = target
    dry_run = not args.apply or args.dry_run
    return render_and_apply(
        project,
        manifest,
        dry_run=dry_run,
        diff=args.diff,
        allow_delete=True,
        verify=verify_framework if args.apply else None,
    )


def command_project_upgrade(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    manifest = load_manifest(project)
    version = resolve_modular_version(args.modular_version)
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


def command_gen(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    if testing_mode():
        return 0
    buf = shutil.which("buf")
    if buf is None:
        raise ScaffoldError("`buf` is required for proto generation")
    run_command([buf, "generate"], cwd=project)
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
    "project upgrade",
    "service add",
    "service remove",
    "transport add",
    "transport remove",
    "resource add",
    "resource remove",
    "sync",
    "doctor",
    "prune",
    "migrate topology",
    "verify",
    "gen",
    "coverage",
    "self-check",
]


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="modular project scaffold v2")
    sub = parser.add_subparsers(dest="command", required=True)

    command = sub.add_parser("init", help="create a project with repository-local scaffold tooling")
    command.add_argument("project")
    command.add_argument("--topology", required=True, choices=sorted(VALID_TOPOLOGIES))
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

    service = sub.add_parser("service", help="manage infrastructure svc shells")
    service_sub = service.add_subparsers(dest="service_command", required=True)
    command = service_sub.add_parser("add")
    command.add_argument("svc")
    command.add_argument("--transport", action="append", choices=sorted(VALID_TRANSPORTS), required=True)
    add_mutation_options(command)
    command.set_defaults(func=command_service_add)
    command = service_sub.add_parser("remove")
    command.add_argument("svc")
    command.add_argument("--apply", action="store_true")
    add_mutation_options(command)
    command.set_defaults(func=command_service_remove)

    transport = sub.add_parser("transport", help="change an existing svc transport selection")
    transport_sub = transport.add_subparsers(dest="transport_command", required=True)
    command = transport_sub.add_parser("add")
    command.add_argument("svc")
    command.add_argument("kind", choices=sorted(VALID_TRANSPORTS))
    add_mutation_options(command)
    command.set_defaults(func=command_transport_add)
    command = transport_sub.add_parser("remove")
    command.add_argument("svc")
    command.add_argument("kind", choices=sorted(VALID_TRANSPORTS))
    command.add_argument("--apply", action="store_true")
    add_mutation_options(command)
    command.set_defaults(func=command_transport_remove)

    resource = sub.add_parser("resource", help="manage library-owned infrastructure resources")
    resource_sub = resource.add_subparsers(dest="resource_command", required=True)
    command = resource_sub.add_parser("add")
    command.add_argument("kind", choices=sorted(VALID_RESOURCES))
    command.add_argument("--svc", default=None)
    command.add_argument("--driver", default="bun", choices=sorted(VALID_DB_DRIVERS))
    command.add_argument("--dialect", default="postgres", choices=sorted(VALID_GORM_DIALECTS))
    add_mutation_options(command)
    command.set_defaults(func=command_resource_add)
    command = resource_sub.add_parser("remove")
    command.add_argument("kind", choices=sorted(VALID_RESOURCES))
    command.add_argument("--svc", default=None)
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

    migrate = sub.add_parser("migrate", help="migrate generated framework structure")
    migrate_sub = migrate.add_subparsers(dest="migrate_command", required=True)
    command = migrate_sub.add_parser("topology")
    command.add_argument("--to", required=True, choices=sorted(VALID_TOPOLOGIES))
    command.add_argument("--apply", action="store_true")
    add_mutation_options(command)
    command.set_defaults(func=command_migrate_topology)

    command = sub.add_parser("doctor", help="read-only structure and ownership checks")
    command.add_argument("--phase", choices=["framework", "contract", "complete"], default="framework")
    command.add_argument("--strict", action="store_true")
    command.add_argument("--project-dir", default=".")
    command.set_defaults(func=command_doctor)

    command = sub.add_parser("verify", help="run a phase completion gate")
    command.add_argument("--phase", choices=["framework", "contract", "complete"], required=True)
    command.add_argument("--project-dir", default=".")
    command.set_defaults(func=command_verify)

    command = sub.add_parser("gen", help="run buf generate")
    command.add_argument("--project-dir", default=".")
    command.set_defaults(func=command_gen)

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
