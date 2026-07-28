#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
TARGET_PARENT="$HOME/.claude/skills"
FORCE=0
DEVELOPMENT_LINK=0

die() {
  printf 'Error: %s\n' "$1" >&2
  exit 1
}

usage() {
  cat <<EOF
Usage: $0 <name> [-f] [-l] [-d DIR]

Install agent/<name> as an atomic copy by default.

Options:
  -d DIR  Skill parent directory (default: $HOME/.claude/skills)
  -f      Replace an existing installation with rollback protection
  -l      Install a development symlink instead of a copy
  -h      Show this help
EOF
}

validate_skill_name() {
  case "$1" in
    "" | "." | ".." | */* | *\\*) die "invalid skill name: $1" ;;
  esac
}

canonical_dir() {
  [ -d "$1" ] || return 1
  (cd "$1" && pwd -P)
}

assert_child_path() {
  case "$2" in
    "$1"/*) ;;
    *) die "install path escapes target parent: $2" ;;
  esac
}

find_python() {
  if command -v python >/dev/null 2>&1; then
    command -v python
  elif command -v python3 >/dev/null 2>&1; then
    command -v python3
  else
    die "python is required to run the modular skill self-check"
  fi
}

self_check() {
  local root="$1"
  local cli="$root/scripts/modular.py"
  [ -f "$cli" ] || return 0
  "$(find_python)" "$cli" self-check
}

if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
  usage
  exit 0
fi

[ "$#" -gt 0 ] || die "missing skill name"
SKILL_NAME="$1"
shift
validate_skill_name "$SKILL_NAME"

while [ "$#" -gt 0 ]; do
  case "$1" in
    -f | --force) FORCE=1; shift ;;
    -l | --development-link) DEVELOPMENT_LINK=1; shift ;;
    -d | --directory)
      [ "$#" -ge 2 ] || die "option $1 requires an argument"
      TARGET_PARENT="$2"
      shift 2
      ;;
    -h | --help) usage; exit 0 ;;
    *) die "unexpected argument: $1" ;;
  esac
done

[ -n "$TARGET_PARENT" ] || die "target parent directory cannot be empty"
SOURCE="$SCRIPT_DIR/agent/$SKILL_NAME"
[ -d "$SOURCE" ] || die "source skill directory not found: $SOURCE"
[ -f "$SOURCE/SKILL.md" ] || die "source skill is missing SKILL.md: $SOURCE/SKILL.md"
SOURCE_REAL="$(canonical_dir "$SOURCE")"

mkdir -p "$TARGET_PARENT"
TARGET_PARENT_REAL="$(canonical_dir "$TARGET_PARENT")"
TARGET="$TARGET_PARENT_REAL/$SKILL_NAME"
assert_child_path "$TARGET_PARENT_REAL" "$TARGET"
[ "$SOURCE_REAL" != "$TARGET" ] || die "source and target are the same path: $TARGET"

STAGE="$(mktemp -d "$TARGET_PARENT_REAL/.${SKILL_NAME}.install.XXXXXX")"
BACKUP="$TARGET_PARENT_REAL/.${SKILL_NAME}.backup.$$"
assert_child_path "$TARGET_PARENT_REAL" "$STAGE"
assert_child_path "$TARGET_PARENT_REAL" "$BACKUP"
BACKUP_CREATED=0
TARGET_INSTALLED=0

rollback() {
  local status=$?
  if [ "$status" -ne 0 ]; then
    if [ "$TARGET_INSTALLED" -eq 1 ] && { [ -e "$TARGET" ] || [ -L "$TARGET" ]; }; then
      rm -rf -- "$TARGET"
    fi
    if [ "$BACKUP_CREATED" -eq 1 ] && { [ -e "$BACKUP" ] || [ -L "$BACKUP" ]; }; then
      mv -- "$BACKUP" "$TARGET"
    fi
  fi
  if [ -e "$STAGE" ] || [ -L "$STAGE" ]; then
    rm -rf -- "$STAGE"
  fi
  exit "$status"
}
trap rollback EXIT

if [ "$DEVELOPMENT_LINK" -eq 1 ]; then
  rmdir "$STAGE"
  ln -s "$SOURCE_REAL" "$STAGE"
else
  cp -R "$SOURCE_REAL/." "$STAGE/"
fi
self_check "$STAGE"

if [ -e "$TARGET" ] || [ -L "$TARGET" ]; then
  [ "$FORCE" -eq 1 ] || die "target already exists: $TARGET (use -f to replace it safely)"
  if [ -f "$TARGET" ] && [ ! -L "$TARGET" ]; then
    die "target is a regular file and will not be replaced: $TARGET"
  fi
  [ ! -e "$BACKUP" ] && [ ! -L "$BACKUP" ] || die "backup path already exists: $BACKUP"
  mv -- "$TARGET" "$BACKUP"
  BACKUP_CREATED=1
fi

mv -- "$STAGE" "$TARGET"
TARGET_INSTALLED=1
self_check "$TARGET"

if [ "$BACKUP_CREATED" -eq 1 ]; then
  rm -rf -- "$BACKUP"
  BACKUP_CREATED=0
fi
TARGET_INSTALLED=0
trap - EXIT

if [ "$DEVELOPMENT_LINK" -eq 1 ]; then
  printf 'Installed %s skill (development link):\n  %s -> %s\n' "$SKILL_NAME" "$TARGET" "$SOURCE_REAL"
else
  printf 'Installed %s skill (copy):\n  %s\n' "$SKILL_NAME" "$TARGET"
fi
printf '\nRun this installer again with -f after updating the repository.\n'
