#!/bin/sh
set -eu
MEMLANE_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
MEMLANE_CLI="$MEMLANE_ROOT/.local/sources/cognee-rs-151905c302fc9b5b9aad88d6b13931c09f0eac30/crates/cli"
test -f "$MEMLANE_CLI/Cargo.toml"
# Add an experiment binary to the ignored source tree without changing upstream
# dependency resolution. Its maintained source is the file in engine/rust/.
mkdir -p "$MEMLANE_CLI/src/bin"
cp "$MEMLANE_ROOT/engine/rust/embedding-probe.rs" "$MEMLANE_CLI/src/bin/memlane-embedding-probe.rs"
exec sh "$MEMLANE_ROOT/scripts/build-engine.sh" --bin memlane-embedding-probe
