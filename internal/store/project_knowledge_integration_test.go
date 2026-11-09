package store

// These exercise the real SQLite/Git/source-reader contract. Process and model
// receipts here are explicit fixtures, not evidence of an Agent launch.
import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/engine"
	"github.com/zwhu/memlane/internal/records"
)

func projectSourceFixture(path, text string) records.ProjectRepositorySource {
	h := sha256.Sum256([]byte(text))
	digest := hex.EncodeToString(h[:])
	id := sha256.Sum256([]byte(path + "\x00" + digest))
	return records.ProjectRepositorySource{Path: path, Text: text, SHA256: digest, ID: hex.EncodeToString(id[:])}
}
func projectRunFixture(t *testing.T, s *Store, p records.Scope, c records.Connection) (context.Context, records.ProjectTask) {
	t.Helper()
	b, err := s.ProjectBinding(context.Background(), p.ID)
	mustMerge(t, err)
	if b.State != "available" {
		b = bindProjectFixture(t, s, p, c)
	}
	task, created, err := s.StartProjectTask(context.Background(), records.ProjectTaskStart{OperationID: uuid.NewString(), Project: p.ID, BindingRevision: b.Revision}, projectExecutorFixture(c), nil)
	mustMerge(t, err)
	if !created {
		t.Fatal("new task not created")
	}
	owner := uuid.NewString()
	mustMerge(t, s.ClaimProjectRun(context.Background(), task.ID, task.Attempt, owner))
	ctx := WithProjectRun(context.Background(), owner)
	mustMerge(t, s.RegisterProjectProcess(ctx, task.ID, task.Attempt, 12345678))
	mustMerge(t, s.ProjectRunOrganizing(ctx, task.ID, task.Attempt))
	mustMerge(t, s.FinishProjectProcess(ctx, task.ID, task.Attempt, records.ProjectTaskExecution{PID: 12345678, StartedMS: time.Now().UnixMilli() - 1, EndedMS: time.Now().UnixMilli(), Reaped: true, GroupGone: true, Terminal: true, Model: task.Snapshot.Executor.Model, Provider: "fixture-provider", Thread: "fixture-thread", Turn: "fixture-turn", PermissionProfile: "memlane_project"}))
	return ctx, task
}
func projectProposalFixture(task records.ProjectTask, source records.ProjectRepositorySource) records.ProjectKnowledgeProposal {
	return records.ProjectKnowledgeProposal{Task: task.ID, Attempt: task.Attempt, Explanation: "SQLite contract fixture", Items: []records.ProjectKnowledgeItem{{Title: "项目依赖规则", Text: "本项目使用 pnpm。", Sources: []records.ProjectKnowledgeQuote{{Source: source.ID, Quote: source.Text}}}}}
}

