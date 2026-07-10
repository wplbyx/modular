#!/usr/bin/env python3
"""CLI backend for the modular skill.

The CLI performs deterministic scaffolding for downstream projects using the
README-aligned svc layout. Agent-side recommendation still lives in SKILL.md
and references/.
"""

from __future__ import annotations

import argparse
import json
import re
import shutil
import subprocess
import sys
from pathlib import Path

SKILL_DIR = Path(__file__).resolve().parents[1]
ASSETS_DIR = SKILL_DIR / "assets"
DEFAULT_GO_VERSION = "1.26.0"
VALID_TOPOLOGIES = {"single", "service"}
KEEP = ".gitkeep"


def info(message: str) -> None:
    print(f"==> {message}")


def warn(message: str) -> None:
    print(f"warning: {message}", file=sys.stderr)


def fail(message: str, code: int = 1) -> int:
    print(f"error: {message}", file=sys.stderr)
    return code


def split_csv(value: str | None) -> list[str]:
    if not value:
        return []
    return [part.strip() for part in value.split(",") if part.strip()]


def normalize_identifier(value: str, *, lower: bool = True) -> str:
    text = re.sub(r"([a-z0-9])([A-Z])", r"\1_\2", value)
    text = re.sub(r"[^A-Za-z0-9_]+", "_", text)
    text = re.sub(r"_+", "_", text).strip("_")
    return text.lower() if lower else text


def snake_case(value: str) -> str:
    return normalize_identifier(value, lower=True)


def pascal_case(value: str) -> str:
    words = [w for w in snake_case(value).split("_") if w]
    return "".join(word[:1].upper() + word[1:] for word in words)


def lower_camel(value: str) -> str:
    p = pascal_case(value)
    return p[:1].lower() + p[1:] if p else ""


def render_template(relative: str, tokens: dict[str, str]) -> str:
    text = (ASSETS_DIR / relative).read_text(encoding="utf-8")
    for key, value in tokens.items():
        text = text.replace("{{" + key + "}}", value)
    return text


def write_file(path: Path, content: str, *, overwrite: bool = False) -> bool:
    path.parent.mkdir(parents=True, exist_ok=True)
    if path.exists() and not overwrite:
        return False
    path.write_text(content, encoding="utf-8")
    return True


def ensure_dir(path: Path) -> None:
    path.mkdir(parents=True, exist_ok=True)
    keep = path / KEEP
    if not keep.exists():
        keep.write_text("", encoding="utf-8")


def read_module(project: Path) -> str:
    gomod = project / "go.mod"
    if not gomod.exists():
        raise ValueError(f"no go.mod in {project}")
    for line in gomod.read_text(encoding="utf-8").splitlines():
        if line.startswith("module "):
            return line.split(None, 1)[1].strip()
    raise ValueError(f"go.mod in {project} has no module directive")


def project_name(project: Path) -> str:
    return read_module(project).split("/")[-1]


def svc_pascal(svc: str, surface: str) -> str:
    base = pascal_case(svc)
    if surface in {"public", svc}:
        return base + "Service"
    return base + pascal_case(surface) + "Service"


def proto_path(project: Path, svc: str, surface: str) -> Path:
    filename = f"{svc}.proto" if surface == "public" else f"{surface}.proto"
    return project / "proto" / svc / filename


def find_proto(project: Path, svc: str, surface: str) -> Path:
    preferred = project / "proto" / svc / f"{surface}.proto"
    fallback = project / "proto" / svc / f"{svc}.proto"
    if preferred.exists():
        return preferred
    if fallback.exists():
        return fallback
    return preferred


def base_tokens(project: Path, svc: str, surface: str) -> dict[str, str]:
    module = read_module(project)
    return {
        "PROJECT": module,
        "SVC": svc,
        "SURFACE": surface,
        "SERVICE_PASCAL": svc_pascal(svc, surface),
        "GO_PACKAGE": f"{module}/common/{svc}",
    }


def run_gen(project: Path, mode: str, buf_args: list[str] | None = None) -> int:
    if mode == "skip":
        return 0
    if not (project / "buf.yaml").exists():
        if mode == "required":
            return fail(f"no buf.yaml in {project}", 2)
        warn(f"skipping buf generate; no buf.yaml in {project}")
        return 0
    if shutil.which("buf") is None:
        if mode == "required":
            return fail("`buf` not found on PATH. Install: go install github.com/bufbuild/buf/cmd/buf@latest", 127)
        warn("skipping buf generate; `buf` not found on PATH")
        return 0
    cmd = ["buf", "generate", *(buf_args or [])]
    info(" ".join(cmd) + f"  (cwd={project})")
    return subprocess.run(cmd, cwd=project).returncode


def gofmt_paths(paths: list[Path]) -> None:
    gofmt = shutil.which("gofmt")
    if gofmt is None:
        return
    files: list[str] = []
    for path in paths:
        if path.is_dir():
            files.extend(str(p) for p in path.rglob("*.go"))
        elif path.exists() and path.suffix == ".go":
            files.append(str(path))
    if files:
        subprocess.run([gofmt, "-w", *files], check=False)


def discover_svcs(project: Path) -> list[str]:
    internal = project / "internal"
    if not internal.exists():
        return []
    return sorted(p.name for p in internal.iterdir() if p.is_dir())


def discover_surfaces(project: Path, svc: str) -> list[str]:
    app_dir = project / "internal" / svc / "app"
    if not app_dir.exists():
        return []
    return sorted(p.name for p in app_dir.iterdir() if p.is_dir())


def service_count(project: Path) -> int:
    proto = project / "proto"
    if not proto.exists():
        return 0
    return len([p for p in proto.iterdir() if p.is_dir()])


def is_single_topology(project: Path) -> bool:
    return (project / "cmd" / project_name(project) / "main.go").exists()


def resources_path(project: Path, svc: str) -> Path:
    return project / "config" / svc / "resources.json"


def read_resources(project: Path, svc: str) -> dict[str, str | bool]:
    path = resources_path(project, svc)
    if not path.exists():
        return {}
    data = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(data, dict):
        return {}
    return data


