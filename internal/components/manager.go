// Package components prepares independent, signed runtime and model archives.
package components

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Descriptor struct {
	ID             string `json:"id"`
	Version        string `json:"version"`
	Platform       string `json:"platform"`
	ProtocolMin    int    `json:"protocol_min"`
	ProtocolMax    int    `json:"protocol_max"`
	URL            string `json:"url"`
	Bytes          int64  `json:"bytes"`
	UnpackedBytes  int64  `json:"unpacked_bytes"`
	SHA256         string `json:"sha256"`
	ManifestSHA256 string `json:"manifest_sha256"`
}

type Asset struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type Link struct {
	Path   string `json:"path"`
	Target string `json:"target"`
}

type Metadata struct {
	ID          string  `json:"id"`
	Version     string  `json:"version"`
	Platform    string  `json:"platform"`
	ProtocolMin int     `json:"protocol_min"`
	ProtocolMax int     `json:"protocol_max"`
	Executable  string  `json:"executable"`
	Library     string  `json:"library"`
	Model       string  `json:"model"`
	Files       []Asset `json:"files"`
	Links       []Link  `json:"links,omitempty"`
}

type Feed struct {
	Version    int          `json:"version"`
	Components []Descriptor `json:"components"`
}
type SignedFeed struct {
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
}
type Progress struct {
	ID         string `json:"id"`
	Phase      string `json:"phase"`
	Downloaded int64  `json:"downloaded"`
	Total      int64  `json:"total"`
}
type Installed struct {
	Directory       string   `json:"directory"`
	Metadata        Metadata `json:"metadata"`
	Reused          bool     `json:"reused"`
	DownloadedBytes int64    `json:"downloaded_bytes"`
}

type Manager struct {
	Root           string
	PublicKey      ed25519.PublicKey
	Protocol       int
	AllowLocalHTTP bool // Controlled test sources only; production requires HTTPS.
	gate           chan struct{}
	HTTP           *http.Client
	mu             sync.RWMutex
	approved       map[string]Descriptor
}

func New(root string, key ed25519.PublicKey, protocol int, localTest bool) (*Manager, error) {
	if !filepath.IsAbs(root) || len(key) != ed25519.PublicKeySize || protocol < 1 {
		return nil, errors.New("invalid component configuration")
	}
	m := &Manager{Root: root, PublicKey: append(ed25519.PublicKey(nil), key...), Protocol: protocol, AllowLocalHTTP: localTest, gate: make(chan struct{}, 1), approved: map[string]Descriptor{}}
	m.HTTP = &http.Client{Timeout: 30 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return errors.New("too many redirects")
		}
		return m.validURL(req.URL)
	}}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) validURL(u *url.URL) error {
	if u.User != nil || u.Fragment != "" {
		return errors.New("component URL cannot contain credentials or a fragment")
	}
	if u.Scheme == "https" && u.Host != "" {
		return nil
	}
	if m.AllowLocalHTTP && u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1") {
		return nil
	}
	return errors.New("component source requires HTTPS")
}