func TestProjectKnowledgeNormalSaveOriginalLineageIndexAndReplay(t *testing.T) {
	s, p, c := projectTaskFixture(t)
	ctx, task := projectRunFixture(t, s, p, c)
	source := projectSourceFixture("docs/rules.md", "本项目使用 pnpm。")
	proposal := projectProposalFixture(task, source)
	if _, err := s.SaveProjectKnowledge(context.Background(), task.ID, task.Attempt, proposal, []records.ProjectRepositorySource{source}); err == nil {
		t.Fatal("unowned write accepted")
	}
	r, err := s.SaveProjectKnowledge(ctx, task.ID, task.Attempt, proposal, []records.ProjectRepositorySource{source})
	mustMerge(t, err)
	if !r.Persisted || len(r.Added) != 1 || len(r.Sources) != 1 {
		t.Fatal(r)
	}
	replay, err := s.SaveProjectKnowledge(ctx, task.ID, task.Attempt, proposal, []records.ProjectRepositorySource{source})
	mustMerge(t, err)
	if UsageDigest(replay) != UsageDigest(r) {
		t.Fatal("replay created another receipt")
	}
	changed := proposal
	changed.Explanation = "different"
	if _, err = s.SaveProjectKnowledge(ctx, task.ID, task.Attempt, changed, []records.ProjectRepositorySource{source}); err == nil {
		t.Fatal("changed replay accepted")
	}
	v, err := s.Current(ctx, r.Added[0].Memory)
	mustMerge(t, err)
	if v.Origin != "knowledge" || v.IndexState == "indexed" {
		t.Fatal(v)
	}
	original, err := s.ReadSource(ctx, records.SourceRead{Project: p.ID, Owner: v.Memory, OwnerRevision: 1, Memory: r.Sources[0].Memory, Revision: 1})
	mustMerge(t, err)
	if original.State != "available" || original.Record.Text != source.Text || original.Record.Origin != "raw" || original.Quote != source.Text {
		t.Fatal(original)
	}
	finished, err := s.FinishProjectIndexes(ctx, task.ID, task.Attempt)
	mustMerge(t, err)
	if finished {
		t.Fatal("pending jobs became success")
	}
	for _, saved := range append(append([]records.ProjectTaskSaved(nil), r.Added...), r.Sources...) {
		version, err := s.Current(ctx, saved.Memory)
		mustMerge(t, err)
		mustMerge(t, s.MarkIndexed(ctx, version.Record, engine.Receipt{Generation: "fixture-index-receipt", ChunkIDs: []string{uuid.NewString()}}))
	}
	finished, err = s.FinishProjectIndexes(ctx, task.ID, task.Attempt)
	mustMerge(t, err)
	if !finished {
		t.Fatal("actual stored receipts did not finish")
	}
	done, err := s.ProjectTask(ctx, task.ID)
	mustMerge(t, err)
	if done.State != "completed" || done.Receipt.Indexed != 1 || done.Receipt.SourceIndexed != 1 {
		t.Fatal(done)
	}
	var created, imported int
	mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM activities WHERE kind='created'`).Scan(&created))
	mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM activities WHERE kind='imported'`).Scan(&imported))
	if created != 1 || imported != 0 {
		t.Fatal("C save used import accounting", created, imported)
	}
	ctx2, task2 := projectRunFixture(t, s, p, c)
	proposal2 := projectProposalFixture(task2, source)
	r2, err := s.SaveProjectKnowledge(ctx2, task2.ID, task2.Attempt, proposal2, []records.ProjectRepositorySource{source})
	mustMerge(t, err)
	if len(r2.Added) != 0 || r2.Skipped != 1 || len(r2.Sources) != 0 {
		t.Fatal("same content duplicated originals or knowledge", r2)
	}
	finished, err = s.FinishProjectIndexes(ctx2, task2.ID, task2.Attempt)
	mustMerge(t, err)
	if !finished {
		t.Fatal("no-new task did not finish")
	}
	var memories int
	mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM memories`).Scan(&memories))
	if memories != 2 {
		t.Fatal(memories)
	}
}

func TestProjectKnowledgeCancelFailureAndSourceValidation(t *testing.T) {
	for _, mode := range []string{"cancel", "disconnect", "hash", "quote", "outside", "raw-edited", "index-failure"} {
		t.Run(mode, func(t *testing.T) {
			s, p, c := projectTaskFixture(t)
			ctx, task := projectRunFixture(t, s, p, c)
			source := projectSourceFixture("README.md", "本项目使用 pnpm。")
			proposal := projectProposalFixture(task, source)
			switch mode {
			case "cancel":
				_, err := s.CancelProjectTask(ctx, task.ID)
				mustMerge(t, err)
			case "disconnect":
				mustMerge(t, s.Disconnect(ctx, c.ID))
			case "hash":
				source.SHA256 = strings.Repeat("b", 64)
			case "quote":
				proposal.Items[0].Sources[0].Quote = "fabricated"
			case "outside":
				source = projectSourceFixture("../other/README.md", source.Text)
				proposal = projectProposalFixture(task, source)
			case "raw-edited":
				id := projectSourceMemory(p.ID, source)
				_, err := s.Apply(ctx, Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: id, Dataset: p.ID, Title: "用户原文", Text: "用户改过原文", Origin: "raw"})
				mustMerge(t, err)
			}
			r, err := s.SaveProjectKnowledge(ctx, task.ID, task.Attempt, proposal, []records.ProjectRepositorySource{source})
			if mode == "raw-edited" {
				mustMerge(t, err)
				if r.SaveFailed != 1 || len(r.Added) != 0 {
					t.Fatal("changed source created knowledge", r)
				}
				original, e := s.Current(ctx, projectSourceMemory(p.ID, source))
				mustMerge(t, e)
				if original.Text != "用户改过原文" {
					t.Fatal("existing original overwritten")
				}
				return
			}
			if mode != "index-failure" {
				if err == nil {
					t.Fatal("invalid scope/source saved")
				}
				var n int
				mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM memories WHERE id!=?`, projectSourceMemory(p.ID, source)).Scan(&n))
				if n != 0 {
					t.Fatal("failed transaction left knowledge", n)
				}
				return
			}
			mustMerge(t, err)
			_, err = s.DB.Exec(`UPDATE jobs SET state='failed',error='fixture-index-failure' WHERE memory=?`, r.Added[0].Memory)
			mustMerge(t, err)
			raw, err := s.Current(ctx, r.Sources[0].Memory)
			mustMerge(t, err)
			mustMerge(t, s.MarkIndexed(ctx, raw.Record, engine.Receipt{Generation: "fixture", ChunkIDs: []string{uuid.NewString()}}))
			finished, err := s.FinishProjectIndexes(ctx, task.ID, task.Attempt)
			mustMerge(t, err)
			if !finished {
				t.Fatal("failed indexes not terminal")
			}
			done, err := s.ProjectTask(ctx, task.ID)
			mustMerge(t, err)
			if done.State != "failed" || done.Receipt.IndexFailed != 1 || len(done.Receipt.Added) != 1 {
				t.Fatal(done)
			}
			knowledge, err := s.Current(ctx, r.Added[0].Memory)
			mustMerge(t, err)
			if knowledge.Origin != "knowledge" || knowledge.Text == "" || knowledge.State != "active" {
				t.Fatal("index failure removed saved knowledge")
			}
		})
	}
}

