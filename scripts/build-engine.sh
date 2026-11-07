#!/bin/sh
set -eu
MEMLANE_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
MEMLANE_SHA=151905c302fc9b5b9aad88d6b13931c09f0eac30
MEMLANE_SOURCE="$MEMLANE_ROOT/.local/sources/cognee-rs-$MEMLANE_SHA"
export CARGO_HOME="$MEMLANE_ROOT/.local/cargo"
export RUSTUP_HOME="$MEMLANE_ROOT/.local/rustup"
export CARGO_TARGET_DIR="$MEMLANE_ROOT/.cache/cognee-target"
export CARGO_BUILD_JOBS=4
export CARGO_PROFILE_RELEASE_DEBUG=0
export CARGO_PROFILE_RELEASE_STRIP=symbols
export ORT_CACHE_DIR="$MEMLANE_ROOT/.cache/ort"
export PATH="$CARGO_HOME/bin:$MEMLANE_ROOT/.local/build-tools/cmake/data/bin:$MEMLANE_ROOT/.local/toolchains/protoc-33.1/bin:$PATH"
if [ ! -f "$MEMLANE_SOURCE/Cargo.toml" ]; then
    echo "缺少固定 Cognee-RS 源码，请先按 docs/development.md 准备。" >&2
    exit 1
fi
python3 "$MEMLANE_ROOT/scripts/apply-engine-patches.py"
cd "$MEMLANE_SOURCE"
if ! cmp -s "$MEMLANE_ROOT/deps/cognee-rs/Cargo.lock" Cargo.lock; then
    # Upstream ignores its lockfile; use this experiment's checked-in resolution.
    cp "$MEMLANE_ROOT/deps/cognee-rs/Cargo.lock" Cargo.lock
fi
exec cargo +1.91.1 build --locked --release -p cognee-cli --no-default-features \
    --features sqlite,ladybug,lancedb,onnx-dynamic,hf-tokenizer "$@"
