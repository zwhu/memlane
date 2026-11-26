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
					wantSession := "session"
					if host == HostAntigravity {
						wantSession = "antigravity-session"
					}
					if got.path != tc.path || got.body["workspace"] != dir || got.body["session_id"] != wantSession {
						t.Fatalf("%+v", got)
					}
					if tc.path == "/recall" {
						if got.body["query"] != "question" || got.body["channel"] != "automatic" || !bytes.Contains(out, []byte("Lesson: Use the contract")) {
							t.Fatalf("%+v %s", got, out)
						}
						outputKey := "hookSpecificOutput"
						switch host {
						case HostPi:
							outputKey = "context"
						case HostAntigravity:
							outputKey = "injectSteps"
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
					if string(out) != want || got.body["kind"] != tc.kind || got.body["host"] != CanonicalHost(host) {
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

func writeTranscriptJSONL(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAntigravityPreInvocation(t *testing.T) {
	t.Setenv("MEMLANE_PROCESSING_SESSION", "")
	workspace := t.TempDir()
	key := privateKey(t)

	type reqRec struct {
		path string
		body object
	}
	requests := make(chan reqRec, 10)
	hitsBody := `{"hits":[{"title":"Memory","snippet":"Check boundaries first"}]}`
	client := localHandler(func(w http.ResponseWriter, r *http.Request) {
		var body object
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests <- reqRec{r.URL.Path, body}
		if _, err := io.WriteString(w, hitsBody); err != nil {
			t.Error(err)
		}
	})
	cfg := Config{GatewayURL: "http://127.0.0.1:1234", KeyFile: key, HTTPClient: client}

	t.Run("first invocation with workspacePaths and hits", func(t *testing.T) {
		payload := mustJSON(t, object{
			"conversationId": "ec33ebf9-0cba-4100-8142-c61503f6c587",
			"workspacePaths": []string{workspace},
			"lastUserInput":  "  Fix the failing unit test  ",
			"invocationNum":  0,
			"hookEventName":  PreInvocation,
		})
		out, err := Handle(context.Background(), HostAntigravity, PreInvocation, payload, cfg)
		if err != nil {
			t.Fatal(err)
		}
		got := <-requests
		if got.path != "/recall" || got.body["workspace"] != workspace ||
			got.body["session_id"] != "antigravity-ec33ebf9-0cba-4100-8142-c61503f6c587" ||
			got.body["query"] != "Fix the failing unit test" {
			t.Fatalf("unexpected recall request: %+v", got)
		}
		var decoded struct {
			InjectSteps []struct {
				SystemMessage struct {
					SystemMessage string `json:"systemMessage"`
				} `json:"systemMessage"`
			} `json:"injectSteps"`
		}
		if err := json.Unmarshal(out, &decoded); err != nil {
			t.Fatalf("invalid output JSON %s: %v", out, err)
		}
		if len(decoded.InjectSteps) != 1 || decoded.InjectSteps[0].SystemMessage.SystemMessage != "- Memory: Check boundaries first" {
			t.Fatalf("unexpected injectSteps: %s", out)
		}
	})

	t.Run("empty hits returns neutral object", func(t *testing.T) {
		hitsBody = `{"hits":[]}`
		defer func() { hitsBody = `{"hits":[{"title":"Memory","snippet":"Check boundaries first"}]}` }()
		payload := mustJSON(t, object{
			"conversationId": "antigravity-already-prefixed",
			"cwd":            workspace,
			"lastUserInput":  "Explain layout",
		})
		out, err := Handle(context.Background(), HostAntigravity, PreInvocation, payload, cfg)
		if err != nil {
			t.Fatal(err)
		}
		got := <-requests
		if got.body["session_id"] != "antigravity-already-prefixed" {
			t.Fatalf("double-prefixed session_id: %v", got.body["session_id"])
		}
		if string(out) != "{}" {
			t.Fatalf("expected {}, got %s", out)
		}
	})

	t.Run("empty lastUserInput skips recall", func(t *testing.T) {
		payload := mustJSON(t, object{
			"conversationId": "conv-empty",
			"cwd":            workspace,
			"lastUserInput":  "   ",
		})
		out, err := Handle(context.Background(), HostAntigravity, PreInvocation, payload, cfg)
		if err != nil || string(out) != "{}" || len(requests) != 0 {
			t.Fatalf("expected neutral skip, got out=%s err=%v reqs=%d", out, err, len(requests))
		}
	})

	t.Run("subsequent invocation skipped via invocationNum", func(t *testing.T) {
		payload := mustJSON(t, object{
			"conversationId": "conv-sub",
			"cwd":            workspace,
			"lastUserInput":  "Fix test",
			"invocationNum":  2,
		})
		out, err := Handle(context.Background(), HostAntigravity, PreInvocation, payload, cfg)
		if err != nil || string(out) != "{}" || len(requests) != 0 {
			t.Fatalf("expected skip on subsequent invocation, got out=%s err=%v reqs=%d", out, err, len(requests))
		}
	})

	t.Run("transcript deduplicates sub-invocation but recalls on new user turn", func(t *testing.T) {
		transcript := writeTranscriptJSONL(t,
			`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"Turn 1"}`,
			`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","tool_calls":[{"id":"c1","name":"run_command"}]}`,
		)
		subPayload := mustJSON(t, object{
			"conversationId": "conv-tr",
			"cwd":            workspace,
			"transcriptPath": transcript,
			"lastUserInput":  "Turn 1",
			"invocationNum":  1,
		})
		out, err := Handle(context.Background(), HostAntigravity, PreInvocation, subPayload, cfg)
		if err != nil || string(out) != "{}" || len(requests) != 0 {
			t.Fatalf("expected skip when MODEL step exists after user turn, got out=%s err=%v reqs=%d", out, err, len(requests))
		}

		// New turn added to transcript with no MODEL step yet recalls even if invocationNum is 1.
		transcript2 := writeTranscriptJSONL(t,
			`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"Turn 1"}`,
			`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","content":"Done"}`,
			`{"step_index":2,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"Turn 2"}`,
		)
		newTurnPayload := mustJSON(t, object{
			"conversationId": "conv-tr",
			"cwd":            workspace,
			"transcriptPath": transcript2,
			"lastUserInput":  "Turn 2",
			"invocationNum":  1,
		})
		out, err = Handle(context.Background(), HostAntigravity, PreInvocation, newTurnPayload, cfg)
		if err != nil || !bytes.Contains(out, []byte("injectSteps")) {
			t.Fatalf("expected recall on fresh user turn, got out=%s err=%v", out, err)
		}
		got := <-requests
		if got.body["query"] != "Turn 2" {
			t.Fatalf("unexpected query: %+v", got.body)
		}
	})

	t.Run("PreInvocation rejected for non-antigravity host", func(t *testing.T) {
		payload := mustJSON(t, object{"session_id": "s1", "cwd": workspace, "prompt": "hi"})
		if _, err := Handle(context.Background(), HostCodex, PreInvocation, payload, cfg); !errors.Is(err, ErrEvent) {
			t.Fatalf("expected ErrEvent for codex PreInvocation, got %v", err)
		}
	})
}

func TestAntigravityPostToolUse(t *testing.T) {
	t.Setenv("MEMLANE_PROCESSING_SESSION", "")
	workspace := t.TempDir()
	key := privateKey(t)

	requests := make(chan object, 10)
	client := localHandler(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ingest" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		var body object
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests <- body
		w.WriteHeader(http.StatusAccepted)
	})
	cfg := Config{GatewayURL: "http://127.0.0.1:1234", KeyFile: key, HTTPClient: client}

	t.Run("success toolCall", func(t *testing.T) {
		payload := mustJSON(t, object{
			"conversationId": "conv-tool",
			"workspacePaths": []string{workspace},
			"stepIdx":        5,
			"toolCall": object{
				"id":   "toolu_01",
				"name": "run_command",
				"args": object{"CommandLine": "npm test"},
			},
			"error": "",
		})
		out, err := Handle(context.Background(), HostAntigravity, PostToolUse, payload, cfg)
		if err != nil || string(out) != "{}" {
			t.Fatalf("out=%s err=%v", out, err)
		}
		got := <-requests
		content := got["content"].(object)
		params := content["method_params"].(object)
		if got["kind"] != "capture_trace" || got["host"] != HostAntigravity ||
			got["session_id"] != "antigravity-conv-tool" || got["workspace"] != workspace ||
			got["message_id"] != "tool:toolu_01" || content["origin_function"] != "run_command" ||
			content["status"] != "success" || params["CommandLine"] != "npm test" {
			t.Fatalf("unexpected trace payload: %+v", got)
		}
	})

	t.Run("error toolCall with stepIdx fallback", func(t *testing.T) {
		payload := mustJSON(t, object{
			"conversationId": "conv-tool",
			"cwd":            workspace,
			"stepIdx":        9,
			"toolCall": object{
				"name": "run_command",
				"args": object{"CommandLine": "go test ./..."},
			},
			"error": "exit status 1",
		})
		out, err := Handle(context.Background(), HostAntigravity, PostToolUse, payload, cfg)
		if err != nil || string(out) != "{}" {
			t.Fatalf("out=%s err=%v", out, err)
		}
		got := <-requests
		content := got["content"].(object)
		if got["message_id"] != "tool:step:9" || content["status"] != "error" || content["method_return_value"] != "exit status 1" {
			t.Fatalf("unexpected error trace: %+v", got)
		}
	})

	t.Run("skips cognee and memlane tools", func(t *testing.T) {
		for _, toolName := range []string{"cognee_recall", "memlane_remember"} {
			payload := mustJSON(t, object{
				"conversationId": "conv-tool",
				"cwd":            workspace,
				"stepIdx":        11,
				"toolCall":       object{"id": "skip-1", "name": toolName, "args": object{}},
			})
			out, err := Handle(context.Background(), HostAntigravity, PostToolUse, payload, cfg)
			if err != nil || string(out) != "{}" || len(requests) != 0 {
				t.Fatalf("tool %s should be skipped: out=%s err=%v reqs=%d", toolName, out, err, len(requests))
			}
		}
	})
}

func TestAntigravityStopTranscript(t *testing.T) {
	t.Setenv("MEMLANE_PROCESSING_SESSION", "")
	workspace := t.TempDir()
	key := privateKey(t)

	requests := make(chan object, 10)
	client := localHandler(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ingest" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		var body object
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests <- body
		w.WriteHeader(http.StatusAccepted)
	})
	cfg := Config{GatewayURL: "http://127.0.0.1:1234", KeyFile: key, HTTPClient: client}

	t.Run("multi-turn extracts latest completed turn deterministically", func(t *testing.T) {
		transcript := writeTranscriptJSONL(t,
			`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-11-26T07:00:00Z","content":"First question"}`,
			`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-26T07:00:05Z","content":"First answer"}`,
			`{"step_index":2,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-11-26T07:10:00Z","content":"Second question"}`,
			`{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-26T07:10:02Z","tool_calls":[{"id":"t1","name":"read_file"}]}`,
			`{"step_index":4,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-11-26T07:10:03Z","content":"file contents"}`,
			`{"step_index":5,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-26T07:10:08Z","content":"Second answer"}`,
			`{"step_index":6,"source":"MODEL","type":"PLANNER_RESPONSE","status":"IN_PROGRESS","content":"partial"}`,
		)
		payload := mustJSON(t, object{
			"conversationId":    "conv-multi",
			"workspacePaths":    []string{workspace},
			"transcriptPath":    transcript,
			"lastUserInput":     "Second question",
			"terminationReason": "NO_TOOL_CALL",
			"executionNum":      0,
			"fullyIdle":         true,
		})
		out1, err := Handle(context.Background(), HostAntigravity, Stop, payload, cfg)
		if err != nil || string(out1) != "{}" {
			t.Fatalf("out=%s err=%v", out1, err)
		}
		got1 := <-requests
		out2, err := Handle(context.Background(), HostAntigravity, Stop, payload, cfg)
		if err != nil || string(out2) != "{}" {
			t.Fatalf("repeat out=%s err=%v", out2, err)
		}
		got2 := <-requests

		content := got1["content"].(object)
		if got1["kind"] != "capture_qa" || got1["host"] != HostAntigravity ||
			got1["session_id"] != "antigravity-conv-multi" || got1["workspace"] != workspace ||
			got1["message_id"] != "turn:conv-multi:2:5" ||
			content["question"] != "Second question" || content["answer"] != "Second answer" ||
			content["prompt_time"] != "2026-11-26T07:10:00Z" || content["time"] != "2026-11-26T07:10:08Z" {
			t.Fatalf("unexpected capture_qa: %+v", got1)
		}
		if !reflect.DeepEqual(got1, got2) {
			t.Fatalf("repeated Stop payloads diverged:\n1: %+v\n2: %+v", got1, got2)
		}
	})

	t.Run("no-reply latest turn does not re-emit prior turn", func(t *testing.T) {
		transcript := writeTranscriptJSONL(t,
			`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"Old Q"}`,
			`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","content":"Old A"}`,
			`{"step_index":2,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"New Q"}`,
			`{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","tool_calls":[{"id":"t1","name":"run_command"}]}`,
		)
		payload := mustJSON(t, object{
			"conversationId": "conv-noreply",
			"cwd":            workspace,
			"transcriptPath": transcript,
			"lastUserInput":  "New Q",
		})
		out, err := Handle(context.Background(), HostAntigravity, Stop, payload, cfg)
		if err != nil || string(out) != "{}" || len(requests) != 0 {
			t.Fatalf("expected no capture for unanswered turn, out=%s err=%v reqs=%d", out, err, len(requests))
		}
	})

	t.Run("user canceled turn is skipped", func(t *testing.T) {
		transcript := writeTranscriptJSONL(t,
			`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"Run tests"}`,
			`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","content":"Starting tests..."}`,
		)
		payload := mustJSON(t, object{
			"conversationId":    "conv-cancel",
			"cwd":               workspace,
			"transcriptPath":    transcript,
			"terminationReason": "USER_CANCELED",
		})
		out, err := Handle(context.Background(), HostAntigravity, Stop, payload, cfg)
		if err != nil || string(out) != "{}" || len(requests) != 0 {
			t.Fatalf("expected canceled turn skip, out=%s err=%v reqs=%d", out, err, len(requests))
		}
	})

	t.Run("truncated user transcript falls back to lastUserInput", func(t *testing.T) {
		transcript := writeTranscriptJSONL(t,
			`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"short...<truncated>","truncated_fields":["content"]}`,
			`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","content":"Full reply"}`,
		)
		payload := mustJSON(t, object{
			"conversationId": "conv-trunc",
			"cwd":            workspace,
			"transcriptPath": transcript,
			"lastUserInput":  "Full untruncated user prompt",
		})
		out, err := Handle(context.Background(), HostAntigravity, Stop, payload, cfg)
		if err != nil || string(out) != "{}" {
			t.Fatalf("out=%s err=%v", out, err)
		}
		got := <-requests
		if got["content"].(object)["question"] != "Full untruncated user prompt" {
			t.Fatalf("expected untruncated question, got %+v", got)
		}
	})

	t.Run("oversized transcript reads bounded 1 MiB tail", func(t *testing.T) {
		filler := `{"step_index":0,"source":"MODEL","type":"GENERIC","status":"DONE","content":"` + strings.Repeat("x", 1024*600) + `"}`
		transcript := writeTranscriptJSONL(t,
			filler,
			filler,
			`{"step_index":10,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-11-26T08:00:00Z","content":"Tail Q"}`,
			`{"step_index":11,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-26T08:00:02Z","content":"Tail A"}`,
		)
		payload := mustJSON(t, object{
			"conversationId": "conv-large",
			"cwd":            workspace,
			"transcriptPath": transcript,
		})
		out, err := Handle(context.Background(), HostAntigravity, Stop, payload, cfg)
		if err != nil || string(out) != "{}" {
			t.Fatalf("out=%s err=%v", out, err)
		}
		got := <-requests
		content := got["content"].(object)
		if got["message_id"] != "turn:conv-large:10:11" || content["question"] != "Tail Q" || content["answer"] != "Tail A" {
			t.Fatalf("unexpected tail extraction: %+v", got)
		}
	})

	t.Run("symlink transcript is rejected and Run fails open", func(t *testing.T) {
		realFile := writeTranscriptJSONL(t,
			`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"Secret Q"}`,
			`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","content":"Secret A"}`,
		)
		linkPath := filepath.Join(t.TempDir(), "link.jsonl")
		if err := os.Symlink(realFile, linkPath); err != nil {
			t.Fatal(err)
		}
		payload := mustJSON(t, object{
			"conversationId": "conv-sym",
			"cwd":            workspace,
			"transcriptPath": linkPath,
		})
		if _, err := Handle(context.Background(), HostAntigravity, Stop, payload, cfg); !errors.Is(err, ErrInput) {
			t.Fatalf("expected ErrInput for symlink transcript, got %v", err)
		}
		var out bytes.Buffer
		if err := Run(context.Background(), HostAntigravity, Stop, bytes.NewReader(payload), &out, cfg); err != nil || out.String() != "{}\n" {
			t.Fatalf("Run did not fail open: out=%q err=%v", out.String(), err)
		}
		if len(requests) != 0 {
			t.Fatalf("symlink transcript must never forward to gateway, got %d requests", len(requests))
		}
	})

	t.Run("directory transcript is rejected and missing transcript is neutral", func(t *testing.T) {
		dirPayload := mustJSON(t, object{
			"conversationId": "conv-dir",
			"cwd":            workspace,
			"transcriptPath": t.TempDir(),
		})
		if _, err := Handle(context.Background(), HostAntigravity, Stop, dirPayload, cfg); !errors.Is(err, ErrInput) {
			t.Fatalf("expected ErrInput for directory transcript, got %v", err)
		}

		missingPayload := mustJSON(t, object{
			"conversationId": "conv-missing",
			"cwd":            workspace,
			"transcriptPath": filepath.Join(t.TempDir(), "does-not-exist.jsonl"),
		})
		out, err := Handle(context.Background(), HostAntigravity, Stop, missingPayload, cfg)
		if err != nil || string(out) != "{}" || len(requests) != 0 {
			t.Fatalf("expected neutral for missing transcript, out=%s err=%v reqs=%d", out, err, len(requests))
		}
	})
}

func TestDefaultHookTimeouts(t *testing.T) {
	t.Setenv("MEMLANE_PROCESSING_SESSION", "")
	key := privateKey(t)
	workspace := t.TempDir()

	for _, tc := range []struct {
		name      string
		event     string
		payload   object
		explicit  time.Duration
		minBudget time.Duration
		maxBudget time.Duration
	}{
		{
			name:      "recall defaults to 10s",
			event:     PreInvocation,
			payload:   object{"conversationId": "c1", "cwd": workspace, "lastUserInput": "find architecture notes"},
			minBudget: 8 * time.Second,
			maxBudget: 11 * time.Second,
		},
		{
			name:      "ingest defaults to 5s",
			event:     PostToolUse,
			payload:   object{"conversationId": "c1", "cwd": workspace, "toolCall": object{"id": "t1", "name": "run_command", "args": object{}}},
			minBudget: 3 * time.Second,
			maxBudget: 6 * time.Second,
		},
		{
			name:      "explicit timeout overrides default",
			event:     PreInvocation,
			payload:   object{"conversationId": "c1", "cwd": workspace, "lastUserInput": "find architecture notes"},
			explicit:  250 * time.Millisecond,
			minBudget: 50 * time.Millisecond,
			maxBudget: 500 * time.Millisecond,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var remaining time.Duration
			var hasDeadline bool
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var deadline time.Time
				deadline, hasDeadline = r.Context().Deadline()
				remaining = time.Until(deadline)
				w := httptest.NewRecorder()
				if _, err := io.WriteString(w, `{"hits":[]}`); err != nil {
					t.Error(err)
				}
				return w.Result(), nil
			})}
			cfg := Config{GatewayURL: "http://127.0.0.1:1234", KeyFile: key, HTTPClient: client, Timeout: tc.explicit}
			if _, err := Handle(context.Background(), HostAntigravity, tc.event, mustJSON(t, tc.payload), cfg); err != nil {
				t.Fatal(err)
			}
			if !hasDeadline {
				t.Fatal("expected request context deadline")
			}
			if remaining < tc.minBudget || remaining > tc.maxBudget {
				t.Fatalf("deadline remaining=%v, want in [%v, %v]", remaining, tc.minBudget, tc.maxBudget)
			}
		})
	}
}

