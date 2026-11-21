package bootstrap

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestExtractWheelExecutableValidatesEntryAndRejectsTraversalOrSymlink(t *testing.T) {
	dir := t.TempDir()
	whlPath := filepath.Join(dir, "uv.whl")
	f, err := os.Create(whlPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create(pinnedUV0123Arm64WheelEntry)
	if err != nil {
		t.Fatal(err)
	}
	payload := "#!/bin/sh\necho uv 0.12.3\n"
	if _, err := w.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	symHdr := &zip.FileHeader{Name: "uv-0.12.3.data/scripts/symuv", Method: zip.Deflate}
	symHdr.SetMode(os.ModeSymlink | 0777)
	sw, err := zw.CreateHeader(symHdr)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = sw.Write([]byte("/etc/passwd"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(dir, "out", "uv")
	if err := extractWheelExecutable(context.Background(), whlPath, pinnedUV0123Arm64WheelEntry, dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != payload {
		t.Fatalf("got %q err=%v", string(got), err)
	}
	info, err := os.Stat(dest)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("expected 0700 mode, got %v err=%v", info.Mode().Perm(), err)
	}
	for _, bad := range []string{"../uv", "uv-0.12.3.data/scripts/symuv", "uv-0.12.3.data/scripts/missing"} {
		if err := extractWheelExecutable(context.Background(), whlPath, bad, filepath.Join(dir, "bad", filepath.Base(bad))); err == nil {
			t.Fatalf("expected error for zip entry %q", bad)
		}
	}
}

func TestToolDriverMatchesDisplayMirrorURLWhenDownloadSourceConfigured(t *testing.T) {
	official := "https://github.com/astral-sh/uv/releases/download/0.12.3/uv-aarch64-apple-darwin.tar.gz"
	d := ToolDriver{
		ID:             "uv",
		Version:        "0.12.3",
		Executable:     "uv",
		Artifact:       Artifact{URL: official, SHA256: pinnedUV0123Arm64OfficialSHA256},
		DownloadSource: DownloadSourceChina,
	}
	chinaDisplay := DisplaySourceURL("uv", official, pinnedUV0123Arm64OfficialSHA256, DownloadSourceChina)
	if !strings.Contains(chinaDisplay, "pypi.tuna.tsinghua.edu.cn") {
		t.Fatalf("unexpected china display url: %s", chinaDisplay)
	}
	if !d.matches(Requirement{ID: "uv", Version: "0.12.3", Source: chinaDisplay, OfficialSource: official, Digest: pinnedUV0123Arm64OfficialSHA256}) {
		t.Fatal("expected tool driver with DownloadSource=china to match DisplaySourceURL requirement")
	}
	if !d.matches(Requirement{ID: "uv", Version: "0.12.3", Source: official, OfficialSource: official, Digest: pinnedUV0123Arm64OfficialSHA256}) {
		t.Fatal("expected tool driver to still accept official URL requirement")
	}
}
