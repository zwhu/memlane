package bootstrap

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
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
type SourceDriver struct {
	Spec           SourceSpec
	DownloadSource string
}

func (d SourceDriver) matches(r Requirement) bool {
	if r.ID != "cognee-source" || r.Version != d.Spec.Commit || r.Digest != d.Spec.SHA256 {
		return false
	}
	if d.DownloadSource == "" {
		return r.Source == d.Spec.URL
	}
	return r.Source == d.Spec.URL || r.Source == DisplaySourceURL(r.ID, d.Spec.URL, d.Spec.SHA256, d.DownloadSource)
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
	if d.DownloadSource != "" {
		candidates := SourceCandidates(d.Spec.URL, r.Digest, d.DownloadSource)
		if _, err := fetchArtifactCandidates(ctx, candidates, func(ArtifactCandidate) string { return archive }); err != nil {
			return err
		}
	} else if err := fetchArchive(ctx, r.Source, r.Digest, archive); err != nil {
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

type existingArchiveError struct{ msg string }

func (e existingArchiveError) Error() string { return e.msg }

const artifactUserAgent = "Memlane/0.1.0-beta.1"

type artifactHTTPError struct{ statusCode int }

func (e artifactHTTPError) Error() string { return fmt.Sprintf("artifact HTTP %d", e.statusCode) }

type summarizedArtifactError struct{ msg string }

func (e summarizedArtifactError) Error() string { return e.msg }

var (
	artifactResponseHeaderTimeout = 30 * time.Second
	artifactReadStallTimeout      = 30 * time.Second
	artifactTotalTimeout          = 30 * time.Minute
	artifactHTTPClientFactory     = newArtifactHTTPClient
	errArtifactStalled            = errors.New("artifact download stalled")
	errArtifactDigestMismatch     = errors.New("downloaded artifact digest mismatch")
	errArtifactHTTPSRequired      = errors.New("HTTPS artifact URL required")
	errArtifactDigestRequired     = errors.New("fixed artifact digest required")
	errUnsafeArtifactRedirect     = errors.New("unsafe artifact redirect")
)

func summarizeArtifactError(err error) string {
	if err == nil {
		return ""
	}
	var summarized summarizedArtifactError
	if errors.As(err, &summarized) {
		return summarized.msg
	}
	if errors.Is(err, errArtifactStalled) {
		return err.Error()
	}
	var httpErr artifactHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Error()
	}
	for _, sentinel := range []error{
		errArtifactDigestMismatch,
		errArtifactHTTPSRequired,
		errArtifactDigestRequired,
		errUnsafeArtifactRedirect,
	} {
		if errors.Is(err, sentinel) {
			return sentinel.Error()
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "request timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "request canceled"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsTimeout {
			return "DNS lookup timeout"
		}
		return "DNS lookup failed"
	}
	var escErr url.EscapeError
	var hostErr url.InvalidHostError
	if errors.As(err, &escErr) || errors.As(err, &hostErr) {
		return "invalid redirect location"
	}
	var unknownAuth x509.UnknownAuthorityError
	var hostCert x509.HostnameError
	var invalidCert x509.CertificateInvalidError
	var tlsRecord tls.RecordHeaderError
	if errors.As(err, &unknownAuth) || errors.As(err, &hostCert) || errors.As(err, &invalidCert) || errors.As(err, &tlsRecord) {
		return "TLS verification failed"
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Timeout() {
			return fmt.Sprintf("network %s timeout", opErr.Op)
		}
		return fmt.Sprintf("network %s failed", opErr.Op)
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "unexpected EOF"
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		if urlErr.Timeout() {
			return "network timeout"
		}
		lower := strings.ToLower(urlErr.Err.Error())
		switch {
		case strings.Contains(lower, "proxy"):
			return "proxy connection failed"
		case strings.Contains(lower, "location") || strings.Contains(lower, "redirect") || strings.Contains(lower, "parse") || strings.Contains(lower, "escape"):
			return "invalid redirect location"
		case strings.Contains(lower, "tls") || strings.Contains(lower, "x509") || strings.Contains(lower, "certificate"):
			return "TLS verification failed"
		default:
			return "HTTP request failed"
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return "network timeout"
		}
		return "network error"
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return "local file error"
	}
	return "download failed"
}

func isHomebrewRegistryURL(u *url.URL) bool {
	return u.Host == "ghcr.io" && strings.HasPrefix(u.Path, "/v2/homebrew/core/")
}

func newArtifactHTTPClient(ctx context.Context) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = artifactProxyFunc(ctx)
	transport.ResponseHeaderTimeout = artifactResponseHeaderTimeout
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" || req.URL.User != nil || len(via) > 5 {
				return errUnsafeArtifactRedirect
			}
			req.Header.Set("User-Agent", artifactUserAgent)
			if !isHomebrewRegistryURL(req.URL) {
				req.Header.Del("Authorization")
			}
			return nil
		},
	}
}

