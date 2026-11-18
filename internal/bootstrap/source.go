package bootstrap

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type SourceSpec struct {
	Commit   string   `json:"commit"`
	URL      string   `json:"archive_url"`
	SHA256   string   `json:"archive_sha256"`
	Packages []string `json:"packages"`
}

// SourceDriver prepares only the pinned engine source. It never starts the
// engine, initializes a library, or claims all installation components ready.
type SourceDriver struct{ Spec SourceSpec }

func (d SourceDriver) matches(r Requirement) bool {
	return r.ID == "cognee-source" && r.Version == d.Spec.Commit && r.Source == d.Spec.URL && r.Digest == d.Spec.SHA256
}
func (d SourceDriver) Probe(ctx context.Context, r Requirement) (Observation, error) {
	o := Observation{Status: Missing, Path: r.Destination, Version: d.Spec.Commit, Fingerprint: d.Spec.SHA256}
	if err := ctx.Err(); err != nil {
		return o, err
	}
	if !d.matches(r) {
		return o, errors.New("source requirement differs from pinned manifest")
	}
	if _, err := os.Lstat(r.Destination); errors.Is(err, os.ErrNotExist) {
		return o, nil
	} else if err != nil {
		return o, err
	}
	err := VerifySource(filepath.Join(r.Destination, "service.tar.gz"), filepath.Join(r.Destination, "source"), d.Spec.SHA256, d.Spec.Packages)
	if err != nil {
		o.Status, o.Reason = Incompatible, err.Error()
	} else {
		o.Status = Available
	}
	return o, nil
}

func (d SourceDriver) Install(ctx context.Context, r Requirement) error {
	if !d.matches(r) {
		return errors.New("source requirement differs from pinned manifest")
	}
	// State ownership must be established before network use. Never replace an
	// existing user source tree, even if a user approved installing another copy.
	if !filepath.IsAbs(r.Destination) {
		return errors.New("absolute source destination required")
	}
	for p := filepath.Clean(r.Destination); ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("source destination traverses symlink")
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	marker := filepath.Join(r.Destination, ".memlane-source.json")
	expected, _ := json.Marshal(d.Spec)
	if _, err := os.Stat(r.Destination); err == nil {
		b, err := os.ReadFile(marker)
		if err != nil || string(b) != string(expected) {
			return errors.New("existing source directory is not owned by this installation; retained")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else {
		if err := os.MkdirAll(filepath.Dir(r.Destination), 0700); err != nil {
			return err
		}
		if err := os.Mkdir(r.Destination, 0700); err != nil {
			return err
		}
		if err := os.WriteFile(marker, expected, 0600); err != nil {
			return err
		}
	}
	archive := filepath.Join(r.Destination, "service.tar.gz")
	if err := fetchArchive(ctx, r.Source, r.Digest, archive); err != nil {
		return err
	}
	source := filepath.Join(r.Destination, "source")
	if _, err := os.Lstat(source); err == nil {
		return VerifySource(archive, source, r.Digest, d.Spec.Packages)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	staging, err := os.MkdirTemp(r.Destination, ".source-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if err := extractPackages(ctx, archive, staging, d.Spec.Packages); err != nil {
		return err
	}
	if err := VerifySource(archive, staging, r.Digest, d.Spec.Packages); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(staging, source)
}

func fetchArchive(ctx context.Context, rawURL, digest, destination string) error {
	if len(digest) != 64 {
		return errors.New("fixed artifact digest required")
	}
	if info, err := os.Lstat(destination); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("archive destination is not a regular file")
		}
		f, err := os.Open(destination)
		if err != nil {
			return err
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		if hex.EncodeToString(h.Sum(nil)) != digest {
			return errors.New("existing archive digest mismatch; retained")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return errors.New("HTTPS artifact URL required")
	}
	client := &http.Client{Timeout: 30 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || req.URL.User != nil || len(via) > 5 {
			return errors.New("unsafe artifact redirect")
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	// GHCR uses this public anonymous pull token for Homebrew bottles. No user
	// credential is read or forwarded, and this is limited to the fixed registry.
	if u.Host == "ghcr.io" && strings.HasPrefix(u.Path, "/v2/homebrew/core/") {
		req.Header.Set("Authorization", "Bearer QQ==")
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("artifact HTTP %d", response.StatusCode)
	}
	f, err := os.CreateTemp(filepath.Dir(destination), ".download-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), response.Body); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != digest {
		return errors.New("downloaded artifact digest mismatch")
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), destination)
}

func extractPackages(ctx context.Context, archive, destination string, packages []string) error {
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
	allowed := map[string]bool{"pyproject.toml": true}
	for _, p := range packages {
		allowed[p] = true
	}
	root := ""
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		member, err := bundle.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if member.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name := strings.TrimSuffix(member.Name, "/")
		if path.IsAbs(name) || path.Clean(name) != name || strings.Contains(name, "\\") || name == ".." || strings.HasPrefix(name, "../") {
			return errors.New("unsafe source archive")
		}
		parts := strings.Split(name, "/")
		if root == "" {
			root = parts[0]
		}
		if root != parts[0] {
			return errors.New("multiple source archive roots")
		}
		if len(parts) < 2 || !allowed[parts[1]] {
			continue
		}
		target := filepath.Join(destination, filepath.Join(parts[1:]...))
		if member.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
			continue
		}
		if member.Typeflag != tar.TypeReg {
			return errors.New("unsafe source archive member")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, bundle)
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
}
