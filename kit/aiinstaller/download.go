package aiinstaller

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

const defaultUserAgent = "AIInstaller/1.0"

type DownloadOptions struct {
	UserAgent             string
	ResponseHeaderTimeout time.Duration
	ReadStallTimeout      time.Duration
	TotalTimeout          time.Duration
	HTTPClientFactory     func(context.Context) *http.Client
	ExtraHeaders          func(*url.URL) http.Header
	AllowAuthOnRedirect   func(*url.URL) bool
}

type Downloader struct{ Options DownloadOptions }

func (d Downloader) userAgent() string {
	if d.Options.UserAgent != "" {
		return d.Options.UserAgent
	}
	return defaultUserAgent
}

type ExistingArchiveError struct{ msg string }

func (e ExistingArchiveError) Error() string { return e.msg }

type ArtifactHTTPError struct{ StatusCode int }

func (e ArtifactHTTPError) Error() string { return fmt.Sprintf("artifact HTTP %d", e.StatusCode) }

var (
	ErrArtifactStalled        = errors.New("artifact download stalled")
	ErrArtifactDigestMismatch = errors.New("downloaded artifact digest mismatch")
	ErrArtifactHTTPSRequired  = errors.New("HTTPS artifact URL required")
	ErrArtifactDigestRequired = errors.New("fixed artifact digest required")
	ErrUnsafeArtifactRedirect = errors.New("unsafe artifact redirect")
)

func IsHomebrewRegistryURL(u *url.URL) bool {
	return u.Host == "ghcr.io" && strings.HasPrefix(u.Path, "/v2/homebrew/core/")
}

func (d Downloader) NewHTTPClient(ctx context.Context) *http.Client {
	var client http.Client
	if d.Options.HTTPClientFactory != nil {
		client = *d.Options.HTTPClientFactory(ctx)
	} else {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = ProxyFunc(ctx)
		transport.ResponseHeaderTimeout = durationOr(d.Options.ResponseHeaderTimeout, 30*time.Second)
		client.Transport = transport
	}
	checkRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || req.URL.User != nil || len(via) > 5 {
			return ErrUnsafeArtifactRedirect
		}
		if checkRedirect != nil {
			if err := checkRedirect(req, via); err != nil {
				return err
			}
		}
		req.Header.Set("User-Agent", d.userAgent())
		if d.Options.AllowAuthOnRedirect == nil || !d.Options.AllowAuthOnRedirect(req.URL) {
			req.Header.Del("Authorization")
		}
		return nil
	}
	return &client
}

func (d Downloader) FetchCandidates(ctx context.Context, candidates []ArtifactCandidate, destinationFor func(ArtifactCandidate) string) (ArtifactCandidate, error) {
	if len(candidates) == 0 {
		return ArtifactCandidate{}, errors.New("no download candidate configured")
	}
	var failures []error
	for idx, cand := range candidates {
		if err := ctx.Err(); err != nil {
			return ArtifactCandidate{}, err
		}
		dest := destinationFor(cand)
		ProgressAttempt(ctx, "downloading", cand.URL, idx+1, len(candidates))
		err := d.FetchArchiveAttempt(ctx, cand.URL, cand.SHA256, dest)
		if ctx.Err() != nil {
			return ArtifactCandidate{}, ctx.Err()
		}
		if err == nil {
			return cand, nil
		}
		var existing ExistingArchiveError
		if errors.As(err, &existing) {
			return ArtifactCandidate{}, err
		}
		label := "invalid artifact URL"
		if u, parseErr := url.Parse(cand.URL); parseErr == nil && u.Host != "" {
			label = u.Host + u.EscapedPath()
		}
		failures = append(failures, fmt.Errorf("%s: %s", label, SummarizeArtifactError(err)))
	}
	return ArtifactCandidate{}, errors.Join(failures...)
}

func (d Downloader) FetchArchive(ctx context.Context, rawURL, digest, destination string) error {
	ProgressAttempt(ctx, "downloading", rawURL, 1, 1)
	return d.FetchArchiveAttempt(ctx, rawURL, digest, destination)
}

