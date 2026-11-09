package official

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/pelletier/go-toml/v2"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestCurrentKnowledgeReaderPreservesOfficialConfigAndRejectsChangedBinding(t *testing.T) {
	c := &Controller{Root: t.TempDir()}
	project := uuid.NewString()
	root, err := c.agentRoot(project)
	if err != nil {
		t.Fatal(err)
	}
	d := AgentScope{Project: project, Home: filepath.Join(root, "home"), Workspace: t.TempDir()}
	configDir := filepath.Join(d.Home, ".codex")
	if err = os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = serviceipc.WriteJSON(filepath.Join(root, "scope.json"), d); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(configDir, "config.toml")
	if err = os.WriteFile(path, []byte("model = 'existing-model'\n[features]\nhooks = true\n[plugins.'cognee@cognee']\nenabled = true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	connection := records.Connection{ID: uuid.NewString(), Project: project, Generation: 1, Enabled: true}
	serviceRoot, bridge := t.TempDir(), filepath.Join(t.TempDir(), "memlane")
	if err = c.EnableCurrentKnowledge(context.Background(), d, connection, serviceRoot, bridge); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err = toml.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if config["model"] != "existing-model" || config["features"].(map[string]any)["hooks"] != true || config["plugins"] == nil || config["developer_instructions"] == "" {
		t.Fatal("current reader changed the official plugin or model")
	}
	if err = c.EnableCurrentKnowledge(context.Background(), d, connection, serviceRoot, bridge); err != nil {
		t.Fatal("same binding should replay", err)
	}
	connection.Generation++
	if err = c.EnableCurrentKnowledge(context.Background(), d, connection, serviceRoot, bridge); err == nil {
		t.Fatal("changed generation silently replaced a current reader")
	}
	after, _ := os.ReadFile(path)
	if !reflect.DeepEqual(raw, after) {
		t.Fatal("rejected binding changed config")
	}
}