func (m *Manager) Feed(ctx context.Context, source string) (Feed, error) {
	var feed Feed
	u, err := url.Parse(source)
	if err != nil {
		return feed, err
	}
	if err = m.validURL(u); err != nil {
		return feed, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return feed, err
	}
	response, err := m.HTTP.Do(req)
	if err != nil {
		return feed, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return feed, fmt.Errorf("manifest HTTP status %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
	if err != nil {
		return feed, err
	}
	if len(data) > 1024*1024 {
		return feed, errors.New("manifest exceeds 1 MiB")
	}
	feed, err = m.acceptFeed(data)
	if err != nil {
		return feed, err
	}
	cache := struct {
		Source string          `json:"source"`
		Feed   json.RawMessage `json:"feed"`
	}{source, data}
	encoded, err := json.Marshal(cache)
	if err != nil {
		return feed, err
	}
	temporary, err := os.CreateTemp(m.Root, ".feed-")
	if err != nil {
		return feed, err
	}
	defer os.Remove(temporary.Name())
	if _, err = temporary.Write(encoded); err != nil {
		temporary.Close()
		return feed, err
	}
	if err = temporary.Sync(); err != nil {
		temporary.Close()
		return feed, err
	}
	if err = temporary.Close(); err != nil {
		return feed, err
	}
	if err = os.Rename(temporary.Name(), filepath.Join(m.Root, "feed-cache.json")); err != nil {
		return feed, err
	}
	return feed, nil
}

// CachedFeed verifies the same signature and compatibility rules while offline.
// Its source must match the configured source; a first offline launch fails.
func (m *Manager) CachedFeed(source string) (Feed, error) {
	var cache struct {
		Source string          `json:"source"`
		Feed   json.RawMessage `json:"feed"`
	}
	data, err := os.ReadFile(filepath.Join(m.Root, "feed-cache.json"))
	if err != nil {
		return Feed{}, err
	}
	if len(data) > 2*1024*1024 {
		return Feed{}, errors.New("cached manifest too large")
	}
	if err = json.Unmarshal(data, &cache); err != nil {
		return Feed{}, err
	}
	if cache.Source != source {
		return Feed{}, errors.New("cached manifest source differs")
	}
	return m.acceptFeed(cache.Feed)
}

func (m *Manager) acceptFeed(data []byte) (Feed, error) {
	var feed Feed
	var signed SignedFeed
	var err error
	if err = json.Unmarshal(data, &signed); err != nil {
		return feed, err
	}
	signature, err := base64.StdEncoding.DecodeString(signed.Signature)
	if err != nil {
		return feed, err
	}
	if !ed25519.Verify(m.PublicKey, signed.Payload, signature) {
		return feed, errors.New("invalid component manifest signature")
	}
	if err = json.Unmarshal(signed.Payload, &feed); err != nil {
		return feed, err
	}
	if feed.Version != 1 {
		return feed, errors.New("incompatible manifest version")
	}
	seen := map[string]bool{}
	for _, component := range feed.Components {
		if err = m.validate(component); err != nil {
			return feed, err
		}
		if seen[component.ID] {
			return feed, errors.New("duplicate component ID")
		}
		seen[component.ID] = true
	}
	m.mu.Lock()
	m.approved = map[string]Descriptor{}
	for _, component := range feed.Components {
		m.approved[component.ID] = component
	}
	m.mu.Unlock()
	return feed, nil
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func (m *Manager) validate(d Descriptor) error {
	if !namePattern.MatchString(d.ID) || !namePattern.MatchString(d.Version) {
		return errors.New("invalid component ID or version")
	}
	if d.Platform != runtime.GOOS+"-"+runtime.GOARCH {
		return errors.New("component platform mismatch")
	}
	if d.ProtocolMin > m.Protocol || d.ProtocolMax < m.Protocol || d.ProtocolMin < 1 {
		return errors.New("component protocol incompatible")
	}
	if d.Bytes <= 0 || d.UnpackedBytes <= 0 || d.Bytes > 10*1024*1024*1024 || d.UnpackedBytes > 20*1024*1024*1024 {
		return errors.New("invalid component size")
	}
	for _, hash := range []string{d.SHA256, d.ManifestSHA256} {
		decoded, err := hex.DecodeString(hash)
		if err != nil || len(decoded) != sha256.Size {
			return errors.New("invalid component SHA-256")
		}
	}
	u, err := url.Parse(d.URL)
	if err != nil {
		return err
	}
	return m.validURL(u)
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	checksum := sha256.New()
	if _, err = io.Copy(checksum, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(checksum.Sum(nil)), nil
}

func safePath(root, name string) (string, error) {
	if name == "" || filepath.IsAbs(name) || strings.Contains(name, "\\") {
		return "", errors.New("invalid archive path")
	}
	clean := filepath.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("archive path escapes component")
	}
	return filepath.Join(root, clean), nil
}

func safeLink(root, name, target string) (string, error) {
	path, err := safePath(root, name)
	if err != nil {
		return "", err
	}
	if target == "" || filepath.IsAbs(target) || strings.Contains(target, "\\") {
		return "", errors.New("invalid component link target")
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(path), target))
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("component link escapes component")
	}
	return path, nil
}

func verify(directory string, d Descriptor) (Metadata, error) {
	var metadata Metadata
	path := filepath.Join(directory, "component.json")
	info, err := os.Lstat(path)
	if err != nil {
		return metadata, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16*1024*1024 {
		return metadata, errors.New("invalid component metadata file")
	}
	hash, err := hashFile(path)
	if err != nil || hash != d.ManifestSHA256 {
		return metadata, errors.New("component metadata hash mismatch")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return metadata, err
	}
	if err = json.Unmarshal(data, &metadata); err != nil {
		return metadata, err
	}
	if metadata.ID != d.ID || metadata.Version != d.Version || metadata.Platform != d.Platform || metadata.ProtocolMin != d.ProtocolMin || metadata.ProtocolMax != d.ProtocolMax {
		return metadata, errors.New("component metadata incompatible")
	}
	if len(metadata.Files) == 0 || len(metadata.Files) > 65536 {
		return metadata, errors.New("invalid component asset list")
	}
	allowed := map[string]bool{"component.json": true}
	links := map[string]string{}
	if len(metadata.Links) > 65536 {
		return metadata, errors.New("too many component links")
	}
	for _, link := range metadata.Links {
		if _, err := safeLink(directory, link.Path, link.Target); err != nil {
			return metadata, err
		}
		if allowed[link.Path] || links[link.Path] != "" {
			return metadata, errors.New("duplicate component link")
		}
		links[link.Path] = link.Target
	}
	// Inspect filesystem links before reading assets, including parent paths.
	err = filepath.WalkDir(directory, func(path string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if entry.Type()&os.ModeSymlink != 0 {
			rel, e := filepath.Rel(directory, path)
			if e != nil {
				return e
			}
			target, e := os.Readlink(path)
			if e != nil {
				return e
			}
			if links[rel] != target {
				return errors.New("unmanifested or changed component link")
			}
			_, e = safeLink(directory, rel, target)
			return e
		}
		return nil
	})
	if err != nil {
		return metadata, err
	}
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return metadata, err
	}
	for name := range links {
		resolved, e := filepath.EvalSymlinks(filepath.Join(directory, name))
		if e != nil {
			return metadata, e
		}
		rel, e := filepath.Rel(canonical, resolved)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return metadata, errors.New("resolved component link escapes component")
		}
		allowed[name] = true
	}
	for _, asset := range metadata.Files {
		path, err := safePath(directory, asset.Path)
		if err != nil {
			return metadata, err
		}
		if allowed[asset.Path] {
			return metadata, errors.New("duplicate component asset")
		}
		allowed[asset.Path] = true
		info, err := os.Lstat(path)
		if err != nil {
			return metadata, err
		}
		if !info.Mode().IsRegular() || info.Size() != asset.Bytes {
			return metadata, errors.New("invalid component asset size or type")
		}
		hash, err := hashFile(path)
		if err != nil {
			return metadata, err
		}
		if hash != asset.SHA256 {
			return metadata, errors.New("component asset hash mismatch")
		}
	}
	err = filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == directory {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			rel, err := filepath.Rel(directory, path)
			if err != nil {
				return err
			}
			if _, ok := links[rel]; !ok {
				return errors.New("component symlink rejected")
			}
			return nil
		}
		if !entry.IsDir() {
			rel, err := filepath.Rel(directory, path)
			if err != nil {
				return err
			}
			if !allowed[rel] {
				return errors.New("unexpected component asset")
			}
		}
		return nil
	})
	if err != nil {
		return metadata, err
	}
	if metadata.Executable != "" {
		path, err := safePath(directory, metadata.Executable)
		if err != nil {
			return metadata, err
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode()&0111 == 0 {
			return metadata, errors.New("component executable is not executable")
		}
	}
	return metadata, nil
}

