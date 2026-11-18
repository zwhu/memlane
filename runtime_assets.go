package memlane

import "embed"

// RuntimeAssets ships the small Memlane adapters and pinned manifests. Python,
// the Cognee SDK, native dependencies and embedding weights stay external.
// Test modules are excluded when materializing a runtime resource tree.
//
//go:embed engine/*.json engine/python/*.py engine/python/*.lock engine/patches/*.py engine/patches/*.json engine/prompts/* engine/agents/pi/*.ts engine/agents/pi/LICENSE.pi-cognee engine/agents/opencode/*.ts scripts/dev.py scripts/build_project_plugin.py scripts/task.py scripts/disk-space.py
var RuntimeAssets embed.FS
