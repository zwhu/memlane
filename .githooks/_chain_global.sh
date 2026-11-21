#!/bin/sh
set -eu

HOOK_NAME="${1:?missing hook name}"
shift

if [ "${MEMLANE_GLOBAL_HOOK_CHAINED:-0}" = "1" ]; then
    exit 0
fi

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
GLOBAL_HOOKS_PATH="$(git config --global --get core.hooksPath 2>/dev/null || git config --system --get core.hooksPath 2>/dev/null || true)"
[ -n "$GLOBAL_HOOKS_PATH" ] || exit 0

case "$GLOBAL_HOOKS_PATH" in
    \~/*) GLOBAL_HOOKS_PATH="$HOME/${GLOBAL_HOOKS_PATH#\~/}" ;;
    \~) GLOBAL_HOOKS_PATH="$HOME" ;;
esac
while [ "${GLOBAL_HOOKS_PATH%/}" != "$GLOBAL_HOOKS_PATH" ] && [ "$GLOBAL_HOOKS_PATH" != "/" ]; do
    GLOBAL_HOOKS_PATH="${GLOBAL_HOOKS_PATH%/}"
done

LOCAL_HOOKS_DIR="$REPO_ROOT/.githooks"
RESOLVED_GLOBAL="$GLOBAL_HOOKS_PATH"
RESOLVED_LOCAL="$LOCAL_HOOKS_DIR"
if [ -d "$GLOBAL_HOOKS_PATH" ]; then
    RESOLVED_GLOBAL="$(cd "$GLOBAL_HOOKS_PATH" 2>/dev/null && pwd -P || printf '%s' "$GLOBAL_HOOKS_PATH")"
fi
if [ -d "$LOCAL_HOOKS_DIR" ]; then
    RESOLVED_LOCAL="$(cd "$LOCAL_HOOKS_DIR" 2>/dev/null && pwd -P || printf '%s' "$LOCAL_HOOKS_DIR")"
fi
[ "$RESOLVED_GLOBAL" != "$RESOLVED_LOCAL" ] || exit 0

TARGET_HOOK="$RESOLVED_GLOBAL/$HOOK_NAME"
if [ -x "$TARGET_HOOK" ]; then
    MEMLANE_GLOBAL_HOOK_CHAINED=1 "$TARGET_HOOK" "$@"
fi
