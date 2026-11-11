package daemon_test

import (
	"context"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/daemon"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestProjectPreferencePrivateIPCUserDecision(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	stop := startService(t, daemon.Config{Root: root})
	defer stop()
	l := serviceipc.Library{Root: root}
	repo := filepath.Join(t.TempDir(), "repo")
	if output, e := exec.Command("git", "init", repo).CombinedOutput(); e != nil {
		t.Fatal(e, string(output))
	}
	project, e := l.RegisterProject(ctx, records.ScopeDraft{Name: "Generated project", Path: repo})
	requireService(t, e)
	scopes, e := l.Scopes(ctx)
	requireService(t, e)
	pref := ""
	for _, s := range scopes {
		if s.Kind == "preference" {
			pref = s.ID
		}
	}
	versions := []records.Version{}
	for _, scope := range []string{project.ID, pref} {
		v, e := l.Apply(ctx, records.Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: uuid.NewString(), Dataset: scope, Text: "Generated independent knowledge " + scope, Source: "Generated user source"})
		requireService(t, e)
		versions = append(versions, v)
	}
	q := records.ProjectPreferenceQuery{Project: project.ID, ProjectRecord: records.UsageTarget{Memory: versions[0].Memory, Dataset: project.ID, Revision: 1}, PreferenceRecord: records.UsageTarget{Memory: versions[1].Memory, Dataset: pref, Revision: 1}, Datasets: []string{project.ID, pref}}
	p, e := l.ProjectPreferenceComparison(ctx, q)
	requireService(t, e)
	if p.Confirmation != nil || p.LibraryIdentity == "" || p.ProjectRecord.Text != versions[0].Text || p.PreferenceRecord.Source != versions[1].Source {
		t.Fatal(p)
	}
	r := records.ProjectPreferenceConfirmation{OperationID: uuid.NewString(), LibraryIdentity: p.LibraryIdentity, Query: q, ProjectQuote: versions[0].Text, PreferenceQuote: versions[1].Text, Conditions: "Only this selected project for this reference", ConfirmProjectRule: true, Decision: "use_project"}
	d, e := l.ConfirmProjectPreference(ctx, r)
	requireService(t, e)
	same, e := l.ConfirmProjectPreference(ctx, r)
	requireService(t, e)
	if !reflect.DeepEqual(d, same) || d.Source != "user" {
		t.Fatal("IPC lost confirmation provenance", same)
	}
	p, e = l.ProjectPreferenceComparison(ctx, q)
	requireService(t, e)
	if p.Confirmation == nil || p.Confirmation.ID != d.ID {
		t.Fatal(p)
	}
	page, e := l.ProjectPreferenceDecisions(ctx, records.ProjectPreferenceDecisionsQuery{LibraryIdentity: p.LibraryIdentity, Project: q.Project, ProjectRecord: q.ProjectRecord, Datasets: q.Datasets})
	requireService(t, e)
	if len(page.Decisions) != 1 || page.Decisions[0].ID != d.ID || len(page.CoverageDatasets) != 2 {
		t.Fatal("IPC failed to discover actual confirmation", page)
	}
	for _, original := range versions {
		v, e := l.Get(ctx, original.Memory)
		requireService(t, e)
		if v.Revision != original.Revision || v.Text != original.Text || v.Dataset != original.Dataset {
			t.Fatal("confirmation altered knowledge", v)
		}
	}
	for _, op := range []string{"project_preference_comparison", "project_preference_confirm", "project_preference_decisions"} {
		if e = serviceipc.Call(ctx, root, serviceipc.Request{Op: op}, nil); e == nil {
			t.Fatal("nil request accepted", op)
		}
	}
	r.ConfirmProjectRule = false
	r.OperationID = uuid.NewString()
	if _, e = l.ConfirmProjectPreference(ctx, r); e == nil {
		t.Fatal("viewing became confirmation")
	}
	q.LibraryIdentity = uuid.NewString()
	if _, e = l.ProjectPreferenceComparison(ctx, q); e == nil {
		t.Fatal("old library identity accepted")
	}
}
