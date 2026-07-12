#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
TARGET_PARENT="$HOME/.claude/skills"
FORCE=0

die() {
  printf 'Error: %s\n' "$1" >&2
  exit 1
}

usage() {
  cat <<EOF
Usage: $0 <name> [-f] [-d DIR]

Install agent/<name> skill as DIR/<name>.

Options:
  -d DIR  Skill parent directory (default: $HOME/.claude/skills)
  -f      Replace an existing <name> symlink
  -h      Show this help
EOF
}

validate_skill_name() {
  case "$1" in
    "" | "." | ".." | */* | *\\*)
      die "invalid skill name: $1"
      ;;
  esac
}

canonical_dir() {
  [ -d "$1" ] || return 1
  (cd "$1" && pwd -P)
}

join_child() {
  case "$1" in
    /)
      printf '/%s\n' "$2"
      ;;
    *)
      printf '%s/%s\n' "${1%/}" "$2"
      ;;
  esac
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

if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
  usage
  exit 0
fi

[ "$#" -gt 0 ] || die "missing skill name"
SKILL_NAME="$1"
shift
validate_skill_name "$SKILL_NAME"

SOURCE="$SCRIPT_DIR/agent/$SKILL_NAME"

while [ "$#" -gt 0 ]; do
  case "$1" in
    -f)
      FORCE=1
      shift
      ;;
    -d)
      [ "$#" -ge 2 ] || die "option -d requires an argument"
      TARGET_PARENT="$2"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    --)
      shift
      [ "$#" -eq 0 ] || die "unexpected argument: $1"
      ;;
    -*)
      die "unknown option: $1"
      ;;
    *)
      die "unexpected argument: $1"
      ;;
  esac
done

[ -n "$TARGET_PARENT" ] || die "target parent directory cannot be empty"

[ -d "$SOURCE" ] || die "source skill directory not found: $SOURCE"
[ -f "$SOURCE/SKILL.md" ] || die "source skill is missing SKILL.md: $SOURCE/SKILL.md"

SOURCE_REAL="$(canonical_dir "$SOURCE")"

mkdir -p "$TARGET_PARENT"
TARGET_PARENT_REAL="$(canonical_dir "$TARGET_PARENT")"
TARGET="$(join_child "$TARGET_PARENT_REAL" "$SKILL_NAME")"

if [ -e "$TARGET" ] || [ -L "$TARGET" ]; then
  if [ -L "$TARGET" ]; then
    TARGET_REAL="$(resolve_link_target_dir "$TARGET" || true)"
    if [ -n "${TARGET_REAL:-}" ] && [ "$TARGET_REAL" = "$SOURCE_REAL" ]; then
      printf '%s skill is already installed:\n  %s -> %s\n' "$SKILL_NAME" "$TARGET" "$SOURCE_REAL"
      exit 0
    fi

    if [ "$FORCE" -eq 1 ]; then
      rm "$TARGET"
    else
      cat >&2 <<EOF
Error: target already exists and is not the expected symlink:
  $TARGET

Use -f to replace an existing symlink, or pass a different parent directory with -d.
EOF
      exit 1
    fi
  else
    cat >&2 <<EOF
Error: target already exists and is not a symlink:
  $TARGET

Refusing to remove a regular file or directory. Remove it manually or pass a different parent directory with -d.
EOF
    exit 1
  fi
fi

ln -s "$SOURCE_REAL" "$TARGET"

cat <<EOF
Installed $SKILL_NAME skill:
  $TARGET -> $SOURCE_REAL

To update it later, run git pull in this repository.
EOF