func (m *Manager) Prepare(ctx context.Context, d Descriptor, progress func(Progress)) (Installed, error) {
	if progress == nil {
		progress = func(Progress) {}
	}
	last := Progress{ID: d.ID, Total: d.Bytes}
	callback := func(p Progress) { last = p; progress(p) }
	installed, err := m.prepare(ctx, d, callback)
	if err != nil {
		last.Phase = "failed"
		progress(last)
	}
	return installed, err
}

func (m *Manager) prepare(ctx context.Context, d Descriptor, progress func(Progress)) (Installed, error) {
	var installed Installed
	m.mu.RLock()
	approved, ok := m.approved[d.ID]
	m.mu.RUnlock()
	if !ok || approved != d {
		return installed, errors.New("component is not authorized by a verified feed")
	}
	if err := m.validate(d); err != nil {
		return installed, err
	}
	select {
	case m.gate <- struct{}{}:
	case <-ctx.Done():
		return installed, ctx.Err()
	}
	defer func() { <-m.gate }()
	unlock, err := acquireOwner(ctx, m.Root)
	if err != nil {
		return installed, err
	}
	defer unlock()
	if progress == nil {
		progress = func(Progress) {}
	}
	parent := filepath.Join(m.Root, "components", d.ID)
	destination := filepath.Join(parent, d.Version)
	if metadata, err := verify(destination, d); err == nil {
		progress(Progress{ID: d.ID, Phase: "installed", Total: d.Bytes})
		return Installed{Directory: destination, Metadata: metadata, Reused: true}, nil
	}
	if err := os.MkdirAll(parent, 0700); err != nil {
		return installed, err
	}
	cache := filepath.Join(m.Root, "downloads")
	if err := os.MkdirAll(cache, 0700); err != nil {
		return installed, err
	}
	partial := filepath.Join(cache, d.ID+"-"+d.Version+"-"+d.SHA256[:16]+".part")
	downloaded, err := m.download(ctx, d, partial, progress)
	if err != nil {
		return installed, err
	}
	stage, err := os.MkdirTemp(parent, ".install-")
	if err != nil {
		return installed, err
	}
	defer os.RemoveAll(stage)
	progress(Progress{ID: d.ID, Phase: "installing", Downloaded: d.Bytes, Total: d.Bytes})
	if err = unpack(ctx, partial, stage, d.UnpackedBytes); err != nil {
		return installed, err
	}
	metadata, err := verify(stage, d)
	if err != nil {
		return installed, err
	}
	if _, err = os.Lstat(destination); err == nil {
		// An existing immutable version is only repaired when its own metadata
		// matches the signed descriptor, never replaced by a different release.
		hash, hashErr := hashFile(filepath.Join(destination, "component.json"))
		if hashErr != nil || hash != d.ManifestSHA256 {
			return installed, errors.New("existing component version conflicts; use a new version")
		}
		backup := stage + ".old"
		if err = os.Rename(destination, backup); err != nil {
			return installed, err
		}
		if err = os.Rename(stage, destination); err != nil {
			_ = os.Rename(backup, destination)
			return installed, err
		}
		_ = os.RemoveAll(backup)
	} else if !os.IsNotExist(err) {
		return installed, err
	} else if err = os.Rename(stage, destination); err != nil {
		return installed, err
	}
	if err = os.Remove(partial); err != nil && !os.IsNotExist(err) {
		return installed, err
	}
	progress(Progress{ID: d.ID, Phase: "installed", Downloaded: d.Bytes, Total: d.Bytes})
	return Installed{Directory: destination, Metadata: metadata, DownloadedBytes: downloaded}, nil
}

