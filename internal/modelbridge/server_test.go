package modelbridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zwhu/memlane/internal/agents"
)

func bridgeFixture(t *testing.T, run runner) *handler {
	t.Helper()
	return &handler{config: Config{Root: t.TempDir(), Executable: "/pi", AuthHome: "/private-agent", Provider: "test", Model: "chosen", Helper: "/helper"}, token: "private-token", run: run, stop: func() {}, slot: make(chan struct{}, 1), active: map[string]context.CancelFunc{}}
}
func request(t *testing.T, h *handler, body, auth, origin string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Authorization", auth)
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

const structuredBody = `{"model":"memlane-pi","messages":[{"role":"system","content":"extract a project convention"},{"role":"user","content":"only-this-request-material"}],"response_format":{"type":"json_schema","json_schema":{"schema":{"type":"object","properties":{"rule":{"type":"string"}},"required":["rule"],"additionalProperties":false}}}}`

func completed(raw string) agents.PiCompletionExecution {
	return agents.PiCompletionExecution{PiRPCProcess: agents.PiRPCProcess{ProposalProcess: agents.ProposalProcess{Terminal: true, Reaped: true, GroupGone: true, Final: json.RawMessage(raw)}}}
}

func TestBridgeAuthorizationPreventsInference(t *testing.T) {
	var calls atomic.Int32
	h := bridgeFixture(t, func(context.Context, agents.PiCompletionOptions) (agents.PiCompletionExecution, error) {
		calls.Add(1)
		return completed(`{"rule":"x"}`), nil
	})
	for _, values := range [][2]string{{"", ""}, {"Bearer incorrect", ""}, {"Bearer private-token", "https://untrusted.example"}} {
		if result := request(t, h, structuredBody, values[0], values[1]); result.Code != 401 {
			t.Fatal(result.Code)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unauthorized request reached Agent")
	}
}

func TestModelBridgeClientUsesBearerAuthentication(t *testing.T) {
	h := bridgeFixture(t, nil)
	server := httptest.NewServer(h)
	defer server.Close()
	var result map[string]any
	if err := Call(context.Background(), server.URL, "/health", "GET", "private-token", "", nil, &result); err != nil || result["state"] != "ready" {
		t.Fatalf("bridge control authentication failed: %v %v", err, result)
	}
}
func TestBridgeSchemaAndTerminalBeforeSuccess(t *testing.T) {
	for _, values := range []struct {
		name, raw  string
		incomplete bool
		status     int
	}{{"valid", `{"rule":"preserve requestId"}`, false, 200}, {"missing property", `{}`, false, 502}, {"extra property", `{"rule":"x","unexpected":true}`, false, 502}, {"incomplete", `{"rule":"x"}`, true, 502}} {
		t.Run(values.name, func(t *testing.T) {
			h := bridgeFixture(t, func(_ context.Context, o agents.PiCompletionOptions) (agents.PiCompletionExecution, error) {
				if o.AuthHome != "/private-agent" || !strings.Contains(o.SystemPrompt, "extract a project convention") || !strings.Contains(o.Prompt, "only-this-request-material") {
					t.Fatal("request or selected execution context lost")
				}
				out := completed(values.raw)
				if values.incomplete {
					out.Terminal = false
				}
				return out, nil
			})
			response := request(t, h, structuredBody, "Bearer private-token", "")
			if response.Code != values.status {
				t.Fatalf("status %d: %s", response.Code, response.Body.String())
			}
			log, err := os.ReadFile(filepath.Join(h.config.Root, "events.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(log), "only-this-request-material") || strings.Contains(string(log), "private-token") || strings.Contains(string(log), "preserve requestId") {
				t.Fatal("sensitive request/output leaked to receipt")
			}
		})
	}
}
func TestBridgeCancelWaitsForRunnerCleanup(t *testing.T) {
	started := make(chan struct{})
	done := make(chan struct{})
	h := bridgeFixture(t, func(ctx context.Context, _ agents.PiCompletionOptions) (agents.PiCompletionExecution, error) {
		close(started)
		<-ctx.Done()
		out := completed(`{"rule":"late"}`)
		out.AbortSent = true
		out.AbortAcknowledged = true
		return out, ctx.Err()
	})
	var result *httptest.ResponseRecorder
	go func() { result = request(t, h, structuredBody, "Bearer private-token", ""); close(done) }()
	<-started
	r := httptest.NewRequest(http.MethodPost, "/v1/cancel", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer private-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	<-done
	if w.Code != 200 || result.Code != 408 || strings.Contains(result.Body.String(), "late") {
		t.Fatalf("cancel result %d %d", w.Code, result.Code)
	}
	if rejected := request(t, h, structuredBody, "Bearer private-token", ""); rejected.Code != 401 {
		t.Fatal("cancelled bridge admitted a new inference")
	}
}
