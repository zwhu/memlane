package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

// SQLite + explicitly injected installation failures. Real pi and packaged
// helper acceptance are separate; no user's Agent settings are touched.
func agentBatchServer(t *testing.T) *Server {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "memory.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{store: db, lifecycle: ctx, stop: cancel}
	t.Cleanup(func() { cancel(); s.agentBatchWG.Wait(); db.Close() })
	return s
}

func awaitAgentBatch(t *testing.T, s *Server, id string, predicate func(records.AgentConnectionBatch) bool) records.AgentConnectionBatch {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, err := s.connectionBatch(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if predicate(b) {
			return b
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("batch state timeout")
	return records.AgentConnectionBatch{}
}

func TestAgentBatchFailureRetryReplayPreservesSuccess(t *testing.T) {
	s := agentBatchServer(t)
	ctx := context.Background()
	var installs, verifies atomic.Int32
	s.agentBatchInstall = func(ctx context.Context, i records.AgentConnectionBatchItem) (records.AgentConnectionBatchItem, error) {
		installs.Add(1)
		i.State = "awaiting_reload"
		i.Generation = 1
		return i, nil
	}
	s.agentBatchVerify = func(ctx context.Context, i records.AgentConnectionBatchItem) (records.AgentConnectionBatchItem, error) {
		if verifies.Add(1) == 1 {
			return i, errors.New("synthetic protocol failure")
		}
		i.State = "connected"
		return i, nil
	}
	d := records.AgentConnectionBatchStart{Identity: batchIdentity(s), OperationID: uuid.NewString(), Targets: []records.AgentConnectionTarget{{Agent: "pi", Path: "/synthetic/pi", Directory: "/synthetic/config"}, {Agent: "traex", Path: "/synthetic/traex", Directory: "/synthetic/traex-config"}}}
	b, err := s.startConnectionBatch(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	b = awaitAgentBatch(t, s, b.ID, func(b records.AgentConnectionBatch) bool { return b.State == "waiting" })
	if b.Items[1].State != "unsupported" {
		t.Fatal("unsupported adapter pretended success", b)
	}
	s.agentBatchWG.Wait()
	if _, err = s.startConnectionBatch(ctx, d); err != nil {
		t.Fatal(err)
	}
	verify := records.AgentConnectionBatchAction{Identity: b.Identity, BatchID: b.ID, OperationID: uuid.NewString(), Kind: "verify"}
	if _, err = s.actionConnectionBatch(ctx, verify); err != nil {
		t.Fatal(err)
	}
	b = awaitAgentBatch(t, s, b.ID, func(b records.AgentConnectionBatch) bool { return b.State == "partial_failed" })
	if b.Items[0].State != "failed" {
		t.Fatal(b)
	}
	if _, err = s.actionConnectionBatch(ctx, verify); err != nil {
		t.Fatal(err)
	}
	if verifies.Load() != 1 || installs.Load() != 1 {
		t.Fatal("action replay restarted work")
	}
	// WaitGroup also establishes that the completed worker left its run slot.
	s.agentBatchWG.Wait()
	retry := records.AgentConnectionBatchAction{Identity: b.Identity, BatchID: b.ID, OperationID: uuid.NewString(), Kind: "retry"}
	if _, err = s.actionConnectionBatch(ctx, retry); err != nil {
		t.Fatal(err)
	}
	b = awaitAgentBatch(t, s, b.ID, func(b records.AgentConnectionBatch) bool {
		return b.State == "partial_failed" && b.Items[0].State == "connected"
	})
	if installs.Load() != 1 || verifies.Load() != 2 {
		t.Fatal("retry reinstalled a successfully configured item")
	}
	s.agentBatchWG.Wait()
	if _, err = s.actionConnectionBatch(ctx, retry); err != nil {
		t.Fatal(err)
	}
	if verifies.Load() != 2 {
		t.Fatal("retry replay invoked verification")
	}
}

func TestAgentBatchCancelWaitsForOwnerAndKeepsCompletedItems(t *testing.T) {
	s := agentBatchServer(t)
	ctx := context.Background()
	started := make(chan struct{})
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	s.agentBatchInstall = func(ctx context.Context, i records.AgentConnectionBatchItem) (records.AgentConnectionBatchItem, error) {
		i.State = "awaiting_reload"
		i.Generation = 1
		return i, nil
	}
	s.agentBatchVerify = func(ctx context.Context, i records.AgentConnectionBatchItem) (records.AgentConnectionBatchItem, error) {
		close(started)
		<-ctx.Done()
		<-release
		return i, ctx.Err()
	}
	b, err := s.startConnectionBatch(ctx, records.AgentConnectionBatchStart{Identity: batchIdentity(s), OperationID: uuid.NewString(), Targets: []records.AgentConnectionTarget{{Agent: "pi", Path: "/synthetic/pi", Directory: "/synthetic/config"}}})
	if err != nil {
		t.Fatal(err)
	}
	awaitAgentBatch(t, s, b.ID, func(b records.AgentConnectionBatch) bool { return b.State == "waiting" })
	s.agentBatchWG.Wait()
	if _, err = s.actionConnectionBatch(ctx, records.AgentConnectionBatchAction{Identity: b.Identity, BatchID: b.ID, OperationID: uuid.NewString(), Kind: "verify"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("verification not active")
	}
	ack, err := s.actionConnectionBatch(ctx, records.AgentConnectionBatchAction{Identity: b.Identity, BatchID: b.ID, OperationID: uuid.NewString(), Kind: "cancel"})
	if err != nil {
		t.Fatal(err)
	}
	if ack.State != "cancelling" {
		t.Fatal("cancellation ACK pretended process exit", ack)
	}
	close(release)
	b = awaitAgentBatch(t, s, b.ID, func(b records.AgentConnectionBatch) bool { return b.State == "cancelled" })
	if b.Items[0].State != "cancelled" || b.Items[0].Generation != 1 {
		t.Fatal("cancel lost completed configuration", b)
	}
}

func batchIdentity(s *Server) string {
	identity, _ := s.store.AgentConnectionBatchIdentity(context.Background())
	return identity
}