func (m *Manager) download(ctx context.Context, d Descriptor, path string, progress func(Progress)) (int64, error) {
	var offset int64
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return 0, errors.New("invalid component partial file")
		}
		offset = info.Size()
		if offset == d.Bytes {
			hash, err := hashFile(path)
			if err == nil && hash == d.SHA256 {
				return 0, nil
			}
			offset = 0
		}
		if offset > d.Bytes {
			offset = 0
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.URL, nil)
	if err != nil {
		return 0, err
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	response, err := m.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		offset = 0
	} else if response.StatusCode == http.StatusPartialContent {
		expected := fmt.Sprintf("bytes %d-", offset)
		if !strings.HasPrefix(response.Header.Get("Content-Range"), expected) {
			return 0, errors.New("invalid component range response")
		}
	} else {
		return 0, fmt.Errorf("component HTTP status %d", response.StatusCode)
	}
	flags := os.O_CREATE | os.O_WRONLY
	if offset == 0 {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_APPEND
	}
	file, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return 0, err
	}
	progress(Progress{ID: d.ID, Phase: "downloading", Downloaded: offset, Total: d.Bytes})
	buffer := make([]byte, 64*1024)
	var downloaded int64
	reader := io.LimitReader(response.Body, d.Bytes-offset+1)
	for {
		if err = ctx.Err(); err != nil {
			file.Close()
			return downloaded, err
		}
		n, readErr := reader.Read(buffer)
		if n > 0 {
			if offset+downloaded+int64(n) > d.Bytes {
				file.Close()
				return downloaded, errors.New("component exceeds declared size")
			}
			if _, err = file.Write(buffer[:n]); err != nil {
				file.Close()
				return downloaded, err
			}
			downloaded += int64(n)
			progress(Progress{ID: d.ID, Phase: "downloading", Downloaded: offset + downloaded, Total: d.Bytes})
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			file.Close()
			return downloaded, readErr
		}
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return downloaded, err
	}
	if err = file.Close(); err != nil {
		return downloaded, err
	}
	if offset+downloaded != d.Bytes {
		return downloaded, errors.New("incomplete component download")
	}
	hash, err := hashFile(path)
	if err != nil {
		return downloaded, err
	}
	if hash != d.SHA256 {
		return downloaded, errors.New("component archive SHA-256 mismatch")
	}
	return downloaded, nil
}

