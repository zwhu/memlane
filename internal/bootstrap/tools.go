package bootstrap

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type Artifact struct {
	URL           string `json:"url"`
	SHA256        string `json:"sha256"`
	DownloadBytes int64  `json:"download_bytes"`
}

// ToolDriver bootstraps standalone executables without a Python/uv prerequisite.
// ReusePath is read-only. Installation always targets a separately owned root.
type ToolDriver struct {
	ID, Version, Executable string
	Artifact                Artifact
	ReusePath               string
	DownloadSource          string
}

func (d ToolDriver) matches(r Requirement) bool {
	if r.ID != d.ID || r.Version != d.Version || r.Digest != d.Artifact.SHA256 {
		return false
	}
	if d.DownloadSource == "" {
		return r.Source == d.Artifact.URL
	}
	return r.Source == d.Artifact.URL || r.Source == DisplaySourceURL(d.ID, d.Artifact.URL, d.Artifact.SHA256, d.DownloadSource)
}
func (d ToolDriver) Path(root string) string {
	return filepath.Join(root, "payload", filepath.FromSlash(d.Executable))
}

func (d ToolDriver) probePath(ctx context.Context, p string) (Observation, error) {
	if d.ID == "python" {
		return ProbePython(ctx, p)
	}
	o := Observation{Status: Missing, Path: p}
	if info, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		return o, nil
	} else if err != nil {
		return o, err
	} else if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		o.Status, o.Reason = Incompatible, "不是可执行的安装工具"
		return o, nil
	}
	b, err := runToolProbe(ctx, p, "--version")
	if err != nil {
		o.Status, o.Reason = Incompatible, "无法执行安装工具"
		return o, nil
	}
	o.Version = strings.TrimSpace(string(b))
	if !strings.HasPrefix(o.Version, "uv ") {
		o.Status, o.Reason = Incompatible, "不是受支持的 uv"
		return o, nil
	}
	// Capability, rather than an arbitrary exact version, determines reuse.
	help, err := runToolProbe(ctx, p, "pip", "sync", "--help")
	if err != nil || !strings.Contains(string(help), "--require-hashes") || !strings.Contains(string(help), "--build-constraints") {
		o.Status, o.Reason = Incompatible, "现有 uv 缺少锁定安装能力"
		return o, nil
	}
	// Help includes environment-derived defaults such as the isolated cache
	// directory. Bind consent to the executable itself, not temporary output.
	file, err := os.Open(p)
	if err != nil {
		return o, err
	}
	defer file.Close()
	h := sha256.New()
	h.Write(b)
	if _, err := io.Copy(h, file); err != nil {
		return o, err
	}
	o.Status, o.Fingerprint = Available, hex.EncodeToString(h.Sum(nil))
	return o, nil
}

func (d ToolDriver) Probe(ctx context.Context, r Requirement) (Observation, error) {
	if !d.matches(r) {
		return Observation{}, errors.New("tool requirement differs from fixed manifest")
	}
	o, err := d.probePath(ctx, d.Path(r.Destination))
	if err != nil || o.Status == Available {
		return o, err
	}
	if d.ReusePath != "" {
		external, err := d.probePath(ctx, d.ReusePath)
		if err != nil || external.Status == Available || o.Status == Missing {
			return external, err
		}
	}
	return o, nil
}

