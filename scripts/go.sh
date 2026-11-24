#!/bin/sh
set -eu
MEMLANE_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
MEMLANE_GO="$MEMLANE_ROOT/.local/toolchains/go-1.27.4/go/bin/go"
if [ ! -x "$MEMLANE_GO" ]; then
    echo "请先运行 python3 scripts/bootstrap.py" >&2
    exit 1
fi
export GOPATH="$MEMLANE_ROOT/.local/gopath"
export GOCACHE="$MEMLANE_ROOT/.cache/go-build"
export GOTOOLCHAIN=local
export CGO_ENABLED="${CGO_ENABLED:-0}"
export PATH="$(dirname -- "$MEMLANE_GO"):$PATH"
# Build archives contain historical Go snippets, not active application packages.
# A nested module boundary preserves those artifacts while excluding them from ./....
mkdir -p "$MEMLANE_ROOT/build"
if [ ! -e "$MEMLANE_ROOT/build/go.mod" ]; then
    printf 'module memlane.local/build-artifacts\n\ngo 1.27.4\n' > "$MEMLANE_ROOT/build/go.mod"
fi
cd "$MEMLANE_ROOT"
python3 "$MEMLANE_ROOT/scripts/task.py" -- python3 "$MEMLANE_ROOT/scripts/mygo_sdk.py" --exec "$MEMLANE_GO" "$@"
