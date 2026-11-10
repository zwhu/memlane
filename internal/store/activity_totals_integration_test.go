package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

// Real SQLite receipt/version checks; no model or graph fixtures count as
// independently received records or effective knowledge.
func TestActivityTotalsKeepReceiptIdentitySeparateFromEffectiveKnowledge(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "memory.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	scopes, err := s.Scopes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw := Mutation{OperationID: uuid.NewString(), Kind: "save", Dataset: scopes[0].ID, Memory: uuid.NewString(), Text: "独立原始记录", Origin: "raw"}
	knowledge := Mutation{OperationID: uuid.NewString(), Kind: "save", Dataset: scopes[0].ID, Memory: uuid.NewString(), Text: "用户确认的知识", Origin: "knowledge"}
	for _, mutation := range []Mutation{raw, raw, knowledge} {
		if _, err := s.Apply(ctx, mutation); err != nil {
			t.Fatal(err)
		}
	}
	assertTotals := func(rawCount, knowledgeCount int) {
		t.Helper()
		value, err := s.Activities(ctx, "UTC")
		if err != nil {
			t.Fatal(err)
		}
		if value.RawTotal == nil || value.KnowledgeTotal == nil || *value.RawTotal != rawCount || *value.KnowledgeTotal != knowledgeCount {
			t.Fatalf("incorrect receipt/effective counts: %#v", value)
		}
	}
	assertTotals(1, 1)
	if _, err := s.Apply(ctx, Mutation{OperationID: uuid.NewString(), Kind: "edit", Dataset: knowledge.Dataset, Memory: knowledge.Memory, Text: "编辑后的当前知识", ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	assertTotals(1, 1)
	if _, err := s.Apply(ctx, Mutation{OperationID: uuid.NewString(), Kind: "delete", Dataset: knowledge.Dataset, Memory: knowledge.Memory, ExpectedRevision: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(ctx, Mutation{OperationID: uuid.NewString(), Kind: "delete", Dataset: raw.Dataset, Memory: raw.Memory, ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	assertTotals(1, 0)
}

func TestActiveTaskQueriesFindOldRunningTasksBeyondHistoryPage(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "memory.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	scopes, err := s.Scopes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Explicit task-table fixtures exercise only the summary query. They do
	// not constitute real Agent execution or runtime/cancellation evidence.
	for _, table := range []string{"merge_tasks", "organization_tasks"} {
		for i := 0; i < 42; i++ {
			state := "completed"
			if i == 0 {
				state = "running"
			}
			if i == 41 {
				state = "queued"
			}
			id := uuid.NewString()
			_, err = s.DB.ExecContext(ctx, "INSERT INTO "+table+"(id,operation_id,request_digest,project,state,created_at) VALUES(?,?,?,?,?,?)", id, id, "explicit query fixture", scopes[0].ID, state, time.Now().Add(time.Duration(i-42)*time.Minute).UTC().Format(time.RFC3339Nano))
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	q := records.MergeListQuery{ActiveOnly: true, Limit: 1}
	merges, err := s.MergeList(ctx, q)
	if err != nil || len(merges.Items) != 1 || merges.Items[0].State != "running" {
		t.Fatal("old running merge was hidden by completed/queued history", merges, err)
	}
	organizations, err := s.ListOrganizations(ctx, records.OrganizationListQuery{MergeListQuery: q})
	if err != nil || len(organizations.Items) != 1 || organizations.Items[0].State != "running" {
		t.Fatal("old running organization was hidden by completed/queued history", organizations, err)
	}
	for _, table := range []string{"merge_tasks", "organization_tasks"} {
		if _, err := s.DB.ExecContext(ctx, "UPDATE "+table+" SET state='completed' WHERE state='running'"); err != nil {
			t.Fatal(err)
		}
	}
	merges, err = s.MergeList(ctx, q)
	if err != nil || len(merges.Items) != 1 || merges.Items[0].State != "queued" {
		t.Fatal("completed merge remained active", merges, err)
	}
	organizations, err = s.ListOrganizations(ctx, records.OrganizationListQuery{MergeListQuery: q})
	if err != nil || len(organizations.Items) != 1 || organizations.Items[0].State != "queued" {
		t.Fatal("completed organization remained active", organizations, err)
	}
}
