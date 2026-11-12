package official

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func captureFixture(kind string) CaptureEntry {
	source := `{"origin":"memlane_pi_capture_v1","host_session":"synthetic-session","answer_entry":"a1"}`
	entry := map[string]any{"type": "qa", "question": "synthetic question", "answer": "synthetic answer", "context": source}
	if kind == "trace" {
		entry = map[string]any{"type": "trace", "origin_function": "bash", "status": "success", "method_params": map[string]string{"command": "synthetic-check"}, "method_return_value": "passed", "error_message": "", "generate_feedback_with_llm": false, "memory_context": source}
	}
	raw, _ := json.Marshal(entry)
	return CaptureEntry{Operation: strings.Repeat("a", 64), Session: "pi-" + strings.Repeat("b", 48), Entry: raw}
}

func TestOfficialCaptureOnePostAndVerifiedTypedReceipt(t *testing.T) {
	for _, kind := range []string{"qa", "trace"} {
		t.Run(kind, func(t *testing.T) {
			instance, dataset, id := uuid.NewString(), uuid.NewString(), uuid.NewString()
			input := captureFixture(kind)
			identity, err := CaptureIdentity(input)
			if err != nil {
				t.Fatal(err)
			}
			posts, verifies := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Api-Key") != "synthetic-capture-key" {
					t.Error("capture key missing")
				}
				if r.URL.Path == "/api/v1/memlane/identity" {
					verifies++
					json.NewEncoder(w).Encode(map[string]string{"instance": instance, "cognee_version": "1.6.7"})
					return
				}
				if r.URL.Path != "/api/v1/remember/entry" || r.Method != "POST" {
					t.Error("unexpected request", r.Method, r.URL.Path)
					w.WriteHeader(404)
					return
				}
				posts++
				var body struct {
					Dataset string                     `json:"dataset_id"`
					Session string                     `json:"session_id"`
					Improve bool                       `json:"self_improvement"`
					Entry   map[string]json.RawMessage `json:"entry"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.Dataset != dataset || body.Session != input.Session || body.Improve {
					t.Error("scope or improvement changed")
				}
				key := "context"
				if kind == "trace" {
					key = "memory_context"
				}
				var text string
				json.Unmarshal(body.Entry[key], &text)
				var marker captureMarker
				if json.Unmarshal([]byte(text), &marker) != nil || marker.Operation != input.Operation || marker.Digest != identity.Digest || marker.Origin != "memlane_pi_capture_v1" {
					t.Error("official context marker missing")
				}
				if strings.Contains(text, "synthetic-capture-key") {
					t.Error("credential escaped")
				}
				json.NewEncoder(w).Encode(map[string]any{"status": "session_stored", "dataset_id": dataset, "session_ids": []string{input.Session}, "entry_type": kind, "entry_id": id})
			}))
			defer server.Close()
			receipt, err := (SessionReader{Endpoint: server.URL, Instance: instance, Dataset: dataset, Token: "synthetic-capture-key"}).WriteCaptureOnce(context.Background(), input)
			if err != nil || receipt.ID != id || receipt.Digest != identity.Digest || posts != 1 || verifies != 2 {
				t.Fatal(receipt, err, posts, verifies)
			}
			metadata, _ := json.Marshal(receipt)
			if strings.Contains(string(metadata), "synthetic question") || strings.Contains(string(metadata), "synthetic-capture-key") {
				t.Fatal("body or key in receipt")
			}
		})
	}
}

func TestOfficialCaptureUnverifiedOutcomeNeverRetriesPost(t *testing.T) {
	for _, failure := range []string{"http_error", "wrong_dataset", "wrong_session", "wrong_kind", "missing_id", "errored", "changed_instance"} {
		t.Run(failure, func(t *testing.T) {
			instance, dataset := uuid.NewString(), uuid.NewString()
			input := captureFixture("qa")
			posts, verifies := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/memlane/identity" {
					verifies++
					current := instance
					if verifies > 1 && failure == "changed_instance" {
						current = uuid.NewString()
					}
					json.NewEncoder(w).Encode(map[string]string{"instance": current, "cognee_version": "1.6.7"})
					return
				}
				posts++
				if failure == "http_error" {
					w.WriteHeader(503)
					return
				}
				result := map[string]any{"status": "session_stored", "dataset_id": dataset, "session_ids": []string{input.Session}, "entry_type": "qa", "entry_id": uuid.NewString()}
				switch failure {
				case "wrong_dataset":
					result["dataset_id"] = uuid.NewString()
				case "wrong_session":
					result["session_ids"] = []string{"other"}
				case "wrong_kind":
					result["entry_type"] = "trace"
				case "missing_id":
					delete(result, "entry_id")
				case "errored":
					result["status"] = "errored"
				}
				json.NewEncoder(w).Encode(result)
			}))
			defer server.Close()
			receipt, err := (SessionReader{Endpoint: server.URL, Instance: instance, Dataset: dataset, Token: "synthetic-key"}).WriteCaptureOnce(context.Background(), input)
			if err == nil || receipt.ID != "" || posts != 1 {
				t.Fatal(receipt, err, posts)
			}
		})
	}
}

func TestOfficialCaptureRejectsUnsupportedOrUnboundedPayloadBeforeHTTP(t *testing.T) {
	for _, mutate := range []func(map[string]any){
		func(m map[string]any) { m["type"] = "feedback" },
		func(m map[string]any) { m["node_set"] = []string{"foreign-scope"} },
		func(m map[string]any) { m["question"] = strings.Repeat("x", 4001) },
		func(m map[string]any) { m["context"] = `{}` },
	} {
		input := captureFixture("qa")
		var fields map[string]any
		json.Unmarshal(input.Entry, &fields)
		mutate(fields)
		input.Entry, _ = json.Marshal(fields)
		if _, err := CaptureIdentity(input); err == nil {
			t.Fatal("unsafe payload accepted", fields)
		}
	}
	for _, feedback := range []any{true, nil, "false"} {
		input := captureFixture("trace")
		var fields map[string]any
		json.Unmarshal(input.Entry, &fields)
		fields["generate_feedback_with_llm"] = feedback
		input.Entry, _ = json.Marshal(fields)
		if _, err := CaptureIdentity(input); err == nil {
			t.Fatal("feedback accepted", feedback)
		}
	}
	input := captureFixture("qa")
	before, _ := CaptureIdentity(input)
	var fields map[string]any
	json.Unmarshal(input.Entry, &fields)
	fields["answer"] = "changed"
	input.Entry, _ = json.Marshal(fields)
	after, _ := CaptureIdentity(input)
	if before.Digest == after.Digest || before.Operation != after.Operation {
		t.Fatal("payload drift does not conflict")
	}
}
