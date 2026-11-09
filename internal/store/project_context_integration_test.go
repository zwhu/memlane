package store

// Real SQLite scope/version/read ownership. No real model or process is claimed.
import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func projectReadingRunFixture(t *testing.T, s *Store, p records.Scope, c records.Connection) (context.Context, records.ProjectTask) {
	b := bindProjectFixture(t, s, p, c)
	task, _, err := s.StartProjectTask(context.Background(), records.ProjectTaskStart{OperationID: uuid.NewString(), Project: p.ID, BindingRevision: b.Revision}, projectExecutorFixture(c), nil)
	mustMerge(t, err)
	owner := uuid.NewString()
	mustMerge(t, s.ClaimProjectRun(context.Background(), task.ID, task.Attempt, owner))
	ctx := WithProjectRun(context.Background(), owner)
	mustMerge(t, s.RegisterProjectProcess(ctx, task.ID, task.Attempt, 12345678))
	mustMerge(t, s.ProjectRunOrganizing(ctx, task.ID, task.Attempt))
	return ctx, task
}

func TestProjectContextPagesCurrentKnowledgeExcludeOtherScopesAndRaw(t *testing.T) {
	s, p, c := projectTaskFixture(t)
	ctx, task := projectReadingRunFixture(t, s, p, c)
	text := strings.Repeat("知", 20000)
	known := map[string]int64{}
	for i := 0; i < 40; i++ {
		id := uuid.NewString()
		_, err := s.Apply(ctx, records.Mutation{OperationID: uuid.NewString(), Kind: "save", Dataset: p.ID, Memory: id, Title: "当前项目知识", Text: text, Origin: "knowledge"})
		mustMerge(t, err)
		known[id] = 1
	}
	for id := range known {
		_, err := s.Apply(ctx, records.Mutation{OperationID: uuid.NewString(), Kind: "edit", Dataset: p.ID, Memory: id, ExpectedRevision: 1, Title: "当前v2", Text: text + "新版"})
		mustMerge(t, err)
		known[id] = 2
		break
	}
	scopes, err := s.Scopes(ctx)
	mustMerge(t, err)
	_, err = s.Apply(ctx, records.Mutation{OperationID: uuid.NewString(), Kind: "save", Dataset: scopes[0].ID, Memory: uuid.NewString(), Text: "其他范围不能作为项目上下文", Origin: "knowledge"})
	mustMerge(t, err)
	_, err = s.Apply(ctx, records.Mutation{OperationID: uuid.NewString(), Kind: "save", Dataset: p.ID, Memory: uuid.NewString(), Text: "原文不是项目知识上下文", Origin: "raw"})
	mustMerge(t, err)
	cursor, pages, seen := "", 0, map[string]bool{}
	for {
		page, e := s.ProjectTaskKnowledge(ctx, task.ID, task.Attempt, cursor)
		mustMerge(t, e)
		pages++
		if page.Total != 40 || len(page.Items) == 0 {
			t.Fatal("foreign/raw data or lost current items", page.Total, len(page.Items))
		}
		for _, item := range page.Items {
			if seen[item.Memory] || known[item.Memory] != item.Revision || !strings.HasPrefix(item.Text, text) {
				t.Fatal("wrong current version or scope", item.Memory, item.Revision)
			}
			seen[item.Memory] = true
		}
		if page.Next == "" {
			break
		}
		cursor = page.Next
	}
	if len(seen) != 40 || pages < 3 {
		t.Fatal("byte-budget pagination omitted knowledge", len(seen), pages)
	}
	if _, e := s.ProjectTaskKnowledge(context.Background(), task.ID, task.Attempt, ""); e == nil {
		t.Fatal("unowned knowledge read accepted")
	}
	if _, e := s.ProjectTaskKnowledge(ctx, task.ID, task.Attempt+1, ""); e == nil {
		t.Fatal("old/wrong attempt read accepted")
	}
	if _, e := s.ProjectTaskKnowledge(ctx, task.ID, task.Attempt, "../../other"); e == nil {
		t.Fatal("filesystem cursor accepted")
	}
	_, err = s.CancelProjectTask(ctx, task.ID)
	mustMerge(t, err)
	if _, e := s.ProjectTaskKnowledge(ctx, task.ID, task.Attempt, ""); e == nil {
		t.Fatal("cancelled task still reads knowledge")
	}
}

func TestProjectContextRevalidatesBindingBeforeEveryPage(t *testing.T) {
	s, p, c := projectTaskFixture(t)
	ctx, task := projectReadingRunFixture(t, s, p, c)
	page, err := s.ProjectTaskKnowledge(ctx, task.ID, task.Attempt, "")
	mustMerge(t, err)
	if page.Total != 0 || len(page.Items) != 0 || page.Next != "" {
		t.Fatal("empty scope fabricated context", page)
	}
	mustMerge(t, s.Disconnect(context.Background(), c.ID))
	if _, e := s.ProjectTaskKnowledge(ctx, task.ID, task.Attempt, ""); e == nil {
		t.Fatal("revoked binding still reads cached scope")
	}
}

func TestProjectContextRejectsReplacedRepository(t *testing.T) {
	s, p, c := projectTaskFixture(t)
	ctx, task := projectReadingRunFixture(t, s, p, c)
	mustMerge(t, os.Rename(p.Path, p.Path+"-previous"))
	if b, err := exec.Command("git", "init", p.Path).CombinedOutput(); err != nil {
		t.Fatal(err, string(b))
	}
	if _, err := s.ProjectTaskKnowledge(ctx, task.ID, task.Attempt, ""); err == nil {
		t.Fatal("replaced repository retained knowledge-read authorization")
	}
}
