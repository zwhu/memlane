package daemon

// SQL failure fixtures exercise persistence/recovery without a model process.
import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

func TestOrganizationDeadOwnerRecovery(t *testing.T) {
	for _, phase := range []string{"queued", "running", "applying"} {
		t.Run(phase, func(t *testing.T) {
			ctx := context.Background()
			db, e := store.Open(filepath.Join(t.TempDir(), "fixture.sqlite"))
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			check := func(e error) {
				t.Helper()
				if e != nil {
					t.Fatal(e)
				}
			}
			project, id := uuid.NewString(), uuid.NewString()
			_, e = db.DB.Exec(`INSERT INTO scopes(id,kind,name) VALUES(?,'project','fault fixture')`, project)
			check(e)
			_, e = db.Apply(ctx, records.Mutation{Kind: "save", OperationID: uuid.NewString(), Dataset: project, Memory: id, Text: "原始项目约定"})
			check(e)
			task, _, e := db.StartOrganization(ctx, records.OrganizationStart{OperationID: uuid.NewString(), Project: project, Inputs: []records.MergeTarget{{Memory: id, Revision: 1}}})
			check(e)
			if phase != "queued" {
				check(db.ClaimOrganization(ctx, task.ID, 1))
			}
			if phase == "applying" {
				x := task.Snapshot
				r := records.OrganizationResult{Schema: records.OrganizationSchema, Task: task.ID, Attempt: 1, Digest: x.Digest, ID: x.ProposalID(), Explanation: "SQL故障夹具", Items: []records.OrganizationItem{{Index: 1, Action: "summary", Title: "摘要", Text: "项目约定摘要", Tags: []string{}, Sources: []records.MergeSource{{Memory: id, Revision: 1, Quote: x.Inputs[0].Text}}}}}
				raw, _ := json.Marshal(r)
				task, e = db.StageOrganization(ctx, task.ID, 1, raw, nil, "")
				check(e)
			}
			server := &Server{store: db, organizationRuns: map[string]organizationOwner{}, work: make(chan struct{}, 1)}
			// Reject every state transition, representing unavailable task persistence.
			_, e = db.DB.Exec(`CREATE TRIGGER injected_state_failure BEFORE UPDATE OF state ON organization_tasks BEGIN SELECT RAISE(ABORT,'SQL fault fixture'); END`)
			check(e)
			if phase == "applying" {
				server.applyOrganization(ctx, task)
			}
			server.finishOrganizationRun(task)
			if _, e = server.getOrganization(ctx, task.ID); e == nil {
				t.Fatal("failed persistence falsely reported success")
			}
			_, e = db.DB.Exec(`DROP TRIGGER injected_state_failure`)
			check(e)
			recovered, e := server.getOrganization(ctx, task.ID)
			check(e)
			if recovered.State != "interrupted" || recovered.Attempt != 2 {
				t.Fatal(recovered)
			}
			if phase == "applying" {
				if recovered.Entries[0].Receipt == nil {
					t.Fatal("committed receipt lost")
				}
				receipt := *recovered.Entries[0].Receipt
				d := records.OrganizationResume{Task: task.ID, ExpectedAttempt: 2, OperationID: uuid.NewString()}
				resumed, created, e := db.ResumeOrganization(ctx, d)
				check(e)
				if !created {
					t.Fatal("finish-only recovery not accepted")
				}
				server.applyOrganization(ctx, resumed)
				done, e := server.getOrganization(ctx, task.ID)
				check(e)
				if done.State != "completed" || done.Entries[0].Receipt.ID != receipt.ID {
					t.Fatal("duplicate/lost receipt or failed completion")
				}
				cancelled := false
				server.organizationRuns[task.ID] = organizationOwner{Attempt: done.Attempt, Cancel: func() { cancelled = true }}
				terminal, e := server.cancelOrganization(ctx, task.ID)
				check(e)
				if cancelled || terminal.Cancelled {
					t.Fatal("cancel on terminal task cancelled an owner")
				}
				delete(server.organizationRuns, task.ID)
				var count int
				check(db.DB.QueryRow(`SELECT count(*) FROM memories`).Scan(&count))
				if count != 2 {
					t.Fatal("duplicated summary")
				}
			}
		})
	}
}

func TestOrganizationOldOwnerCannotRemoveNewAttempt(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "owner-fixture.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := &Server{store: db, organizationRuns: map[string]organizationOwner{"task": {Attempt: 3, Cancel: func() {}}}}
	s.finishOrganizationRun(records.OrganizationTask{ID: "task", Attempt: 1})
	if s.organizationRuns["task"].Attempt != 3 {
		t.Fatal("old finalizer removed new owner")
	}
}
