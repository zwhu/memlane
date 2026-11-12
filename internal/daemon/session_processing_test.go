package daemon

// Actual SQLite, HTTP, owner and gateway with synthetic SDK/model responders.
// This does not run an installed Agent, SDK improve, Native window or model.
import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/processing"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

func TestSessionProcessingOwnerReplayRevocationAndSealedGateway(t *testing.T) {
	var block, capability, cleanup atomic.Bool
	capability.Store(true)
	var modelCalls, posts atomic.Int64
	modelStarted := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "synthetic-model", "owned_by": "fixture"}}})
			return
		}
		modelCalls.Add(1)
		io.Copy(io.Discard, r.Body)
		if block.Load() {
			select {
			case modelStarted <- struct{}{}:
			default:
			}
			select {
			case <-r.Context().Done():
			case <-time.After(8 * time.Second):
			}
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"model": "reported-synthetic-model", "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"role": "assistant", "content": `{"marker":"memlane-model-probe","value":7}`}}}})
	}))
	defer upstream.Close()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "memlane.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := filepath.Join(root, "repo")
	if out, err := exec.Command("git", "init", repo).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	project, err := db.RegisterProject(ctx, records.ScopeDraft{Name: "synthetic official session", Path: repo})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := db.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "codex", Name: "synthetic session grant", Project: project.ID, AllowedScopes: []string{project.ID}})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: db, config: Config{Root: root, ClientRoot: root}, lifecycle: ctx, stop: stop, gate: make(chan struct{}, 1)}
	s.processing = &processing.Owner{Ledger: db, Lifecycle: ctx, ClientRoot: root, Acquire: s.acquireOrganizer}
	defer s.processing.Close()
	revision, err := db.SaveProcessing(ctx, records.ProcessingSave{OperationID: uuid.NewString(), Spec: records.ProcessingSpec{Mode: "magpie", Endpoint: upstream.URL, Protocol: "openai-chat", Model: "synthetic-model"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	validation, err := s.processing.Begin(ctx, uuid.NewString(), revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status := waitProcessingStatus(t, s, validation.ID); status.Validation.State != "succeeded" {
		t.Fatal(status)
	}
	active, err := s.processing.Activate(ctx, records.ProcessingActivation{OperationID: uuid.NewString(), Revision: revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	instance, dataset, user := uuid.NewString(), uuid.NewString(), uuid.NewString()
	detail := records.SessionDetail{SessionSummary: records.SessionSummary{Session: "synthetic-official-session", User: user, Dataset: dataset}, QACount: 1, QAs: []records.SessionQA{{ID: "qa", Question: "Synthetic source must not be mirrored", Answer: "Synthetic answer"}}}
	cognee := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "synthetic-official-token" {
			t.Error("official credential not bound")
			w.WriteHeader(403)
			return
		}
		switch r.URL.Path {
		case "/api/v1/memlane/identity":
			capabilities := []string{}
			if capability.Load() {
				capabilities = append(capabilities, "session_improve_v1", "coding_memory_format_v1", "coding_memory_trigger_v1")
			}
			json.NewEncoder(w).Encode(map[string]any{"instance": instance, "cognee_version": "1.6.7", "source_commit": "ec6f40859e5f2df9e7862dde27d580ac3268ed43", "capabilities": capabilities})
			return
		case "/api/v1/sessions/" + detail.Session:
			json.NewEncoder(w).Encode(detail)
			return
		case "/api/v1/memlane/session-improve/cancel":
			var q map[string]string
			json.NewDecoder(r.Body).Decode(&q)
			if q["owner"] != s.sessionProcessing.capability {
				t.Error("official cancellation lost daemon identity")
				w.WriteHeader(403)
				return
			}
			cleanup.Store(true)
			json.NewEncoder(w).Encode(map[string]string{"instance": instance, "library": q["library"], "operation": q["operation"], "state": "stopped"})
			return
		case "/api/v1/memlane/session-improve":
		default:
			t.Errorf("unexpected official path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		posts.Add(1)
		var envelope struct {
			Owner                                                   string
			Automatic                                               bool
			Instance, Library, Operation, Project, Dataset, Session string
			Processing                                              records.ProcessingSnapshot
			Gateway                                                 map[string]string
		}
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		frozen, loadErr := db.LoadSessionProcessing(ctx, envelope.Operation)
		if loadErr != nil || envelope.Owner != s.sessionProcessing.capability || envelope.Automatic != (frozen.Task.Request.AutomaticGrant != nil) {
			t.Error("official improve lost owner or automatic stage-default identity")
			w.WriteHeader(403)
			return
		}
		payload := []byte(`{"model":"memlane-processing","messages":[{"role":"user","content":"synthetic"}],"stream":false,"response_format":{"type":"json_schema","json_schema":{"name":"fixture","schema":{"type":"object","required":["marker","value"],"properties":{"marker":{"type":"string","const":"memlane-model-probe"},"value":{"type":"integer","const":7}}}}}}`)
		request, _ := http.NewRequestWithContext(r.Context(), "POST", envelope.Gateway["endpoint"]+"/chat/completions", bytes.NewReader(payload))
		request.Header.Set("Authorization", "Bearer "+envelope.Gateway["token"])
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			w.WriteHeader(503)
			return
		}
		defer response.Body.Close()
		io.Copy(io.Discard, response.Body)
		if response.StatusCode != 200 {
			w.WriteHeader(503)
			return
		}
		json.NewEncoder(w).Encode(records.SessionProcessingReceipt{Instance: instance, Library: envelope.Library, Operation: envelope.Operation, Project: envelope.Project, Dataset: dataset, Session: detail.Session, User: user, CogneeVersion: "1.6.7", RevisionDigest: envelope.Processing.Revision.Digest, DependencyDigest: envelope.Processing.DependencyDigest, Status: "completed", Stages: []records.SessionProcessingStage{{Stage: "distill_sessions", Status: "completed", Counts: map[string]int{"sessions": 1}}}})
	}))
	defer cognee.Close()
	b := records.CogneeBinding{Operation: uuid.NewString(), Instance: instance, Dataset: dataset, Project: project.ID, Endpoint: cognee.URL, ConnectionID: connection.ID, Generation: connection.Generation, Workspace: repo}
	if _, err = db.BindCognee(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err = s.writeCogneeToken(instance, dataset, "synthetic-official-token"); err != nil {
		t.Fatal(err)
	}
	owner, err := newSessionProcessingOwner(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	s.sessionProcessing = owner
	q := records.SessionProcessingRequest{Operation: uuid.NewString(), Project: project.ID, Instance: instance, Dataset: dataset, Session: detail.Session, User: user, ObservedDigest: records.SessionProcessingSourceDigest(detail), Processing: records.ProcessingTaskAuthorization{Revision: revision.ID, Generation: active.Generation}}
	if _, err := s.perform(ctx, Request{Op: "session_processing_start", SessionProcessing: &q}); err == nil {
		t.Fatal("processing bypassed explicit Native action")
	}
	capability.Store(false)
	if _, err := owner.Start(ctx, q); err == nil {
		t.Fatal("old official service accepted new task")
	}
	capability.Store(true)
	before := modelCalls.Load()
	if _, err := owner.Start(ctx, q); err != nil {
		t.Fatal(err)
	}
	terminal := waitSessionProcessingTask(t, db, q.Operation)
	if terminal.State != "completed" || terminal.Receipt == nil || !strings.Contains(string(terminal.Executions), `"success":true`) {
		t.Fatal("official/gateway evidence not sealed", terminal)
	}
	if _, err := owner.Start(ctx, q); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 1 || modelCalls.Load() != before+1 {
		t.Fatal("lost response triggered repeated processing")
	}
	raw, _ := json.Marshal(terminal)
	for _, secret := range []string{"synthetic-official-token", "Bearer", detail.QAs[0].Question, owner.capability} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("task persisted capability or mirrored source")
		}
	}
	// An official trigger carries no project/model/grant from a prompt. The
	// daemon derives them and freezes the explicit dataset authorization.
	trigger := records.CodingMemoryTrigger{Operation: uuid.NewString(), Instance: instance, Dataset: dataset, User: user, Session: detail.Session}
	if _, err := owner.StartAutomatic(ctx, "wrong-capability", trigger); err == nil {
		t.Fatal("untrusted process triggered automatic model use")
	}
	if _, err := owner.StartAutomatic(ctx, owner.capability, trigger); err == nil {
		t.Fatal("connection alone authorized automatic model use")
	}
	grantChange := records.CodingMemoryGrantChange{Operation: uuid.NewString(), Binding: b, User: user, Enabled: true}
	grant, err := db.ChangeCodingMemoryGrant(ctx, grantChange)
	if err != nil {
		t.Fatal(err)
	}
	spoofedManual := q
	spoofedManual.Operation, spoofedManual.AutomaticGrant = uuid.NewString(), &grant
	if _, err := s.perform(ctx, Request{Op: "session_processing_start", SessionProcessing: &spoofedManual, UserConfirmed: true}); err == nil {
		t.Fatal("manual IPC forged an automatic trigger")
	}
	beforeAuto := modelCalls.Load()
	if _, err := owner.StartAutomatic(ctx, owner.capability, trigger); err != nil {
		t.Fatal(err)
	}
	autoTerminal := waitSessionProcessingTask(t, db, trigger.Operation)
	if autoTerminal.State != "completed" || autoTerminal.Request.AutomaticGrant == nil || *autoTerminal.Request.AutomaticGrant != grant || autoTerminal.Request.Processing.Revision != revision.ID {
		t.Fatal("automatic run lost its scope/model ownership", autoTerminal)
	}
	if _, err := owner.StartAutomatic(ctx, owner.capability, trigger); err != nil || modelCalls.Load() != beforeAuto+1 {
		t.Fatal("official trigger replay paid for a second execution", err)
	}
	foreign := trigger
	foreign.User = uuid.NewString()
	if _, err := owner.AutomaticTask(ctx, owner.capability, foreign); err == nil {
		t.Fatal("automatic receipt accepted another user")
	}
	block.Store(true)
	trigger.Operation = uuid.NewString()
	if _, err := owner.StartAutomatic(ctx, owner.capability, trigger); err != nil {
		t.Fatal(err)
	}
	select {
	case <-modelStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("automatic gateway did not start")
	}
	grantChange.Operation, grantChange.ExpectedGeneration, grantChange.Enabled = uuid.NewString(), grant.Generation, false
	if _, err = s.changeCodingMemoryGrant(ctx, grantChange); err != nil {
		t.Fatal(err)
	}
	autoTerminal = waitSessionProcessingTask(t, db, trigger.Operation)
	if autoTerminal.State != "failed" || autoTerminal.ErrorCode != "scope_denied" || !cleanup.Load() {
		t.Fatal("disabling coding memory did not cancel the active official run", autoTerminal)
	}
	if _, err = owner.AutomaticTask(ctx, owner.capability, trigger); err != nil {
		t.Fatal("service cannot observe cancellation after disable", err)
	}
	beforeDisabled := modelCalls.Load()
	trigger.Operation = uuid.NewString()
	if _, err = owner.StartAutomatic(ctx, owner.capability, trigger); err == nil || modelCalls.Load() != beforeDisabled {
		t.Fatal("disabled scope started a new model", err)
	}
	cleanup.Store(false)
	block.Store(true)
	q.Operation = uuid.NewString()
	if _, err := owner.Start(ctx, q); err != nil {
		t.Fatal(err)
	}
	select {
	case <-modelStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("synthetic gateway did not start")
	}
	s.agentAccess.Lock()
	err = db.Disconnect(ctx, connection.ID)
	s.agentAccess.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	terminal = waitSessionProcessingTask(t, db, q.Operation)
	if terminal.State != "failed" || terminal.ErrorCode != "scope_denied" || !cleanup.Load() {
		t.Fatal("grant revocation failed to stop/clean official processing", terminal)
	}
	for _, table := range []string{"memories", "versions", "operations", "jobs"} {
		var n int
		if err := db.DB.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatal("processing mirrored business content", table, n, err)
		}
	}
}

func waitSessionProcessingTask(t *testing.T, db *store.Store, operation string) records.SessionProcessingTask {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		f, err := db.LoadSessionProcessing(context.Background(), operation)
		if err != nil {
			t.Fatal(err)
		}
		if f.Task.State != "prepared" && f.Task.State != "running" {
			return f.Task
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("owned processing did not reach a terminal state")
	return records.SessionProcessingTask{}
}
