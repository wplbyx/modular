#!/usr/bin/env python3
"""Compatibility wrapper for the modular skill `gen` command."""

from __future__ import annotations

import sys

from modular import main


def translate(argv: list[str]) -> list[str]:
    return ["gen", *argv]


if __name__ == "__main__":
    raise SystemExit(main(translate(sys.argv[1:])))
