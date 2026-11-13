package simpledesktop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBufferAdapterVerifiesInputAndUsesExactInstallation(t *testing.T) {
	root := t.TempDir()
	scripts := filepath.Join(root, "scripts")
	if err := os.Mkdir(scripts, 0700); err != nil {
		t.Fatal(err)
	}
	helper := []byte(`import json,os,sys
assert os.path.realpath(os.environ["HOME"]) == os.getcwd()
assert os.environ["COGNEE_ENV_FILE"].endswith("managed.env")
assert "--session-key" in sys.argv and "host-session" in sys.argv
print(json.dumps({"pending": 1, "signature": "reviewed-snapshot"}))
`)
	path := filepath.Join(scripts, "_memlane_policy.py")
	if err := os.WriteFile(path, helper, 0600); err != nil {
		t.Fatal(err)
	}
	helperSHA := fmt.Sprintf("%x", sha256.Sum256(helper))
	receipt, _ := json.Marshal(map[string]any{"files": map[string]string{"scripts/_memlane_policy.py": helperSHA}})
	if err := os.WriteFile(filepath.Join(root, ".memlane-project-access.json"), receipt, 0600); err != nil {
		t.Fatal(err)
	}
	installation := AgentInstallation{PluginDir: root, EnvFile: filepath.Join(root, "managed.env"), Home: root, HelperSHA: helperSHA, ManifestSHA: fmt.Sprintf("%x", sha256.Sum256(receipt))}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	python, _ = filepath.Abs(python)
	result, err := runPendingMaterials(context.Background(), python, installation, ProjectBinding{HostSessionKey: "host-session"}, "dataset", "show", "")
	if err != nil || result.Pending != 1 || result.Signature != "reviewed-snapshot" {
		t.Fatalf("wrong installation result: %#v %v", result, err)
	}
	if err = os.WriteFile(path, []byte("raise Exception('tampered helper must not run')"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = runPendingMaterials(context.Background(), python, installation, ProjectBinding{HostSessionKey: "host-session"}, "dataset", "replay", "reviewed-snapshot")
	if err == nil || !strings.Contains(err.Error(), "文件发生变化") {
		t.Fatal("modified helper was executed")
	}
}

func TestCommonEditorWithdrawsRevokedBodyWithoutClaimingInflightCancel(t *testing.T) {
	a := New(Config{ServiceURL: "http://127.0.0.1:18999"}, nil)
	d := Document{ID: "document", Dataset: "common"}
	a.commonEdit = commonEditorState{Open: true, Busy: true, Existing: &d, Target: Project{Dataset: "common"}, Draft: CommonDraft{Body: "protected body"}, Receipt: CommonWriteReceipt{PipelineRunID: "original-run"}}
	a.handleReadingFailure(Item{ID: d.ID, Dataset: d.Dataset, Kind: "知识"}, fmt.Errorf("official HTTP 403"))
	if a.commonEdit.Open || a.commonEdit.Draft.Body != "" || a.commonEdit.Existing != nil {
		t.Fatal("revoked editor body remains copyable")
	}
	if !a.commonEdit.Busy || a.commonEdit.Receipt.PipelineRunID != "original-run" {
		t.Fatal("revocation pretended inflight operation was cancelled")
	}
}
