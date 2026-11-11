#!/bin/sh
set -eu
MEMLANE_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
MEMLANE_GO="$MEMLANE_ROOT/.local/toolchains/go-1.27.3/go/bin/go"
if [ ! -x "$MEMLANE_GO" ]; then
    echo "请先运行 python3 scripts/bootstrap.py" >&2
    exit 1
fi
export GOPATH="$MEMLANE_ROOT/.local/gopath"
export GOCACHE="$MEMLANE_ROOT/.cache/go-build"
export GOTOOLCHAIN=local
export CGO_ENABLED="${CGO_ENABLED:-0}"
export PATH="$(dirname -- "$MEMLANE_GO"):$PATH"
cd "$MEMLANE_ROOT"
python3 "$MEMLANE_ROOT/scripts/task.py" -- "$MEMLANE_GO" "$@"