func unpack(ctx context.Context, archive, directory string, maxBytes int64) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	var total int64
	count := 0
	links := []Link{}
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		count++
		if count > 65537 {
			return errors.New("component has too many files")
		}
		path, err := safePath(directory, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeSymlink:
			if _, err := safeLink(directory, header.Name, header.Linkname); err != nil {
				return err
			}
			links = append(links, Link{Path: header.Name, Target: header.Linkname})
		case tar.TypeDir:
			if err = os.MkdirAll(path, 0700); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 || header.Size > maxBytes-total {
				return errors.New("component exceeds unpacked size")
			}
			total += header.Size
			if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				return err
			}
			mode := os.FileMode(0600)
			if header.Mode&0111 != 0 {
				mode = 0700
			}
			dest, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(dest, reader, header.Size)
			syncErr := dest.Sync()
			closeErr := dest.Close()
			if err = errors.Join(copyErr, syncErr, closeErr); err != nil {
				return err
			}
		default:
			return errors.New("component links and special files rejected")
		}
	}
	if total != maxBytes {
		return errors.New("component unpacked size differs from manifest")
	}
	if _, err = io.Copy(io.Discard, compressed); err != nil {
		return err
	}
	// Create links only after payload writes and gzip integrity checks finish.
	for _, link := range links {
		path, err := safeLink(directory, link.Path, link.Target)
		if err != nil {
			return err
		}
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		if err = os.Symlink(link.Target, path); err != nil {
			return err
		}
	}
	return nil
}
