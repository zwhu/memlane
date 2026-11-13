package simpledesktop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCodexConnectionActivationCarriesReviewedHashAndNeverClaimsSessionFromConfig(t *testing.T) {
	root := t.TempDir()
	token := filepath.Join(root, "control-token")
	if err := os.WriteFile(token, []byte("manager-token-at-least-32-characters"), 0600); err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/codex/activate" || r.Method != "POST" {
			t.Error("wrong activation route")
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(`{"revision":18,"codex_connection":{"status":"configured_pending_new_session","new_sessions":0}}`))
	}))
	defer server.Close()
	client := ProjectControlClient{Config: ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}
	var result AccessDirectory
	err := client.request(context.Background(), "POST", "/v1/codex/activate", map[string]any{"expected_revision": 17, "proposed_sha256": "reviewed-original-hash"}, &result)
	if err != nil {
		t.Fatal(err)
	}
	if request["proposed_sha256"] != "reviewed-original-hash" || request["expected_revision"] != float64(17) {
		t.Fatalf("wrong reviewed configuration: %v", request)
	}
	if codexConnectionLabel(*result.CodexConnection) != "配置已启用，等待新会话加载" {
		t.Fatal("configuration was reported as actual host usage")
	}
	if codexConnectionLabel(CodexConnection{Status: "activation_unknown"}) != "原操作结果未确认，请核对原操作" {
		t.Fatal("unknown activation reported as connected")
	}
}
