package completion

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/zwhu/memlane/kit/agents"
	"github.com/zwhu/memlane/kit/agents/bridge"
)

const testToken = "sample-bearer-token-must-be-long"
const testRequest = `{"model":"test","messages":[{"role":"user","content":"hi"}]}`

func setupProfile(t *testing.T, script string) *ActiveProfile {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, "cli")
	for file, data := range map[string]string{path: script, filepath.Join(home, "auth.json"): "{}", filepath.Join(home, "config.toml"): `model = "test"`} {
		mode := os.FileMode(0o600)
		if file == path {
			mode = 0o700
		}
		if err := os.WriteFile(file, []byte(data), mode); err != nil {
			t.Fatal(err)
		}
	}
	return &ActiveProfile{Mode: "agent_bridge", BridgeKind: "codex", Model: "test", Executable: path, AuthHome: home,
		Defaults: &agents.ModelDefaults{Model: "test", Provider: "openai", Source: "agent_config"}}
}

type gateFunc func(context.Context) (*ActiveProfile, error)

func (f gateFunc) ResolveActive(ctx context.Context) (*ActiveProfile, error) { return f(ctx) }

func request(s *Server, ctx context.Context, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(ctx, method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w
}
func failCode(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var body struct{ Error map[string]string }
	if w.Code != status || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Error["code"] != code || body.Error["message"] != code || body.Error["type"] != "completion_error" {
		t.Fatalf("response %d %s, want %d %s", w.Code, w.Body, status, code)
	}
}

func TestAuthAndProfileFailures(t *testing.T) {
	profile := setupProfile(t, "#!/bin/sh\nexit 1\n")
	ok := func(context.Context, bridge.ExecOptions, *bridge.ParsedRequest) (string, error) { return "reply", nil }
	s, err := NewServer(Config{Token: testToken, Gate: StaticProfileGate{profile}, Executor: ok})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	complete := "/v1/chat/completions"
	for _, token := range []string{"", "wrong", testToken + " "} {
		failCode(t, request(s, ctx, "POST", complete, token, testRequest), 401, "unauthorized")
	}
	if w := request(s, ctx, "POST", complete, testToken, testRequest); w.Code != 200 || !strings.Contains(w.Body.String(), `"content":"reply"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	failCode(t, request(s, ctx, "POST", complete, testToken, strings.Replace(testRequest, `"test"`, `"other"`, 1)), 400, "codex_model_mismatch")
	var typedNil *StaticProfileGate
	inactive := *profile
	inactive.Mode = "provider"
	changed := *profile
	changed.ExpectedFingerprint = strings.Repeat("0", 64)
	mismatch := *profile
	mismatch.Model = "different"
	for _, item := range []struct {
		gate   ActiveProfileGate
		status int
		code   string
	}{
		{nil, 503, "bridge_inactive"}, {typedNil, 503, "bridge_inactive"}, {StaticProfileGate{&inactive}, 503, "bridge_inactive"},
		{StaticProfileGate{&changed}, 409, "generation_bridge_changed_revalidate"},
		{gateFunc(func(context.Context) (*ActiveProfile, error) { return &mismatch, nil }), 409, "bridge_profile_mismatch"},
	} {
		s, err := NewServer(Config{Token: testToken, Gate: item.gate, Executor: ok})
		if err != nil {
			t.Fatal(err)
		}
		failCode(t, request(s, ctx, "GET", "/health", "", ""), item.status, item.code)
		failCode(t, request(s, ctx, "POST", complete, testToken, testRequest), item.status, item.code)
	}
}

func TestBoundsAndSafeErrors(t *testing.T) {
	profile := setupProfile(t, "#!/bin/sh\nexit 1\n")
	ctx := context.Background()
	complete := "/v1/chat/completions"
	for _, testErr := range []error{errors.New("Bearer sk-private"), &bridge.BridgeError{Code: "provider_secret", Status: 400}} {
		s, err := NewServer(Config{Token: testToken, Gate: StaticProfileGate{profile}, MaxBodyBytes: 128,
			Executor: func(context.Context, bridge.ExecOptions, *bridge.ParsedRequest) (string, error) { return "", testErr }})
		if err != nil {
			t.Fatal(err)
		}
		failCode(t, request(s, ctx, "POST", complete, testToken, testRequest), 502, "bridge_execution_failed")
		failCode(t, request(s, ctx, "POST", complete, testToken, strings.Repeat("x", 129)), 413, "request_body_too_large")
		failCode(t, request(s, ctx, "GET", complete, testToken, ""), 405, "method_not_allowed")
		failCode(t, request(s, ctx, "GET", "/missing", "", ""), 404, "not_found")
	}
	for _, cfg := range []Config{{Token: "short"}, {Token: testToken, MaxConcurrency: 3, MaxInflight: 2},
		{Token: testToken, MaxInflight: 33}, {Token: testToken, IdleTimeout: -time.Second}} {
		if _, err := NewServer(cfg); err == nil {
			t.Errorf("allowed invalid configuration")
		}
	}
}

func TestConcurrencyAndProfileRecheck(t *testing.T) {
	for _, scenario := range []string{"waiter-cancel", "profile-switch", "fingerprint-drift", "success"} {
		t.Run(scenario, func(t *testing.T) {
			profile := setupProfile(t, "#!/bin/sh\nexit 1\n")
			var active atomic.Pointer[ActiveProfile]
			active.Store(profile)
			checked := make(chan struct{}, 8)
			gate := gateFunc(func(context.Context) (*ActiveProfile, error) {
				current := active.Load()
				checked <- struct{}{}
				return current, nil
			})
			entered := make(chan struct{}, 2)
			release := make(chan struct{})
			var calls atomic.Int32
			s, err := NewServer(Config{Token: testToken, Gate: gate, MaxConcurrency: 1, MaxInflight: 2, Executor: func(ctx context.Context, opts bridge.ExecOptions, req *bridge.ParsedRequest) (string, error) {
				if calls.Add(1) == 1 {
					entered <- struct{}{}
					select {
					case <-release:
						return "", errors.New("internal secret")
					case <-ctx.Done():
						return "", ctx.Err()
					}
				}
				return "next", nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			var first, second = make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			wait, cancelWait := context.WithCancel(ctx)
			defer cancelWait()
			go func() { first <- request(s, ctx, "POST", "/v1/chat/completions", testToken, testRequest) }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("executor did not start")
			}
			<-checked
			<-checked
			bodyRead := make(chan struct{}, 1)
			go func() {
				body := io.MultiReader(&readNotice{ready: bodyRead}, strings.NewReader(testRequest))
				req := httptest.NewRequestWithContext(wait, "POST", "/v1/chat/completions", body)
				req.Header.Set("Authorization", "Bearer "+testToken)
				recorder := httptest.NewRecorder()
				s.Handler().ServeHTTP(recorder, req)
				second <- recorder
			}()
			select {
			case <-bodyRead:
			case <-ctx.Done():
				t.Fatal("waiter did not finish its initial profile check")
			}
			failCode(t, request(s, ctx, "POST", "/v1/chat/completions", testToken, testRequest), 429, "bridge_busy")
			switch scenario {
			case "waiter-cancel":
				cancelWait()
				failCode(t, takeResponse(t, ctx, second), 429, "bridge_busy")
			case "profile-switch":
				newProfile := *profile
				newProfile.Mode = "provider"
				active.Store(&newProfile)
			case "fingerprint-drift":
				if err := os.Chtimes(profile.Executable, time.Unix(100, 0), time.Unix(100, 0)); err != nil {
					t.Fatal(err)
				}
			}
			close(release)
			failCode(t, takeResponse(t, ctx, first), 502, "bridge_execution_failed")
			switch scenario {
			case "profile-switch":
				failCode(t, takeResponse(t, ctx, second), 503, "bridge_inactive")
				active.Store(profile)
			case "fingerprint-drift":
				failCode(t, takeResponse(t, ctx, second), 409, "generation_bridge_changed_revalidate")
			case "success":
				if w := takeResponse(t, ctx, second); w.Code != 200 {
					t.Fatalf("waiter: %d %s", w.Code, w.Body)
				}
			}
			if w := request(s, ctx, "POST", "/v1/chat/completions", testToken, testRequest); w.Code != 200 {
				t.Fatalf("slot leaked: %d %s", w.Code, w.Body)
			}
		})
	}
}

func takeResponse(t *testing.T, ctx context.Context, result <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case w := <-result:
		return w
	case <-ctx.Done():
		t.Fatal("request did not finish")
	}
	return nil
}

type readNotice struct{ ready chan<- struct{} }

func (notice *readNotice) Read([]byte) (int, error) {
	notice.ready <- struct{}{}
	return 0, io.EOF
}

type handlerTransport struct{ handler http.Handler }

func (h handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, req)
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	return recorder.Result(), nil
}

func TestHTTPCodexRetryAndCancel(t *testing.T) {
	for _, mode := range []string{"handler", "loopback"} {
		t.Run(mode, func(t *testing.T) { runHTTPCodex(t, mode == "loopback") })
	}
}

func runHTTPCodex(t *testing.T, loopback bool) {
	ready := filepath.Join(t.TempDir(), "ready")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	script := `#!/bin/sh
if [ "$1" = features ]; then printf 'plugins stable false\n'; exit 0; fi
IFS= read -r line || exit 3
printf '%s\n' '{"id":1,"result":{}}'
IFS= read -r line || exit 3
IFS= read -r line || exit 3
printf '%s\n' '{"id":2,"result":{"thread":{"id":"t"},"model":"test","modelProvider":"openai","runtimeWorkspaceRoots":[],"activePermissionProfile":{"id":"memlane_completion"}}}'
IFS= read -r turn || exit 3
printf '%s\n' '{"id":3,"result":{"turn":{"id":"turn"}}}'
printf '%s\n' '{"method":"turn/started","params":{"threadId":"t","turn":{"id":"turn"}}}'
case "$turn" in
  *cancel-me*)
    /bin/sleep 300 &
    grandson=$!
    trap 'wait "$grandson"; exit 0' TERM INT
    printf '%s' "$grandson" > ` + quote(ready) + `
    while IFS= read -r line; do :; done
    wait "$grandson"
    ;;
  *)
    printf '%s\n' '{"method":"item/completed","params":{"threadId":"t","turnId":"turn","item":{"type":"agentMessage","text":"{\"text\":\"partial\"}"}}}'
    printf '%s\n' '{"method":"error","params":{"threadId":"t","turnId":"turn","willRetry":true,"error":{"codexErrorInfo":"responseStreamDisconnected","message":"Bearer sk-local-secret"}}}'
    printf '%s\n' '{"method":"item/completed","params":{"threadId":"t","turnId":"turn","item":{"type":"agentMessage","text":"{\"text\":\"ok\"}"}}}'
    printf '%s\n' '{"method":"turn/completed","params":{"threadId":"t","turn":{"id":"turn","status":"completed","error":null}}}'
    ;;
esac
`
	profile := setupProfile(t, script)
	trace := make(chan bridge.TraceEvent, 1)
	s, err := NewServer(Config{Token: testToken, Gate: StaticProfileGate{profile}, OnTrace: func(event bridge.TraceEvent) { trace <- event }})
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"localhost:0", "0.0.0.0:0", "[::1]:0"} {
		if _, err := s.StartLoopback(address); err == nil {
			t.Errorf("accepted %s", address)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := &http.Client{Transport: handlerTransport{s.Handler()}}
	base := "http://127.0.0.1"
	if loopback {
		addr, err := s.StartLoopback("127.0.0.1:0")
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			t.Skipf("sandbox denies loopback bind: %v", err)
		}
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := s.Shutdown(ctx); err != nil {
				t.Error(err)
			}
		}()
		client.Transport = &http.Transport{Proxy: nil}
		base = "http://" + addr
	}
	defer client.CloseIdleConnections()
	health, err := client.Get(base + "/health")
	if err != nil {
		t.Fatal(err)
	}
	health.Body.Close()
	if health.StatusCode != 200 {
		t.Fatalf("health: %s", health.Status)
	}
	send := func(ctx context.Context, body string) (*http.Response, error) {
		r, err := http.NewRequestWithContext(ctx, "POST", base+"/v1/chat/completions", strings.NewReader(body))
		if err != nil {
			return nil, err
		}
		r.Header.Set("Authorization", "Bearer "+testToken)
		return client.Do(r)
	}
	response, err := send(ctx, testRequest)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(data), `"content":"ok"`) || strings.Contains(string(data), "partial") {
		t.Fatalf("completion: %d %s %v", response.StatusCode, data, err)
	}
	select {
	case event := <-trace:
		if !event.WillRetry || event.Code != "codex_stream_disconnected" || event.RequestID == "" {
			t.Fatalf("trace %+v", event)
		}
	case <-ctx.Done():
		t.Fatal("missing retry trace")
	}
	cancelCtx, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan error, 1)
	go func() {
		resp, err := send(cancelCtx, strings.Replace(testRequest, `"hi"`, `"cancel-me"`, 1))
		if resp != nil {
			resp.Body.Close()
		}
		done <- err
	}()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	var pid int
	for pid <= 1 {
		select {
		case <-ctx.Done():
			t.Fatal("subprocess did not enter the turn")
		case <-tick.C:
			if raw, err := os.ReadFile(ready); err == nil {
				pid, _ = strconv.Atoi(string(raw))
			}
		}
	}
	stop()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("client did not cancel")
	}
	for s.admitted.Load() != 0 {
		select {
		case <-tick.C:
		case <-ctx.Done():
			t.Fatal("server retained the execution slot")
		}
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("grandchild %d still exists: %v", pid, err)
	}
}
