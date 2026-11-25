package memlane

import "embed"

// RuntimeAssets ships the small Memlane adapters, verified official graph bundle,
// uninstaller, and pinned manifests. Python, the Cognee SDK, native dependencies,
// embedding weights, and Python test modules stay out of the embedded binary.
//
//go:embed engine/*.json engine/python/[^t]*.py engine/python/t[^e]*.py engine/python/user_profiles/*.py engine/python/user_profiles/providers/*.py engine/python/*.lock engine/patches/*.py engine/patches/*.json engine/prompts/* engine/agents/pi/*.ts engine/agents/pi/LICENSE.pi-cognee engine/agents/opencode/*.ts web/official-graph/dist/* scripts/dev.py scripts/dev_recovery.py scripts/local_prepare.py scripts/build_project_plugin.py scripts/task.py scripts/disk-space.py scripts/uninstall.py
var RuntimeAssets embed.FS

// GoEngineAssets supplies the internal Cognee runner for packaged daemon launches.
//
//go:embed engine/coding-memory-components.json engines/cognee/[^t]*.py engines/cognee/t[^e]*.py
var GoEngineAssets embed.FS
