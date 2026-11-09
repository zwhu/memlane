package official

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

// EnableCurrentKnowledge installs only the Memlane current-version reader in
// this App-owned Codex home. Official hooks and historical material stay in
// their own data domain; user edits never trigger an unapproved write-back.
func (c *Controller) EnableCurrentKnowledge(ctx context.Context, d AgentScope, connection records.Connection, serviceRoot, bridge string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !connection.Enabled || connection.Project != d.Project || connection.Generation < 1 || !filepath.IsAbs(serviceRoot) || !filepath.IsAbs(bridge) {
		return errors.New("approved current project connection and bridge required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, busy := c.tasks[d.Project]; busy {
		return errors.New("finish the active task before enabling current knowledge")
	}
	stored, err := c.LoadAgent(d.Project)
	if err != nil || stored != d {
		return errors.New("approved official Agent identity changed")
	}
	path := filepath.Join(d.Home, ".codex", "config.toml")
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) > 1024*1024 {
		return errors.New("bounded App-owned Codex configuration required")
	}
	var config map[string]any
	if err = toml.Unmarshal(raw, &config); err != nil {
		return err
	}
	servers, _ := config["mcp_servers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	args := []string{"mcp", "--root", serviceRoot, "--connection", connection.ID, "--generation", fmt.Sprint(connection.Generation), "--workspace", d.Workspace, "--usage-channel", "daily"}
	entry := map[string]any{"command": bridge, "args": args}
	if previous := servers["memlane"]; previous != nil {
		old, _ := json.Marshal(previous)
		want, _ := json.Marshal(entry)
		if string(old) != string(want) {
			return errors.New("current-knowledge bridge changed; reconnect explicitly")
		}
	}
	servers["memlane"] = entry
	config["mcp_servers"] = servers
	config["developer_instructions"] = "本项目由 Memlane 管理当前有效知识。回答项目约定、偏好、经验或复用先前结论时，先调用 Memlane memory_search，必要时 memory_get，使用其当前正文、版本和适用范围。官方 Cognee 插件继续负责本轮采集，其召回材料是保留的历史出处；历史材料与当前知识冲突时，以 Memlane 当前版本为准并说明出处。读取失败请明确报告，不能把历史材料冒称当前知识，也不能猜测或伪造采用。新增采集按官方流程，不修改或覆盖既有知识。"
	encoded, err := toml.Marshal(config)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".memlane-current-")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if err = temporary.Chmod(0600); err == nil {
		_, err = temporary.Write(encoded)
	}
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(temporary.Name(), path); err != nil {
		return err
	}
	root, _ := c.agentRoot(d.Project)
	return serviceipc.WriteJSON(filepath.Join(root, "current-knowledge.json"), map[string]any{"connection": connection.ID, "generation": connection.Generation, "project": connection.Project, "workspace": d.Workspace, "current_version_reader": "Memlane", "official_history_reader": "Cognee", "write_back": false})
}