func TestAntigravityXMLWrapperStripping(t *testing.T) {
	t.Setenv("MEMLANE_PROCESSING_SESSION", "")
	workspace := t.TempDir()
	key := privateKey(t)

	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "USER_REQUEST with ADDITIONAL_METADATA and USER_SETTINGS_CHANGE",
			raw:  "<USER_REQUEST>\nFix Antigravity XML tags\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: 2026-11-26T15:30:38+08:00.\n</ADDITIONAL_METADATA>\n<USER_SETTINGS_CHANGE>\nmode=fast\n</USER_SETTINGS_CHANGE>",
			want: "Fix Antigravity XML tags",
		},
		{
			name: "ADDITIONAL_METADATA without USER_REQUEST",
			raw:  "Explain WAL limits\n<ADDITIONAL_METADATA>\nThe current local time is: 2026-11-26\n</ADDITIONAL_METADATA>",
			want: "Explain WAL limits",
		},
		{
			name: "USER_SETTINGS_CHANGE only suffix",
			raw:  "  Check hooks  \n<USER_SETTINGS_CHANGE>\nfoo=bar\n</USER_SETTINGS_CHANGE>",
			want: "Check hooks",
		},
		{
			name: "plain text unchanged",
			raw:  "  Plain question text  ",
			want: "Plain question text",
		},
		{
			name: "metadata only returns empty",
			raw:  "<ADDITIONAL_METADATA>\nonly metadata\n</ADDITIONAL_METADATA>",
			want: "",
		},
		{
			name: "truncated unclosed USER_REQUEST",
			raw:  "<USER_REQUEST>\nLong truncated prompt...",
			want: "Long truncated prompt...",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cleanAntigravityUserText(tc.raw); got != tc.want {
				t.Fatalf("cleanAntigravityUserText() = %q, want %q", got, tc.want)
			}
		})
	}

	type reqRec struct {
		path string
		body object
	}
	requests := make(chan reqRec, 10)
	client := localHandler(func(w http.ResponseWriter, r *http.Request) {
		var body object
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests <- reqRec{r.URL.Path, body}
		if r.URL.Path == "/recall" {
			if _, err := io.WriteString(w, `{"hits":[{"title":"Rule","snippet":"Strip wrappers"}]}`); err != nil {
				t.Error(err)
			}
		} else {
			w.WriteHeader(http.StatusAccepted)
		}
	})
	cfg := Config{GatewayURL: "http://127.0.0.1:1234", KeyFile: key, HTTPClient: client}

	wrappedTurn1 := "<USER_REQUEST>\n为什么下午的对话没记录\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: 2026-11-26T15:30:38+08:00.\n</ADDITIONAL_METADATA>"
	wrappedTurn2 := "<USER_REQUEST>\n再查一下 hook 日志\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: 2026-11-26T15:35:00+08:00.\n</ADDITIONAL_METADATA>\n<USER_SETTINGS_CHANGE>\nfast=1\n</USER_SETTINGS_CHANGE>"

	t.Run("Stop strips XML wrappers from transcript USER_INPUT", func(t *testing.T) {
		transcript := writeTranscriptJSONL(t,
			string(mustJSON(t, object{
				"step_index": 0, "source": "USER_EXPLICIT", "type": "USER_INPUT", "status": "DONE",
				"created_at": "2026-11-26T07:30:38Z", "content": wrappedTurn1,
			})),
			string(mustJSON(t, object{
				"step_index": 1, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE",
				"created_at": "2026-11-26T07:30:45Z", "content": "已定位到旧版 hook 协议不兼容。",
			})),
		)
		payload := mustJSON(t, object{
			"conversationId": "conv-xml-stop",
			"workspacePaths": []string{workspace},
			"transcriptPath": transcript,
			"lastUserInput":  wrappedTurn1,
		})
		out, err := Handle(context.Background(), HostAntigravity, Stop, payload, cfg)
		if err != nil || string(out) != "{}" {
			t.Fatalf("out=%s err=%v", out, err)
		}
		got := <-requests
		content := got.body["content"].(object)
		if content["question"] != "为什么下午的对话没记录" {
			t.Fatalf("question = %q, want stripped text", content["question"])
		}
		if strings.Contains(content["question"].(string), "<USER_REQUEST>") {
			t.Fatalf("question still contains XML wrapper: %q", content["question"])
		}
	})

	t.Run("PreInvocation strips query and matches wrapped transcript consistently", func(t *testing.T) {
		// Transcript has wrapped USER_INPUT followed by MODEL -> plain lastUserInput must match and skip recall.
		transcriptAnswered := writeTranscriptJSONL(t,
			string(mustJSON(t, object{
				"step_index": 0, "source": "USER_EXPLICIT", "type": "USER_INPUT", "status": "DONE",
				"content": wrappedTurn1,
			})),
			string(mustJSON(t, object{
				"step_index": 1, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE",
				"tool_calls": []any{object{"id": "t1", "name": "run_command"}},
			})),
		)
		skipPayload := mustJSON(t, object{
			"conversationId": "conv-xml-recall",
			"cwd":            workspace,
			"transcriptPath": transcriptAnswered,
			"lastUserInput":  "为什么下午的对话没记录",
			"invocationNum":  1,
		})
		out, err := Handle(context.Background(), HostAntigravity, PreInvocation, skipPayload, cfg)
		if err != nil || string(out) != "{}" || len(requests) != 0 {
			t.Fatalf("expected skip when wrapped transcript matches plain lastUserInput, got out=%s err=%v reqs=%d", out, err, len(requests))
		}

		// Fresh wrapped turn with no MODEL step yet -> wrapped lastUserInput recalls with stripped query.
		transcriptFresh := writeTranscriptJSONL(t,
			string(mustJSON(t, object{
				"step_index": 0, "source": "USER_EXPLICIT", "type": "USER_INPUT", "status": "DONE",
				"content": wrappedTurn1,
			})),
			string(mustJSON(t, object{
				"step_index": 1, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE",
				"content": "Done",
			})),
			string(mustJSON(t, object{
				"step_index": 2, "source": "USER_EXPLICIT", "type": "USER_INPUT", "status": "DONE",
				"content": wrappedTurn2,
			})),
		)
		recallPayload := mustJSON(t, object{
			"conversationId": "conv-xml-recall",
			"cwd":            workspace,
			"transcriptPath": transcriptFresh,
			"lastUserInput":  wrappedTurn2,
			"invocationNum":  2,
		})
		out, err = Handle(context.Background(), HostAntigravity, PreInvocation, recallPayload, cfg)
		if err != nil || !bytes.Contains(out, []byte("injectSteps")) {
			t.Fatalf("expected recall on fresh wrapped turn, got out=%s err=%v", out, err)
		}
		got := <-requests
		if got.path != "/recall" || got.body["query"] != "再查一下 hook 日志" {
			t.Fatalf("unexpected recall query: %+v", got)
		}
	})
}