func fetchArtifactCandidates(ctx context.Context, candidates []ArtifactCandidate, destinationFor func(ArtifactCandidate) string) (ArtifactCandidate, error) {
	if len(candidates) == 0 {
		return ArtifactCandidate{}, errors.New("no download candidate configured")
	}
	var failures []error
	for _, cand := range candidates {
		if err := ctx.Err(); err != nil {
			return ArtifactCandidate{}, err
		}
		dest := destinationFor(cand)
		err := fetchArchive(ctx, cand.URL, cand.SHA256, dest)
		if ctx.Err() != nil {
			return ArtifactCandidate{}, ctx.Err()
		}
		if err == nil {
			return cand, nil
		}
		var existing existingArchiveError
		if errors.As(err, &existing) {
			return ArtifactCandidate{}, err
		}
		label := "invalid artifact URL"
		if u, parseErr := url.Parse(cand.URL); parseErr == nil && u.Host != "" {
			label = u.Host + u.EscapedPath()
		}
		failures = append(failures, fmt.Errorf("%s: %s", label, summarizeArtifactError(err)))
	}
	return ArtifactCandidate{}, errors.Join(failures...)
}

// artifactStallReader gives each successful read a fresh idle window. A timer
// callback already waiting for the lock checks the latest deadline before firing.
type artifactStallReader struct {
	body     io.Reader
	idle     time.Duration
	cancel   context.CancelCauseFunc
	mu       sync.Mutex
	timer    *time.Timer
	deadline time.Time
	stopped  bool
}

func newArtifactStallReader(body io.Reader, idle time.Duration, cancel context.CancelCauseFunc) *artifactStallReader {
	r := &artifactStallReader{body: body, idle: idle, cancel: cancel}
	r.mu.Lock()
	r.deadline = time.Now().Add(idle)
	r.timer = time.AfterFunc(idle, r.check)
	r.mu.Unlock()
	return r
}

func (r *artifactStallReader) check() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	if remaining := time.Until(r.deadline); remaining > 0 {
		r.timer.Reset(remaining)
		return
	}
	r.stopped = true
	r.cancel(errArtifactStalled)
}

func (r *artifactStallReader) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	if n > 0 {
		r.mu.Lock()
		if !r.stopped {
			r.deadline = time.Now().Add(r.idle)
			if r.timer.Stop() {
				r.timer.Reset(r.idle)
			}
		}
		r.mu.Unlock()
	}
	return n, err
}

func (r *artifactStallReader) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = true
	r.timer.Stop()
}

func fetchArchive(ctx context.Context, rawURL, digest, destination string) error {
	if len(digest) != 64 {
		return errArtifactDigestRequired
	}
	if info, err := os.Lstat(destination); err == nil {
		if !info.Mode().IsRegular() {
			return existingArchiveError{msg: "archive destination is not a regular file"}
		}
		f, err := os.Open(destination)
		if err != nil {
			return existingArchiveError{msg: err.Error()}
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return existingArchiveError{msg: err.Error()}
		}
		if hex.EncodeToString(h.Sum(nil)) != digest {
			return existingArchiveError{msg: "existing archive digest mismatch; retained"}
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return existingArchiveError{msg: err.Error()}
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return errArtifactHTTPSRequired
	}
	totalCtx, cancelTotal := context.WithTimeout(ctx, artifactTotalTimeout)
	defer cancelTotal()
	attemptCtx, cancelAttempt := context.WithCancelCause(totalCtx)
	defer cancelAttempt(nil)
	client := artifactHTTPClientFactory(attemptCtx)
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return summarizedArtifactError{msg: summarizeArtifactError(err)}
	}
	// GHCR uses this public anonymous pull token for Homebrew bottles. No user
	// credential is read or forwarded, and this is limited to the fixed registry.
	req.Header.Set("User-Agent", artifactUserAgent)
	if isHomebrewRegistryURL(u) {
		req.Header.Set("Authorization", "Bearer QQ==")
	}
	response, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return summarizedArtifactError{msg: summarizeArtifactError(err)}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return artifactHTTPError{statusCode: response.StatusCode}
	}
	f, err := os.CreateTemp(filepath.Dir(destination), ".download-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	h := sha256.New()
	stallTimeout := artifactReadStallTimeout
	body := newArtifactStallReader(response.Body, stallTimeout, cancelAttempt)
	defer body.stop()
	_, copyErr := io.Copy(io.MultiWriter(f, h), body)
	body.stop()
	if err := ctx.Err(); err != nil {
		return err
	}
	if copyErr != nil {
		if errors.Is(context.Cause(attemptCtx), errArtifactStalled) {
			return fmt.Errorf("%w (no data for %s)", errArtifactStalled, stallTimeout)
		}
		return summarizedArtifactError{msg: summarizeArtifactError(copyErr)}
	}
	if hex.EncodeToString(h.Sum(nil)) != digest {
		return errArtifactDigestMismatch
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