func TestProjectKnowledgePartialSaveAndCancelKeepEarlierReceipts(t *testing.T) {
	for _, mode := range []string{"failure", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s, p, c := projectTaskFixture(t)
			ctx, task := projectRunFixture(t, s, p, c)
			a := projectSourceFixture("first.md", "第一份规则：使用 pnpm。")
			b := projectSourceFixture("second.md", "第二份规则：使用严格类型。")
			proposal := projectProposalFixture(task, a)
			proposal.Items = append(proposal.Items, records.ProjectKnowledgeItem{Title: "第二份规则", Text: "使用严格类型。", Sources: []records.ProjectKnowledgeQuote{{Source: b.ID, Quote: b.Text}}})
			if mode == "failure" {
				_, err := s.Apply(ctx, Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: projectSourceMemory(p.ID, b), Dataset: p.ID, Title: "原始用户记录", Text: "已修改的原始记录", Origin: "raw"})
				mustMerge(t, err)
			}
			mustMerge(t, s.PrepareProjectKnowledge(ctx, task.ID, 1, proposal, []records.ProjectRepositorySource{a, b}))
			mustMerge(t, s.applyProjectKnowledgeItem(ctx, task.ID, 1, 1))
			if mode == "cancel" {
				_, err := s.CancelProjectTask(ctx, task.ID)
				mustMerge(t, err)
				mustMerge(t, s.EndProjectRun(ctx, task.ID, 1, "cancelled", ""))
			} else {
				r, err := s.ApplyProjectKnowledge(ctx, task.ID, 1)
				mustMerge(t, err)
				if r.SaveFailed != 1 || len(r.Added) != 1 {
					t.Fatal(r)
				}
			}
			current, err := s.ProjectTask(ctx, task.ID)
			mustMerge(t, err)
			if current.Receipt == nil || len(current.Receipt.Added) != 1 {
				t.Fatal("earlier receipt was lost", current)
			}
			v, err := s.Current(ctx, current.Receipt.Added[0].Memory)
			mustMerge(t, err)
			if v.State != "active" || v.Origin != "knowledge" {
				t.Fatal(v)
			}
			var state string
			mustMerge(t, s.DB.QueryRow(`SELECT state FROM project_task_items WHERE task=? AND position=2`, task.ID).Scan(&state))
			if mode == "cancel" && state != "cancelled" || mode == "failure" && state != "failed" {
				t.Fatal(state)
			}
		})
	}
}

