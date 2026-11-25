package memlane

import "embed"

// RuntimeAssets ships the Cognee adapters, pinned manifests, Pi integration,
// prompts, and official graph bundle.
//
//go:embed engine/first-install-components.json engine/coding-memory-components.json engine/embedding-components.json engines/cognee/[^t]*.py engines/cognee/t[^e]*.py engine/agents/pi/*.ts engine/agents/pi/LICENSE.pi-cognee engine/prompts/* web/official-graph/dist/* deps/models/*.json deps/cognee-rs/patches.json deps/cognee-rs/Cargo.lock deps/cognee-rs/onnx-input-contract.patch
var RuntimeAssets embed.FS

// GoEngineAssets supplies the internal Cognee runner for packaged daemon launches.
//
//go:embed engine/coding-memory-components.json engines/cognee/[^t]*.py engines/cognee/t[^e]*.py
var GoEngineAssets embed.FS
