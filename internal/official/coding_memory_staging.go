package official

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"path/filepath"

	"github.com/zwhu/memlane/internal/components"
)

// StageCodingMemory prepares signed assets beside the current components.
// It never opens a business database, saves settings/cache, starts a process,
// installs an Agent plugin or grants a project. Activation follows a verified
// consistent backup and explicit lifecycle handoff.
func (c *Controller) StageCodingMemory(ctx context.Context, notify func(Status)) (map[string]components.Installed, error) {
	c.gate.Lock()
	defer c.gate.Unlock()
	key, err := base64.StdEncoding.DecodeString(c.Config.PublicKey)
	if err != nil {
		return nil, err
	}
	m, err := components.New(filepath.Join(c.Root, "runtime"), ed25519.PublicKey(key), 1, c.Config.AllowLocalTestSource)
	if err != nil {
		return nil, err
	}
	feed, err := m.PreviewFeed(ctx, c.Config.Source)
	if err != nil {
		return nil, err
	}
	selected := map[string]components.Descriptor{}
	for _, d := range feed.Components {
		selected[d.ID] = d
	}
	if selected["cognee-service"].Version != CodingMemoryComponentVersion || selected["cognee-codex-plugin"].Version != "1.8.0" || selected["cognee-bge-zh"].Version != "1.5" {
		return nil, errors.New("coding_memory_component_pair_required")
	}
	installed := map[string]components.Installed{}
	for _, id := range []string{"cognee-service", "cognee-codex-plugin", "cognee-bge-zh"} {
		d := selected[id]
		item, err := m.Prepare(ctx, d, func(p components.Progress) {
			if notify != nil {
				notify(Status{Phase: p.Phase, Message: "正在准备候选组件，当前服务保持运行", Component: &components.PreparationReceipt{Progress: p, ExpectedBytes: d.UnpackedBytes}})
			}
		})
		if err != nil {
			return nil, err
		}
		installed[id] = item
		if notify != nil {
			receipt := components.InstalledReceipt(ctx, d, item)
			notify(Status{Phase: "staged", Message: "候选组件已校验，尚未切换运行", Component: &receipt})
		}
	}
	settings := Settings{}
	if coding, err := c.codingMemorySettings(installed, &settings); err != nil || !coding {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("coding_memory_component_pair_required")
	}
	return installed, nil
}
