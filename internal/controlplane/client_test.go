package controlplane

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

const testManagementToken = "controlmanagedtokenabcdef0123456789"

func testClient(t *testing.T, origin string) *Client {
	t.Helper()
	tokenFile := filepath.Join(t.TempDir(), "management.token")
	if err := os.WriteFile(tokenFile, []byte(testManagementToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return NewClient(Config{URL: origin, ManagementTokenFile: tokenFile})
}

// Use a real IPv4 listener when available. On restricted runners, send the
// same HTTP requests over an in-memory connection to the test handler.
func testHTTPServer(t *testing.T, fallbackAddress string, handler http.HandlerFunc) (string, func(*Client)) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err == nil {
		server := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler}}
		server.Start()
		t.Cleanup(server.Close)
		return server.URL, func(*Client) {}
	}
	if !errors.Is(err, syscall.EPERM) && !errors.Is(err, syscall.EACCES) {
		t.Fatal(err)
	}
	t.Log("TCP bind denied, testing HTTP over net.Pipe")
	return "http://" + fallbackAddress, func(client *Client) {
		transport := NewLoopbackTransport()
		transport.DisableKeepAlives = true
		transport.DialContext = func(_ context.Context, _, address string) (net.Conn, error) {
			if address != fallbackAddress {
				t.Errorf("unexpected dial address %q", address)
				return nil, errors.New("unexpected address")
			}
			conn, peer := net.Pipe()
			go func() {
				defer peer.Close()
				req, err := http.ReadRequest(bufio.NewReader(peer))
				if err != nil {
					t.Error(err)
					return
				}
				defer req.Body.Close()
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, req)
				result := response.Result()
				defer result.Body.Close()
				if err := result.Write(peer); err != nil && !errors.Is(err, io.ErrClosedPipe) {
					t.Error(err)
				}
			}()
			return conn, nil
		}
		client.HTTP = &http.Client{Transport: transport, Timeout: 5 * time.Second}
	}
}

func TestControlPlaneHasZeroMyGoDependency(t *testing.T) {
	goCommand := filepath.Join(runtime.GOROOT(), "bin", "go")
	cmd := exec.Command(goCommand, "list", "-deps", "github.com/zwhu/memlane/internal/controlplane")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, name := range []string{"github.com/egoist/mygo", "internal/simpledesktop", "github.com/ebitengine/purego", "internal/bootstrap", "internal/supervisor"} {
		if strings.Contains(string(out), name) {
			t.Errorf("unexpected dependency: %s", name)
		}
	}
}

func TestLoopbackClientDisablesProxy(t *testing.T) {
	var redirected atomic.Bool
	proxyURL, _ := testHTTPServer(t, "127.0.0.1:12346", func(w http.ResponseWriter, r *http.Request) {
		t.Error("intercepting proxy received a request")
		w.WriteHeader(http.StatusBadGateway)
	})
	for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "ALL_PROXY"} {
		t.Setenv(name, proxyURL)
	}
	if NewLoopbackTransport().Proxy != nil {
		t.Fatal("loopback transport has a proxy function")
	}
	original := &http.Client{Timeout: time.Second}
	cloned := NewLoopbackHTTPClient(original, 0)
	if cloned == original || cloned.Transport != sharedLoopbackTransport || original.Transport != nil ||
		cloned.Timeout != original.Timeout || cloned.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("override was not cloned with the loopback defaults")
	}
	if got := NewLoopbackHTTPClient(nil, 0); got.Timeout != 125*time.Second || got.Transport != cloned.Transport {
		t.Fatal("unexpected default loopback client")
	}

	const canary = "raw-canary-secret"
	origin, configureTransport := testHTTPServer(t, "127.0.0.1:12345", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testManagementToken {
			t.Error("management authorization missing")
		}
		switch r.URL.Path {
		case "/direct":
			if r.Header.Get("X-Test") != "forwarded" {
				t.Error("custom header missing")
			}
			fmt.Fprint(w, `{"direct":true}`)
		case "/redirect":
			http.Redirect(w, r, "/redirected", http.StatusFound)
		case "/redirected":
			redirected.Store(true)
		case "/error":
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprintf(w, `{"error":"%s %s","code":"%s","phase":"%s"}`, canary, testManagementToken, testManagementToken, canary)
		default:
			t.Error("unexpected request")
		}
	})

	client := testClient(t, origin+"/")
	configureTransport(client)
	ctx := context.Background()
	var output map[string]bool
	headers := map[string]string{"authorization": "Bearer unwanted", "X-Test": "forwarded"}
	if err := client.RequestWithHeaders(ctx, "GET", "/direct", headers, nil, &output, 20); err != nil || !output["direct"] {
		t.Fatalf("direct request: output=%v, err=%v", output, err)
	}
	if err := client.RequestWithHeaders(ctx, "GET", "/direct", headers, nil, &output, 5); err == nil {
		t.Error("oversize response succeeded")
	}
	if client.HTTP == nil {
		client.HTTP = &http.Client{}
	}
	client.HTTP.CheckRedirect = func(*http.Request, []*http.Request) error { return nil }
	var ce *ControlError
	err := client.Request(ctx, "GET", "/redirect", nil, nil)
	if !errors.As(err, &ce) || ce.Status != http.StatusFound || redirected.Load() {
		t.Fatalf("redirect refusal: %v", err)
	}
	err = client.Request(ctx, "GET", "/error", nil, nil)
	if !errors.As(err, &ce) || !ce.IsAuthDenied() {
		t.Fatalf("auth denial: %v", err)
	}
	encoded, err := json.Marshal(ce)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{canary, testManagementToken, "management.token"} {
		if strings.Contains(ce.Error(), bad) || strings.Contains(string(encoded), bad) {
			t.Error("structured error exposed a canary")
		}
	}

	for _, path := range []string{"http://example.com/a", "//example.com", "/%2Fexample.com", "/\\example.com", "/foo/%2e%2e/bar", "/a#"} {
		if err := client.Request(ctx, "GET", path, nil, nil); err == nil {
			t.Errorf("accepted malformed path %q", path)
		}
	}
	for _, origin := range []string{"http://localhost:123", "http://127.0.0.1:65536", "http://127.0.0.1:0",
		"https://127.0.0.1:12", "http://someone@127.0.0.1:12", "http://127.0.0.1:12?", "http://127.0.0.1:12#", "http://127.0.0.1:12/api"} {
		client.Config.URL = origin
		if err := client.Request(ctx, "GET", "/direct", nil, nil); err == nil {
			t.Errorf("accepted malformed origin %q", origin)
		}
	}
}

