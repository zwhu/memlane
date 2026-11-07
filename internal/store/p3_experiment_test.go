package store_test

// These are SQLite/service integration tests, using disposable databases and
// synthetic knowledge. The optional Rust test uses the existing real worker.
import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/engine"
	"github.com/zwhu/memlane/internal/memory"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

var p3ctx = context.Background()

func require(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func count(t *testing.T, s *store.Store, query string, args ...any) int {
	t.Helper()
	var n int
	require(t, s.DB.QueryRow(query, args...).Scan(&n))
	return n
}

func database(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "synthetic.sqlite")
	s, err := store.OpenP3Experiment(path)
	require(t, err)
	t.Cleanup(func() { s.Close() })
	return s, path
}

func task(t *testing.T, s *store.Store) (records.P3Snapshot, records.P3Proposal, []byte) {
	t.Helper()
	dataset := uuid.NewString()
	ids := []string{uuid.NewString(), uuid.NewString()}
	for i, text := range []string{"Synthetic project uses pnpm.", "Synthetic project commits pnpm-lock.yaml whenever dependencies change."} {
		_, err := s.Apply(p3ctx, store.Mutation{OperationID: uuid.NewString(), Kind: "save", Dataset: dataset, Memory: ids[i], Text: text, Title: "Synthetic source", Source: "synthetic-agent", SourceDetail: &records.AgentSource{Fragment: text, References: []string{"synthetic://p3/" + ids[i]}}})
		require(t, err)
	}
	x, err := s.StartP3Experiment(p3ctx, uuid.NewString(), ids)
	require(t, err)
	p := records.P3Proposal{Schema: x.Schema, Task: x.Task, Attempt: x.Attempt, Digest: x.Digest, ID: x.ProposalID(), Action: "propose_merge", Title: "Synthetic package policy", Text: "Synthetic project uses pnpm and commits pnpm-lock.yaml whenever dependencies change."}
	for _, v := range x.Inputs {
		p.Sources = append(p.Sources, records.P3Source{Memory: v.Memory, Revision: v.Revision, Quote: v.Text})
	}
	b, err := json.Marshal(p)
	require(t, err)
	return x, p, b
}

func stage(t *testing.T, s *store.Store, x records.P3Snapshot, b []byte) {
	t.Helper()
	_, err := s.StageP3Experiment(p3ctx, x.Task, x.Attempt, b)
	require(t, err)
}

func TestP3AtomicMergeAndRetainedSources(t *testing.T) {
	s, _ := database(t)
	x, p, b := task(t, s)
	// Source index/activity receipts here are explicit protocol fixtures.
	for _, v := range x.Inputs {
		require(t, s.MarkIndexed(p3ctx, v.Record, engine.Receipt{Generation: "fixture", ChunkIDs: []string{uuid.NewString()}}))
	}
	stage(t, s, x, b)
	if count(t, s, "SELECT count(*) FROM versions") != 2 {
		t.Fatal("proposal changed knowledge before confirmation")
	}
	if _, err := s.ApplyP3Experiment(p3ctx, p.ID, "receipt", false); err == nil {
		t.Fatal("missing confirmation accepted")
	}
	r, err := s.ApplyP3Experiment(p3ctx, p.ID, "receipt", true)
	require(t, err)
	merged, err := s.Current(p3ctx, r.Memory)
	require(t, err)
	if merged.Text != p.Text || merged.Revision != 1 || merged.State != "active" || merged.IndexState != "pending" {
		t.Fatalf("wrong merged version: %+v", merged)
	}
	if count(t, s, "SELECT count(*) FROM versions") != 5 || count(t, s, "SELECT count(*) FROM p3_experiment_sources") != 2 || count(t, s, "SELECT count(*) FROM p3_experiment_receipts") != 1 || count(t, s, "SELECT count(*) FROM jobs WHERE state='pending'") != 1 {
		t.Fatal("incomplete transaction")
	}
	for _, source := range x.Inputs {
		v, err := s.Current(p3ctx, source.Memory)
		require(t, err)
		if v.State != "archived" || v.Revision != 2 || v.Text != source.Text || v.Original != source.Text || v.SourceDetail.References[0] != source.SourceDetail.References[0] {
			t.Fatal("archive lost source")
		}
		var raw string
		require(t, s.DB.QueryRow("SELECT snapshot FROM p3_experiment_sources WHERE source_memory=?", source.Memory).Scan(&raw))
		var retained records.Version
		require(t, json.Unmarshal([]byte(raw), &retained))
		if retained.Revision != 1 || retained.Text != source.Text || retained.SourceDetail.Fragment != source.Text {
			t.Fatal("source snapshot lost")
		}
	}
	if count(t, s, "SELECT count(*) FROM activities") != 2 {
		t.Fatal("merge changed prior activity facts")
	}
	page, err := s.List(p3ctx, records.ListQuery{Datasets: []string{x.Dataset}})
	require(t, err)
	if len(page.Items) != 1 || page.Items[0].Memory != r.Memory {
		t.Fatal("archived sources still in active list")
	}
	service := memory.Service{Store: s}
	if err = service.IndexCurrent(p3ctx, r.Memory); err == nil {
		t.Fatal("missing engine reported success")
	}
	v, err := s.Current(p3ctx, r.Memory)
	require(t, err)
	if v.IndexState != "failed" || v.Text != p.Text || count(t, s, "SELECT count(*) FROM p3_experiment_receipts") != 1 {
		t.Fatal("index failure lost commit")
	}
	require(t, s.Requeue(p3ctx, r.Memory, 1))
	require(t, s.MarkIndexed(p3ctx, v.Record, engine.Receipt{Generation: "fixture", ChunkIDs: []string{uuid.NewString()}}))
	if count(t, s, "SELECT count(*) FROM activities") != 3 {
		t.Fatal("merge not counted once on first usable index")
	}
	r2, err := s.ApplyP3Experiment(p3ctx, p.ID, "another-receipt", true)
	require(t, err)
	if r2 != r {
		t.Fatal("proposal duplicate not idempotent")
	}
	r3, err := s.ApplyP3Experiment(p3ctx, p.ID, "receipt", true)
	require(t, err)
	if r3 != r || count(t, s, "SELECT count(*) FROM versions") != 5 {
		t.Fatal("receipt duplicate wrote knowledge")
	}
}

