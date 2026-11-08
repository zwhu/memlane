package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

// This is a filesystem authorization regression, not an official service or
// model fixture. A second principal on one instance must not replace the first.
func TestCogneeCredentialsRetainDatasetBoundary(t *testing.T) {
	root := t.TempDir()
	server := &Server{config: Config{Root: root}}
	instance, a, b := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if err := server.writeCogneeToken(instance, a, "synthetic-project-a"); err != nil {
		t.Fatal(err)
	}
	if err := server.writeCogneeToken(instance, b, "synthetic-project-b"); err != nil {
		t.Fatal(err)
	}
	for dataset, want := range map[string]string{a: "synthetic-project-a", b: "synthetic-project-b"} {
		got, err := server.cogneeToken(instance, dataset)
		if err != nil || got != want {
			t.Fatalf("scope credential changed: got %q, error %v", got, err)
		}
	}
	path := filepath.Join(root, "cognee-auth", instance+"-"+a+".token")
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := server.cogneeToken(instance, a); err == nil {
		t.Fatal("public credential file was accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "cognee-auth", instance+"-"+b+".token"), path); err != nil {
		t.Fatal(err)
	}
	if _, err := server.cogneeToken(instance, a); err == nil {
		t.Fatal("cross-dataset credential symlink was followed")
	}
}
