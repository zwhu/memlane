package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

type piCaptureDeliveryFixture struct {
	s           *Server
	q           records.PiCaptureBatchRequest
	grant       records.PiCaptureConsent
	posts       atomic.Int64
	lookups     atomic.Int64
	unavailable atomic.Bool
	started     chan struct{}
	startOnce   sync.Once
}

func (f *piCaptureDeliveryFixture) qa(id string) records.OfficialCaptureInput {
	operation := piCaptureFingerprint([]any{f.q.Grant, f.q.HostSession, "qa", []string{"user-" + id}, "answer-" + id})
	source, _ := json.Marshal(map[string]any{"origin": "memlane_pi_capture_v1", "host_session": f.q.HostSession, "question_entries": []string{"user-" + id}, "answer_entry": "answer-" + id})
	entry, _ := json.Marshal(map[string]any{"type": "qa", "question": "synthetic question API_KEY='synthetic-capture-secret'", "answer": "synthetic answer", "context": string(source)})
	return records.OfficialCaptureInput{Operation: operation, Session: "pi-" + piCaptureFingerprint([]string{f.q.Grant, f.q.HostSession})[:48], Entry: entry}
}

func (f *piCaptureDeliveryFixture) trace(name, command string) records.OfficialCaptureInput {
	operation := piCaptureFingerprint([]string{f.q.Grant, f.q.HostSession, "trace", "call-1", "result-1"})
	source, _ := json.Marshal(map[string]any{"origin": "memlane_pi_capture_v1", "host_session": f.q.HostSession, "call_entry": "call-1", "result_entry": "result-1", "tool_call": "tool-1"})
	entry, _ := json.Marshal(map[string]any{"type": "trace", "origin_function": name, "status": "success", "method_params": map[string]any{"command": command}, "method_return_value": "synthetic output", "error_message": "", "generate_feedback_with_llm": false, "memory_context": string(source)})
	return records.OfficialCaptureInput{Operation: operation, Session: "pi-" + piCaptureFingerprint([]string{f.q.Grant, f.q.HostSession})[:48], Entry: entry}
}

