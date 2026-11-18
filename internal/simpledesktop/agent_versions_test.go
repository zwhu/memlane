package simpledesktop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestAgentVersionsUseConfiguredHostAndDoNotRunUndetectedHost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "selected host")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n[ \"$1\" = --version ] || exit 7\nprintf 'pi 0.84.1\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	var config Config
	b, _ := json.Marshal(map[string]any{"agent_hosts": map[string]any{"pi": map[string]string{"executable": path}}})
	if err := json.Unmarshal(b, &config); err != nil {
		t.Fatal(err)
	}
	rows := []AgentConnection{{Kind: "pi", Detected: true}, {Kind: "pi", Detected: false}}
	loadAgentVersions(context.Background(), config, rows)
	if rows[0].HostVersion != "0.84.1" || rows[1].HostVersion != "" {
		t.Fatalf("wrong host versions: %+v", rows)
	}
}

func TestAgentVersionSurvivesCaptureStatusRefresh(t *testing.T) {
	a := New(Config{}, nil)
	a.integrations.HostVersions = map[string]string{"pi": "0.84.1"}
	row := AgentConnection{ID: "pi", Kind: "pi", Detected: true, Status: "configured_pending_new_session"}
	tt := ui.NewTester(func(c *ui.Context) { a.agentConnectionRow(c, row) }, 1000, 120)
	if !tt.HasText("0.84.1") {
		t.Fatal("capture refresh erased detected host version")
	}
}
