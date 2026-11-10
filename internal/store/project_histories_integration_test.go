package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

// SQLite/source fixtures only; these do not claim a normal Agent inference.
func frozenHistoryFixture(t *testing.T) (*Store, context.Context, records.ProjectTask, records.ProjectRepositorySource, records.Connection) {
	t.Helper()
	s, p, c := projectTaskFixture(t)
	b := bindProjectFixture(t, s, p, c)
	session := uuid.NewString()
	source := projectSourceFixture("history/codex/"+session+".txt", "2026-10-01T01:00:01Z · 用户\nTicketBox 仅允许 release 分支正式发布。\n")
	source.History = &records.ProjectHistoryOriginal{Agent: "codex", Session: "codex_" + session, Workspace: p.Path, CLIVersion: "0.167.0-alpha.3.2", CapturedAt: "2026-10-01T01:00:00Z", Digest: strings.Repeat("b", 64)}
	choice := records.ProjectSessionSelection{ID: source.History.Session, Digest: source.History.Digest}
	resolved := records.ProjectSessionSource{ProjectSessionSelection: choice, Agent: "codex", Project: p.ID}
	_, identity, err := s.projectTaskWorkspace(context.Background(), p.ID)
	mustMerge(t, err)
	d := records.ProjectTaskStart{OperationID: uuid.NewString(), Project: p.ID, BindingRevision: b.Revision, IncludeHistory: true, Sessions: []records.ProjectSessionSelection{choice}, HistoryWorkspaceID: identity}
	task, created, err := s.StartProjectTaskWithHistory(context.Background(), d, projectExecutorFixture(c), []records.ProjectSessionSource{resolved}, []records.ProjectRepositorySource{source})
	mustMerge(t, err)
	if !created {
		t.Fatal("new history task missing")
	}
	owner := uuid.NewString()
	mustMerge(t, s.ClaimProjectRun(context.Background(), task.ID, task.Attempt, owner))
	ctx := WithProjectRun(context.Background(), owner)
	mustMerge(t, s.RegisterProjectProcess(ctx, task.ID, task.Attempt, 12345678))
	mustMerge(t, s.ProjectRunOrganizing(ctx, task.ID, task.Attempt))
	prior, found, err := s.ProjectTaskForIntent(ctx, d)
	mustMerge(t, err)
	if !found || prior.ID != task.ID {
		t.Fatal("uncertain intent cannot recover without rereading live history")
	}
	return s, ctx, task, source, c
}

func TestProjectHistoryFrozenReaderOwnerCancelAndRevocation(t *testing.T) {
	for _, mode := range []string{"cancel", "disconnect"} {
		t.Run(mode, func(t *testing.T) {
			s, ctx, task, source, c := frozenHistoryFixture(t)
			read, err := s.ProjectTaskHistory(ctx, task.ID, task.Attempt, source.History.Session)
			mustMerge(t, err)
			if !reflect.DeepEqual(read, source) {
				t.Fatal("frozen version changed")
			}
			for _, wrong := range []struct {
				ctx     context.Context
				attempt int64
				session string
			}{{context.Background(), task.Attempt, source.History.Session}, {ctx, task.Attempt + 1, source.History.Session}, {ctx, task.Attempt, "codex_" + uuid.NewString()}} {
				if _, err = s.ProjectTaskHistory(wrong.ctx, task.ID, wrong.attempt, wrong.session); err == nil {
					t.Fatal("unowned/unselected history read accepted")
				}
			}
			if _, err = s.DB.Exec(`UPDATE project_task_histories SET payload='{}' WHERE task=?`, task.ID); err == nil {
				t.Fatal("frozen source mutable")
			}
			if mode == "cancel" {
				_, err = s.CancelProjectTask(ctx, task.ID)
				mustMerge(t, err)
			} else {
				mustMerge(t, s.Disconnect(ctx, c.ID))
			}
			if _, err = s.ProjectTaskHistory(ctx, task.ID, task.Attempt, source.History.Session); err == nil {
				t.Fatal("closed/revoked owner still reads source")
			}
		})
	}
}

