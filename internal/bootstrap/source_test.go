package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSourcePreparationResumesVerifiedArchiveWithoutNetwork(t *testing.T) {
	archive, _, digest := sourceFixture(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "engine")
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	d := SourceDriver{Spec: SourceSpec{Commit: "fixed-commit", URL: "https://invalid.example.test/must-not-download", SHA256: digest, Packages: []string{"cognee"}}}
	marker, _ := json.Marshal(d.Spec)
	if err := os.WriteFile(filepath.Join(destination, ".memlane-source.json"), marker, 0600); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "service.tar.gz"), b, 0600); err != nil {
		t.Fatal(err)
	}
	r := Requirement{ID: "cognee-source", Version: d.Spec.Commit, Source: d.Spec.URL, Digest: d.Spec.SHA256, Destination: destination}
	if err := d.Install(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	o, err := d.Probe(context.Background(), r)
	if err != nil || o.Status != Available {
		t.Fatal(o, err)
	}
	// A second invocation validates and reuses; it never downloads over good data.
	if err := d.Install(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "source/cognee/__init__.py"), []byte("# modified"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := d.Install(context.Background(), r); err == nil {
		t.Fatal("modified source overwritten")
	}
}
func TestSourceInstallPreservesUnownedDirectory(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "existing")
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "user.txt"), []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	d := SourceDriver{Spec: SourceSpec{Commit: "fixed", URL: "https://invalid.example.test/no-download", SHA256: "fixed-sha"}}
	r := Requirement{ID: "cognee-source", Version: d.Spec.Commit, Source: d.Spec.URL, Digest: d.Spec.SHA256, Destination: destination}
	if err := d.Install(context.Background(), r); err == nil {
		t.Fatal("unowned destination accepted")
	}
	entries, _ := os.ReadDir(destination)
	if len(entries) != 1 || entries[0].Name() != "user.txt" {
		t.Fatal(entries)
	}
}

const testArtifact = "pinned artifact content"

func artifactTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	oldFactory := artifactHTTPClientFactory
	artifactHTTPClientFactory = func(ctx context.Context) *http.Client {
		client := *server.Client()
		client.CheckRedirect = newArtifactHTTPClient(ctx).CheckRedirect
		return &client
	}
	t.Cleanup(func() {
		artifactHTTPClientFactory = oldFactory
		server.Close()
	})
	return server
}

func fetchTestArtifacts(server *httptest.Server, destination string, paths ...string) (ArtifactCandidate, error) {
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(testArtifact)))
	var candidates []ArtifactCandidate
	for _, path := range paths {
		candidates = append(candidates, ArtifactCandidate{URL: server.URL + path, SHA256: digest})
	}
	return fetchArtifactCandidates(context.Background(), candidates, func(ArtifactCandidate) string { return destination })
}

func checkTestArtifact(t *testing.T, destination string) {
	t.Helper()
	body, err := os.ReadFile(destination)
	if err != nil || string(body) != testArtifact {
		t.Fatalf("unexpected artifact: %q, %v", body, err)
	}
}

func TestArtifactUserAgentAndForbiddenMirrorFailover(t *testing.T) {
	server := artifactTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "Memlane/0.1.0-beta.2" {
			t.Errorf("unexpected user agent on %s: %q", r.URL.Path, got)
			http.Error(w, "user agent rejected", http.StatusForbidden)
			return
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("authorization sent to a mirror")
		}
		switch r.URL.Path {
		case "/mirror":
			io.WriteString(w, testArtifact)
		case "/forbidden":
			http.Error(w, "unavailable", http.StatusForbidden)
		case "/fallback":
			http.Redirect(w, r, "/mirror", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	})
	dest := filepath.Join(t.TempDir(), "artifact")
	chosen, err := fetchTestArtifacts(server, dest, "/forbidden", "/fallback")
	if err != nil || chosen.URL != server.URL+"/fallback" {
		t.Fatalf("fallback failed: %+v, %v", chosen, err)
	}
	checkTestArtifact(t, dest)
}

