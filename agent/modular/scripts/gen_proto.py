#!/usr/bin/env python3
"""modular skill: gen_proto

Regenerate `common/` from domain-partitioned `proto/`. Mechanical backend for
the `gen` command.

Usage:
    gen_proto.py [--project-dir <dir>] [-- buf args...]

Runs `buf generate` in the project directory. Passes any extra args after `--`
through to buf. Requires the `buf` CLI on PATH (install via
`go install github.com/bufbuild/buf/cmd/buf@latest` or `bufbuild/buf` releases).

Contract: `common/` is pure protoc output. With paths=source_relative,
`proto/<domain>/<file>.proto` generates to `common/<domain>/<file>.pb.go`.
Never hand-edit generated files.
"""

from __future__ import annotations

import argparse
import shutil
import subprocess
import sys
from pathlib import Path


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Regenerate proto code into common/.")
    parser.add_argument("--project-dir", default=".", help="project root containing buf.yaml")
    rest, buf_args = parser.parse_known_args(argv)
    # Allow `--`-separated passthrough: argparse leaves them attached; split manually.
    raw = sys.argv[1:]
    if "--" in raw:
        buf_args = raw[raw.index("--") + 1:]

    project = Path(rest.project_dir).resolve()
    if not (project / "buf.yaml").exists():
        print(f"error: no buf.yaml in {project}", file=sys.stderr)
        return 2
    if shutil.which("buf") is None:
        print("error: `buf` not found on PATH. Install: go install github.com/bufbuild/buf/cmd/buf@latest",
              file=sys.stderr)
        return 127

    cmd = ["buf", "generate", *buf_args]
    print("==> " + " ".join(cmd) + f"  (cwd={project})")
    completed = subprocess.run(cmd, cwd=project)
    if completed.returncode != 0:
        print(f"buf generate failed (exit {completed.returncode})", file=sys.stderr)
        return completed.returncode

    print("==> common/ regenerated. Remember: never hand-edit .pb.go or _grpc.pb.go files.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
