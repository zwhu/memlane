package supervisor

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLoopbackClientDisablesProxy(t *testing.T) {
	proxy := startIPv4TestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("loopback request reached the proxy or followed a redirect")
		w.WriteHeader(http.StatusBadGateway)
	}))
	for _, key := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"} {
		t.Setenv(key, proxy.URL)
	}
	target := startIPv4TestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "test-key" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("target did not receive authentication headers")
		}
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, proxy.URL, http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	headers := map[string]string{"X-Api-Key": "test-key", "Authorization": "Bearer test-token"}
	client := NewLoopbackClient(time.Second)
	// net/http bypasses proxies for literal loopback hosts by default, so
	// also verify that this client has explicitly disabled proxy selection.
	tr, ok := client.Transport.(*http.Transport)
	if !ok || tr.Proxy != nil {
		t.Fatalf("client transport does not explicitly disable proxying: %T", client.Transport)
	}
	for _, tc := range []struct {
		path   string
		status int
	}{{"/ok", http.StatusOK}, {"/redirect", http.StatusFound}} {
		req, err := http.NewRequest(http.MethodGet, target.URL+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Fatalf("GET %s returned HTTP %d, want %d", tc.path, resp.StatusCode, tc.status)
		}
	}
	result, err := LoopbackGetJSON(context.Background(), target.URL, "/ok", headers, time.Second)
	if err != nil || result["ok"] != true {
		t.Fatalf("LoopbackGetJSON: %v, %v", result, err)
	}
	_, err = LoopbackGetJSON(context.Background(), target.URL, "/redirect", headers, time.Second)
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusFound {
		t.Fatalf("LoopbackGetJSON redirect: %v", err)
	}
}

// LoopbackGetJSON requires an explicit IPv4 loopback origin.
func startIPv4TestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler}}
	server.Start()
	t.Cleanup(server.Close)
	return server
}
