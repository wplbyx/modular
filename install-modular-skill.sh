#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
SOURCE="$SCRIPT_DIR/agent/modular"
TARGET="${1:-$HOME/.claude/skills/modular}"

die() {
  printf 'Error: %s\n' "$1" >&2
  exit 1
}

canonical_dir() {
  [ -d "$1" ] || return 1
  (cd "$1" && pwd -P)
}

resolve_link_target_dir() {
  local link_path="$1"
  local raw_target
  local link_dir

  raw_target="$(readlink "$link_path")"
  case "$raw_target" in
    /*)
      canonical_dir "$raw_target"
      ;;
    *)
      link_dir="$(dirname "$link_path")"
      canonical_dir "$link_dir/$raw_target"
      ;;
  esac
}

[ -d "$SOURCE" ] || die "source skill directory not found: $SOURCE"
[ -f "$SOURCE/SKILL.md" ] || die "source skill is missing SKILL.md: $SOURCE/SKILL.md"

SOURCE_REAL="$(canonical_dir "$SOURCE")"
TARGET_PARENT="$(dirname "$TARGET")"

mkdir -p "$TARGET_PARENT"

if [ -e "$TARGET" ] || [ -L "$TARGET" ]; then
  if [ -L "$TARGET" ]; then
    TARGET_REAL="$(resolve_link_target_dir "$TARGET" || true)"
    if [ -n "${TARGET_REAL:-}" ] && [ "$TARGET_REAL" = "$SOURCE_REAL" ]; then
      printf 'modular skill is already installed:\n  %s -> %s\n' "$TARGET" "$SOURCE_REAL"
      exit 0
    fi
  fi

  cat >&2 <<EOF
Error: target already exists and is not the expected symlink:
  $TARGET

Remove it manually or pass a different target path.
EOF
  exit 1
fi

ln -s "$SOURCE_REAL" "$TARGET"

cat <<EOF
Installed modular skill:
  $TARGET -> $SOURCE_REAL

To update it later, run git pull in this repository.
EOF
