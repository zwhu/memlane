package store

import (
	"context"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func TestConnectedAgentsReadAndWriteAllRegisteredGroups(t *testing.T) {
	s, _ := mergeDB(t)
	ctx := context.Background()
	projects := []records.Scope{}
	for _, name := range []string{"one", "two"} {
		repo := filepath.Join(t.TempDir(), name)
		if b, e := exec.Command("/usr/bin/git", "init", repo).CombinedOutput(); e != nil {
			t.Fatal(e, string(b))
		}
		p, e := s.RegisterProject(ctx, records.ScopeDraft{Name: name, Path: repo})
		mustMerge(t, e)
		projects = append(projects, p)
	}
	for _, agent := range []string{"codex", "pi"} {
		c, e := s.ConfigureConnection(ctx, records.ConnectionDraft{Agent: agent, Name: agent, ConnectionOnly: true})
		mustMerge(t, e)
		for _, project := range projects {
			r := records.AgentRequest{ConnectionID: c.ID, Generation: c.Generation, Workspace: projects[0].Path, Tool: "memory_save", Arguments: records.AgentArguments{Scope: project.ID}}
			resolved, groups, e := s.ResolveAgentScopes(ctx, r)
			mustMerge(t, e)
			if !reflect.DeepEqual(groups, []string{project.ID}) {
				t.Fatal("explicit destination was restricted", groups)
			}
			id := uuid.NewString()
			saved, e := s.ApplyAgentSave(ctx, resolved, Mutation{Kind: "save", OperationID: "agent:" + id, Memory: id, Dataset: project.ID, Text: agent + " synthetic record"})
			mustMerge(t, e)
			if saved.Dataset != project.ID {
				t.Fatal("wrong destination", saved)
			}
			r.Tool = "memory_get"
			if _, _, e = s.ResolveAgentScopesReadOnly(ctx, r); e != nil {
				t.Fatal("another project read was restricted", e)
			}
		}
		_, groups, e := s.ResolveAgentScopes(ctx, records.AgentRequest{ConnectionID: c.ID, Generation: c.Generation, Tool: "memory_scopes"})
		mustMerge(t, e)
		for _, project := range projects {
			found := false
			for _, id := range groups {
				found = found || id == project.ID
			}
			if !found {
				t.Fatal("project hidden from connected Agent")
			}
		}
	}
}

func TestConnectionLifecycleAndSaveDestinationRemainExplicit(t *testing.T) {
	s, _ := mergeDB(t)
	ctx := context.Background()
	c, e := s.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "pi", Name: "pi", ConnectionOnly: true})
	mustMerge(t, e)
	r := records.AgentRequest{ConnectionID: c.ID, Generation: c.Generation, Tool: "memory_save"}
	if _, _, e = s.ResolveAgentScopes(ctx, r); e == nil {
		t.Fatal("save invented a destination")
	}
	r.Tool = "memory_search"
	if _, _, e = s.ResolveAgentScopes(ctx, r); e != nil {
		t.Fatal(e)
	}
	mustMerge(t, s.Disconnect(ctx, c.ID))
	if _, _, e = s.ResolveAgentScopes(ctx, r); e == nil {
		t.Fatal("disconnected transport identity still accepted")
	}
}