func (d Downloader) FetchArchiveAttempt(ctx context.Context, rawURL, digest, destination string) error {
	if len(digest) != 64 {
		return ErrArtifactDigestRequired
	}
	if info, err := os.Lstat(destination); err == nil {
		if !info.Mode().IsRegular() {
			return ExistingArchiveError{msg: "archive destination is not a regular file"}
		}
		ProgressStage(ctx, "verifying")
		f, err := os.Open(destination)
		if err != nil {
			return ExistingArchiveError{msg: err.Error()}
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return ExistingArchiveError{msg: err.Error()}
		}
		if hex.EncodeToString(h.Sum(nil)) != digest {
			return ExistingArchiveError{msg: "existing archive digest mismatch; retained"}
		}
		if p := progressTracker(ctx); p != nil {
			p.finishTransfer(info.Size(), true)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return ExistingArchiveError{msg: err.Error()}
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return ErrArtifactHTTPSRequired
	}
	totalCtx, cancelTotal := context.WithTimeout(ctx, durationOr(d.Options.TotalTimeout, 30*time.Minute))
	defer cancelTotal()
	attemptCtx, cancelAttempt := context.WithCancelCause(totalCtx)
	defer cancelAttempt(nil)
	client := d.NewHTTPClient(attemptCtx)
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return summarizedArtifactError{msg: SummarizeArtifactError(err)}
	}
	if d.Options.ExtraHeaders != nil {
		for key, values := range d.Options.ExtraHeaders(u) {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
	}
	req.Header.Set("User-Agent", d.userAgent())
	response, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, ErrUnsafeArtifactRedirect) {
			return ErrUnsafeArtifactRedirect
		}
		return summarizedArtifactError{msg: SummarizeArtifactError(err)}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ArtifactHTTPError{StatusCode: response.StatusCode}
	}
	f, err := os.CreateTemp(filepath.Dir(destination), ".download-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	h := sha256.New()
	stallTimeout := durationOr(d.Options.ReadStallTimeout, 30*time.Second)
	body := newArtifactStallReader(response.Body, stallTimeout, cancelAttempt)
	defer body.stop()
	var reader io.Reader = body
	p := progressTracker(ctx)
	if p != nil {
		p.setLength(response.ContentLength)
		reader = installProgressReader{Reader: body, tracker: p}
	}
	size, copyErr := io.Copy(io.MultiWriter(f, h), reader)
	body.stop()
	if err := ctx.Err(); err != nil {
		return err
	}
	if copyErr != nil {
		if errors.Is(context.Cause(attemptCtx), ErrArtifactStalled) {
			return fmt.Errorf("%w (no data for %s)", ErrArtifactStalled, stallTimeout)
		}
		return summarizedArtifactError{msg: SummarizeArtifactError(copyErr)}
	}
	if hex.EncodeToString(h.Sum(nil)) != digest {
		return ErrArtifactDigestMismatch
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := totalCtx.Err(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), destination); err != nil {
		return err
	}
	if p != nil {
		p.finishTransfer(size, false)
	}
	return nil
}

func ExtractTarGzPackages(ctx context.Context, archive, destination string, packages []string) error {
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

// VerifyTarGzSource checks the archive digest and verifies the selected source tree.
func VerifyTarGzSource(archive, source, expectedSHA string, packages []string) error {
	var err error
	source, err = filepath.Abs(source)
	if err != nil {
		return err
	}
	if len(expectedSHA) != 64 || len(packages) == 0 {
		return errors.New("fixed source digest required")
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != expectedSHA {
		return errors.New("fixed official archive digest mismatch")
	}
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	bundle := tar.NewReader(gz)
	allowed, found := map[string]bool{"pyproject.toml": true}, map[string]bool{}
	for _, p := range packages {
		if p == "" || path.Base(p) != p || p == "." || p == ".." {
			return errors.New("invalid package name")
		}
		allowed[p] = true
	}
	names := map[string]bool{}
	root := ""
	for {
		member, err := bundle.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		// GitHub codeload emits a global PAX metadata record before the tree.
		// It is not a filesystem entry or a second archive root.
		if member.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name := strings.TrimSuffix(member.Name, "/")
		if path.IsAbs(name) || path.Clean(name) != name || strings.Contains(name, "\\") || name == ".." || strings.HasPrefix(name, "../") {
			return errors.New("unsafe source archive path")
		}
		parts := strings.Split(name, "/")
		if root == "" {
			root = parts[0]
		}
		if parts[0] != root {
			return errors.New("multiple archive roots")
		}
		if len(parts) < 2 || !allowed[parts[1]] {
			continue
		}
		if member.Typeflag == tar.TypeDir {
			continue
		}
		if member.Typeflag != tar.TypeReg {
			return errors.New("linked source package is not supported")
		}
		relative := strings.Join(parts[1:], "/")
		if names[relative] {
			return errors.New("duplicate archive member")
		}
		names[relative], found[parts[1]] = true, true
		target := filepath.Join(source, filepath.FromSlash(relative))
		// Reject package-tree symlinks while allowing a user-selected parent path.
		for current := target; current != source; current = filepath.Dir(current) {
			info, err := os.Lstat(current)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return errors.New("linked source package is not supported")
			}
		}
		file, err := os.Open(target)
		if err != nil {
			return err
		}
		actual := sha256.New()
		_, err = io.Copy(actual, file)
		file.Close()
		if err != nil {
			return err
		}
		pinned := sha256.New()
		if _, err := io.Copy(pinned, bundle); err != nil {
			return err
		}
		if !strings.EqualFold(hex.EncodeToString(actual.Sum(nil)), hex.EncodeToString(pinned.Sum(nil))) {
			return fmt.Errorf("source differs from pinned archive: %s", relative)
		}
	}
	for p := range allowed {
		if !found[p] {
			return fmt.Errorf("required source missing: %s", p)
		}
	}
	for _, p := range packages {
		err := filepath.WalkDir(filepath.Join(source, p), func(target string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return errors.New("linked source package is not supported")
			}
			if !entry.IsDir() && strings.HasSuffix(target, ".py") {
				rel, _ := filepath.Rel(source, target)
				if !names[filepath.ToSlash(rel)] {
					return errors.New("unexpected official Python module")
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