func TestP3StaleSecondTargetAndRollback(t *testing.T) {
	for _, scenario := range []string{"edited_second_target", "mid_transaction_failure"} {
		t.Run(scenario, func(t *testing.T) {
			s, _ := database(t)
			x, p, b := task(t, s)
			stage(t, s, x, b)
			if scenario == "edited_second_target" {
				v := x.Inputs[1]
				_, err := s.Apply(p3ctx, store.Mutation{OperationID: uuid.NewString(), Kind: "edit", Memory: v.Memory, Dataset: v.Dataset, ExpectedRevision: 1, Text: "Synthetic newer user decision"})
				require(t, err)
			} else {
				_, err := s.DB.Exec("CREATE TRIGGER p3_fail_receipt BEFORE INSERT ON p3_experiment_receipts BEGIN SELECT RAISE(ABORT,'synthetic disk failure'); END")
				require(t, err)
			}
			before := count(t, s, "SELECT count(*) FROM versions")
			beforeJobs := count(t, s, "SELECT count(*) FROM jobs")
			beforeOperations := count(t, s, "SELECT count(*) FROM operations")
			_, err := s.ApplyP3Experiment(p3ctx, p.ID, "receipt", true)
			if err == nil {
				t.Fatal("invalid transaction accepted")
			}
			if scenario == "edited_second_target" && !errors.Is(err, records.ErrP3Expired) {
				t.Fatal(err)
			}
			if count(t, s, "SELECT count(*) FROM versions") != before || count(t, s, "SELECT count(*) FROM memories") != 2 || count(t, s, "SELECT count(*) FROM p3_experiment_sources") != 0 || count(t, s, "SELECT count(*) FROM p3_experiment_receipts") != 0 || count(t, s, "SELECT count(*) FROM memories WHERE state='archived'") != 0 {
				t.Fatal("partial merge committed")
			}
			if count(t, s, "SELECT count(*) FROM jobs") != beforeJobs || count(t, s, "SELECT count(*) FROM operations") != beforeOperations {
				t.Fatal("partial index job or operation committed")
			}
			first, err := s.Current(p3ctx, x.Inputs[0].Memory)
			require(t, err)
			if first.Revision != 1 || first.State != "active" {
				t.Fatal("first target overwritten")
			}
		})
	}
}