type failingTransport struct{}

func (failingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("%s via http://proxyuser:proxypass@example.test", req.Header.Get("Authorization"))
}

func TestTransportErrorIsSafe(t *testing.T) {
	client := testClient(t, "http://127.0.0.1:123")
	client.HTTP = &http.Client{Transport: failingTransport{}}
	for _, method := range []string{"GET", "POST"} {
		err := client.Request(context.Background(), method, "/v1/connections?secret=hidden", nil, nil)
		if err == nil {
			t.Fatal("failing transport succeeded")
		}
		for _, bad := range []string{testManagementToken, "proxyuser", "proxypass", "hidden"} {
			if strings.Contains(err.Error(), bad) {
				t.Error("transport error exposed a credential")
			}
		}
	}
}

func TestReadPrivateToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "management.token")
	if err := os.WriteFile(path, []byte("  "+testManagementToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if token, err := ReadPrivateToken(path, 32, true); err != nil || token != testManagementToken {
		t.Fatal("private token was not read and trimmed")
	}
	if _, err := ReadPrivateToken(path, len(testManagementToken)+1, true); err == nil {
		t.Fatal("short token accepted")
	}
	link := path + "-link"
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPrivateToken(link, 32, true); err == nil {
		t.Fatal("private symlink accepted")
	}
	if _, err := ReadPrivateToken(link, 32, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPrivateToken(path, 32, true); err == nil {
		t.Fatal("non-private token accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, 64<<10+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPrivateToken(path, 32, true); err == nil {
		t.Fatal("oversize token accepted")
	}
}

func TestRedactSecretText(t *testing.T) {
	input := `Authorization: Bearer auth-canary
Proxy-Authorization: Basic proxy-auth-canary
{"X-Api-Key":"api-canary","password":"quoted pass-canary","token":"token-canary","secret":"secret-canary"}
Cookie: session=cookie-canary; id=second-cookie-canary
api_key=key-canary&keep=yes
Bearer bearer-canary, Basic basic-canary
sk-canary-key eyJcanary.header.signature
https://proxy-user:proxy-pass@proxy.example:4321 /Users/local-user/project /home/home-user/project`
	got := RedactSecretText(input)
	for _, bad := range []string{"canary", "proxy-user", "proxy-pass", "local-user", "home-user", ".signature"} {
		if strings.Contains(got, bad) {
			t.Errorf("redaction left %q in %q", bad, got)
		}
	}
	for _, want := range []string{"keep=yes", "proxy.example:4321", "/[REDACTED]/project"} {
		if !strings.Contains(got, want) {
			t.Errorf("redaction removed context %q", want)
		}
	}
}

func TestControlErrorReadIsBounded(t *testing.T) {
	reader := strings.NewReader(strings.Repeat("x", 8192))
	ce := decodeControlError(http.StatusBadGateway, reader, "")
	if reader.Len() != 4096 || ce.Code != "" || !strings.Contains(ce.Error(), "502") {
		t.Fatalf("unexpected error read: remaining=%d, error=%#v", reader.Len(), ce)
	}
}
