package simpledesktop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestKnowledgeRecallOfficialIdentityAndDisplay(t *testing.T) {
	token := filepath.Join(t.TempDir(), "control.token")
	if err := os.WriteFile(token, []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/knowledge/recalls" || r.URL.Query().Get("dataset_id") != "dataset" || r.URL.Query().Get("document_id") != "document" || r.Header.Get("Authorization") != "Bearer aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
			t.Error("wrong stats identity")
		}
		json.NewEncoder(w).Encode(KnowledgeRecallStats{Count: 7, StartedAt: "2026-11-14T02:00:00Z"})
	}))
	defer server.Close()
	i := Item{Dataset: "dataset", ID: "document", Kind: "知识"}
	stats, err := (ProjectControlClient{Config: ProjectControlConfig{URL: server.URL, ManagementTokenFile: token}}).knowledgeRecall(context.Background(), i)
	if err != nil {
		t.Fatal(err)
	}
	i.RecallStats = stats
	tt := ui.NewTester(func(c *ui.Context) { knowledgeRecallLabel(c, i) }, 500, 100)
	if !tt.HasText("· 召回 7 次") {
		t.Fatal("count absent")
	}
	i.RecallStats = nil
	tt = ui.NewTester(func(c *ui.Context) { knowledgeRecallLabel(c, i) }, 500, 100)
	if !tt.HasText("· 召回 0 次") {
		t.Fatal("default zero absent")
	}
	i.Kind = "原始记录"
	tt = ui.NewTester(func(c *ui.Context) { knowledgeRecallLabel(c, i) }, 500, 100)
	if tt.HasText("· 召回 0 次") {
		t.Fatal("raw records counted as knowledge")
	}
}