func TestP3InvalidResultsAndIdempotency(t *testing.T) {
	s, _ := database(t)
	x, p, b := task(t, s)
	for _, change := range []string{"attempt", "source", "quote", "action", "unknown", "missing"} {
		t.Run(change, func(t *testing.T) {
			var value map[string]any
			require(t, json.Unmarshal(b, &value))
			switch change {
			case "attempt":
				value["attempt"] = 2
			case "source":
				value["sources"].([]any)[1].(map[string]any)["memory"] = uuid.NewString()
			case "quote":
				value["sources"].([]any)[0].(map[string]any)["quote"] = "fabricated"
			case "action":
				value["action"] = "delete"
			case "unknown":
				value["approved"] = true
			case "missing":
				delete(value, "title")
			}
			bad, _ := json.Marshal(value)
			if _, err := s.StageP3Experiment(p3ctx, x.Task, 1, bad); !errors.Is(err, records.ErrP3Invalid) {
				t.Fatalf("invalid output accepted: %v", err)
			}
		})
	}
	if _, err := s.StageP3Experiment(p3ctx, x.Task, 2, b); !errors.Is(err, records.ErrP3Cancelled) {
		t.Fatal("old/wrong attempt accepted")
	}
	stage(t, s, x, b)
	stage(t, s, x, b)
	p.Text = "different synthetic body"
	altered, _ := json.Marshal(p)
	if _, err := s.StageP3Experiment(p3ctx, x.Task, 1, altered); !errors.Is(err, store.ErrConflict) {
		t.Fatal("same proposal different body accepted")
	}
	if count(t, s, "SELECT count(*) FROM p3_experiment_results") != 1 {
		t.Fatal("duplicate result receipt")
	}
	r, err := s.ApplyP3Experiment(p3ctx, p.ID, "receipt", true)
	require(t, err)
	x2, p2, b2 := task(t, s)
	stage(t, s, x2, b2)
	if _, err = s.ApplyP3Experiment(p3ctx, p2.ID, r.ID, true); !errors.Is(err, store.ErrConflict) {
		t.Fatal("receipt reused for different proposal")
	}
}

func TestP3CancelOrderingAndRestart(t *testing.T) {
	for _, order := range []string{"cancel_before_result", "cancel_after_stage", "cancel_after_apply"} {
		t.Run(order, func(t *testing.T) {
			s, path := database(t)
			x, p, b := task(t, s)
			if order != "cancel_before_result" {
				stage(t, s, x, b)
			}
			var applied store.P3Receipt
			if order == "cancel_after_apply" {
				var err error
				applied, err = s.ApplyP3Experiment(p3ctx, p.ID, "receipt", true)
				require(t, err)
			}
			require(t, s.CancelP3Experiment(p3ctx, x.Task, 1))
			require(t, s.Close())
			fresh, err := store.OpenP3Experiment(path)
			require(t, err)
			defer fresh.Close()
			if _, err = fresh.StageP3Experiment(p3ctx, x.Task, 1, b); !errors.Is(err, records.ErrP3Cancelled) {
				t.Fatal("late result accepted")
			}
			r, err := fresh.ApplyP3Experiment(p3ctx, p.ID, "receipt", true)
			if order == "cancel_after_apply" {
				require(t, err)
				if r != applied || count(t, fresh, "SELECT count(*) FROM versions") != 5 {
					t.Fatal("cancel erased committed fact")
				}
			} else {
				if err == nil || count(t, fresh, "SELECT count(*) FROM versions") != 2 {
					t.Fatal("cancel allowed late knowledge")
				}
			}
		})
	}
}

func TestP3ConcurrentCancelAndApply(t *testing.T) {
	s, _ := database(t)
	for i := 0; i < 20; i++ {
		x, p, b := task(t, s)
		stage(t, s, x, b)
		var applyErr, cancelErr error
		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, applyErr = s.ApplyP3Experiment(p3ctx, p.ID, uuid.NewString(), true)
		}()
		go func() { defer wg.Done(); <-start; cancelErr = s.CancelP3Experiment(p3ctx, x.Task, 1) }()
		close(start)
		wg.Wait()
		require(t, cancelErr)
		n := count(t, s, "SELECT count(*) FROM p3_experiment_receipts WHERE proposal=?", p.ID)
		if applyErr == nil {
			if n != 1 {
				t.Fatal("successful apply has no fact")
			}
		} else if !errors.Is(applyErr, records.ErrP3Cancelled) || n != 0 {
			t.Fatalf("race violated ordering: %v %d", applyErr, n)
		}
	}
}