func writeBrainTranscript(t *testing.T, brainRoot, convID string, lines ...string) string {
	t.Helper()
	dir := filepath.Join(brainRoot, convID, ".system_generated", "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAntigravityParentLinkage(t *testing.T) {
	t.Setenv("MEMLANE_PROCESSING_SESSION", "")
	workspace := t.TempDir()
	key := privateKey(t)

	requests := make(chan object, 20)
	client := localHandler(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ingest" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		var body object
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests <- body
		w.WriteHeader(http.StatusAccepted)
	})
	cfg := Config{GatewayURL: "http://127.0.0.1:1234", KeyFile: key, HTTPClient: client}

	const parentUUID = "557aa2c8-1b54-4330-9577-e535cf0cfd97"
	const workerUUID = "18b4bf18-cc50-42c7-901d-7d837e901309"
	const memberOnlyUUID = "64805579-8d2f-4fb8-88a2-08400e59f5dd"
	const subagentUUID = "8637694b-fd95-4830-ac3a-08f2e66b9050"

	t.Run("swarm worker detected from sender and for @role in PostToolUse and Stop", func(t *testing.T) {
		brainRoot := t.TempDir()
		transcript := writeBrainTranscript(t, brainRoot, workerUUID,
			string(mustJSON(t, object{
				"step_index": 0, "source": "SYSTEM", "type": "SYSTEM_MESSAGE", "status": "DONE",
				"content": "The following is a <SYSTEM_MESSAGE>.\n\n<SYSTEM_MESSAGE>\n[Message] timestamp=2026-11-26T07:40:13Z sender=system priority=MESSAGE_PRIORITY_HIGH content=[swarm] new board message #3\n</SYSTEM_MESSAGE>",
			})),
			string(mustJSON(t, object{
				"step_index": 1, "source": "SYSTEM", "type": "SYSTEM_MESSAGE", "status": "DONE",
				"content": "The following is a <SYSTEM_MESSAGE>.\n\n<SYSTEM_MESSAGE>\n[Message] timestamp=2026-11-26T07:40:13Z sender=" + parentUUID + " priority=MESSAGE_PRIORITY_HIGH content=You have just started. Your assignment (recorded on the board as #3 for @verifier):\n\nInspect runtime layout.\n</SYSTEM_MESSAGE>",
			})),
			string(mustJSON(t, object{
				"step_index": 2, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE",
				"content": "Starting reconnaissance.",
			})),
		)

		toolPayload := mustJSON(t, object{
			"conversationId": workerUUID,
			"workspacePaths": []string{workspace},
			"transcriptPath": transcript,
			"stepIdx":        3,
			"toolCall":       object{"id": "toolu_w1", "name": "run_command", "args": object{"CommandLine": "git status"}},
		})
		if out, err := Handle(context.Background(), HostAntigravity, PostToolUse, toolPayload, cfg); err != nil || string(out) != "{}" {
			t.Fatalf("PostToolUse failed: out=%s err=%v", out, err)
		}
		gotTool := <-requests
		if gotTool["parent_session_id"] != "antigravity-"+parentUUID || gotTool["subagent_role"] != "verifier" {
			t.Fatalf("unexpected tool parent linkage: %+v", gotTool)
		}

		stopPayload := mustJSON(t, object{
			"conversationId": workerUUID,
			"workspacePaths": []string{workspace},
			"transcriptPath": transcript,
			"question":       "Verify hooks",
			"answer":         "All checks passed",
		})
		if out, err := Handle(context.Background(), HostAntigravity, Stop, stopPayload, cfg); err != nil || string(out) != "{}" {
			t.Fatalf("Stop failed: out=%s err=%v", out, err)
		}
		gotStop := <-requests
		if gotStop["parent_session_id"] != "antigravity-"+parentUUID || gotStop["subagent_role"] != "verifier" {
			t.Fatalf("unexpected stop parent linkage: %+v", gotStop)
		}
	})

	t.Run("swarm member role falls back to parent members.json lookup", func(t *testing.T) {
		brainRoot := t.TempDir()
		swarmDir := filepath.Join(brainRoot, parentUUID, ".system_generated", "swarm")
		if err := os.MkdirAll(swarmDir, 0o700); err != nil {
			t.Fatal(err)
		}
		membersJSON := `{"title":"Swarm","members":{"coordinator":{"conversation_id":"` + parentUUID + `","role":"coordinator"},"hook-adapter":{"conversation_id":"` + memberOnlyUUID + `","role":"worker"}}}`
		if err := os.WriteFile(filepath.Join(swarmDir, "members.json"), []byte(membersJSON), 0o600); err != nil {
			t.Fatal(err)
		}
		transcript := writeBrainTranscript(t, brainRoot, memberOnlyUUID,
			string(mustJSON(t, object{
				"step_index": 0, "source": "SYSTEM", "type": "SYSTEM_MESSAGE", "status": "DONE",
				"content": "<SYSTEM_MESSAGE>\n[Message] timestamp=2026-11-26T07:40:13Z sender=" + parentUUID + " priority=MESSAGE_PRIORITY_HIGH content=Implement Antigravity hooks.\n</SYSTEM_MESSAGE>",
			})),
		)
		payload := mustJSON(t, object{
			"conversationId": memberOnlyUUID,
			"cwd":            workspace,
			"transcriptPath": transcript,
			"toolCall":       object{"id": "toolu_m1", "name": "edit_file", "args": object{}},
		})
		if out, err := Handle(context.Background(), HostAntigravity, PostToolUse, payload, cfg); err != nil || string(out) != "{}" {
			t.Fatalf("out=%s err=%v", out, err)
		}
		got := <-requests
		if got["parent_session_id"] != "antigravity-"+parentUUID || got["subagent_role"] != "hook-adapter" {
			t.Fatalf("expected members.json role hook-adapter, got %+v", got)
		}
	})

	t.Run("regular invoke_subagent falls back to subagent role", func(t *testing.T) {
		brainRoot := t.TempDir()
		transcript := writeBrainTranscript(t, brainRoot, subagentUUID,
			string(mustJSON(t, object{
				"step_index": 0, "source": "SYSTEM", "type": "SYSTEM_MESSAGE", "status": "DONE",
				"content": "The following is a <SYSTEM_MESSAGE>.\n\n<SYSTEM_MESSAGE>\n[Message] timestamp=2026-11-25T14:20:13Z sender=" + parentUUID + " priority=MESSAGE_PRIORITY_HIGH content=Perform read-only code review.\n</SYSTEM_MESSAGE>",
			})),
		)
		payload := mustJSON(t, object{
			"conversationId": subagentUUID,
			"cwd":            workspace,
			"transcriptPath": transcript,
			"toolCall":       object{"id": "toolu_s1", "name": "read_file", "args": object{}},
		})
		if out, err := Handle(context.Background(), HostAntigravity, PostToolUse, payload, cfg); err != nil || string(out) != "{}" {
			t.Fatalf("out=%s err=%v", out, err)
		}
		got := <-requests
		if got["parent_session_id"] != "antigravity-"+parentUUID || got["subagent_role"] != "subagent" {
			t.Fatalf("expected subagent linkage, got %+v", got)
		}
	})

	t.Run("explicit payload fields take precedence and reject self-parenting", func(t *testing.T) {
		explicitPayload := mustJSON(t, object{
			"conversationId":       workerUUID,
			"parentConversationId": parentUUID,
			"subagentRole":         "custom-reviewer",
			"cwd":                  workspace,
			"toolCall":             object{"id": "toolu_e1", "name": "read_file", "args": object{}},
		})
		if out, err := Handle(context.Background(), HostAntigravity, PostToolUse, explicitPayload, cfg); err != nil || string(out) != "{}" {
			t.Fatalf("out=%s err=%v", out, err)
		}
		got := <-requests
		if got["parent_session_id"] != "antigravity-"+parentUUID || got["subagent_role"] != "custom-reviewer" {
			t.Fatalf("unexpected explicit parent linkage: %+v", got)
		}

		selfPayload := mustJSON(t, object{
			"conversationId":       workerUUID,
			"parentConversationId": "antigravity-" + workerUUID,
			"subagent_role":        "verifier",
			"cwd":                  workspace,
			"toolCall":             object{"id": "toolu_e2", "name": "read_file", "args": object{}},
		})
		if out, err := Handle(context.Background(), HostAntigravity, PostToolUse, selfPayload, cfg); err != nil || string(out) != "{}" {
			t.Fatalf("out=%s err=%v", out, err)
		}
		gotSelf := <-requests
		if _, hasParent := gotSelf["parent_session_id"]; hasParent {
			t.Fatalf("self-parent must be rejected, got %+v", gotSelf)
		}
		if _, hasRole := gotSelf["subagent_role"]; hasRole {
			t.Fatalf("self-parent must not attach subagent_role, got %+v", gotSelf)
		}
	})

	t.Run("transcript self-parent and task sender are ignored", func(t *testing.T) {
		brainRoot := t.TempDir()
		transcript := writeBrainTranscript(t, brainRoot, workerUUID,
			string(mustJSON(t, object{
				"step_index": 0, "source": "SYSTEM", "type": "SYSTEM_MESSAGE", "status": "DONE",
				"content": "<SYSTEM_MESSAGE>\n[Message] timestamp=2026-11-26T07:40:13Z sender=" + workerUUID + " priority=MESSAGE_PRIORITY_HIGH content=self message\n</SYSTEM_MESSAGE>",
			})),
			string(mustJSON(t, object{
				"step_index": 1, "source": "SYSTEM", "type": "SYSTEM_MESSAGE", "status": "DONE",
				"content": "<SYSTEM_MESSAGE>\n[Message] timestamp=2026-11-26T07:43:12Z sender=" + parentUUID + "/task-6 priority=MESSAGE_PRIORITY_HIGH content=[monitor] tick\n</SYSTEM_MESSAGE>",
			})),
		)
		payload := mustJSON(t, object{
			"conversationId": workerUUID,
			"cwd":            workspace,
			"transcriptPath": transcript,
			"toolCall":       object{"id": "toolu_self", "name": "read_file", "args": object{}},
		})
		if out, err := Handle(context.Background(), HostAntigravity, PostToolUse, payload, cfg); err != nil || string(out) != "{}" {
			t.Fatalf("out=%s err=%v", out, err)
		}
		got := <-requests
		if _, hasParent := got["parent_session_id"]; hasParent {
			t.Fatalf("self/task sender must not produce parent_session_id: %+v", got)
		}
	})

	t.Run("non-subagent parent session with later subagent reply emits no parent_session_id", func(t *testing.T) {
		brainRoot := t.TempDir()
		transcript := writeBrainTranscript(t, brainRoot, parentUUID,
			string(mustJSON(t, object{
				"step_index": 0, "source": "USER_EXPLICIT", "type": "USER_INPUT", "status": "DONE",
				"content": "<USER_REQUEST>\nRun parallel checks\n</USER_REQUEST>",
			})),
			string(mustJSON(t, object{
				"step_index": 1, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE",
				"content": "Spawning subagent.",
			})),
			string(mustJSON(t, object{
				"step_index": 2, "source": "SYSTEM", "type": "SYSTEM_MESSAGE", "status": "DONE",
				"content": "<SYSTEM_MESSAGE>\n[Message] timestamp=2026-11-26T07:45:00Z sender=" + subagentUUID + " priority=MESSAGE_PRIORITY_HIGH content=Subagent finished\n</SYSTEM_MESSAGE>",
			})),
		)
		payload := mustJSON(t, object{
			"conversationId": parentUUID,
			"cwd":            workspace,
			"transcriptPath": transcript,
			"toolCall":       object{"id": "toolu_top", "name": "run_command", "args": object{}},
		})
		if out, err := Handle(context.Background(), HostAntigravity, PostToolUse, payload, cfg); err != nil || string(out) != "{}" {
			t.Fatalf("out=%s err=%v", out, err)
		}
		got := <-requests
		if _, hasParent := got["parent_session_id"]; hasParent {
			t.Fatalf("top-level session must not emit parent_session_id: %+v", got)
		}
		if _, hasRole := got["subagent_role"]; hasRole {
			t.Fatalf("top-level session must not emit subagent_role: %+v", got)
		}
	})

	t.Run("symlink transcript and symlink members.json are rejected", func(t *testing.T) {
		brainRoot := t.TempDir()
		realTranscript := writeBrainTranscript(t, brainRoot, workerUUID,
			string(mustJSON(t, object{
				"step_index": 0, "source": "SYSTEM", "type": "SYSTEM_MESSAGE", "status": "DONE",
				"content": "<SYSTEM_MESSAGE>\n[Message] sender=" + parentUUID + " content=task\n</SYSTEM_MESSAGE>",
			})),
		)
		linkTranscript := filepath.Join(t.TempDir(), "transcript-link.jsonl")
		if err := os.Symlink(realTranscript, linkTranscript); err != nil {
			t.Fatal(err)
		}
		symPayload := mustJSON(t, object{
			"conversationId": workerUUID,
			"cwd":            workspace,
			"transcriptPath": linkTranscript,
			"toolCall":       object{"id": "toolu_sym1", "name": "run_command", "args": object{}},
		})
		if _, err := Handle(context.Background(), HostAntigravity, PostToolUse, symPayload, cfg); !errors.Is(err, ErrInput) {
			t.Fatalf("expected ErrInput for symlink transcript in PostToolUse, got %v", err)
		}

		// Symlink members.json
		swarmDir := filepath.Join(brainRoot, parentUUID, ".system_generated", "swarm")
		if err := os.MkdirAll(swarmDir, 0o700); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(t.TempDir(), "outside.json")
		if err := os.WriteFile(outside, []byte(`{"members":{"leaked":{"conversation_id":"`+workerUUID+`"}}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(swarmDir, "members.json")); err != nil {
			t.Fatal(err)
		}
		symMembersPayload := mustJSON(t, object{
			"conversationId": workerUUID,
			"cwd":            workspace,
			"transcriptPath": realTranscript,
			"toolCall":       object{"id": "toolu_sym2", "name": "run_command", "args": object{}},
		})
		if _, err := Handle(context.Background(), HostAntigravity, PostToolUse, symMembersPayload, cfg); !errors.Is(err, ErrInput) {
			t.Fatalf("expected ErrInput for symlink members.json, got %v", err)
		}
		if len(requests) != 0 {
			t.Fatalf("symlink inputs must never forward to gateway, got %d requests", len(requests))
		}
	})
}
