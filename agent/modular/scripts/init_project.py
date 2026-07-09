#!/usr/bin/env python3
"""modular skill: init_project

Create a downstream project shell that depends on the `modular` library.
This is the mechanical, script-driven backend for the `init` command.

Usage:
    init_project.py <project> [single|service] [--go-version 1.26.0] [--out <dir>]

Default topology is `single` (one shared cmd). `service` topology creates a
cmd-per-domain layout (domains are added later by the `service` command).

Produces:
    go.mod, buf.yaml, buf.gen.yaml, Makefile, proto/ common/ internal/
    config/config.go, config/config.yaml, and a cmd/ entry. Generated common/
    mirrors proto/<domain>/... via paths=source_relative.

The project depends on `github.com/wplbyx/modular` via a go.mod replace pointing at the path
passed via --modular-path (defaults to the sibling ../../.. from this skill so
projects generated inside the repo resolve locally). Downstream users will
edit the replace directive to a real path or remove it for publishing.
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

VALID_TOPOLOGY = {"single", "service"}
DEFAULT_GO_VERSION = "1.26.0"

GOMOD = """\
module {project}

go {go_version}

require github.com/wplbyx/modular v0.0.0

// Points at the local modular checkout. Edit or remove when publishing.
replace github.com/wplbyx/modular => {modular_path}
"""

BUF_YAML = """\
version: v2
modules:
  - path: proto
lint:
  use:
    - STANDARD
"""

BUF_GEN_YAML = """\
version: v2
managed:
  enabled: true
plugins:
  - remote: buf.build/protocolbuffers/go
    out: common
    opt:
      - paths=source_relative
  - remote: buf.build/grpc/go
    out: common
    opt:
      - paths=source_relative
      - require_unimplemented_servers=false
"""

MAKEFILE = """\
.PHONY: gen build tidy check

gen:
\tbuf generate

build:
\tgo build ./...

tidy:
\tgo mod tidy

check:
\tgo vet ./...
\tgofmt -l .
"""

# config.yaml mirrors config.* strong types (references/config.md).
CONFIG_YAML = """\
application:
  Name: {project}
  Mode: dev
  Version: v0.1.0
  ShutdownTimeout: 10s

http:
  Host: "0.0.0.0"
  Port: 18080

grpc:
  Host: "0.0.0.0"
  Port: 19090

logging:
  Level: info
  Output:
    - console
"""

CONFIG_GO = """\
package config

import modularconfig "github.com/wplbyx/modular/packages/config"

type Config struct {
\tmodularconfig.Application `mapstructure:"application,squash"`
\tHTTP    modularconfig.HTTP    `mapstructure:"http"`
\tGRPC    modularconfig.GRPC    `mapstructure:"grpc"`
\tLogging modularconfig.Logging `mapstructure:"logging"`
}

func Load(paths ...string) (*Config, error) {
\tcfg := new(Config)
\tif len(paths) == 0 {
\t\tpaths = []string{"./config"}
\t}
\terr := modularconfig.InitConfigure(cfg,
\t\tmodularconfig.WithConfigFile("config", "yaml", paths...),
\t)
\treturn cfg, err
}
"""

# Single-topology main: one Application holds every domain's endpoints.
# Domains are added by the `service` command via the REGISTER markers.
MAIN_SINGLE = """\
package main

import (
\t"context"
\t"fmt"
\t"os"
\t"os/signal"
\t"syscall"

\tprojectconfig "{project}/config"

\t"github.com/wplbyx/modular/packages/app"
\t"github.com/wplbyx/modular/packages/core"
\thttpserver "github.com/wplbyx/modular/packages/transport/server/http"
)

func main() {{
\tctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
\tdefer cancel()

\tcfg, err := projectconfig.Load("./config")
\tif err != nil {{
\t\tfmt.Printf("load config failed: %v\\n", err)
\t\tos.Exit(1)
\t}}

\thttpServer, err := httpserver.NewServer(&cfg.HTTP)
\tif err != nil {{
\t\tfmt.Printf("create http server failed: %v\\n", err)
\t\tos.Exit(1)
\t}}

\t// <REGISTER-ROUTES> - the `service` command adds httpServer.RegisterRoute(...) calls here.

\tnode := core.NewServiceNode(cfg.Name, cfg.Version,
\t\tcore.Transport{{Protocol: "http", Address: core.NormalizeHost(cfg.HTTP.Host), Port: cfg.HTTP.Port, HealthPath: "/health"}},
\t)

\tapplication, err := app.NewApplication(ctx, &cfg.Application,
\t\tapp.WithEndpoint(httpServer),
\t\tapp.WithServiceNode(node),
\t\t// <REGISTER-RESOURCES> - the `resource` command adds app.WithResource(...) calls here.
\t)
\tif err != nil {{
\t\tfmt.Printf("create application failed: %v\\n", err)
\t\tos.Exit(1)
\t}}

\tif err := application.Run(); err != nil {{
\t\tfmt.Printf("application exited: %v\\n", err)
\t}}
}}
"""

# Service-topology: one cmd per domain. The project-level cmd below is a
# placeholder; each `service <domain>` creates its own cmd/<domain>/main.go.
MAIN_SERVICE_ROOT = """\
// Topology: service. Each domain has its own cmd/<domain>/main.go created by
// the `service` command. This file is intentionally a no-op placeholder so
// `go build ./...` succeeds before any service is added.
package main

func main() {{}}
"""

KEEP = ".gitkeep"


def write(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Initialize a modular downstream project shell.")
    parser.add_argument("project", help="project name (also the go module name)")
    parser.add_argument("topology", nargs="?", default="single", choices=sorted(VALID_TOPOLOGY),
                        help="project topology (default: single)")
    parser.add_argument("--go-version", default=DEFAULT_GO_VERSION, help="go directive version")
    parser.add_argument("--out", default=".", help="output directory (default: current dir)")
    parser.add_argument("--modular-path", default=None,
                        help="path for the go.mod replace directive (default: inferred)")
    args = parser.parse_args(argv)

    root = Path(args.out).resolve() / args.project
    if root.exists():
        print(f"error: {root} already exists", file=sys.stderr)
        return 1

    # Default replace path: three levels up from this script lands at the repo
    # root when the skill sits at <repo>/agent/modular/scripts.
    modular_path = args.modular_path or str(Path(__file__).resolve().parents[3])

    print(f"==> Creating {root}")
    write(root / "go.mod", GOMOD.format(project=args.project, go_version=args.go_version,
                                        modular_path=modular_path.replace("\\", "/")))
    write(root / "buf.yaml", BUF_YAML)
    write(root / "buf.gen.yaml", BUF_GEN_YAML)
    write(root / "Makefile", MAKEFILE)
    write(root / "proto" / KEEP, "")
    write(root / "common" / KEEP, "")
    write(root / "internal" / KEEP, "")
    write(root / "config" / "config.go", CONFIG_GO)
    write(root / "config" / "config.yaml", CONFIG_YAML.format(project=args.project))

    cmd_dir = root / "cmd" / (args.project if args.topology == "single" else "root")
    if args.topology == "single":
        write(cmd_dir / "main.go", MAIN_SINGLE.format(project=args.project))
    else:
        write(cmd_dir / "main.go", MAIN_SERVICE_ROOT.format())

    print(f"==> Initialized '{args.project}' (topology={args.topology})")
    print("    Next: cd " + args.project + " && go mod tidy")
    print("    Then: add a domain with the `service <domain>` command (run `gen` after)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