func TestProjectHistorySaveKeepsOriginalTimesAndLocalSourceOwnership(t *testing.T) {
	s, ctx, task, source, _ := frozenHistoryFixture(t)
	encoded, _ := json.Marshal(source)
	h := sha256.New()
	h.Write(encoded)
	h.Write([]byte{'\n'})
	execution := records.ProjectTaskExecution{PID: 12345678, StartedMS: 1, EndedMS: 2, Reaped: true, GroupGone: true, Terminal: true, Model: task.Snapshot.Executor.Model, Provider: "fixture-provider", PermissionProfile: "memlane_project", Thread: "fixture-thread", Turn: "fixture-turn",
		HistoryContext: &records.ProjectHistoryContextReceipt{Selected: 1, Read: 1, Sent: 1, Bytes: int64(len(encoded)), Digest: hex.EncodeToString(h.Sum(nil))}}
	mustMerge(t, s.FinishProjectProcess(ctx, task.ID, task.Attempt, execution))
	proposal := records.ProjectKnowledgeProposal{Task: task.ID, Attempt: task.Attempt, Explanation: "source/SQLite contract fixture", Items: []records.ProjectKnowledgeItem{{Title: "TicketBox 发布分支约定", Text: "本项目只允许 release 分支正式发布。", Sources: []records.ProjectKnowledgeQuote{{Source: source.ID, Quote: "TicketBox 仅允许 release 分支正式发布。"}}}}}
	r, err := s.SaveProjectKnowledge(ctx, task.ID, task.Attempt, proposal, []records.ProjectRepositorySource{source})
	mustMerge(t, err)
	if len(r.Added) != 1 || len(r.Sources) != 1 {
		t.Fatal(r)
	}
	v, err := s.Current(ctx, r.Sources[0].Memory)
	mustMerge(t, err)
	if v.SourceDetail.History == nil || v.SourceDetail.Native != nil || v.SourceDetail.Official != nil || v.SourceDetail.History.CapturedAt != source.History.CapturedAt || v.CreatedAt == source.History.CapturedAt {
		t.Fatal("local historical ownership/time blended with current save", v)
	}
	read, err := s.ReadSource(ctx, records.SourceRead{Project: task.Snapshot.Project, Owner: r.Added[0].Memory, OwnerRevision: 1, Memory: r.Sources[0].Memory, Revision: 1})
	mustMerge(t, err)
	if read.State != "available" || read.Record.Text != source.Text || !strings.Contains(source.Text, read.Quote) {
		t.Fatal("local original requires an unrelated Cognee binding", read)
	}
	changed := source
	changed.Text += "forged append"
	changed.SHA256 = UsageDigest(changed.Text)
	if err = s.PrepareProjectKnowledge(ctx, task.ID, task.Attempt, proposal, []records.ProjectRepositorySource{changed}); err == nil {
		t.Fatal("changed frozen material accepted")
	}
	var rows int
	mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM versions`).Scan(&rows))
	if rows != 2 {
		t.Fatal("replay changed knowledge versions")
	}
	if _, err = time.Parse(time.RFC3339Nano, v.SourceDetail.History.CapturedAt); err != nil {
		t.Fatal(err)
	}
}

func TestOrdinaryAgentCannotStampHistoricalProvenance(t *testing.T) {
	s, ctx, task, source, c := frozenHistoryFixture(t)
	_, err := s.ApplyAgentSave(ctx, c, records.Mutation{OperationID: "forged-history", Kind: "save", Memory: "forged", Dataset: task.Snapshot.Project, Text: source.Text, SourceDetail: &records.AgentSource{History: source.History}})
	var denied *records.AccessError
	if !errors.As(err, &denied) || denied.Code != "invalid_source" {
		t.Fatal("Agent forged host-owned history provenance", err)
	}
}