func TestP2ApplyTransactionRegression(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p2.sqlite")
	s, err := store.Open(path)
	require(t, err)
	defer s.Close()
	if count(t, s, "SELECT count(*) FROM sqlite_master WHERE name LIKE 'p3_experiment_%'") != 0 {
		t.Fatal("P2 opened experimental schema")
	}
	m := store.Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: uuid.NewString(), Dataset: uuid.NewString(), Text: "Synthetic original"}
	v, err := s.Apply(p3ctx, m)
	require(t, err)
	again, err := s.Apply(p3ctx, m)
	require(t, err)
	if v.Revision != again.Revision {
		t.Fatal("P2 retry changed")
	}
	m.Text = "conflicting"
	if _, err = s.Apply(p3ctx, m); !errors.Is(err, store.ErrConflict) {
		t.Fatal("P2 operation conflict lost")
	}
	_, err = s.Apply(p3ctx, store.Mutation{Kind: "edit", OperationID: uuid.NewString(), Memory: m.Memory, Dataset: m.Dataset, ExpectedRevision: 1, Text: "Synthetic new"})
	require(t, err)
	_, err = s.Apply(p3ctx, store.Mutation{Kind: "restore", OperationID: uuid.NewString(), Memory: m.Memory, Dataset: m.Dataset, ExpectedRevision: 2, RestoreRevision: 1})
	require(t, err)
	current, err := s.Current(p3ctx, m.Memory)
	require(t, err)
	if current.Revision != 3 || current.Text != "Synthetic original" || count(t, s, "SELECT count(*) FROM versions") != 3 {
		t.Fatal("P2 restore changed history")
	}
}

func TestP3RealRustIndexRetry(t *testing.T) {
	config := os.Getenv("MEMLANE_P3_ENGINE_CONFIG")
	if config == "" {
		t.Skip("explicit real Rust worker config required; no fixture substituted")
	}
	var cfg engine.Config
	require(t, json.Unmarshal([]byte(config), &cfg))
	cfg.DataDir = filepath.Join(t.TempDir(), "synthetic-index")
	ctx, cancel := context.WithTimeout(p3ctx, 90*time.Second)
	defer cancel()
	worker, err := engine.Start(ctx, cfg)
	require(t, err)
	defer worker.Close()
	s, path := database(t)
	x, p, b := task(t, s)
	svc := memory.Service{Store: s, Engine: worker}
	for _, v := range x.Inputs {
		require(t, svc.IndexCurrent(ctx, v.Memory))
	}
	old, err := worker.Search(ctx, x.Dataset, "pnpm", 10)
	require(t, err)
	if len(old) < 2 {
		t.Fatal("real source candidates missing")
	}
	stage(t, s, x, b)
	r, err := s.ApplyP3Experiment(ctx, p.ID, "real-index-receipt", true)
	require(t, err)
	for _, candidate := range old {
		_, valid, err := s.Resolve(ctx, candidate)
		require(t, err)
		if valid {
			t.Fatal("archived source still searchable")
		}
	}
	svc.Engine = nil
	if svc.IndexCurrent(ctx, r.Memory) == nil {
		t.Fatal("engine unavailable not recorded")
	}
	v, err := s.Current(ctx, r.Memory)
	require(t, err)
	if v.IndexState != "failed" || v.Text != p.Text {
		t.Fatal("failed index lost body")
	}
	require(t, s.Close())
	fresh, err := store.OpenP3Experiment(path)
	require(t, err)
	defer fresh.Close()
	svc.Store = fresh
	retried, err := fresh.ApplyP3Experiment(ctx, p.ID, "real-index-receipt", true)
	require(t, err)
	if retried != r {
		t.Fatal("receipt not durable")
	}
	require(t, fresh.Requeue(ctx, r.Memory, 1))
	svc.Engine = worker
	require(t, svc.IndexCurrent(ctx, r.Memory))
	hits, err := svc.Search(ctx, []string{x.Dataset}, "pnpm lockfile dependencies", 10)
	require(t, err)
	if len(hits) != 1 || hits[0].Version.Memory != r.Memory || hits[0].Version.Text != p.Text || hits[0].Version.Revision != 1 {
		t.Fatalf("real search wrong: %+v", hits)
	}
	if count(t, fresh, "SELECT count(*) FROM activities") != 3 || count(t, fresh, "SELECT count(*) FROM p3_experiment_receipts") != 1 {
		t.Fatal("real retry duplicated activity/receipt")
	}
	t.Logf("real Rust generation=%s sources=2 archived=2 merged=%s revision=1 receipts=1 activities=3", worker.Generation, r.Memory)
}
