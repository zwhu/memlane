package daemon

// Real SQLite + HTTP owner/gateway with synthetic Cognee/model responders.
// No installed model, Keychain, Agent process, or Native window is used.
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

func TestCogneeSubmissionOwnerFrozenHTTPExecutionAndCancellation(t *testing.T) {
	var block atomic.Bool
	var modelCalls atomic.Int64
	started := make(chan struct{}, 1)
	shutdown := make(chan struct{})
	defer close(shutdown)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "synthetic-model", "owned_by": "fixture"}}})
			return
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("gateway used Magpie credentials")
		}
		modelCalls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		if block.Load() {
			select {
			case started <- struct{}{}:
			default:
			}
			select {
			case <-r.Context().Done():
			case <-shutdown:
			case <-time.After(5 * time.Second):
			}
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"model": "reported-synthetic-model", "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"role": "assistant", "content": `{"marker":"memlane-model-probe","value":7}`}}}})
	}))
	defer upstream.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "memlane.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := filepath.Join(t.TempDir(), "synthetic-project")
	if out, e := exec.Command("git", "init", repo).CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	project, err := db.RegisterProject(ctx, records.ScopeDraft{Name: "synthetic graph project", Path: repo})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := db.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "pi", Name: "synthetic graph connection", Project: project.ID, AllowedScopes: []string{project.ID}})
	if err != nil {
		t.Fatal(err)
	}
	v, err := db.Apply(ctx, records.Mutation{OperationID: uuid.NewString(), Kind: "save", Dataset: project.ID, Memory: uuid.NewString(), Origin: "knowledge", Text: "Synthetic graph source retains current version."})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: db, config: Config{Root: root, ClientRoot: root}, lifecycle: ctx, stop: cancel, gate: make(chan struct{}, 1)}
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
	wait := time.Now().Add(5 * time.Second)
	for {
		status, e := s.processing.Status(ctx, validation.ID)
		if e != nil {
			t.Fatal(e)
		}
		if status.Validation.State != "validating" {
			if status.Validation.State != "succeeded" {
				t.Fatal(status)
			}
			break
		}
		if time.Now().After(wait) {
			t.Fatal("validation timeout")
		}
		time.Sleep(5 * time.Millisecond)
	}
	active, err := s.processing.Activate(ctx, records.ProcessingActivation{OperationID: uuid.NewString(), Revision: revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	instance, dataset := uuid.NewString(), uuid.NewString()
	var cleanup atomic.Bool
	cognee := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/memlane/identity" {
			json.NewEncoder(w).Encode(map[string]any{"instance": instance, "cognee_version": "1.6.7", "capabilities": []string{"material_submission_v1"}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/cancel") {
			var q map[string]string
			json.NewDecoder(r.Body).Decode(&q)
			cleanup.Store(true)
			json.NewEncoder(w).Encode(map[string]string{"instance": instance, "library": q["library"], "operation": q["operation"], "state": "stopped"})
			return
		}
		var envelope struct {
			Material records.CogneeSubmissionMaterial `json:"material"`
			Gateway  map[string]string                `json:"gateway"`
		}
		if json.NewDecoder(r.Body).Decode(&envelope) != nil {
			w.WriteHeader(400)
			return
		}
		// The fake Cognee calls the actual daemon gateway; a response alone without
		// any gateway execution must never publish the durable graph projection.
		payload := []byte(`{"model":"memlane-processing","messages":[{"role":"user","content":"synthetic"}],"stream":false,"response_format":{"type":"json_schema","json_schema":{"name":"fixture","schema":{"type":"object","required":["marker","value"],"properties":{"marker":{"type":"string","const":"memlane-model-probe"},"value":{"type":"integer","const":7}}}}}}`)
		request, _ := http.NewRequestWithContext(r.Context(), "POST", envelope.Gateway["endpoint"]+"/chat/completions", bytes.NewReader(payload))
		request.Header.Set("Authorization", "Bearer "+envelope.Gateway["token"])
		request.Header.Set("Content-Type", "application/json")
		response, e := http.DefaultClient.Do(request)
		if e != nil {
			w.WriteHeader(503)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			w.WriteHeader(503)
			return
		}
		m := envelope.Material
		json.NewEncoder(w).Encode(records.CogneeSubmissionReceipt{Library: m.Library, Operation: m.Operation, Instance: m.Instance, Dataset: m.Dataset, Project: m.Project, Memory: m.Memory, Revision: m.Revision, Digest: m.Digest, Document: uuid.NewString(), State: "processed", CogneeVersion: "1.6.7", ProcessingRevisionDigest: m.Processing.Revision.Digest, ProcessingDependencyDigest: m.Processing.DependencyDigest})
	}))
	defer cognee.Close()
	b := records.CogneeBinding{Operation: uuid.NewString(), Instance: instance, Dataset: dataset, Project: project.ID, Endpoint: cognee.URL, ConnectionID: connection.ID, Generation: connection.Generation, Workspace: repo, MaterialSubmission: true}
	if _, err = db.BindCognee(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err = s.writeCogneeToken(instance, dataset, "synthetic-cognee-token"); err != nil {
		t.Fatal(err)
	}
	owner, err := newCogneeSubmissionOwner(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	q := records.CogneeSubmissionRequest{Operation: uuid.NewString(), Project: project.ID, Dataset: dataset, Sources: []records.MergeSource{{Memory: v.Memory, Revision: 1}}, Processing: records.ProcessingTaskAuthorization{Revision: revision.ID, Generation: active.Generation}}
	if _, err = owner.Start(ctx, q); err != nil {
		t.Fatal(err)
	}
	terminal := waitCogneeTask(t, db, q.Operation)
	if terminal.State != "completed" || terminal.Completed != 1 || !strings.Contains(string(terminal.Executions), `"success":true`) {
		t.Fatal("real safe execution evidence missing", terminal)
	}
	raw, _ := json.Marshal(terminal)
	if strings.Contains(string(raw), "synthetic-cognee-token") || strings.Contains(string(raw), "Bearer") {
		t.Fatal("credential leaked")
	}
	// A lost successful reply must remain replayable after a source edit
	// and explicit revalidation deactivates the current processing selection.
	_, err = db.Apply(ctx, records.Mutation{OperationID: uuid.NewString(), Kind: "edit", Dataset: project.ID, Memory: v.Memory, ExpectedRevision: 1, Text: "Synthetic source version two."})
	if err != nil {
		t.Fatal(err)
	}
	repeatValidation, err := s.processing.Begin(ctx, uuid.NewString(), revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, e := s.processing.Status(ctx, repeatValidation.ID)
		if e != nil {
			t.Fatal(e)
		}
		if status.Validation.State != "validating" {
			if status.Validation.State != "succeeded" {
				t.Fatal(status)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("revalidation timeout")
		}
		time.Sleep(5 * time.Millisecond)
	}
	beforeReplay := modelCalls.Load()
	replay, err := owner.Start(ctx, q)
	if err != nil || replay.State != "completed" || replay.Completed != 1 || modelCalls.Load() != beforeReplay {
		t.Fatal("lost reply retried or inspected new settings/source", replay, err)
	}
	altered := q
	altered.Sources = []records.MergeSource{{Memory: v.Memory, Revision: 2}}
	if _, err = owner.Start(ctx, altered); err == nil {
		t.Fatal("changed request reused old operation")
	}
	configuration, e := db.ProcessingConfiguration(ctx)
	if e != nil {
		t.Fatal(e)
	}
	active, err = s.processing.Activate(ctx, records.ProcessingActivation{OperationID: uuid.NewString(), Revision: revision.ID, ExpectedGeneration: configuration.Current.Generation})
	if err != nil {
		t.Fatal(err)
	}
	q.Processing.Generation = active.Generation
	q.Sources = []records.MergeSource{{Memory: v.Memory, Revision: 2}}
	block.Store(true)
	q.Operation = uuid.NewString()
	if _, err = owner.Start(ctx, q); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("inference did not begin")
	}
	busyRequest := q
	busyRequest.Operation = uuid.NewString()
	if _, err = owner.Start(ctx, busyRequest); err == nil {
		t.Fatal("second material job was queued")
	}
	if _, err = db.CogneeSubmissionTask(ctx, busyRequest.Operation); err == nil {
		t.Fatal("busy rejection wrote a misleading latest task")
	}
	latest, err := db.LatestCogneeSubmission(ctx, project.ID)
	if err != nil || latest.Operation != q.Operation {
		t.Fatal("latest lost active operation", latest, err)
	}
	if _, err = owner.Cancel(ctx, q.Operation); err != nil {
		t.Fatal(err)
	}
	terminal = waitCogneeTask(t, db, q.Operation)
	if terminal.State != "cancelled" || !cleanup.Load() || terminal.Completed != 0 {
		t.Fatal("cancel did not confirm cleanup", terminal)
	}
}

func waitCogneeTask(t *testing.T, db *store.Store, id string) records.CogneeSubmissionTask {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		task, err := db.CogneeSubmissionTask(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if task.State != "prepared" && task.State != "running" {
			return task
		}
		if time.Now().After(deadline) {
			t.Fatal("material task timed out", task)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