def write_resources(project: Path, svc: str, data: dict[str, str | bool]) -> None:
    path = resources_path(project, svc)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def command_init(args: argparse.Namespace) -> int:
    if args.topology not in VALID_TOPOLOGIES:
        return fail(f"invalid topology: {args.topology}")
    root = (Path(args.out).resolve() / args.project).resolve()
    if root.exists():
        return fail(f"{root} already exists")

    modular_path = args.modular_path or str(Path(__file__).resolve().parents[3])
    tokens = {
        "PROJECT": args.project,
        "GO_VERSION": args.go_version,
        "MODULAR_PATH": modular_path.replace("\\", "/"),
    }

    info(f"Creating {root}")
    write_file(root / "go.mod", render_template("project/go.mod.tmpl", tokens))
    write_file(root / "buf.yaml", render_template("project/buf.yaml.tmpl", tokens))
    write_file(root / "buf.gen.yaml", render_template("project/buf.gen.yaml.tmpl", tokens))
    write_file(root / "Makefile", render_template("project/Makefile.tmpl", tokens))
    for dirname in ["proto", "common", "internal", "config", "cmd"]:
        ensure_dir(root / dirname)

    if args.topology == "single":
        write_file(root / "cmd" / args.project / "main.go", render_empty_main("single topology placeholder"), overwrite=True)

    info(f"Initialized '{args.project}' (topology={args.topology})")
    info("Next: add a svc with `service <svc>`")
    return 0


def scaffold_svc_config(project: Path, svc: str) -> None:
    index = service_count(project)
    tokens = {
        "SVC": svc,
        "SVC_PASCAL": pascal_case(svc),
        "HTTP_PORT": str(18080 + index),
        "GRPC_PORT": str(19090 + index),
    }
    write_file(project / "config" / svc / "config.go", render_template("svc/config.go.tmpl", tokens))
    write_file(project / "config" / svc / "config.yaml", render_template("svc/config.yaml.tmpl", tokens))


def scaffold_surface(project: Path, svc: str, surface: str, *, create_svc: bool) -> None:
    tokens = base_tokens(project, svc, surface)
    write_file(proto_path(project, svc, surface), render_template("svc/proto.tmpl", tokens))
    write_file(project / "internal" / svc / "api" / surface / "grpc.go", render_template("svc/api_grpc.go.tmpl", tokens))
    write_file(project / "internal" / svc / "api" / surface / "http.go", render_template("svc/api_http.go.tmpl", tokens))
    write_file(project / "internal" / svc / "api" / surface / "event.go", render_template("svc/api_event.go.tmpl", tokens))
    write_file(project / "internal" / svc / "app" / surface / "adapter.go", render_template("svc/app_adapter.go.tmpl", tokens))
    write_file(project / "internal" / svc / "app" / surface / "server.go", render_template("svc/app_server.go.tmpl", tokens))

    if create_svc:
        write_file(project / "internal" / svc / "domain" / "adapter.go", render_template("svc/domain_adapter.go.tmpl", tokens))
        write_file(project / "internal" / svc / "repository" / "app" / "repository.go", render_template("svc/app_repository.go.tmpl", tokens))
    write_file(project / "internal" / svc / "repository" / "app" / f"{surface}_example.go", render_template("svc/app_repository_example.go.tmpl", tokens))


