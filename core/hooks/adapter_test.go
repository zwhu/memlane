package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func privateKey(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.key")
	if err := os.WriteFile(path, []byte("test-agent-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHookEvents(t *testing.T) {
	t.Setenv("MEMLANE_PROCESSING_SESSION", "")
	dir := t.TempDir()
	scope := `"session_id":"session","cwd":` + string(mustJSON(t, dir)) + `,"workspace_roots":[` + string(mustJSON(t, dir)) + `]`
	type request struct {
		path string
		body object
	}
	requests := make(chan request, 20)
	client := localHandler(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "test-agent-key" || r.Method != "POST" {
			t.Error("wrong gateway authentication or method")
		}
		var body object
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests <- request{r.URL.Path, body}
		if r.URL.Path == "/recall" {
			if _, err := io.WriteString(w, `{"hits":[{"title":"Lesson","snippet":"Use the contract"},{"title":"  "}]}`); err != nil {
				t.Error(err)
			}
		} else {
			w.WriteHeader(202)
		}
	})
	cfg := Config{GatewayURL: "http://127.0.0.1:1234", KeyFile: privateKey(t), HTTPClient: client}
	cases := []struct{ event, fields, path, kind string }{
		{SessionStart, "", "", ""},
		{UserPromptSubmit, `,"prompt":"  question  "`, "/recall", ""},
		{PostToolUse, `,"tool_name":"bash","tool_use_id":"a","tool_input":{"cmd":"x"},"tool_response":"ok"`, "/ingest", "capture_trace"},
		{PostToolUseFailure, `,"origin_function":"bash","call_id":"b","error":{"message":"failed"}`, "/ingest", "capture_trace"},
		{Stop, `,"question":"q","last_assistant_message":"a","turn_id":"turn"`, "/ingest", "capture_qa"},
		{SessionEnd, `,"question":"q","answer":"a","event_id":"event"`, "/ingest", "capture_qa"},
		{PostToolUse, `,"tool_name":"memlane_test"`, "", ""},
		{PostToolUse, `,"tool_name":"cognee_recall"`, "", ""},
	}
	for _, host := range []string{"trae_ide", HostTraeX, HostCodex, HostAntigravity, HostPi} {
		hostCases := cases
		if host == HostPi {
			hostCases = append(append([]struct{ event, fields, path, kind string }{}, cases...),
				struct{ event, fields, path, kind string }{"recall", `,"query":"question"`, "/recall", ""},
				struct{ event, fields, path, kind string }{"capture", `,"event_id":"event","entry":{"type":"trace","origin_function":"bash","call_id":"pi"}`, "/ingest", "capture_trace"},
				struct{ event, fields, path, kind string }{"capture", `,"entry":{"question":"q","answer":"a"}`, "/ingest", "capture_qa"},
			)
		}
		for _, tc := range hostCases {
			t.Run(host+"/"+tc.event, func(t *testing.T) {
				out, err := Handle(context.Background(), host, tc.event, []byte("{"+scope+tc.fields+"}"), cfg)
				if err != nil {
					t.Fatal(err)
				}
				if tc.path == "" {
					if len(requests) != 0 || string(out) != "{}" {
						t.Fatalf("unexpected operation: %s", out)
					}
					return
				}
				select {
				case got := <-requests:
					if got.path != tc.path || got.body["workspace"] != dir || got.body["session_id"] != "session" {
						t.Fatalf("%+v", got)
					}
					if tc.path == "/recall" {
						if got.body["query"] != "question" || got.body["channel"] != "automatic" || !bytes.Contains(out, []byte("Lesson: Use the contract")) {
							t.Fatalf("%+v %s", got, out)
						}
						outputKey := "hookSpecificOutput"
						if host == HostPi {
							outputKey = "context"
						}
						if !bytes.Contains(out, []byte(outputKey)) {
							t.Fatalf("%s", out)
						}
						return
					}
					want := "{}"
					if host == HostPi && (tc.event == "capture" || tc.event == Stop || tc.event == SessionEnd) {
						want = `{"status":"complete"}`
					}
					if string(out) != want || got.body["kind"] != tc.kind {
						t.Fatalf("%+v %s", got, out)
					}
					content := got.body["content"].(object)
					if _, err := time.Parse(time.RFC3339, content["time"].(string)); err != nil {
						t.Fatal(err)
					}
					switch tc.event {
					case PostToolUse:
						if content["status"] != "success" || got.body["message_id"] != "tool:a" {
							t.Fatal(got)
						}
					case PostToolUseFailure:
						if content["status"] != "error" || got.body["message_id"] != "tool:b" {
							t.Fatal(got)
						}
					case Stop:
						if content["answer"] != "a" || got.body["message_id"] != "turn" || content["context"].(object)["date"] == "" {
							t.Fatal(got)
						}
					}
				default:
					t.Fatal("no forwarded request")
				}
			})
		}
	}
}

func TestCaptureQAPreservesDates(t *testing.T) {
	const date = "2026-11-20"
	const promptTime = "2026-11-24T23:55:00Z"
	const eventTime = "2026-11-24T23:59:00Z"
	for _, tc := range []struct {
		name       string
		context    any
		promptTime string
		eventTime  string
		want       object
	}{
		{"explicit date in map", object{"date": date, "source": "host"}, promptTime, eventTime, object{"date": date, "source": "host"}},
		{"explicit date in JSON", `{"date":"2026-11-20","source":"host"}`, "", "", object{"date": date, "source": "host"}},
		{"prompt time", object{"source": "host"}, promptTime, "", object{"source": "host"}},
		{"event time", nil, "", eventTime, nil},
		{"default date", object{"source": "host"}, "", "", object{"source": "host"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields := object{"question": "q", "answer": "a", "context": tc.context}
			if tc.promptTime != "" {
				fields["prompt_time"] = tc.promptTime
			}
			if tc.eventTime != "" {
				fields["time"] = tc.eventTime
			}
			kind, _, content := capture(fields, Stop)
			if kind != "capture_qa" {
				t.Fatalf("kind = %q, want capture_qa", kind)
			}
			if got := stringField(content, "prompt_time"); got != tc.promptTime {
				t.Errorf("prompt_time = %q, want %q", got, tc.promptTime)
			}
			gotTime := stringField(content, "time")
			capturedAt, err := time.Parse(time.RFC3339, gotTime)
			if err != nil {
				t.Fatal(err)
			}
			if tc.eventTime != "" && gotTime != tc.eventTime {
				t.Errorf("time = %q, want %q", gotTime, tc.eventTime)
			}
			if tc.name == "default date" {
				tc.want["date"] = capturedAt.Local().Format("2006-01-02")
				if _, ok := tc.context.(object)["date"]; ok {
					t.Error("capture mutated the incoming context")
				}
			}
			gotContext, present := content["context"]
			if tc.want == nil {
				if present {
					t.Errorf("unexpected context = %#v", gotContext)
				}
			} else if !reflect.DeepEqual(gotContext, tc.want) {
				t.Errorf("context = %#v, want %#v", gotContext, tc.want)
			}
		})
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHookFailsOpen(t *testing.T) {
	t.Setenv("MEMLANE_PROCESSING_SESSION", "")
	key := privateKey(t)
	symlink := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(key, symlink); err != nil {
		t.Fatal(err)
	}
	loose := filepath.Join(t.TempDir(), "loose")
	if err := os.WriteFile(loose, []byte("private"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(loose, 0o644); err != nil {
		t.Fatal(err)
	}
	client := localHandler(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("X-Api-Key") {
		case "forbidden":
			w.WriteHeader(http.StatusForbidden)
		case "overloaded":
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			w.Header().Set("Location", "http://example.com:1234")
			w.WriteHeader(http.StatusTemporaryRedirect)
		}
	})
	timeoutClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	const gateway = "http://127.0.0.1:1234"
	slow := filepath.Join(t.TempDir(), "slow")
	if err := os.WriteFile(slow, []byte("slow"), 0o600); err != nil {
		t.Fatal(err)
	}
	keyWithContent := func(value string) string {
		path := filepath.Join(t.TempDir(), "agent.key")
		writeConfig(t, path, value)
		return path
	}
	for _, tc := range []struct {
		url, key string
		want     error
		client   *http.Client
	}{
		{"http://example.com:1234", key, ErrInvalidGateway, client},
		{gateway, symlink, ErrInvalidKey, client},
		{gateway, loose, ErrInvalidKey, client},
		{gateway, slow, ErrTransport, timeoutClient},
		{"http://127.0.0.1:1", key, ErrTransport, nil},
		{gateway, key, HTTPError(307), client},
		{gateway, keyWithContent("forbidden"), HTTPError(403), client},
		{gateway, keyWithContent("overloaded"), HTTPError(503), client},
		{gateway, filepath.Join(t.TempDir(), "missing"), ErrInvalidKey, client},
	} {
		var out, note bytes.Buffer
		cfg := Config{GatewayURL: tc.url, KeyFile: tc.key, HTTPClient: tc.client, Timeout: 100 * time.Millisecond, Verbose: true, ErrOut: &note}
		raw := `{"prompt":"private prompt","cwd":"/project"}`
		if _, err := Handle(context.Background(), HostCodex, UserPromptSubmit, []byte(raw), cfg); !errors.Is(err, tc.want) {
			t.Fatalf("%v, want %v", err, tc.want)
		}
		if err := Run(context.Background(), HostCodex, UserPromptSubmit, strings.NewReader(raw), &out, cfg); err != nil || out.String() != "{}\n" {
			t.Fatalf("%s %v", out.String(), err)
		}
		if strings.Count(note.String(), "\n") != 1 || strings.Contains(note.String(), "private prompt") || strings.Contains(note.String(), tc.key) {
			t.Fatal(note.String())
		}
	}
}

type unreadable struct{}

func (unreadable) Read([]byte) (int, error) { panic("stdin must remain unread") }

func TestHookBypassAndIDEBinding(t *testing.T) {
	var out bytes.Buffer
	t.Setenv("MEMLANE_PROCESSING_SESSION", "1")
	if err := Run(context.Background(), HostPi, "capture", unreadable{}, &out, Config{}); err != nil || out.String() != "{}\n" {
		t.Fatalf("%s %v", out.String(), err)
	}
	t.Setenv("MEMLANE_PROCESSING_SESSION", "")
	for _, raw := range []string{
		`{"prompt":"q","cwd":"/project","workspace_roots":["/project","/other"],"project_id":"supplied"}`,
		`{"prompt":"q","cwd":"/project","workspace_roots":["/other"]}`,
		`{"prompt":"q","cwd":"/project","workspace_roots":[]}`,
	} {
		got, err := Handle(context.Background(), HostTraeIDE, UserPromptSubmit, []byte(raw), Config{})
		if err != nil || string(got) != "{}" {
			t.Fatalf("%s %v", got, err)
		}
	}
	if _, err := Handle(context.Background(), HostPi, "recall", []byte(`{"query":"q"}`), Config{MaxInputBytes: 4}); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func localHandler(handler http.HandlerFunc) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		defer r.Body.Close()
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Result(), nil
	})}
}
