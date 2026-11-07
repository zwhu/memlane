#!/bin/sh
set -eu
MEMLANE_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
MEMLANE_CLI="$MEMLANE_ROOT/.local/sources/cognee-rs-151905c302fc9b5b9aad88d6b13931c09f0eac30/crates/cli"
mkdir -p "$MEMLANE_CLI/src/bin/memlane-cognee-worker"
cp "$MEMLANE_ROOT/engine/rust/worker.rs" "$MEMLANE_CLI/src/bin/memlane-cognee-worker/main.rs"
cp "$MEMLANE_ROOT/engine/rust/backend.rs" "$MEMLANE_CLI/src/bin/memlane-cognee-worker/backend.rs"
exec sh "$MEMLANE_ROOT/scripts/build-engine.sh" --bin memlane-cognee-worker
