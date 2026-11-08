package memory

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/engine"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

type rebuildReceiptWorker struct{}

func (rebuildReceiptWorker) Index(context.Context, engine.Record) (engine.Receipt, error) {
	return engine.Receipt{Generation: "synthetic-receipt", ChunkIDs: []string{uuid.NewString()}}, nil
}
func (rebuildReceiptWorker) Search(context.Context, string, string, int) ([]engine.Candidate, error) {
	return nil, nil
}

// Explicit SQL publication fault; no Rust/model. Engine success cannot hide a failed receipt.
func TestIndexRebuildPublicationFailureRemainsVisible(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "data.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var scope string
	if err = s.DB.QueryRow(`SELECT id FROM scopes WHERE kind='experience'`).Scan(&scope); err != nil {
		t.Fatal(err)
	}
	v, err := s.Apply(ctx, records.Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: uuid.NewString(), Dataset: scope, Text: "synthetic receipt fault original"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.StartIndexRebuild(ctx, records.IndexRebuildStart{OperationID: uuid.NewString(), Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.Exec(`CREATE TRIGGER reject_rebuild_completed BEFORE UPDATE ON index_rebuild_items WHEN NEW.state='completed' BEGIN SELECT RAISE(ABORT,'explicit receipt publication fault'); END`)
	if err != nil {
		t.Fatal(err)
	}
	svc := Service{Store: s, Engine: rebuildReceiptWorker{}}
	if err = svc.IndexCurrent(ctx, v.Memory); err == nil {
		t.Fatal("failed publication accepted")
	}
	state, err := s.IndexRebuild(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.State != "failed" || state.Items[0].Error == "" {
		t.Fatal("publication failure invisible", state)
	}
	current, err := s.Current(ctx, v.Memory)
	if err != nil {
		t.Fatal(err)
	}
	if current.IndexState != "failed" || current.Text != v.Text || current.Revision != 1 {
		t.Fatal(current)
	}
	_, err = s.DB.Exec(`DROP TRIGGER reject_rebuild_completed`)
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.IndexCurrent(ctx, v.Memory); err != nil {
		t.Fatal(err)
	}
	state, err = s.IndexRebuild(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.State != "completed" || state.Items[0].Error == "" {
		t.Fatal("recovery or failure history lost", state)
	}
}
