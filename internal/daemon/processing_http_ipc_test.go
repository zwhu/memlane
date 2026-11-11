package daemon

// Private Unix IPC + loopback HTTP + SQLite exercise the real daemon dispatch
// without starting an Agent, touching Keychain, or using an external account.
import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/credentials"
	"github.com/zwhu/memlane/internal/processing"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"github.com/zwhu/memlane/internal/store"
)

type noOSCredentialFixture struct{ calls atomic.Int64 }

func (v *noOSCredentialFixture) Get(credentials.Reference, credentials.Target) ([]byte, error) {
	v.calls.Add(1)
	return nil, errors.New("fixture vault only")
}
func (v *noOSCredentialFixture) Put(credentials.Reference, credentials.Target, []byte) error {
	v.calls.Add(1)
	return errors.New("fixture vault only")
}

func TestHTTPProcessingPrivateIPCAndReviewNeverWritesBeforeConfirmation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("x-api-key") != "" {
			t.Error("Magpie forwarded credentials")
		}
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "loopback-exact-model", "owned_by": "loopback-provider"}}})
			return
		}
		if r.Header.Get("X-Memlane-Internal-Processing") != "1" {
			t.Error("internal transport marker missing")
		}
		var wire struct {
			Model    string                           `json:"model"`
			Messages []struct{ Role, Content string } `json:"messages"`
			Tools    json.RawMessage                  `json:"tools"`
		}
		if json.NewDecoder(r.Body).Decode(&wire) != nil || len(wire.Messages) == 0 || len(wire.Tools) != 0 {
			w.WriteHeader(400)
			return
		}
		text := `{"marker":"memlane-model-probe","value":7}`
		prompt := wire.Messages[len(wire.Messages)-1].Content
		if strings.Contains(prompt, "冻结授权材料：\n") {
			var snapshot records.OrganizationSnapshot
			if json.Unmarshal([]byte(strings.Split(prompt, "冻结授权材料：\n")[1]), &snapshot) != nil || len(snapshot.Inputs) == 0 {
				w.WriteHeader(400)
				return
			}
			input := snapshot.Inputs[0]
			item := records.OrganizationItem{Index: 1, Action: "overwrite", Title: "Synthetic reviewed rule", Text: "Explicitly reviewed synthetic replacement.", Target: input.Memory, Tags: []string{}, Reason: "explicit fixture suggesting a change, never approval", Sources: []records.MergeSource{{Memory: input.Memory, Revision: input.Revision, Quote: input.Text}}}
			// v2 requires destination even when no move is proposed.
			result := map[string]any{"schema": snapshot.Schema, "task": snapshot.Task, "attempt": snapshot.Attempt, "input_digest": snapshot.Digest, "result_id": snapshot.ProposalID(), "explanation": "controlled HTTP fixture", "items": []any{map[string]any{"index": item.Index, "action": item.Action, "title": item.Title, "text": item.Text, "target": item.Target, "tags": item.Tags, "reason": item.Reason, "destination": "", "sources": item.Sources}}}
			raw, _ := json.Marshal(result)
			text = string(raw)
		}
		json.NewEncoder(w).Encode(map[string]any{"model": "reported-loopback-model", "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"role": "assistant", "content": text}}}})
	}))
	defer upstream.Close()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "memlane.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{store: db, config: Config{Root: root, ClientRoot: root}, lifecycle: ctx, stop: cancel, organizationRuns: map[string]organizationOwner{}, work: make(chan struct{}, 1)}
	vault := &noOSCredentialFixture{}
	s.processing = &processing.Owner{Ledger: db, Lifecycle: ctx, ClientRoot: root, Credentials: vault, Acquire: s.acquireOrganizer}
	path := serviceipc.SocketPath(root)
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		listener.Close()
		s.processing.Close()
		s.organizationWG.Wait()
		db.Close()
		os.RemoveAll(filepath.Dir(path))
	}()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); s.handle(ctx, conn) }()
		}
	}()
	callCtx, stop := context.WithTimeout(context.Background(), 8*time.Second)
	defer stop()
	if err = serviceipc.Call(callCtx, root, serviceipc.Request{Op: "processing_credential_save", ProcessingCredentialWrite: &records.ProcessingCredentialWrite{Credential: uuid.NewString(), Revision: 1, Provider: "fixture", Endpoint: upstream.URL, Protocol: "openai-chat", Secret: []byte("synthetic-not-written")}}, nil); err == nil {
		t.Fatal("credential write bypassed confirmation")
	}
	if vault.calls.Load() != 0 {
		t.Fatal("unconfirmed write touched vault")
	}
	library := serviceipc.Library{Root: root}
	models, err := library.ProcessingModels(callCtx, records.ProcessingModelQuery{Spec: records.ProcessingSpec{Mode: "magpie", Endpoint: upstream.URL, Protocol: "openai-chat"}})
	if err != nil || len(models.Models) != 1 || models.Models[0].ID != "loopback-exact-model" {
		t.Fatal(models, err)
	}
	revision, err := library.SaveProcessing(callCtx, records.ProcessingSave{OperationID: uuid.NewString(), Spec: records.ProcessingSpec{Mode: "magpie", Endpoint: upstream.URL, Protocol: "openai-chat", Model: models.Models[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	probe, err := library.ValidateProcessing(callCtx, records.ProcessingValidationRequest{OperationID: uuid.NewString(), Revision: revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	for {
		result, err := library.ProcessingValidation(callCtx, probe.ID)
		if err != nil {
			t.Fatal(err)
		}
		if result.Validation.State != "validating" {
			if result.Validation.State != "succeeded" || result.Validation.HTTP == nil || !result.Validation.HTTP.RequestSent {
				t.Fatal(result)
			}
			break
		}
		time.Sleep(time.Millisecond)
	}
	current, err := library.ProcessingConfiguration(callCtx)
	if err != nil || current.Current.Active {
		t.Fatal("probe activated configuration", current, err)
	}
	activation, err := library.ActivateProcessing(callCtx, records.ProcessingActivation{OperationID: uuid.NewString(), Revision: revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	project := uuid.NewString()
	if _, err = db.DB.Exec(`INSERT INTO scopes(id,kind,name,path) VALUES(?,'project','HTTP review fixture','/synthetic/http-review')`, project); err != nil {
		t.Fatal(err)
	}
	source, err := db.Apply(callCtx, records.Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: uuid.NewString(), Dataset: project, Text: "Original synthetic rule remains authoritative until confirmation."})
	if err != nil {
		t.Fatal(err)
	}
	organizer := serviceipc.OrganizationClient{Root: root}
	task, err := organizer.Start(callCtx, records.OrganizationStart{OperationID: uuid.NewString(), Project: project, Review: true, Inputs: []records.MergeTarget{{Memory: source.Memory, Revision: source.Revision}}, Processing: &records.ProcessingTaskAuthorization{Revision: revision.ID, Generation: activation.Generation}})
	if err != nil {
		t.Fatal(err)
	}
	for {
		task, err = organizer.Get(callCtx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if task.State == "completed" {
			break
		}
		if task.State == "failed" || task.State == "interrupted" {
			t.Fatal(task)
		}
		time.Sleep(time.Millisecond)
	}
	s.organizationWG.Wait()
	before, err := db.Current(callCtx, source.Memory)
	if err != nil || before.Revision != 1 || before.Text != source.Text {
		t.Fatal("HTTP overwrite suggestion applied without confirmation", before, err)
	}
	if len(task.Entries) != 1 || task.Entries[0].State != "review_pending" {
		t.Fatal(task)
	}
	var proof struct {
		HTTP           records.ProcessingHTTPReceipt `json:"http"`
		ProcessStarted bool                          `json:"process_started"`
	}
	if err = json.Unmarshal(task.Execution, &proof); err != nil || !proof.HTTP.SchemaValid || proof.ProcessStarted {
		t.Fatal("formal HTTP task has no actual HTTP proof or invented process", proof, err)
	}
	entry := task.Entries[0]
	if _, err = organizer.Review(callCtx, records.OrganizationReview{Task: task.ID, Item: entry.ID, Digest: records.MergeDigest(entry.Item), Receipt: uuid.NewString(), Confirm: true}); err != nil {
		t.Fatal(err)
	}
	after, err := db.Current(callCtx, source.Memory)
	if err != nil || after.Revision != 2 || after.Text == source.Text {
		t.Fatal("explicit confirmation did not publish new version", after, err)
	}
	var pid int
	var reaped bool
	if err = db.DB.QueryRow(`SELECT process_pid,process_reaped FROM organization_leases WHERE task=?`, task.ID).Scan(&pid, &reaped); err != nil || pid != 0 || reaped {
		t.Fatal("HTTP lease fabricated process reap", pid, reaped, err)
	}
}
