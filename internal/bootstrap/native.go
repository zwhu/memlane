package bootstrap

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/zwhu/memlane/kit/aiinstaller"
)

// NativeSupport is a pinned, separately downloaded runtime dependency. Only the
// two required OpenSSL libraries and their license are retained from the bottle.
type NativeSupport struct {
	Version        string   `json:"version"`
	Artifact       Artifact `json:"artifact"`
	ArchivePrefix  string   `json:"archive_prefix"`
	DownloadSource string   `json:"-"`
}

func nativeWrapper(root string) string { return filepath.Join(root, "venv/bin/memlane-python") }
func shellLiteral(s string) string     { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func nativeWrapperBody(root string) []byte {
	return []byte("#!/bin/sh\nexport DYLD_LIBRARY_PATH=" + shellLiteral(filepath.Join(root, "native/lib")) + "\nexec " + shellLiteral(filepath.Join(root, "venv/bin/python")) + " \"$@\"\n")
}
func (n NativeSupport) verify(root string) error {
	expected, _ := json.Marshal(n)
	b, err := privateRead(filepath.Join(root, "native/.memlane-native.json"))
	if err != nil {
		return err
	}
	if string(b) != string(expected) {
		return errors.New("native runtime source identity differs")
	}
	// Verify the selected files against the pinned archive, not mutable receipts.
	archive := filepath.Join(root, "native/artifact.tar.gz")
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != n.Artifact.SHA256 {
		return errors.New("native archive digest differs")
	}
	if _, err = f.Seek(0, 0); err != nil {
		return err
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	found := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		rel, ok := n.selected(hdr.Name)
		if !ok {
			continue
		}
		if hdr.Typeflag != tar.TypeReg || found[rel] {
			return errors.New("invalid native archive member")
		}
		actual, err := readNativeFile(filepath.Join(root, "native", rel))
		if err != nil {
			return err
		}
		digest := sha256.New()
		if _, err = io.Copy(digest, tr); err != nil {
			return err
		}
		sum := sha256.Sum256(actual)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), hex.EncodeToString(digest.Sum(nil))) {
			return errors.New("native file digest differs")
		}
		found[rel] = true
	}
	if len(found) != 3 {
		return errors.New("native runtime archive incomplete")
	}
	entries, err := os.ReadDir(filepath.Join(root, "native/lib"))
	if err != nil {
		return err
	}
	if len(entries) != 2 {
		return errors.New("unexpected native library retained")
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || (entry.Name() != "libssl.3.dylib" && entry.Name() != "libcrypto.3.dylib") {
			return errors.New("unexpected native library retained")
		}
	}
	b, err = privateRead(nativeWrapper(root))
	if err != nil {
		return err
	}
	if string(b) != string(nativeWrapperBody(root)) {
		return errors.New("native launcher differs; retained")
	}
	return nil
}
func (n NativeSupport) selected(name string) (string, bool) {
	if path.Clean(name) != name || strings.Contains(name, "\\") {
		return "", false
	}
	rel := strings.TrimPrefix(name, n.ArchivePrefix+"/")
	if rel == name {
		return "", false
	}
	switch rel {
	case "lib/libcrypto.3.dylib", "lib/libssl.3.dylib":
		return rel, true
	case "LICENSE.txt":
		return rel, true
	}
	return "", false
}
func (n NativeSupport) install(ctx context.Context, root string) error {
	progressDetail(ctx, "OpenSSL")
	dir := filepath.Join(root, "native")
	identity, _ := json.Marshal(n)
	if err := aiinstaller.ClaimComponentRoot(dir, ".memlane-native.json", identity); err != nil {
		return err
	}
	archive := filepath.Join(dir, "artifact.tar.gz")
	if n.DownloadSource != "" {
		candidates := NativeCandidates(n.Artifact.URL, n.Artifact.SHA256, n.DownloadSource)
		if _, err := fetchArtifactCandidates(ctx, candidates, func(ArtifactCandidate) string { return archive }); err != nil {
			return err
		}
	} else if err := fetchArchive(ctx, n.Artifact.URL, n.Artifact.SHA256, archive); err != nil {
		return err
	}
	progressStage(ctx, "extracting")
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	found := map[string]bool{}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		rel, ok := n.selected(hdr.Name)
		if !ok {
			continue
		}
		if hdr.Typeflag != tar.TypeReg || found[rel] || hdr.Size > 32<<20 {
			return errors.New("invalid native archive member")
		}
		found[rel] = true
		b, err := io.ReadAll(io.LimitReader(tr, (32<<20)+1))
		if err != nil {
			return err
		}
		target := filepath.Join(dir, rel)
		if previous, err := readNativeFile(target); err == nil {
			if string(previous) != string(b) {
				return errors.New("existing native file differs; retained")
			}
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		if err := atomicPrivateFile(target, b); err != nil {
			return err
		}
	}
	if len(found) != 3 {
		return errors.New("native runtime archive incomplete")
	}
	wrapper := nativeWrapper(root)
	body := nativeWrapperBody(root)
	if old, err := privateRead(wrapper); err == nil {
		if string(old) != string(body) {
			return errors.New("existing native launcher differs; retained")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else {
		if err := atomicPrivateFile(wrapper, body); err != nil {
			return err
		}
	}
	if err := os.Chmod(wrapper, 0700); err != nil {
		return err
	}
	progressStage(ctx, "verifying")
	return n.verify(root)
}

func readNativeFile(p string) ([]byte, error) {
	info, err := os.Lstat(filepath.Dir(p))
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("native parent must be a directory")
	}
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 32<<20 || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("native file must be bounded and private")
	}
	return io.ReadAll(io.LimitReader(f, (32<<20)+1))
}