def command_service(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    try:
        svc = snake_case(args.svc)
        surface = snake_case(args.surface)
        _ = read_module(project)
    except ValueError as err:
        return fail(str(err))

    scaffold_svc_config(project, svc)
    scaffold_surface(project, svc, surface, create_svc=True)
    for method in [*split_csv(args.methods), *args.method]:
        add_method(project, svc, surface, method)
    rebuild_cmd(project, svc)
    gofmt_paths([project / "cmd", project / "config" / svc, project / "internal" / svc])

    rc = run_gen(project, args.gen)
    if rc != 0:
        return rc
    info(f"Service '{svc}' scaffolded (surface={surface})")
    return 0


def command_surface(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    try:
        svc = snake_case(args.svc)
        surface = snake_case(args.surface)
        _ = read_module(project)
    except ValueError as err:
        return fail(str(err))
    if not (project / "internal" / svc).exists():
        return fail(f"svc '{svc}' does not exist; run service {svc} first")
    scaffold_surface(project, svc, surface, create_svc=False)
    for method in [*split_csv(args.methods), *args.method]:
        add_method(project, svc, surface, method)
    rebuild_cmd(project, svc)
    gofmt_paths([project / "cmd", project / "internal" / svc])
    rc = run_gen(project, args.gen)
    if rc != 0:
        return rc
    info(f"Surface '{surface}' scaffolded for svc '{svc}'")
    return 0


def add_method(project: Path, svc: str, surface: str, method: str) -> None:
    method_pascal = pascal_case(method)
    method_snake = snake_case(method)
    tokens = base_tokens(project, svc, surface)
    tokens.update({"METHOD_PASCAL": method_pascal, "METHOD_SNAKE": method_snake})
    path = find_proto(project, svc, surface)
    if not path.exists():
        raise ValueError(f"surface proto not found: {path}")
    text = path.read_text(encoding="utf-8")
    rpc_line = f"  rpc {method_pascal}({method_pascal}Request) returns ({method_pascal}Response);"
    if f"rpc {method_pascal}(" not in text:
        service_name = svc_pascal(svc, surface)
        service_start = text.find(f"service {service_name} ")
        if service_start == -1:
            raise ValueError(f"service {service_name} not found in {path}")
        close = text.find("\n}", service_start)
        if close == -1:
            raise ValueError(f"service {service_name} has no closing brace in {path}")
        text = text[:close] + "\n" + rpc_line + text[close:]
    if f"message {method_pascal}Request" not in text:
        text += (
            f"\nmessage {method_pascal}Request {{\n"
            "  string id = 1;\n"
            "}\n\n"
            f"message {method_pascal}Response {{\n"
            "  string id = 1;\n"
            "}\n"
        )
    path.write_text(text, encoding="utf-8")
    write_file(project / "internal" / svc / "app" / surface / f"{method_snake}.go", render_template("svc/app_method.go.tmpl", tokens))


def command_method(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    svc = snake_case(args.svc)
    surface = snake_case(args.surface)
    try:
        add_method(project, svc, surface, args.method_name)
    except ValueError as err:
        return fail(str(err))
    rebuild_cmd(project, svc)
    gofmt_paths([project / "cmd", project / "internal" / svc])
    rc = run_gen(project, args.gen)
    if rc != 0:
        return rc
    info(f"Method '{pascal_case(args.method_name)}' scaffolded")
    return 0


def rebuild_cmd(project: Path, touched_svc: str | None = None) -> None:
    if is_single_topology(project):
        write_file(project / "cmd" / project_name(project) / "main.go", render_main(project, discover_svcs(project), aggregate=True), overwrite=True)
        return
    if touched_svc is not None:
        write_file(project / "cmd" / touched_svc / "main.go", render_main(project, [touched_svc], aggregate=False), overwrite=True)


def render_empty_main(reason: str) -> str:
    return f"package main\n\n// {reason}.\nfunc main() {{}}\n"


def render_main(project: Path, svcs: list[str], *, aggregate: bool) -> str:
    module = read_module(project)
    svcs = [svc for svc in svcs if discover_surfaces(project, svc)]
    if not svcs:
        return render_empty_main("no svc has been scaffolded yet")

    imports: list[str] = [
        '"context"',
        '"fmt"',
        '"os"',
        '"os/signal"',
        '"syscall"',
    ]
    if aggregate:
        imports.append('"time"')
    imports.extend([
        "",
        '"google.golang.org/grpc"',
        "",
        '"github.com/wplbyx/modular/packages/app"',
        '"github.com/wplbyx/modular/packages/core"',
    ])
    if aggregate:
        imports.append('modularconfig "github.com/wplbyx/modular/packages/config"')
    imports.extend([
        'httpserver "github.com/wplbyx/modular/packages/transport/server/http"',
        'rpcserver "github.com/wplbyx/modular/packages/transport/server/rpc"',
    ])

    for svc in svcs:
        imports.append(f'{alias(svc, "config")} "{module}/config/{svc}"')
        imports.append(f'{alias(svc, "app_repository")} "{module}/internal/{svc}/repository/app"')
        resources = read_resources(project, svc)
        if resources.get("db") == "bun":
            imports.append('"github.com/wplbyx/modular/packages/infra/database/bun"')
        if resources.get("db") == "gorm":
            imports.append('"github.com/wplbyx/modular/packages/infra/database/gorm"')
        if resources.get("redis"):
            imports.append('"github.com/wplbyx/modular/packages/infra/cache/redis"')
        if resources.get("telemetry"):
            imports.append('"github.com/wplbyx/modular/packages/telemetry"')
        if resources.get("db") == "mongo" or resources.get("storage"):
            imports.append(f'{alias(svc, "repository")} "{module}/internal/{svc}/repository"')
        for surface in discover_surfaces(project, svc):
            imports.append(f'{alias(svc, surface, "api")} "{module}/internal/{svc}/api/{surface}"')
            imports.append(f'{alias(svc, surface, "app")} "{module}/internal/{svc}/app/{surface}"')

    lines: list[str] = [
        "package main",
        "",
        "import (",
        *[("\t" + item if item else "") for item in unique_preserve(imports)],
        ")",
        "",
        "func main() {",
        "\tctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)",
        "\tdefer cancel()",
        "",
        "\tendpoints := make([]core.Endpoint, 0)",
        "\tresources := make([]core.Resource, 0)",
        "\ttransports := make([]core.Transport, 0)",
        "",
    ]

    for svc in svcs:
        lines.extend(render_svc_wiring(project, svc))

    if aggregate:
        lines.extend([
            "\tapplicationCfg := modularconfig.Application{",
            f'\t\tName: "{project_name(project)}",',
            '\t\tMode: "dev",',
            '\t\tVersion: "v0.1.0",',
            "\t\tShutdownTimeout: 10 * time.Second,",
            "\t}",
            "",
            f'\tnode := core.NewServiceNode("{project_name(project)}", "v0.1.0", transports...)',
            "",
            "\toptions := []app.Option{app.WithServiceNode(node)}",
        ])
    else:
        svc = svcs[0]
        cfg_var = lower_camel(svc) + "Cfg"
        lines.extend([
            f"\tnode := core.NewServiceNode({cfg_var}.Name, {cfg_var}.Version, transports...)",
            "",
            "\toptions := []app.Option{app.WithServiceNode(node)}",
        ])
    lines.extend([
        "\tfor _, endpoint := range endpoints {",
        "\t\toptions = append(options, app.WithEndpoint(endpoint))",
        "\t}",
        "\tfor _, resource := range resources {",
        "\t\toptions = append(options, app.WithResource(resource))",
        "\t}",
        "",
    ])
    if aggregate:
        lines.append("\tapplication, err := app.NewApplication(ctx, &applicationCfg, options...)")
    else:
        cfg_var = lower_camel(svcs[0]) + "Cfg"
        lines.append(f"\tapplication, err := app.NewApplication(ctx, &{cfg_var}.Application, options...)")
    lines.extend([
        "\tif err != nil {",
        '\t\tfmt.Printf("create application failed: %v\\n", err)',
        "\t\tos.Exit(1)",
        "\t}",
        "",
        "\tif err := application.Run(); err != nil {",
        '\t\tfmt.Printf("application exited: %v\\n", err)',
        "\t}",
        "}",
        "",
    ])
    return "\n".join(lines)


def render_svc_wiring(project: Path, svc: str) -> list[str]:
    cfg_alias = alias(svc, "config")
    cfg_var = lower_camel(svc) + "Cfg"
    http_var = lower_camel(svc) + "HTTPServer"
    grpc_var = lower_camel(svc) + "GRPCServer"
    repo_var = lower_camel(svc) + "AppRepo"
    repo_alias = alias(svc, "app_repository")
    lines = [
        f"\t{cfg_var}, err := {cfg_alias}.Load(\"./config/{svc}\")",
        "\tif err != nil {",
        f'\t\tfmt.Printf("load {svc} config failed: %v\\n", err)',
        "\t\tos.Exit(1)",
        "\t}",
        "",
        f"\t{http_var}, err := httpserver.NewServer(&{cfg_var}.HTTP)",
        "\tif err != nil {",
        f'\t\tfmt.Printf("create {svc} http server failed: %v\\n", err)',
        "\t\tos.Exit(1)",
        "\t}",
        "",
        f"\t{repo_var} := {repo_alias}.NewRepository()",
    ]

    for surface in discover_surfaces(project, svc):
        server_var = lower_camel(svc + "_" + surface + "_server")
        app_alias = alias(svc, surface, "app")
        api_alias = alias(svc, surface, "api")
        lines.extend([
            f"\t{server_var} := {app_alias}.NewServer({repo_var}, {repo_var})",
            f"\t{http_var}.RegisterRoute({api_alias}.HTTPRoutes({server_var}))",
        ])

    lines.extend([
        "",
        f"\t{grpc_var}, err := rpcserver.NewServer(&{cfg_var}.GRPC, func(registrar grpc.ServiceRegistrar) error {{",
    ])
    for surface in discover_surfaces(project, svc):
        server_var = lower_camel(svc + "_" + surface + "_server")
        api_alias = alias(svc, surface, "api")
        lines.extend([
            f"\t\tif err := {api_alias}.RegisterGRPC(registrar, {server_var}); err != nil {{",
            "\t\t\treturn err",
            "\t\t}",
        ])
    lines.extend([
        "\t\treturn nil",
        "\t})",
        "\tif err != nil {",
        f'\t\tfmt.Printf("create {svc} grpc server failed: %v\\n", err)',
        "\t\tos.Exit(1)",
        "\t}",
        "",
    ])

    lines.extend(render_resource_wiring(project, svc, cfg_var))
    lines.extend([
        f"\tendpoints = append(endpoints, {http_var}, {grpc_var})",
        "\ttransports = append(transports,",
        f'\t\tcore.Transport{{Protocol: "http", Address: core.NormalizeHost({cfg_var}.HTTP.Host), Port: {cfg_var}.HTTP.Port, HealthPath: "/health"}},',
        f"\t\t{grpc_var}.Transport(),",
        "\t)",
        "",
    ])
    return lines


def render_resource_wiring(project: Path, svc: str, cfg_var: str) -> list[str]:
    resources = read_resources(project, svc)
    lines: list[str] = []
    if resources.get("db") == "bun":
        var_name = lower_camel(svc) + "DBResource"
        lines.append(f"\t{var_name} := bun.NewResource(&{cfg_var}.Database)")
        lines.append(f"\tresources = append(resources, {var_name})")
    if resources.get("db") == "gorm":
        var_name = lower_camel(svc) + "DBResource"
        lines.append(f"\t{var_name} := gorm.NewResource(&{cfg_var}.Database)")
        lines.append(f"\tresources = append(resources, {var_name})")
    if resources.get("db") == "mongo":
        var_name = lower_camel(svc) + "MongoResource"
        repo_alias = alias(svc, "repository")
        lines.append(f"\t{var_name} := {repo_alias}.NewMongoResource(&{cfg_var}.Database)")
        lines.append(f"\tresources = append(resources, {var_name})")
    if resources.get("redis"):
        var_name = lower_camel(svc) + "RedisResource"
        lines.append(f"\t{var_name} := redis.NewResource(&{cfg_var}.Redis)")
        lines.append(f"\tresources = append(resources, {var_name})")
    if resources.get("storage"):
        var_name = lower_camel(svc) + "StorageResource"
        repo_alias = alias(svc, "repository")
        lines.append(f"\t{var_name} := {repo_alias}.NewStorageResource(&{cfg_var}.Storage)")
        lines.append(f"\tresources = append(resources, {var_name})")
    if resources.get("telemetry"):
        var_name = lower_camel(svc) + "TelemetryResource"
        lines.extend([
            f"\t{var_name}, err := telemetry.NewOpenTelemetry(ctx, {cfg_var}.Name, {cfg_var}.Version, &{cfg_var}.Telemetry)",
            "\tif err != nil {",
            f'\t\tfmt.Printf("create {svc} telemetry failed: %v\\n", err)',
            "\t\tos.Exit(1)",
            "\t}",
            f"\tresources = append(resources, {var_name})",
        ])
    if lines:
        lines.append("")
    return lines


def alias(*parts: str) -> str:
    return lower_camel("_".join(parts))


def unique_preserve(items: list[str]) -> list[str]:
    seen: set[str] = set()
    result: list[str] = []
    for item in items:
        if item == "":
            if result and result[-1] != "":
                result.append(item)
            continue
        if item in seen:
            continue
        seen.add(item)
        result.append(item)
    while result and result[-1] == "":
        result.pop()
    return result


DATABASE_YAML = """\
database:
  Dsn: postgres
  Host: "127.0.0.1"
  Port: 5432
  Database: app
  Username: app
  Password: app
"""

DATABASE_MONGO_YAML = """\
database:
  Dsn: mongodb
  Host: "127.0.0.1"
  Port: 27017
  Database: app
"""

REDIS_YAML = """\
redis:
  Host: "127.0.0.1"
  Port: 6379
"""

STORAGE_YAML = """\
storage:
  Type: disk
  Disk:
    RootDir: storage/upload
    BaseUrl: /upload
"""

TELEMETRY_YAML = """\
telemetry:
  Tracer: ""
  Metric: ""
  Logger: ""
"""


def resolve_resource_svc(project: Path, requested: str | None) -> str:
    if requested:
        svc = snake_case(requested)
        if not (project / "internal" / svc).exists():
            raise ValueError(f"svc '{svc}' does not exist")
        return svc
    svcs = discover_svcs(project)
    if len(svcs) == 1:
        return svcs[0]
    if not svcs:
        raise ValueError("no svc exists; run service <svc> first")
    raise ValueError("multiple svc modules exist; pass --svc")


def update_config_go(project: Path, svc: str, field_line: str) -> bool:
    path = project / "config" / svc / "config.go"
    if not path.exists():
        warn(f"config/{svc}/config.go not found; skipping config struct update")
        return False
    text = path.read_text(encoding="utf-8")
    field_name = field_line.strip().split(None, 1)[0]
    if re.search(rf"\b{re.escape(field_name)}\s+modularconfig\.", text):
        return False
    anchor = "\tLogging modularconfig.Logging"
    if anchor in text:
        text = text.replace(anchor, field_line.rstrip() + "\n" + anchor, 1)
    else:
        end = text.find("}\n\nfunc Load")
        if end == -1:
            warn(f"could not find Config struct end in config/{svc}/config.go")
            return False
        text = text[:end] + field_line + text[end:]
    path.write_text(text, encoding="utf-8")
    return True


def update_config_yaml(project: Path, svc: str, key: str, block: str) -> bool:
    path = project / "config" / svc / "config.yaml"
    if not path.exists():
        warn(f"config/{svc}/config.yaml not found; skipping config yaml update")
        return False
    text = path.read_text(encoding="utf-8")
    if re.search(rf"(?m)^{re.escape(key)}:\s*$", text):
        return False
    if not text.endswith("\n"):
        text += "\n"
    text += "\n" + block.strip() + "\n"
    path.write_text(text, encoding="utf-8")
    return True


def command_resource(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    try:
        module = read_module(project)
        svc = resolve_resource_svc(project, args.svc)
    except ValueError as err:
        return fail(str(err))

    resources = read_resources(project, svc)
    kind = args.kind
    driver = args.driver
    if kind == "db":
        if driver not in {"bun", "gorm", "mongo"}:
            return fail("--driver for db must be bun, gorm, or mongo")
        update_config_go(project, svc, '\tDatabase modularconfig.Database `mapstructure:"database"`\n')
        update_config_yaml(project, svc, "database", DATABASE_MONGO_YAML if driver == "mongo" else DATABASE_YAML)
        resources["db"] = driver
        if driver == "mongo":
            write_file(project / "internal" / svc / "repository" / "mongo_resource.go", render_template("resource/mongo_resource.go.tmpl", {"PROJECT": module}), overwrite=True)
    elif kind == "redis":
        update_config_go(project, svc, '\tRedis modularconfig.Redis `mapstructure:"redis"`\n')
        update_config_yaml(project, svc, "redis", REDIS_YAML)
        resources["redis"] = True
    elif kind == "storage":
        update_config_go(project, svc, '\tStorage modularconfig.Storage `mapstructure:"storage"`\n')
        update_config_yaml(project, svc, "storage", STORAGE_YAML)
        resources["storage"] = True
        write_file(project / "internal" / svc / "repository" / "storage_resource.go", render_template("resource/storage_resource.go.tmpl", {"PROJECT": module}), overwrite=True)
    elif kind == "telemetry":
        update_config_go(project, svc, '\tTelemetry modularconfig.Telemetry `mapstructure:"telemetry"`\n')
        update_config_yaml(project, svc, "telemetry", TELEMETRY_YAML)
        resources["telemetry"] = True
    else:
        return fail(f"unsupported resource kind: {kind}")
    write_resources(project, svc, resources)
    rebuild_cmd(project, svc)
    gofmt_paths([project / "cmd", project / "config" / svc, project / "internal" / svc / "repository"])
    info(f"Resource '{kind}' configured for svc '{svc}'")
    return 0


def parse_app_signature(name_or_sig: str, aggregate: str) -> str:
    text = name_or_sig.strip()
    if "(" in text:
        return text
    name = pascal_case(text)
    dto_type = aggregate + "DTO"
    if name.startswith("List"):
        return f"{name}(ctx context.Context) ([]{dto_type}, error)"
    if name.startswith(("Get", "Find", "Load")):
        return f"{name}(ctx context.Context, id string) ({dto_type}, error)"
    if name.startswith(("Delete", "Remove")):
        return f"{name}(ctx context.Context, id string) error"
    return f"{name}(ctx context.Context, item {dto_type}) error"


def parse_domain_signature(name_or_sig: str, aggregate: str) -> str:
    text = name_or_sig.strip()
    if "(" in text:
        return text
    name = pascal_case(text)
    entity_type = f"*entity.{aggregate}"
    if name.startswith("List"):
        return f"{name}(ctx context.Context) ([]{entity_type}, error)"
    if name.startswith(("Get", "Find", "Load")):
        return f"{name}(ctx context.Context, id string) ({entity_type}, error)"
    if name.startswith(("Delete", "Remove")):
        return f"{name}(ctx context.Context, id string) error"
    return f"{name}(ctx context.Context, item {entity_type}) error"


COMPLEX_RECOMMEND_TERMS = [
    "aggregate",
    "domain",
    "invariant",
    "policy",
    "transaction",
    "consistency",
    "state machine",
    "workflow",
    "saga",
    "orchestration",
    "reconcile",
    "settlement",
    "allocation",
]

SIMPLE_RECOMMEND_TERMS = [
    "crud",
    "create",
    "read",
    "update",
    "delete",
    "list",
    "page",
    "search",
    "query",
    "admin",
    "management",
]

DOMAIN_METHOD_PREFIXES = (
    "Approve",
    "Reject",
    "Settle",
    "Reconcile",
    "Reserve",
    "Release",
    "Allocate",
    "Cancel",
    "Refund",
    "Pay",
    "Confirm",
    "Transition",
)


def collect_repository_inputs(args: argparse.Namespace) -> tuple[list[str], list[str]]:
    queries = [item for item in [*split_csv(args.queries), *args.query] if item]
    commands = [item for item in [*split_csv(args.commands), *args.command] if item]
    return queries, commands


def recommend_repository_placement(
    feature: str,
    complexity: str,
    raw_queries: list[str],
    raw_commands: list[str],
) -> tuple[str, list[str]]:
    if complexity == "simple":
        return "app", ["--complexity simple was provided"]
    if complexity == "domain":
        return "domain", ["--complexity domain was provided"]

    text = feature.lower()
    complex_hits = [term for term in COMPLEX_RECOMMEND_TERMS if term in text]
    simple_hits = [term for term in SIMPLE_RECOMMEND_TERMS if term in text]
    method_names = [pascal_case(method_name_from_signature(item)) for item in [*raw_queries, *raw_commands]]
    domain_methods = [name for name in method_names if any(name.startswith(prefix) for prefix in DOMAIN_METHOD_PREFIXES)]

    if complex_hits:
        return "domain", ["feature mentions complex domain signals: " + ", ".join(complex_hits[:4])]
    if domain_methods:
        return "domain", ["method names look domain-behavior oriented: " + ", ".join(domain_methods[:4])]
    if simple_hits:
        return "app", ["feature looks like simple app CRUD/query work: " + ", ".join(simple_hits[:4])]
    if raw_queries and not raw_commands:
        return "app", ["query-only ports usually fit app-layer adapters"]
    return "app", ["no aggregate/invariant/transaction signal found; start with app adapter and promote only when rules emerge"]


def quote_cli_arg(value: str) -> str:
    if re.match(r"^[A-Za-z0-9_./:=-]+$", value):
        return value
    return '"' + value.replace('"', '\\"') + '"'


def repository_scaffold_command(
    placement: str,
    svc: str,
    surface: str,
    aggregate: str,
    queries: list[str],
    commands: list[str],
    project_dir: str,
) -> str:
    parts = ["python", "agent/modular/scripts/modular.py", "repository", placement, svc]
    if placement == "app":
        parts.append(surface)
    parts.extend(["--aggregate", aggregate])
    for sig in queries:
        parts.extend(["--query", sig])
    for sig in commands:
        parts.extend(["--command", sig])
    if project_dir != ".":
        parts.extend(["--project-dir", project_dir])
    return " ".join(quote_cli_arg(part) for part in parts)


def command_repository_recommend(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    try:
        read_module(project)
    except ValueError as err:
        return fail(str(err))

    svc = snake_case(args.svc)
    surface = snake_case(args.surface)
    aggregate = pascal_case(args.aggregate)
    raw_queries, raw_commands = collect_repository_inputs(args)
    placement, reasons = recommend_repository_placement(args.feature or "", args.complexity, raw_queries, raw_commands)
    parser = parse_app_signature if placement == "app" else parse_domain_signature
    queries = [parser(value, aggregate) for value in raw_queries]
    commands = [parser(value, aggregate) for value in raw_commands]

    target = f"internal/{svc}/app/{surface}/adapter.go" if placement == "app" else f"internal/{svc}/domain/adapter.go"
    implementation = f"internal/{svc}/repository/{placement}"
    warnings: list[str] = []
    if not (project / "internal" / svc).exists():
        warnings.append(f"svc '{svc}' does not exist yet; run service {svc} first")
    elif placement == "app" and not (project / "internal" / svc / "app" / surface).exists():
        warnings.append(f"surface '{svc}/{surface}' does not exist yet; run surface {svc} {surface} first")
    if not queries and not commands:
        warnings.append("no ports provided; pass --query/--command names or full Go signatures before scaffolding")

    scaffold = repository_scaffold_command(placement, svc, surface, aggregate, queries, commands, args.project_dir)
    result = {
        "placement": placement,
        "target_adapter": target,
        "implementation": implementation,
        "reasons": reasons,
        "queries": queries,
        "commands": commands,
        "warnings": warnings,
        "scaffold_command": scaffold,
    }
    if args.json:
        print(json.dumps(result, indent=2, ensure_ascii=False))
        return 0

    print(f"placement: {placement}")
    print(f"target adapter: {target}")
    print(f"implementation: {implementation}")
    print("why:")
    for reason in reasons:
        print(f"- {reason}")
    if queries:
        print("queries:")
        for sig in queries:
            print(f"- {sig}")
    if commands:
        print("commands:")
        for sig in commands:
            print(f"- {sig}")
    if warnings:
        print("warnings:")
        for warning in warnings:
            print(f"- {warning}")
    print("scaffold:")
    print(scaffold)
    return 0


def method_name_from_signature(signature: str) -> str:
    return signature.split("(", 1)[0].strip()


def return_types(signature: str) -> list[str]:
    matches = list(re.finditer(r"\)", signature))
    if not matches:
        return []
    returns = signature[matches[-1].end():].strip()
    if not returns:
        return []
    if returns.startswith("(") and returns.endswith(")"):
        return [p.strip() for p in returns[1:-1].split(",")]
    return [returns]


def zero_return_for(signature: str) -> str:
    parts = return_types(signature)
    if not parts:
        return ""
    if parts == ["error"]:
        return "\treturn nil"
    values: list[str] = []
    for part in parts:
        if part == "error":
            values.append("nil")
        elif part.startswith("[]") or part.startswith("map[") or part.startswith("*"):
            values.append("nil")
        elif part == "string":
            values.append('""')
        elif part == "bool":
            values.append("false")
        elif re.match(r"^(int|int64|int32|uint|uint64|uint32|float64|float32)$", part):
            values.append("0")
        else:
            values.append(part + "{}")
    return "\treturn " + ", ".join(values)


def param_names(signature: str) -> list[str]:
    match = re.search(r"\((.*?)\)", signature)
    if not match:
        return []
    params = match.group(1).strip()
    if not params:
        return []
    names: list[str] = []
    for part in params.split(","):
        fields = part.strip().split()
        if len(fields) < 2:
            continue
        for name in fields[0].split(","):
            name = name.strip()
            if name and name != "_":
                names.append(name)
    return names


def command_repository_app(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    try:
        module = read_module(project)
    except ValueError as err:
        return fail(str(err))
    svc = snake_case(args.svc)
    surface = snake_case(args.surface)
    aggregate = pascal_case(args.aggregate)
    if not (project / "internal" / svc / "app" / surface).exists():
        return fail(f"app surface '{svc}/{surface}' does not exist")
    queries = [parse_app_signature(value, aggregate) for value in [*split_csv(args.queries), *args.query]]
    commands = [parse_app_signature(value, aggregate) for value in [*split_csv(args.commands), *args.command]]
    if not queries and not commands:
        return fail("provide at least one --query/--queries or --command/--commands")
    write_file(project / "internal" / svc / "app" / surface / "adapter.go", render_app_adapter(surface, aggregate, queries, commands), overwrite=args.force)
    write_file(project / "internal" / svc / "repository" / "app" / "repository.go", render_template("svc/app_repository.go.tmpl", base_tokens(project, svc, surface)), overwrite=False)
    write_file(
        project / "internal" / svc / "repository" / "app" / f"{surface}_{snake_case(aggregate)}.go",
        render_app_repository_methods(module, svc, surface, aggregate, queries, commands),
        overwrite=args.force,
    )
    gofmt_paths([project / "internal" / svc])
    info(f"App repository scaffolded for svc '{svc}' surface '{surface}'")
    return 0


def command_repository_domain(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    try:
        module = read_module(project)
    except ValueError as err:
        return fail(str(err))
    svc = snake_case(args.svc)
    aggregate = pascal_case(args.aggregate)
    if not (project / "internal" / svc).exists():
        return fail(f"svc '{svc}' does not exist")
    queries = [parse_domain_signature(value, aggregate) for value in [*split_csv(args.queries), *args.query]]
    commands = [parse_domain_signature(value, aggregate) for value in [*split_csv(args.commands), *args.command]]
    if not queries and not commands:
        return fail("provide at least one --query/--queries or --command/--commands")
    write_file(project / "internal" / svc / "domain" / "adapter.go", render_domain_adapter(svc, aggregate, queries, commands, module), overwrite=args.force)
    write_file(project / "internal" / svc / "domain" / "entity" / f"{snake_case(aggregate)}.go", render_entity(aggregate), overwrite=args.force)
    write_file(project / "internal" / svc / "repository" / "domain" / "repository.go", render_domain_repository_base(), overwrite=False)
    write_file(
        project / "internal" / svc / "repository" / "domain" / f"{snake_case(aggregate)}.go",
        render_domain_repository_methods(module, svc, aggregate, queries, commands),
        overwrite=args.force,
    )
    gofmt_paths([project / "internal" / svc])
    info(f"Domain repository scaffolded for svc '{svc}'")
    return 0


def render_app_adapter(surface: str, aggregate: str, queries: list[str], commands: list[str]) -> str:
    lines = [
        f"package {surface}",
        "",
        "import \"context\"",
        "",
        f"type {aggregate}DTO struct {{",
        "\tID string",
        "}",
        "",
        "type QueryRepository interface {",
    ]
    lines.extend(f"\t{sig}" for sig in queries)
    lines.extend(["}", "", "type CommandRepository interface {"])
    lines.extend(f"\t{sig}" for sig in commands)
    lines.extend(["}", ""])
    return "\n".join(lines)


def render_app_repository_methods(module: str, svc: str, surface: str, aggregate: str, queries: list[str], commands: list[str]) -> str:
    app_alias = "surfaceapp"
    lines = [
        "package app",
        "",
        "import (",
        '\t"context"',
        "",
        f'\t{app_alias} "{module}/internal/{svc}/app/{surface}"',
        ")",
        "",
    ]
    for sig in [*queries, *commands]:
        repo_sig = qualify_app_signature(sig, aggregate, app_alias)
        assignments = ["\t_ = r", *[f"\t_ = {name}" for name in param_names(repo_sig)]]
        lines.extend([
            f"func (r *Repository) {repo_sig} {{",
            *assignments,
            zero_return_for(repo_sig),
            "}",
            "",
        ])
    if queries:
        lines.append(f"var _ {app_alias}.QueryRepository = (*Repository)(nil)")
    if commands:
        lines.append(f"var _ {app_alias}.CommandRepository = (*Repository)(nil)")
    lines.append("")
    return "\n".join(lines)


def qualify_app_signature(signature: str, aggregate: str, app_alias: str) -> str:
    dto = aggregate + "DTO"
    return re.sub(rf"(?<!\.)\b{re.escape(dto)}\b", f"{app_alias}.{dto}", signature)


def render_domain_adapter(svc: str, aggregate: str, queries: list[str], commands: list[str], module: str) -> str:
    imports = ['\t"context"']
    if any("entity." in sig for sig in [*queries, *commands]):
        imports.extend(["", f'\t"{module}/internal/{svc}/domain/entity"'])
    lines = [
        "package domain",
        "",
        "import (",
        *imports,
        ")",
        "",
        "type QueryRepository interface {",
    ]
    lines.extend(f"\t{sig}" for sig in queries)
    lines.extend(["}", "", "type CommandRepository interface {"])
    lines.extend(f"\t{sig}" for sig in commands)
    lines.extend(["}", ""])
    return "\n".join(lines)


def render_domain_repository_base() -> str:
    return "package domain\n\ntype Repository struct {\n\t// db/cache/client fields live here.\n}\n\nfunc NewRepository() *Repository {\n\treturn &Repository{}\n}\n"


def render_domain_repository_methods(module: str, svc: str, aggregate: str, queries: list[str], commands: list[str]) -> str:
    lines = [
        "package domain",
        "",
        "import (",
        '\t"context"',
        "",
        f'\tdomainports "{module}/internal/{svc}/domain"',
    ]
    if any("entity." in sig for sig in [*queries, *commands]):
        lines.append(f'\t"{module}/internal/{svc}/domain/entity"')
    lines.extend([")", ""])
    for sig in [*queries, *commands]:
        assignments = ["\t_ = r", *[f"\t_ = {name}" for name in param_names(sig)]]
        lines.extend([
            f"func (r *Repository) {sig} {{",
            *assignments,
            zero_return_for(sig),
            "}",
            "",
        ])
    if queries:
        lines.append("var _ domainports.QueryRepository = (*Repository)(nil)")
    if commands:
        lines.append("var _ domainports.CommandRepository = (*Repository)(nil)")
    lines.append("")
    return "\n".join(lines)


def render_entity(aggregate: str) -> str:
    var_name = aggregate[:1].lower()
    return (
        "package entity\n\n"
        f"type {aggregate} struct {{\n"
        "\tid string\n"
        "}\n\n"
        f"func New{aggregate}(id string) *{aggregate} {{\n"
        f"\treturn &{aggregate}{{id: id}}\n"
        "}\n\n"
        f"func ({var_name} *{aggregate}) ID() string {{\n"
        f"\tif {var_name} == nil {{\n"
        "\t\treturn \"\"\n"
        "\t}\n"
        f"\treturn {var_name}.id\n"
        "}\n"
    )


def command_gen(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    buf_args = args.buf_args
    if buf_args and buf_args[0] == "--":
        buf_args = buf_args[1:]
    return run_gen(project, "required", buf_args)


def command_doctor(args: argparse.Namespace) -> int:
    project = Path(args.project_dir).resolve()
    errors: list[str] = []
    warnings: list[str] = []
    try:
        module = read_module(project)
    except ValueError as err:
        return fail(str(err), 2)

    for required in ["buf.yaml", "buf.gen.yaml"]:
        if not (project / required).exists():
            errors.append(f"missing {required}")
    if (project / "config" / "config.go").exists() or (project / "config" / "config.yaml").exists():
        errors.append("stale top-level config/config.go or config/config.yaml found; use config/<svc>/")
    if (project / "internal" / "infra").exists():
        errors.append("stale internal/infra found; project-side resource wrappers now live under internal/<svc>/repository")
    if (project / "common").exists():
        for path in (project / "common").rglob("*.go"):
            if not path.name.endswith((".pb.go", "_grpc.pb.go")):
                errors.append(f"common/ contains hand-written Go file: {path.relative_to(project)}")
    if (project / "internal").exists():
        svcs = {p.name for p in (project / "internal").iterdir() if p.is_dir()}
        for svc in svcs:
            if not (project / "config" / svc / "config.go").exists():
                errors.append(f"missing config/{svc}/config.go")
            if (project / "internal" / svc / "domain" / "repository.go").exists():
                errors.append(f"stale internal/{svc}/domain/repository.go found; use domain/adapter.go")
            if (project / "internal" / svc / "repository" / "repository.go").exists():
                errors.append(f"stale internal/{svc}/repository/repository.go found; use repository/app or repository/domain")
        for path in (project / "internal").rglob("*.go"):
            rel = path.relative_to(project)
            parts = rel.parts
            if len(parts) < 3:
                continue
            own_svc = parts[1]
            text = path.read_text(encoding="utf-8")
            for svc in svcs:
                if svc != own_svc and f'"{module}/internal/{svc}/' in text:
                    errors.append(f"{rel} imports another svc internal package: {svc}")
    if (project / "cmd").exists():
        for main in (project / "cmd").glob("*/main.go"):
            text = main.read_text(encoding="utf-8")
            if "app.NewApplication" in text and "app.WithEndpoint(" not in text:
                warnings.append(f"{main.relative_to(project)} creates an Application without endpoints")

    for item in warnings:
        warn(item)
    if errors:
        for item in errors:
            print(f"error: {item}", file=sys.stderr)
        return 1
    info("doctor passed" if not warnings else "doctor passed with warnings")
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="modular skill helper CLI")
    sub = parser.add_subparsers(dest="command", required=True)

    p = sub.add_parser("init", help="create a downstream modular project shell")
    p.add_argument("project")
    p.add_argument("topology", nargs="?", default="single", choices=sorted(VALID_TOPOLOGIES))
    p.add_argument("--go-version", default=DEFAULT_GO_VERSION)
    p.add_argument("--out", default=".")
    p.add_argument("--modular-path", default=None)
    p.set_defaults(func=command_init)

    p = sub.add_parser("gen", help="run buf generate")
    p.add_argument("--project-dir", default=".")
    p.add_argument("buf_args", nargs=argparse.REMAINDER)
    p.set_defaults(func=command_gen)

    p = sub.add_parser("service", help="add a svc")
    p.add_argument("svc")
    p.add_argument("--surface", default="public")
    p.add_argument("--methods", default="")
    p.add_argument("--method", action="append", default=[])
    p.add_argument("--gen", choices=["auto", "skip", "required"], default="auto")
    p.add_argument("--project-dir", default=".")
    p.set_defaults(func=command_service)

    p = sub.add_parser("surface", help="add an interface surface to an existing svc")
    p.add_argument("svc")
    p.add_argument("surface")
    p.add_argument("--methods", default="")
    p.add_argument("--method", action="append", default=[])
    p.add_argument("--gen", choices=["auto", "skip", "required"], default="auto")
    p.add_argument("--project-dir", default=".")
    p.set_defaults(func=command_surface)

    p = sub.add_parser("method", help="add a pb method and app method file")
    p.add_argument("svc")
    p.add_argument("surface")
    p.add_argument("method_name")
    p.add_argument("--gen", choices=["auto", "skip", "required"], default="auto")
    p.add_argument("--project-dir", default=".")
    p.set_defaults(func=command_method)

    p = sub.add_parser("resource", help="add config and wiring for a svc infrastructure resource")
    p.add_argument("kind", choices=["db", "redis", "storage", "telemetry"])
    p.add_argument("--driver", default="bun", help="db: bun|gorm|mongo")
    p.add_argument("--svc", default=None)
    p.add_argument("--project-dir", default=".")
    p.set_defaults(func=command_resource)

    repo = sub.add_parser("repository", help="repository helper commands")
    repo_sub = repo.add_subparsers(dest="repository_command", required=True)
    p = repo_sub.add_parser("recommend", help="recommend app/domain repository placement and scaffold command")
    p.add_argument("svc")
    p.add_argument("surface", nargs="?", default="public")
    p.add_argument("--aggregate", required=True)
    p.add_argument("--feature", default="")
    p.add_argument("--complexity", choices=["auto", "simple", "domain"], default="auto")
    p.add_argument("--queries", default="")
    p.add_argument("--commands", default="")
    p.add_argument("--query", action="append", default=[])
    p.add_argument("--command", action="append", default=[])
    p.add_argument("--json", action="store_true")
    p.add_argument("--project-dir", default=".")
    p.set_defaults(func=command_repository_recommend)
    for mode, handler, help_text in [
        ("app", command_repository_app, "generate app adapter ports and repository/app stubs"),
        ("domain", command_repository_domain, "generate domain ports and repository/domain stubs"),
    ]:
        p = repo_sub.add_parser(mode, help=help_text)
        p.add_argument("svc")
        if mode == "app":
            p.add_argument("surface")
        p.add_argument("--aggregate", required=True)
        p.add_argument("--queries", default="")
        p.add_argument("--commands", default="")
        p.add_argument("--query", action="append", default=[])
        p.add_argument("--command", action="append", default=[])
        p.add_argument("--force", action="store_true")
        p.add_argument("--project-dir", default=".")
        p.set_defaults(func=handler)

    p = sub.add_parser("doctor", help="read-only project convention checks")
    p.add_argument("--project-dir", default=".")
    p.set_defaults(func=command_doctor)
    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    raise SystemExit(main())
