package processing

// HTTP fixtures test request binding and rejection rules only. They do not
// establish real supplier/model support or upstream cancellation/billing.
import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zwhu/memlane/internal/records"
)

var httpFixtureSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"required":["fact","source"],"properties":{"fact":{"type":"integer","const":7},"source":{"type":"object","additionalProperties":false,"required":["id"],"properties":{"id":{"type":"string","const":"explicit-fixture"}}}}}`)

func responseFixture(protocol, content string) map[string]any {
	switch protocol {
	case "openai-responses":
		return map[string]any{"model": "fixture-model", "status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]string{"type": "output_text", "text": content}}}}}
	case "openai-chat":
		return map[string]any{"model": "fixture-model", "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"role": "assistant", "content": content}}}}
	default:
		return map[string]any{"model": "fixture-model", "stop_reason": "end_turn", "content": []any{map[string]string{"type": "text", "text": content}}}
	}
}

func TestHTTPNativeSchemaBindingAllProtocols(t *testing.T) {
	for _, protocol := range []string{"openai-responses", "openai-chat", "anthropic-messages"} {
		t.Run(protocol, func(t *testing.T) {
			var received map[string]any
			var actualPath, auth, key string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				actualPath, auth, key = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("x-api-key")
				if json.NewDecoder(r.Body).Decode(&received) != nil {
					w.WriteHeader(400)
					return
				}
				json.NewEncoder(w).Encode(responseFixture(protocol, `{"fact":7,"source":{"id":"explicit-fixture"}}`))
			}))
			defer srv.Close()
			b := HTTPBinding{Spec: records.ProcessingSpec{Mode: "provider", Provider: "explicit fixture", Model: "fixture-model", Protocol: protocol, Endpoint: srv.URL + "/v1"}, Credential: []byte("synthetic-credential")}
			out, err := CompleteHTTP(context.Background(), b, "synthetic fixture system", "synthetic fixture prompt", httpFixtureSchema)
			if err != nil || !out.Terminal || out.Model != "fixture-model" {
				t.Fatal("native protocol fixture failed", err)
			}
			var full any
			if json.Unmarshal(httpFixtureSchema, &full) != nil {
				t.Fatal("fixture schema invalid")
			}
			var supplied any
			wantPath := "/v1/responses"
			switch protocol {
			case "openai-responses":
				format := received["text"].(map[string]any)["format"].(map[string]any)
				supplied = format["schema"]
				if format["type"] != "json_schema" || format["strict"] != true || received["store"] != false {
					t.Fatal("Responses native schema/store gate missing")
				}
			case "openai-chat":
				wantPath = "/v1/chat/completions"
				format := received["response_format"].(map[string]any)
				definition := format["json_schema"].(map[string]any)
				supplied = definition["schema"]
				if format["type"] != "json_schema" || definition["strict"] != true {
					t.Fatal("Chat native schema gate missing")
				}
			case "anthropic-messages":
				wantPath = "/v1/messages"
				format := received["output_config"].(map[string]any)["format"].(map[string]any)
				supplied = format["schema"]
				if format["type"] != "json_schema" || key != "synthetic-credential" || auth != "" {
					t.Fatal("Anthropic native schema/auth gate missing")
				}
			}
			if actualPath != wantPath || !reflect.DeepEqual(full, supplied) || received["tools"] != nil || received["stream"] != false {
				t.Fatal("complete schema/path/tool isolation changed")
			}
			if protocol != "anthropic-messages" && (auth != "Bearer synthetic-credential" || key != "") {
				t.Fatal("OpenAI credential binding changed")
			}
		})
	}
}

func TestHTTPSchemaFailureNeverFallsBack(t *testing.T) {
	for _, protocol := range []string{"openai-responses", "openai-chat", "anthropic-messages"} {
		t.Run(protocol, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				json.NewEncoder(w).Encode(responseFixture(protocol, `{"fact":8,"source":{"id":"fabricated-fixture"}}`))
			}))
			defer srv.Close()
			b := HTTPBinding{Spec: records.ProcessingSpec{Mode: "provider", Protocol: protocol, Model: "fixture", Endpoint: srv.URL}}
			if _, err := CompleteHTTP(context.Background(), b, "synthetic", "synthetic", httpFixtureSchema); err == nil || calls != 1 {
				t.Fatal("invalid schema accepted or retry/fallback attempted", err, calls)
			}
		})
	}
}

func TestHTTPRedirectCannotForwardCredential(t *testing.T) {
	forwarded := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded = true }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, 307) }))
	defer srv.Close()
	b := HTTPBinding{Spec: records.ProcessingSpec{Mode: "provider", Protocol: "openai-responses", Model: "fixture", Endpoint: srv.URL}, Credential: []byte("synthetic-only")}
	if _, err := CompleteHTTP(context.Background(), b, "synthetic", "synthetic", httpFixtureSchema); err == nil || forwarded {
		t.Fatal("redirect accepted or forwarded the credential")
	}
}

func TestHTTPCancelRejectsLateResponseAndMagpieRejectsCredential(t *testing.T) {
	started, finish := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-finish
		json.NewEncoder(w).Encode(responseFixture("openai-responses", `{"fact":7,"source":{"id":"explicit-fixture"}}`))
	}))
	defer srv.Close()
	b := HTTPBinding{Spec: records.ProcessingSpec{Mode: "magpie", Protocol: "openai-responses", Model: "fixture", Endpoint: srv.URL}}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := CompleteHTTP(ctx, b, "synthetic", "synthetic", httpFixtureSchema); result <- err }()
	<-started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("transport cancellation was not reported", err)
		}
	case <-time.After(time.Second):
		close(finish)
		t.Fatal("cancel did not release local request")
	}
	close(finish)
	b.Credential = []byte("synthetic-should-never-be-sent")
	if _, err := CompleteHTTP(context.Background(), b, "synthetic", "synthetic", httpFixtureSchema); err == nil {
		t.Fatal("Magpie forwarded independent Provider credentials")
	}
}

func TestHTTPModelCatalogPreservesExactIDsAndIncompletePage(t *testing.T) {
	var actualPath, actualAfter string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actualPath, actualAfter = r.URL.Path, r.URL.Query().Get("after_id")
		json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"id": "exact-fixture-id", "display_name": "API supplied fixture name"}}, "has_more": true, "last_id": "exact-fixture-id"})
	}))
	defer srv.Close()
	b := HTTPBinding{Spec: records.ProcessingSpec{Mode: "provider", Protocol: "anthropic-messages", Endpoint: srv.URL + "/v1"}, Credential: []byte("synthetic-private-credential")}
	catalog, err := ReadModels(context.Background(), b, "previous+fixture&cursor")
	if err != nil || len(catalog.Models) != 1 || catalog.Models[0].ID != "exact-fixture-id" ||
		!catalog.HasMore || catalog.LastID != "exact-fixture-id" || catalog.ReadMS == 0 || actualPath != "/v1/models" || actualAfter != "previous+fixture&cursor" {
		t.Fatal("directory fabricated identifiers or lost pagination", catalog, err)
	}
	raw, err := json.Marshal(b)
	if err != nil || strings.Contains(string(raw), "synthetic-private-credential") || strings.Contains(string(raw), "Credential") {
		t.Fatal("transient credential entered a serialized binding")
	}
	b.Close()
	if len(b.Credential) != 0 {
		t.Fatal("task credential remained after Close")
	}
}

func TestHTTPMalformedDirectoryIsUnknownRatherThanEmptySuccess(t *testing.T) {
	for _, fixture := range []string{`{}`, `{"data":[{"id":"same"},{"id":"same"}]}`, `{"data":[],"has_more":true}`} {
		t.Run(fixture, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(fixture)) }))
			defer srv.Close()
			b := HTTPBinding{Spec: records.ProcessingSpec{Mode: "magpie", Protocol: "openai-responses", Endpoint: srv.URL}}
			if catalog, err := ReadModels(context.Background(), b, ""); err == nil || catalog.ReadMS != 0 {
				t.Fatal("bad directory became trustworthy zero", catalog, err)
			}
		})
	}
}
