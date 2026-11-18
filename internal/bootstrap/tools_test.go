package bootstrap

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func toolArchive(t *testing.T, headers []tar.Header, bodies []string) (string, string) {
	t.Helper()
	archive := filepath.Join(t.TempDir(), "tool.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for n, h := range headers {
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(bodies[n]))
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			if _, err := tw.Write([]byte(bodies[n])); err != nil {
				t.Fatal(err)
			}
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
	return archive, hex.EncodeToString(h[:])
}

func TestStandaloneToolExtractionPreservesInternalLinksAndExecutable(t *testing.T) {
	archive, _ := toolArchive(t, []tar.Header{{Name: "python/bin/python3.12", Mode: 0755, Typeflag: tar.TypeReg}, {Name: "python/bin/python3", Linkname: "python3.12", Typeflag: tar.TypeSymlink}}, []string{"executable", ""})
	destination := t.TempDir()
	if err := extractTool(context.Background(), archive, destination); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(destination, "bin/python3"))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal(info, err)
	}
}
func TestStandaloneToolRejectsTraversalAndEscapingLinks(t *testing.T) {
	for _, header := range []tar.Header{{Name: "python/../../outside", Typeflag: tar.TypeReg}, {Name: "python/link", Typeflag: tar.TypeSymlink, Linkname: "../../outside"}, {Name: "python/link", Typeflag: tar.TypeSymlink, Linkname: "/outside"}} {
		archive, _ := toolArchive(t, []tar.Header{header}, []string{"bad"})
		if err := extractTool(context.Background(), archive, t.TempDir()); err == nil {
			t.Fatal("unsafe archive accepted", header)
		}
	}
}
func TestStandaloneToolResumesCachedArchiveAndProtectsUnknownRoot(t *testing.T) {
	archive, digest := toolArchive(t, []tar.Header{{Name: "uv-tool/uv", Mode: 0755, Typeflag: tar.TypeReg}}, []string{"#!/bin/sh\necho 'uv 0.12.3 --require-hashes --build-constraints'\n"})
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d := ToolDriver{ID: "uv", Version: "0.12.3", Executable: "uv", Artifact: Artifact{URL: "https://invalid.example.test/no-network", SHA256: digest}}
	r := Requirement{ID: d.ID, Version: d.Version, Source: d.Artifact.URL, Digest: digest, Destination: filepath.Join(root, "uv")}
	if err := os.Mkdir(r.Destination, 0700); err != nil {
		t.Fatal(err)
	}
	if err := d.Install(context.Background(), r); err == nil {
		t.Fatal("unowned directory claimed")
	}
	identity, _ := json.Marshal(struct {
		ID, Version, Executable string
		Artifact                Artifact
	}{d.ID, d.Version, d.Executable, d.Artifact})
	if err := os.WriteFile(filepath.Join(r.Destination, ".memlane-tool.json"), identity, 0600); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(archive)
	if err := os.WriteFile(filepath.Join(r.Destination, "artifact.tar.gz"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := d.Install(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	o, err := d.Probe(context.Background(), r)
	if err != nil || o.Status != Available {
		t.Fatal(o, err)
	}
}

func TestUVProbeFingerprintStableAcrossTemporaryProbeHomes(t *testing.T) {
	uv, err := exec.LookPath("uv")
	if err != nil {
		t.Skip("real uv not installed")
	}
	d := ToolDriver{ID: "uv"}
	first, err := d.probePath(context.Background(), uv)
	if err != nil || first.Status != Available {
		t.Fatal(first, err)
	}
	second, err := d.probePath(context.Background(), uv)
	if err != nil || second != first {
		t.Fatal("same executable changed identity between isolated probes", first, second, err)
	}
}

func TestUVProbeFingerprintChangesWhenExecutableIsReplaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uv")
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'uv 0.12.3'; else echo '--require-hashes --build-constraints'; fi\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	d := ToolDriver{ID: "uv"}
	first, err := d.probePath(context.Background(), path)
	if err != nil || first.Status != Available {
		t.Fatal(first, err)
	}
	if err := os.WriteFile(path, []byte(script+"# replaced binary contents\n"), 0700); err != nil {
		t.Fatal(err)
	}
	second, err := d.probePath(context.Background(), path)
	if err != nil || second.Status != Available || second.Fingerprint == first.Fingerprint {
		t.Fatal("replaced executable retained approved identity", first, second, err)
	}
}