func (d ToolDriver) Install(ctx context.Context, r Requirement) error {
	progressComponent(ctx, r.ID, false)
	if !d.matches(r) {
		return errors.New("tool requirement differs from fixed manifest")
	}
	identity, _ := json.Marshal(struct {
		ID, Version, Executable string
		Artifact                Artifact
	}{d.ID, d.Version, d.Executable, d.Artifact})
	if err := claimComponentRoot(r.Destination, ".memlane-tool.json", identity); err != nil {
		return err
	}
	archivePathFor := func(cand ArtifactCandidate) string {
		if cand.Format == "wheel" {
			return filepath.Join(r.Destination, "artifact.whl")
		}
		return filepath.Join(r.Destination, "artifact.tar.gz")
	}
	var chosen ArtifactCandidate
	if d.DownloadSource != "" {
		var err error
		chosen, err = fetchArtifactCandidates(ctx, ToolCandidates(d.ID, d.Artifact.URL, r.Digest, d.DownloadSource), archivePathFor)
		if err != nil {
			return err
		}
	} else {
		archive := filepath.Join(r.Destination, "artifact.tar.gz")
		if err := fetchArchive(ctx, r.Source, r.Digest, archive); err != nil {
			return err
		}
		chosen = ArtifactCandidate{URL: r.Source, SHA256: r.Digest, Format: "tar.gz"}
	}
	payload := filepath.Join(r.Destination, "payload")
	if _, err := os.Lstat(payload); err == nil {
		return errors.New("existing tool payload is incompatible; retained for diagnosis")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	staging, err := os.MkdirTemp(r.Destination, ".payload-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	progressStage(ctx, "extracting")
	archive := archivePathFor(chosen)
	if chosen.Format == "wheel" {
		target := filepath.Join(staging, filepath.FromSlash(d.Executable))
		if err := extractWheelExecutable(ctx, archive, chosen.ZipEntry, target); err != nil {
			return err
		}
	} else if err := extractTool(ctx, archive, staging); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Standalone distributions are installed at the final path before probing.
	// A venv is never created in staging and moved afterwards.
	return os.Rename(staging, payload)
}

func claimComponentRoot(root, marker string, identity []byte) error {
	if !filepath.IsAbs(root) {
		return errors.New("absolute managed component destination required")
	}
	for p := filepath.Clean(root); ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("component destination traverses symlink")
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	if _, err := os.Stat(root); err == nil {
		b, err := os.ReadFile(filepath.Join(root, marker))
		if err != nil || string(b) != string(identity) {
			return errors.New("unowned component destination retained unchanged")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(root), 0700); err != nil {
		return err
	}
	if err := os.Mkdir(root, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, marker), identity, 0600)
}

func runToolProbe(ctx context.Context, executable string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	home, err := os.MkdirTemp("", "memlane-tool-probe-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(home)
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = home
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "HOME=" + home, "TMPDIR=" + home, "LANG=en_US.UTF-8", "UV_NO_CONFIG=1", "UV_OFFLINE=1", "UV_CACHE_DIR=" + filepath.Join(home, "cache")}
	return cmd.Output()
}

// Extract standalone distributions with one archive root. Create links last,
// then resolve them to prove they stay inside the payload. No subsequent writes
// follow those links, and archive permissions never grant group/other access.
func extractTool(ctx context.Context, archive, destination string) error {
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
	bundle := tar.NewReader(gz)
	type link struct {
		name, target string
		hard         bool
	}
	var links []link
	root := ""
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		member, err := bundle.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if member.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name := strings.TrimSuffix(member.Name, "/")
		if name == "" || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, "\\") {
			return errors.New("unsafe tool archive path")
		}
		parts := strings.Split(name, "/")
		if root == "" {
			root = parts[0]
		}
		if root != parts[0] {
			return errors.New("multiple tool archive roots")
		}
		if len(parts) == 1 {
			if member.Typeflag != tar.TypeDir {
				return errors.New("tool archive requires a root directory")
			}
			continue
		}
		relative := strings.Join(parts[1:], "/")
		target := filepath.Join(destination, filepath.FromSlash(relative))
		switch member.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			mode := os.FileMode(0600)
			if member.Mode&0111 != 0 {
				mode = 0700
			}
			out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, bundle)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		case tar.TypeSymlink, tar.TypeLink:
			ln := member.Linkname
			if path.IsAbs(ln) || strings.Contains(ln, "\\") {
				return errors.New("unsafe tool archive link")
			}
			hard := member.Typeflag == tar.TypeLink
			resolved := path.Clean(path.Join(path.Dir(relative), ln))
			if hard {
				if !strings.HasPrefix(ln, root+"/") {
					return errors.New("unsafe tool hard link")
				}
				resolved = strings.TrimPrefix(ln, root+"/")
			}
			if resolved == ".." || strings.HasPrefix(resolved, "../") {
				return errors.New("tool archive link escapes payload")
			}
			links = append(links, link{relative, ln, hard})
		default:
			return fmt.Errorf("unsupported tool archive member %d", member.Typeflag)
		}
	}
	for _, ln := range links {
		target := filepath.Join(destination, filepath.FromSlash(ln.name))
		for parent := filepath.Dir(target); parent != destination; parent = filepath.Dir(parent) {
			info, statErr := os.Lstat(parent)
			if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
				return errors.New("tool link parent is a symlink")
			}
			if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
				return statErr
			}
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		if ln.hard {
			source, resolveErr := filepath.EvalSymlinks(filepath.Join(destination, filepath.FromSlash(strings.TrimPrefix(ln.target, root+"/"))))
			if resolveErr != nil {
				return resolveErr
			}
			base, resolveErr := filepath.EvalSymlinks(destination)
			if resolveErr != nil {
				return resolveErr
			}
			rel, resolveErr := filepath.Rel(base, source)
			if resolveErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return errors.New("tool hard link escapes payload")
			}
			err = os.Link(source, target)
		} else {
			err = os.Symlink(ln.target, target)
		}
		if err != nil {
			return err
		}
	}
	canonical, err := filepath.EvalSymlinks(destination)
	if err != nil {
		return err
	}
	for _, ln := range links {
		resolved, err := filepath.EvalSymlinks(filepath.Join(destination, filepath.FromSlash(ln.name)))
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(canonical, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return errors.New("tool link escapes payload")
		}
	}
	return nil
}

func extractWheelExecutable(ctx context.Context, archive, zipEntry, destination string) error {
	if zipEntry == "" || strings.HasPrefix(zipEntry, "/") || strings.Contains(zipEntry, "..") {
		return errors.New("invalid wheel executable entry")
	}
	cleanEntry := path.Clean(zipEntry)
	if cleanEntry != zipEntry || cleanEntry == "." || strings.HasPrefix(cleanEntry, "../") {
		return errors.New("wheel executable entry escapes archive")
	}
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		if f.Name != cleanEntry {
			continue
		}
		mode := f.Mode()
		if mode&os.ModeSymlink != 0 || f.FileInfo().IsDir() {
			return errors.New("wheel executable entry is not a regular file")
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return err
		}
		out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
		if err != nil {
			return err
		}
		const maxWheelExecutableBytes = 256 << 20
		n, copyErr := io.Copy(out, io.LimitReader(rc, maxWheelExecutableBytes+1))
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if n > maxWheelExecutableBytes {
			return errors.New("wheel executable exceeds maximum supported size")
		}
		if closeErr != nil {
			return closeErr
		}
		return os.Chmod(destination, 0700)
	}
	return fmt.Errorf("wheel entry %q not found", cleanEntry)
}