func newPiCaptureDeliveryFixture(t *testing.T, mode string) *piCaptureDeliveryFixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "memlane.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := filepath.Join(root, "synthetic-pi-repo")
	if output, e := exec.Command("git", "init", repo).CombinedOutput(); e != nil {
		t.Fatal(e, string(output))
	}
	project, err := db.RegisterProject(ctx, records.ScopeDraft{Name: "synthetic capture delivery", Path: repo})
	if err != nil {
		t.Fatal(err)
	}
	c, err := db.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "pi", Name: "synthetic pi capture", Project: project.ID, AllowedScopes: []string{project.ID}})
	if err != nil {
		t.Fatal(err)
	}
	instance, dataset, user := uuid.NewString(), uuid.NewString(), uuid.NewString()
	f := &piCaptureDeliveryFixture{started: make(chan struct{})}
	var savedEntry json.RawMessage
	var savedID string
	var savedMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "synthetic-capture-token" {
			t.Error("wrong capture credential")
			w.WriteHeader(403)
			return
		}
		if r.URL.Path == "/api/v1/memlane/identity" {
			if f.unavailable.Load() {
				w.WriteHeader(503)
				return
			}
			capabilities := []string{"capture_receipt_v1"}
			if mode == "old_component" {
				capabilities = nil
			}
			json.NewEncoder(w).Encode(map[string]any{"instance": instance, "cognee_version": "1.6.7", "capabilities": capabilities})
			return
		}
		if r.URL.Path == "/api/v1/remember/entry" && r.Method == "POST" {
			f.posts.Add(1)
			var body struct {
				Dataset string          `json:"dataset_id"`
				Session string          `json:"session_id"`
				Entry   json.RawMessage `json:"entry"`
				Improve bool            `json:"self_improvement"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Dataset != dataset || body.Session != f.q.Entries[0].Session || body.Improve {
				t.Error("dispatch scope/improvement changed")
			}
			if strings.Contains(string(body.Entry), "synthetic-capture-secret") {
				t.Error("raw secret reached official API")
			}
			if mode == "revoke_inflight" {
				f.startOnce.Do(func() { close(f.started) })
				select {
				case <-r.Context().Done():
				case <-time.After(3 * time.Second):
				}
				return
			}
			id := uuid.NewString()
			if mode != "not_arrived" {
				savedMu.Lock()
				savedEntry = append(json.RawMessage(nil), body.Entry...)
				savedID = id
				savedMu.Unlock()
			}
			if mode == "ack_lost" || mode == "not_arrived" || mode == "changed_cached_body" {
				w.WriteHeader(503)
				return
			}
			var kind struct {
				Type string `json:"type"`
			}
			json.Unmarshal(body.Entry, &kind)
			json.NewEncoder(w).Encode(map[string]any{"status": "session_stored", "dataset_id": dataset, "session_ids": []string{body.Session}, "entry_type": kind.Type, "entry_id": id})
			return
		}
		if strings.Contains(r.URL.Path, "/capture-receipts/") && r.Method == "GET" {
			if mode == "revoke_read" {
				f.startOnce.Do(func() { close(f.started) })
				select {
				case <-r.Context().Done():
				case <-time.After(time.Second):
				}
			}
			f.lookups.Add(1)
			input := f.q.Entries[0]
			var entryKind struct {
				Type string `json:"type"`
			}
			json.Unmarshal(input.Entry, &entryKind)
			response := map[string]any{"instance": instance, "dataset": dataset, "session": input.Session, "user": user, "operation": input.Operation, "kind": entryKind.Type, "state": "unknown"}
			savedMu.Lock()
			entry := append(json.RawMessage(nil), savedEntry...)
			id := savedID
			savedMu.Unlock()
			if id != "" {
				response["state"] = "saved"
				response["entry_id"] = id
				response["entry"] = entry
			}
			if mode == "changed_cached_body" {
				var altered map[string]any
				json.Unmarshal(entry, &altered)
				altered["answer"] = "altered despite unchanged context marker"
				response["entry"] = altered
			}
			json.NewEncoder(w).Encode(response)
			return
		}
		t.Error("unexpected capture request", r.Method, r.URL.Path)
		w.WriteHeader(404)
	}))
	t.Cleanup(server.Close)
	b, err := db.BindCognee(ctx, records.CogneeBinding{Operation: uuid.NewString(), Instance: instance, Dataset: dataset, Project: project.ID, ConnectionID: c.ID, Generation: c.Generation, Workspace: repo, Endpoint: server.URL, MaterialSubmission: true})
	if err != nil {
		t.Fatal(err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	python, _ = filepath.Abs(python)
	policy, _ := filepath.Abs("../agents/testdata/cognee-policy-1.7.7/_capture_policy.py")
	f.s = &Server{store: db, config: Config{Root: root, PiCapturePython: python, PiCapturePolicy: policy}}
	if err = f.s.writeCogneeToken(instance, dataset, "synthetic-capture-token"); err != nil {
		t.Fatal(err)
	}
	grant, err := f.s.reviewPiCaptureConsent(ctx, &records.PiCaptureConsentReview{Operation: uuid.NewString(), Binding: b}, true)
	if err != nil {
		t.Fatal(err)
	}
	f.grant = grant
	f.q = records.PiCaptureBatchRequest{Batch: strings.Repeat("a", 64), Grant: grant.Key, HostSession: "synthetic-session", Request: records.AgentRequest{Protocol: 1, Tool: "memory_save", Channel: "daily", ConnectionID: c.ID, Generation: c.Generation, Workspace: repo, Arguments: records.AgentArguments{Scope: project.ID}}}
	f.q.Entries = []records.OfficialCaptureInput{f.qa("1")}
	return f
}

func assertPiCaptureNoMirror(t *testing.T, f *piCaptureDeliveryFixture) {
	t.Helper()
	for _, table := range []string{"memories", "versions", "operations", "jobs"} {
		var count int
		if err := f.s.store.DB.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("capture mirrored content", table, count, err)
		}
	}
}

func TestPiCaptureDeliverySuccessReplayAndResponseLossReconciliation(t *testing.T) {
	for _, mode := range []string{"success", "ack_lost", "not_arrived", "changed_cached_body"} {
		t.Run(mode, func(t *testing.T) {
			f := newPiCaptureDeliveryFixture(t, mode)
			ctx := context.Background()
			first, err := f.s.processPiCaptureBatch(ctx, &f.q, false)
			if err != nil {
				t.Fatal(err)
			}
			want := "saved"
			if mode != "success" {
				want = "unknown"
			}
			if first.Operations[0].State != want {
				t.Fatal(first)
			}
			for i := 0; i < 2; i++ {
				receipt, err := f.s.processPiCaptureBatch(ctx, &f.q, i == 0)
				if err != nil {
					t.Fatal(err)
				}
				state := "saved"
				if mode == "not_arrived" || mode == "changed_cached_body" {
					state = "unknown"
				}
				if receipt.Operations[0].State != state {
					t.Fatal(receipt)
				}
			}
			if f.posts.Load() != 1 {
				t.Fatal("unknown or replay caused duplicate POST", f.posts.Load())
			}
			assertPiCaptureNoMirror(t, f)
		})
	}
}

func TestPiCaptureDeliveryPolicyFilteredAndMalformedNeighborNeverDispatch(t *testing.T) {
	for _, mode := range []string{"filtered", "malformed_neighbor", "policy_unavailable", "wrong_operation", "wrong_session"} {
		t.Run(mode, func(t *testing.T) {
			f := newPiCaptureDeliveryFixture(t, "success")
			switch mode {
			case "filtered":
				f.q.Entries = []records.OfficialCaptureInput{f.trace("bash", "printf "+strings.Repeat("x", 17000)+"; cat /synthetic/.env")}
			case "malformed_neighbor":
				f.q.Entries = append(f.q.Entries, f.trace(strings.Repeat("x", 257), "synthetic-check"))
			case "policy_unavailable":
				f.s.config.PiCapturePolicy = "/synthetic/missing-policy"
			case "wrong_operation":
				f.q.Entries[0].Operation = strings.Repeat("e", 64)
			case "wrong_session":
				f.q.Entries[0].Session = "pi-" + strings.Repeat("e", 48)
			}
			receipt, err := f.s.processPiCaptureBatch(context.Background(), &f.q, false)
			if mode == "filtered" {
				if err != nil || receipt.Operations[0].State != "policy_filtered" {
					t.Fatal(receipt, err)
				}
			} else if err == nil {
				t.Fatal("invalid batch accepted", mode)
			}
			if f.posts.Load() != 0 {
				t.Fatal("filtered/invalid batch dispatched", mode)
			}
			var count int
			if err := f.s.store.DB.QueryRow(`SELECT count(*) FROM official_capture_receipts`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if mode != "filtered" && count != 0 {
				t.Fatal("malformed batch partially claimed", count)
			}
			assertPiCaptureNoMirror(t, f)
		})
	}
}

func TestPiCaptureDeliveryPreflightFailureProvesNotDispatchedThenRecovers(t *testing.T) {
	f := newPiCaptureDeliveryFixture(t, "success")
	f.unavailable.Store(true)
	if _, err := f.s.processPiCaptureBatch(context.Background(), &f.q, false); err == nil {
		t.Fatal("unavailable preflight accepted")
	}
	receipt, err := f.s.processPiCaptureBatch(context.Background(), &f.q, true)
	if err != nil || receipt.Operations[0].State != "not_dispatched" {
		t.Fatal(receipt, err)
	}
	if f.posts.Load() != 0 {
		t.Fatal("preflight failed after POST")
	}
	f.unavailable.Store(false)
	receipt, err = f.s.processPiCaptureBatch(context.Background(), &f.q, false)
	if err != nil || receipt.Operations[0].State != "saved" || f.posts.Load() != 1 {
		t.Fatal(receipt, err, f.posts.Load())
	}
}

func TestPiCaptureDeliveryConcurrentClaimsDispatchOnce(t *testing.T) {
	f := newPiCaptureDeliveryFixture(t, "success")
	var wait sync.WaitGroup
	for i := 0; i < 3; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := f.s.processPiCaptureBatch(context.Background(), &f.q, false); err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
	if f.posts.Load() != 1 {
		t.Fatal("concurrent claim duplicated write", f.posts.Load())
	}
	receipt, err := f.s.processPiCaptureBatch(context.Background(), &f.q, true)
	if err != nil || receipt.Operations[0].State != "saved" {
		t.Fatal(receipt, err)
	}
}

func TestPiCaptureDeliveryRefusesOldComponentBeforeDispatch(t *testing.T) {
	f := newPiCaptureDeliveryFixture(t, "old_component")
	if _, err := f.s.processPiCaptureBatch(context.Background(), &f.q, false); err == nil {
		t.Fatal("old component accepted new capture without receipt capability")
	}
	if f.posts.Load() != 0 {
		t.Fatal("old component received capture")
	}
	var count int
	if err := f.s.store.DB.QueryRow(`SELECT count(*) FROM official_capture_receipts`).Scan(&count); err != nil || count != 0 {
		t.Fatal("unsupported component claimed dispatch", count, err)
	}
}

func TestPiCaptureDeliveryInFlightRevokeCancelsAndDoesNotAdopt(t *testing.T) {
	f := newPiCaptureDeliveryFixture(t, "revoke_inflight")
	done := make(chan error, 1)
	go func() { _, err := f.s.processPiCaptureBatch(context.Background(), &f.q, false); done <- err }()
	select {
	case <-f.started:
	case <-time.After(4 * time.Second):
		t.Fatal("dispatch did not start")
	}
	if err := f.s.disablePiCaptureConsent(context.Background(), &records.PiCaptureConsentDisable{Connection: f.q.Request.ConnectionID, Project: f.q.Request.Arguments.Scope, ExpectedKey: f.q.Grant}, true); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("revoked result adopted")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("revoked dispatch did not stop")
	}
	var state string
	if err := f.s.store.DB.QueryRow(`SELECT state FROM official_capture_receipts WHERE operation=?`, f.q.Entries[0].Operation).Scan(&state); err != nil || state != "dispatching" {
		t.Fatal("unknown intent lost", state, err)
	}
	if f.posts.Load() != 1 {
		t.Fatal("unexpected number of in-flight requests")
	}
	assertPiCaptureNoMirror(t, f)
}
