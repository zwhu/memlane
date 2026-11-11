package processing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

// The network is real loopback HTTP, while all model content is synthetic.
// Original/proposal/approval publication uses real SQLite business gates.
func TestHTTPOrganizationFrozenExecutorAndStrictCitationPublication(t *testing.T) {
	for _, badQuote := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "forged-citation"}[badQuote], func(t *testing.T) {
			var actualModel atomic.Value
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var wire struct {
					Model    string                           `json:"model"`
					Messages []struct{ Role, Content string } `json:"messages"`
				}
				if json.NewDecoder(r.Body).Decode(&wire) != nil || len(wire.Messages) == 0 {
					w.WriteHeader(400)
					return
				}
				prompt := wire.Messages[len(wire.Messages)-1].Content
				if !strings.Contains(prompt, "冻结授权材料：\n") {
					json.NewEncoder(w).Encode(probeHTTPReply("openai-chat"))
					return
				}
				actualModel.Store(wire.Model)
				snapshotRaw := strings.Split(prompt, "冻结授权材料：\n")[1]
				var snapshot records.OrganizationSnapshot
				if json.Unmarshal([]byte(snapshotRaw), &snapshot) != nil || len(snapshot.Inputs) == 0 {
					w.WriteHeader(400)
					return
				}
				source := snapshot.Inputs[0]
				quote := source.Text
				if badQuote {
					quote = "a fabricated quote absent from material"
				}
				result := records.OrganizationResult{Schema: snapshot.Schema, Task: snapshot.Task, Attempt: snapshot.Attempt, Digest: snapshot.Digest, ID: snapshot.ProposalID(), Explanation: "explicit loopback model fixture", Items: []records.OrganizationItem{{Index: 1, Action: "summary", Title: "synthetic HTTP source summary", Text: source.Text, Tags: []string{}, Sources: []records.MergeSource{{Memory: source.Memory, Revision: source.Revision, Quote: quote}}}}}
				raw, _ := json.Marshal(result)
				json.NewEncoder(w).Encode(responseFixture("openai-chat", string(raw)))
			}))
			defer srv.Close()
			owner, db, _ := httpOwnerFixture(t)
			revision := saveHTTPFixture(t, owner, db, srv.URL, "openai-chat", "provider")
			validation, err := owner.Begin(context.Background(), uuid.NewString(), revision.ID)
			if err != nil {
				t.Fatal(err)
			}
			if result := waitValidation(t, db, validation.ID); result.State != "succeeded" {
				t.Fatal(result)
			}
			activated, err := owner.Activate(context.Background(), records.ProcessingActivation{OperationID: uuid.NewString(), Revision: revision.ID})
			if err != nil {
				t.Fatal(err)
			}
			project := uuid.NewString()
			_, err = db.DB.Exec(`INSERT INTO scopes(id,kind,name,path) VALUES(?,'project','HTTP synthetic project','/synthetic/http')`, project)
			if err != nil {
				t.Fatal(err)
			}
			source, err := db.Apply(context.Background(), records.Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: uuid.NewString(), Dataset: project, Text: "Synthetic project keeps requests to three retries, then dead_letter."})
			if err != nil {
				t.Fatal(err)
			}
			auth := records.ProcessingTaskAuthorization{Revision: revision.ID, Generation: activated.Generation}
			binding, err := owner.ResolveCurrentTask(context.Background(), auth)
			if err != nil {
				t.Fatal(err)
			}
			task, created, err := db.StartProcessingOrganization(context.Background(), records.OrganizationStart{OperationID: uuid.NewString(), Project: project, Inputs: []records.MergeTarget{{Memory: source.Memory, Revision: source.Revision}}, Processing: &auth}, binding.Dependency)
			if err != nil || !created {
				t.Fatal(task, created, err)
			}
			// Switch global selection after task creation; the frozen task still asks
			// its original model and immutable independent credential version.
			nextSpec := revision.Spec
			nextSpec.Model = "a-different-explicit-model"
			next, err := db.SaveProcessing(context.Background(), records.ProcessingSave{OperationID: uuid.NewString(), ExpectedSaved: revision.ID, Spec: nextSpec}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			nextValidation, err := owner.Begin(context.Background(), uuid.NewString(), next.ID)
			if err != nil {
				t.Fatal(err)
			}
			if result := waitValidation(t, db, nextValidation.ID); result.State != "succeeded" {
				t.Fatal(result)
			}
			if _, err = owner.Activate(context.Background(), records.ProcessingActivation{OperationID: uuid.NewString(), Revision: next.ID, ExpectedGeneration: activated.Generation}); err != nil {
				t.Fatal(err)
			}
			result, err := owner.RunHTTPOrganization(context.Background(), task.Snapshot, nil)
			if actualModel.Load() != "fixture-model" {
				t.Fatal("frozen execution silently switched model", actualModel.Load())
			}
			if badQuote {
				if err == nil || result.Result != nil {
					t.Fatal("schema-valid fabricated citation accepted", result, err)
				}
			} else {
				if err != nil || result.Result == nil || !result.HTTP.SchemaValid {
					t.Fatal(result, err)
				}
				if err = db.ClaimOrganization(context.Background(), task.ID, task.Attempt); err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(result.Result)
				staged, err := db.StageOrganization(context.Background(), task.ID, task.Attempt, raw, result, "")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = db.ApplyOrganizationItem(context.Background(), staged.ID, staged.Attempt, 1); err != nil {
					t.Fatal(err)
				}
				completed, err := db.FinishOrganization(context.Background(), staged.ID, staged.Attempt)
				if err != nil || completed.State != "completed" {
					t.Fatal(completed, err)
				}
			}
			current, err := db.Current(context.Background(), source.Memory)
			if err != nil || current.Revision != source.Revision || current.Text != source.Text {
				t.Fatal("proposal processing overwrote existing knowledge", current, err)
			}
			var count int
			if err = db.DB.QueryRow(`SELECT count(*) FROM versions WHERE memory!=?`, source.Memory).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if badQuote && count != 0 || !badQuote && count != 1 {
				t.Fatal("unexpected publication count", count)
			}
		})
	}
}

