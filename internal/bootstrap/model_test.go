package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func modelFixture(t *testing.T) (string, ModelSpec) {
	t.Helper()
	root := t.TempDir()
	data := []byte("synthetic model fixture")
	h := sha256.Sum256(data)
	s := ModelSpec{Schema: 1, Model: "synthetic/model", Dimensions: 3, Repositories: []ModelRepository{{Repo: "synthetic/model", Revision: strings.Repeat("a", 40), Files: []ModelFile{{Name: "model.onnx", SHA256: hex.EncodeToString(h[:]), DownloadBytes: int64(len(data)), URL: "https://invalid.example.test/no-network"}}}}}
	p := filepath.Join(modelSnapshot(root, s.Repositories[0]), "model.onnx")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	return root, s
}
func TestModelFilesArePinnedAndTamperingIsDetected(t *testing.T) {
	root, spec := modelFixture(t)
	if err := VerifyModelFiles(context.Background(), root, spec); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(modelSnapshot(root, spec.Repositories[0]), "model.onnx")
	if err := os.WriteFile(p, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyModelFiles(context.Background(), root, spec); err == nil {
		t.Fatal("modified model accepted")
	}
}
func TestCompatibleExternalModelIsNeverCopiedOrClaimed(t *testing.T) {
	root, spec := modelFixture(t)
	destination := filepath.Join(t.TempDir(), "must-not-create")
	d := ModelDriver{Spec: spec, ExistingRoot: root}
	if err := d.Install(context.Background(), Requirement{ID: "embedding", Digest: spec.Digest(), Destination: destination}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("external model was copied or ownership changed")
	}
}