func TestArtifactRedirectScopesAuthorizationAndRequiresHTTPS(t *testing.T) {
	redirect := newArtifactHTTPClient(context.Background()).CheckRedirect
	for _, tc := range []struct {
		url     string
		want    string
		blocked bool
	}{
		{"https://ghcr.io/v2/homebrew/core/openssl", "Bearer QQ==", false},
		{"https://ghcr.io/v2/other", "", false},
		{"https://mirror.example/openssl", "", false},
		{"https://sub.ghcr.io/v2/homebrew/core/openssl", "", false},
		{"http://mirror.example/openssl", "", true},
		{"https://user:password@mirror.example/openssl", "", true},
	} {
		req, err := http.NewRequest(http.MethodGet, tc.url, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer QQ==")
		err = redirect(req, nil)
		if (err != nil) != tc.blocked {
			t.Fatalf("unexpected redirect result for %s: %v", tc.url, err)
		}
		if !tc.blocked && (req.Header.Get("Authorization") != tc.want || req.Header.Get("User-Agent") != artifactUserAgent) {
			t.Fatalf("unexpected redirect headers for %s: %v", tc.url, req.Header)
		}
	}
}

func TestArtifactCandidateErrorsIncludeAllHostsPathsAndStatuses(t *testing.T) {
	server := artifactTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/empty":
			w.WriteHeader(http.StatusNoContent)
		case "/blocked":
			w.WriteHeader(http.StatusForbidden)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	dest := filepath.Join(t.TempDir(), "artifact")
	_, err := fetchTestArtifacts(server, dest, "/empty?secret=hidden", "/blocked", "/error")
	if err == nil {
		t.Fatal("failed candidates were accepted")
	}
	host := strings.TrimPrefix(server.URL, "https://")
	for _, expected := range []string{"/empty: artifact HTTP 204", "/blocked: artifact HTTP 403", "/error: artifact HTTP 500"} {
		if !strings.Contains(err.Error(), host+expected) {
			t.Errorf("missing %q in %v", host+expected, err)
		}
	}
	if strings.Contains(err.Error(), "hidden") {
		t.Fatalf("query leaked: %v", err)
	}
}

func TestArtifactStallCancelsBodyAndCleansPartialFileBeforeFallback(t *testing.T) {
	oldTimeout := artifactReadStallTimeout
	artifactReadStallTimeout = 100 * time.Millisecond
	t.Cleanup(func() { artifactReadStallTimeout = oldTimeout })
	dest := filepath.Join(t.TempDir(), "artifact")
	server := artifactTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stalled" {
			io.WriteString(w, "partial")
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
			case <-time.After(3 * time.Second):
			}
			return
		}
		entries, err := os.ReadDir(filepath.Dir(dest))
		if err != nil || len(entries) != 0 {
			t.Errorf("first attempt left a file before fallback: %v, %v", entries, err)
		}
		io.WriteString(w, testArtifact)
	})
	start := time.Now()
	_, err := fetchTestArtifacts(server, dest, "/stalled")
	if err == nil || !strings.Contains(err.Error(), "artifact download stalled (no data for 100ms)") {
		t.Fatalf("missing stall diagnosis: %v", err)
	}
	chosen, err := fetchTestArtifacts(server, dest, "/stalled", "/healthy")
	if err != nil || chosen.URL != server.URL+"/healthy" || time.Since(start) >= 2*time.Second {
		t.Fatalf("stall did not fail over promptly: %+v, %v", chosen, err)
	}
	checkTestArtifact(t, dest)
	entries, err := os.ReadDir(filepath.Dir(dest))
	if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(dest) {
		t.Fatalf("unexpected download files: %v, %v", entries, err)
	}
}

func TestArtifactDigestMismatchFailsClosedAcrossCandidates(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "artifact")
	server := artifactTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/verified" {
			if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("destination was written before verification: %v", err)
			}
			io.WriteString(w, testArtifact)
			return
		}
		io.WriteString(w, "corrupted artifact")
	})
	_, err := fetchTestArtifacts(server, dest, "/bad", "/also-bad")
	if err == nil || strings.Count(err.Error(), "artifact digest mismatch") != 2 {
		t.Fatalf("expected both candidates to fail verification: %v", err)
	}
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unverified destination exists: %v", err)
	}
	chosen, err := fetchTestArtifacts(server, dest, "/bad", "/verified")
	if err != nil || chosen.URL != server.URL+"/verified" {
		t.Fatalf("fallback failed: %+v, %v", chosen, err)
	}
	checkTestArtifact(t, dest)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestArtifactErrorsDoNotLeakMalformedRedirectOrProxySecrets(t *testing.T) {
	t.Run("malformed relative redirect with signed query", func(t *testing.T) {
		server := artifactTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "/signed/%zz?sig=top-secret-signature")
			w.WriteHeader(http.StatusFound)
		})
		dest := filepath.Join(t.TempDir(), "artifact")
		_, err := fetchTestArtifacts(server, dest, "/redirect")
		if err == nil {
			t.Fatal("malformed redirect accepted")
		}
		if strings.Contains(err.Error(), "top-secret-signature") || strings.Contains(err.Error(), "%zz") {
			t.Fatalf("malformed redirect location leaked into error: %v", err)
		}
	})

	t.Run("proxy connect error hides upstream text", func(t *testing.T) {
		oldFactory := artifactHTTPClientFactory
		artifactHTTPClientFactory = func(context.Context) *http.Client {
			return &http.Client{
				Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					return nil, &url.Error{
						Op:  "Get",
						URL: req.URL.String(),
						Err: errors.New("proxyconnect tcp: 407 Proxy Authentication Required realm=corp-secret-token"),
					}
				}),
			}
		}
		t.Cleanup(func() { artifactHTTPClientFactory = oldFactory })

		dest := filepath.Join(t.TempDir(), "artifact")
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(testArtifact)))
		_, err := fetchArtifactCandidates(context.Background(), []ArtifactCandidate{
			{URL: "https://mirror.example.test/archive.tar.gz?token=hidden-query", SHA256: digest},
		}, func(ArtifactCandidate) string { return dest })
		if err == nil {
			t.Fatal("proxy failure accepted")
		}
		if strings.Contains(err.Error(), "corp-secret-token") || strings.Contains(err.Error(), "hidden-query") {
			t.Fatalf("proxy or query secret leaked into error: %v", err)
		}
		if !strings.Contains(err.Error(), "mirror.example.test/archive.tar.gz: proxy connection failed") {
			t.Fatalf("expected structured proxy classification, got: %v", err)
		}
	})
}