func TestHTTPValidationBadSchemaAndHTTPErrorDoNotEnableOrLeak(t *testing.T) {
	for _, status := range []int{200, 401, 429, 400} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if status != 200 {
					w.WriteHeader(status)
					w.Write([]byte("private-provider-error-must-never-be-persisted"))
					return
				}
				json.NewEncoder(w).Encode(responseFixture("openai-chat", `{"marker":"fabricated","value":8}`))
			}))
			defer srv.Close()
			owner, db, _ := httpOwnerFixture(t)
			revision := saveHTTPFixture(t, owner, db, srv.URL, "openai-chat", "provider")
			probe, err := owner.Begin(context.Background(), uuid.NewString(), revision.ID)
			if err != nil {
				t.Fatal(err)
			}
			terminal := waitValidation(t, db, probe.ID)
			expected := map[int]string{200: "model_schema_invalid", 401: "provider_authentication_failed", 429: "provider_rate_limited", 400: "provider_request_invalid"}[status]
			if terminal.State != "failed" || terminal.ErrorCode != expected || terminal.HTTP == nil || terminal.HTTP.SchemaValid {
				t.Fatal(terminal)
			}
			raw, _ := json.Marshal(terminal)
			if strings.Contains(string(raw), "private-provider-error") || strings.Contains(string(raw), "owned-synthetic-key") {
				t.Fatal("upstream body/credential persisted")
			}
			if _, err = owner.Activate(context.Background(), records.ProcessingActivation{OperationID: uuid.NewString(), Revision: revision.ID}); err == nil {
				t.Fatal("bad capability enabled")
			}
		})
	}
}

func TestHTTPOrganizationCancelReturnsNoLateCandidate(t *testing.T) {
	started, finish := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "fixture-model", "owned_by": "gateway-fixture"}}})
			return
		}
		var wire struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&wire)
		if len(wire.Messages) > 0 && strings.Contains(wire.Messages[len(wire.Messages)-1].Content, "冻结授权材料") {
			close(started)
			select {
			case <-finish:
			case <-r.Context().Done():
			}
			return
		}
		json.NewEncoder(w).Encode(probeHTTPReply("openai-chat"))
	}))
	defer srv.Close()
	defer close(finish)
	owner, db, _ := httpOwnerFixture(t)
	revision := saveHTTPFixture(t, owner, db, srv.URL, "openai-chat", "magpie")
	probe, err := owner.Begin(context.Background(), uuid.NewString(), revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v := waitValidation(t, db, probe.ID); v.State != "succeeded" {
		t.Fatal(v)
	}
	active, err := owner.Activate(context.Background(), records.ProcessingActivation{OperationID: uuid.NewString(), Revision: revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	project := uuid.NewString()
	_, err = db.DB.Exec(`INSERT INTO scopes(id,kind,name,path) VALUES(?,'project','cancel fixture','/synthetic/http-cancel')`, project)
	if err != nil {
		t.Fatal(err)
	}
	source, err := db.Apply(context.Background(), store.Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: uuid.NewString(), Dataset: project, Text: "synthetic original stays unchanged"})
	if err != nil {
		t.Fatal(err)
	}
	auth := records.ProcessingTaskAuthorization{Revision: revision.ID, Generation: active.Generation}
	binding, err := owner.ResolveCurrentTask(context.Background(), auth)
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := db.StartProcessingOrganization(context.Background(), records.OrganizationStart{OperationID: uuid.NewString(), Project: project, Inputs: []records.MergeTarget{{Memory: source.Memory, Revision: source.Revision}}, Processing: &auth}, binding.Dependency)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		result, err := owner.RunHTTPOrganization(ctx, task.Snapshot, nil)
		if result.Result != nil {
			done <- errors.New("cancelled HTTP returned candidate")
			return
		}
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("local transport not released")
	}
	current, err := db.Current(context.Background(), source.Memory)
	if err != nil || current.Revision != 1 {
		t.Fatal("cancel changed original", current, err)
	}
}
