package bootstrap

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func sourceFixture(t *testing.T) (archive, source, digest string) {
	t.Helper()
	root := t.TempDir()
	archive, source = filepath.Join(root, "source.tar.gz"), filepath.Join(root, "source")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "pax_global_header", Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": "fixed upstream commit"}}); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"pyproject.toml": "version='1.6.7'", "cognee/__init__.py": "# pinned upstream"} {
		if err := tw.WriteHeader(&tar.Header{Name: "upstream/" + name, Mode: 0600, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(source, name)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(archive)
	h := sha256.Sum256(b)
	return archive, source, hex.EncodeToString(h[:])
}
func TestFixedSourceRequiresArchiveAndExactFiles(t *testing.T) {
	archive, source, digest := sourceFixture(t)
	if err := VerifySource(archive, source, digest, []string{"cognee"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "cognee/__init__.py"), []byte("# modified but same version"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := VerifySource(archive, source, digest, []string{"cognee"}); err == nil {
		t.Fatal("version-equivalent mutation accepted")
	}
}
func TestFixedSourceRejectsExtraModulesAndMissingPackages(t *testing.T) {
	archive, source, digest := sourceFixture(t)
	if err := VerifySource(archive, source, digest, []string{"cognee", "missing"}); err == nil {
		t.Fatal("missing package accepted")
	}
	if err := os.WriteFile(filepath.Join(source, "cognee/injected.py"), []byte("# extra"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := VerifySource(archive, source, digest, []string{"cognee"}); err == nil {
		t.Fatal("extra module accepted")
	}
}
func TestFixedSourceRejectsLinkedPackageAndWrongDigest(t *testing.T) {
	archive, source, digest := sourceFixture(t)
	if err := VerifySource(archive, source, "wrong", []string{"cognee"}); err == nil {
		t.Fatal("wrong digest accepted")
	}
	target := filepath.Join(source, "cognee/__init__.py")
	if err := os.Rename(target, target+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target+".original", target); err != nil {
		t.Fatal(err)
	}
	if err := VerifySource(archive, source, digest, []string{"cognee"}); err == nil {
		t.Fatal("linked package accepted")
	}
}