func TestProjectProcessTerminalAndOwnerFences(t *testing.T) {
	s, p, c := projectTaskFixture(t)
	b := bindProjectFixture(t, s, p, c)
	task, _, err := s.StartProjectTask(context.Background(), records.ProjectTaskStart{OperationID: uuid.NewString(), Project: p.ID, BindingRevision: b.Revision}, projectExecutorFixture(c), nil)
	mustMerge(t, err)
	owner := uuid.NewString()
	mustMerge(t, s.ClaimProjectRun(context.Background(), task.ID, 1, owner))
	ctx := WithProjectRun(context.Background(), owner)
	if err = s.ClaimProjectRun(context.Background(), task.ID, 1, uuid.NewString()); err == nil {
		t.Fatal("second owner claimed task")
	}
	mustMerge(t, s.RegisterProjectProcess(ctx, task.ID, 1, 12345678))
	if err = s.RegisterProjectProcess(ctx, task.ID, 1, 12345679); err == nil {
		t.Fatal("process overwritten")
	}
	if err = s.EndProjectRun(ctx, task.ID, 1, "cancelled", ""); err == nil {
		t.Fatal("live process declared stopped")
	}
	_, err = s.DB.Exec(`UPDATE project_task_runs SET expires_ms=0 WHERE task=?`, task.ID)
	mustMerge(t, err)
	if err = s.ProjectRunOrganizing(ctx, task.ID, 1); err == nil {
		t.Fatal("expired owner advanced task")
	}
	mustMerge(t, s.FinishProjectProcess(ctx, task.ID, 1, records.ProjectTaskExecution{PID: 12345678, Reaped: true, GroupGone: true, StartedMS: 1, EndedMS: 2}))
	mustMerge(t, s.EndProjectRun(ctx, task.ID, 1, "interrupted", "lease_expired"))
	if err = s.RegisterProjectProcess(ctx, task.ID, 1, 12345679); err == nil {
		t.Fatal("terminal task restarted")
	}
}

func TestProjectSchema18MigrationKeepsBindingIntentAndOriginalBackup(t *testing.T) {
	s, p, c := projectTaskFixture(t)
	b := bindProjectFixture(t, s, p, c)
	task, _, err := s.StartProjectTask(context.Background(), records.ProjectTaskStart{OperationID: uuid.NewString(), Project: p.ID, BindingRevision: b.Revision}, projectExecutorFixture(c), nil)
	mustMerge(t, err)
	var path string
	mustMerge(t, s.DB.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path))
	_, err = s.DB.Exec(`DROP TABLE project_task_items; DROP TABLE project_task_sources; DROP TABLE project_task_runs; PRAGMA user_version=18`)
	mustMerge(t, err)
	mustMerge(t, s.Close())
	current, err := Open(path)
	mustMerge(t, err)
	defer current.Close()
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "backups/schema-v18-*.sqlite"))
	mustMerge(t, err)
	if len(backups) != 1 {
		t.Fatal(backups)
	}
	old, err := sql.Open("sqlite", backups[0])
	mustMerge(t, err)
	defer old.Close()
	var version int
	var stored string
	mustMerge(t, old.QueryRow(`PRAGMA user_version`).Scan(&version))
	mustMerge(t, old.QueryRow(`SELECT state FROM project_tasks WHERE id=?`, task.ID).Scan(&stored))
	if version != 18 || stored != "queued" {
		t.Fatal("migration changed original backup", version, stored)
	}
	bound, err := current.ProjectBinding(context.Background(), p.ID)
	mustMerge(t, err)
	if bound.ConnectionID != c.ID || bound.Revision != b.Revision {
		t.Fatal("migration changed explicit binding")
	}
	var processes int
	mustMerge(t, current.DB.QueryRow(`SELECT count(*) FROM project_task_runs`).Scan(&processes))
	if processes != 0 {
		t.Fatal("migration invented an execution")
	}
}
