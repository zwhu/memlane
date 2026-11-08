package store

// These are real SQLite/Git contract and fault checks with synthetic API
// inputs. Official Agent capture, Cognee processing and Rust/App acceptance
// are independently evidenced by the integration validation environment.
import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func cogneeImportFixture(t *testing.T) (*Store, records.CogneeImport, records.CogneeImport) {
	t.Helper()
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "memory.sqlite"))
	mustMerge(t, err)
	t.Cleanup(func() { s.Close() })
	makeInput := func(name string) records.CogneeImport {
		repo := filepath.Join(t.TempDir(), name)
		out, err := exec.Command("git", "init", repo).CombinedOutput()
		if err != nil {
			t.Fatal(err, string(out))
		}
		p, err := s.RegisterProject(ctx, records.ScopeDraft{Name: name, Path: repo})
		mustMerge(t, err)
		c, err := s.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "codex", Name: name, Project: p.ID, AllowedScopes: []string{p.ID}})
		mustMerge(t, err)
		return records.CogneeImport{ConnectionID: c.ID, Generation: c.Generation, Workspace: repo, Project: p.ID, ExternalID: "cognee://" + uuid.NewString() + "/sessions/synthetic-session/qa/" + uuid.NewString(), Origin: "raw", Title: "synthetic original", Text: "原始约定：Ticket 仅接受 str。", Fragment: "原始约定：Ticket 仅接受 str。"}
	}
	return s, makeInput("scope-a"), makeInput("scope-b")
}

func cogneeImportCounts(t *testing.T, s *Store) []int {
	t.Helper()
	out := []int{}
	for _, table := range []string{"memories", "versions", "operations", "jobs", "activities", "organization_sources", "derived_memories"} {
		var n int
		mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM `+table).Scan(&n))
		out = append(out, n)
	}
	return out
}

func cogneeKnowledge(d records.CogneeImport, source records.SaveReceipt) records.CogneeImport {
	d.ExternalID = strings.Split(d.ExternalID, "/qa/")[0] + "/data/" + uuid.NewString()
	d.Origin, d.Title, d.Text = "knowledge", "synthetic learned document", "Ticket 只接受字符串。"
	d.Sources = []records.MergeSource{{Memory: source.Record.Memory, Revision: 1, Quote: "Ticket 仅接受 str"}}
	return d
}

func TestCogneeImportActualSQLiteReplayCurrentVersionAndEvidenceRoots(t *testing.T) {
	s, a, _ := cogneeImportFixture(t)
	ctx := context.Background()
	raw, err := s.ImportCognee(ctx, a)
	mustMerge(t, err)
	k := cogneeKnowledge(a, raw)
	learned, err := s.ImportCognee(ctx, k)
	mustMerge(t, err)
	if !learned.Persisted || learned.SavedRevision != 1 || learned.Record.Derived == nil || learned.Record.Derived.State != "valid" {
		t.Fatal(learned)
	}
	before := cogneeImportCounts(t, s)
	replay, err := s.ImportCognee(ctx, k)
	mustMerge(t, err)
	if replay.Record.Memory != learned.Record.Memory || !reflect.DeepEqual(before, cogneeImportCounts(t, s)) {
		t.Fatal("replay duplicated durable facts")
	}
	sources, err := s.OrganizationSources(ctx, learned.Record.Memory, 1)
	mustMerge(t, err)
	if len(sources) != 1 || sources[0].Memory != raw.Record.Memory || sources[0].Input.Origin != "raw" {
		t.Fatal(sources)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	mustMerge(t, err)
	roots, err := s.evidenceRoots(ctx, tx, learned.Record.Memory, 1)
	mustMerge(t, err)
	mustMerge(t, tx.Rollback())
	if !reflect.DeepEqual(roots, []records.MergeTarget{{Memory: raw.Record.Memory, Revision: 1}}) {
		t.Fatal("learned document invented an independent origin", roots)
	}
	k.Text = "same ID, conflicting prose"
	if _, err = s.ImportCognee(ctx, k); !errors.Is(err, ErrConflict) {
		t.Fatal("different payload should conflict", err)
	}
	_, err = s.Apply(ctx, Mutation{OperationID: uuid.NewString(), Kind: "edit", Dataset: a.Project, Memory: raw.Record.Memory, ExpectedRevision: 1, Text: "用户修订的实际原始记录。"})
	mustMerge(t, err)
	rawReplay, err := s.ImportCognee(ctx, a)
	mustMerge(t, err)
	if rawReplay.SavedRevision != 1 || rawReplay.Record.Revision != 2 || rawReplay.Record.Text != "用户修订的实际原始记录。" {
		t.Fatal("replay rewrote user version", rawReplay)
	}
	k.Text = "Ticket 只接受字符串。"
	replay, err = s.ImportCognee(ctx, k)
	mustMerge(t, err)
	if replay.Record.Derived.State != "stale" {
		t.Fatal("source change did not invalidate import", replay)
	}
}

func TestCogneeImportCrossScopeOldSourcesDisconnectAndAtomicFailure(t *testing.T) {
	s, a, b := cogneeImportFixture(t)
	ctx := context.Background()
	// A free-form Agent citation is not a trusted external-dataset binding.
	_, err := s.Apply(ctx, Mutation{OperationID: uuid.NewString(), Kind: "save", Dataset: b.Project, Memory: uuid.NewString(), Text: "ordinary Agent citation", Origin: "raw", Source: "codex", SourceDetail: &records.AgentSource{References: []string{a.ExternalID}}})
	mustMerge(t, err)
	ra, err := s.ImportCognee(ctx, a)
	mustMerge(t, err)
	rb, err := s.ImportCognee(ctx, b)
	mustMerge(t, err)
	k := cogneeKnowledge(a, rb)
	before := cogneeImportCounts(t, s)
	wrong := b
	wrong.ExternalID = strings.Split(a.ExternalID, "/sessions/")[0] + "/sessions/synthetic-session/qa/" + uuid.NewString()
	if _, err = s.ImportCognee(ctx, wrong); err == nil || !reflect.DeepEqual(before, cogneeImportCounts(t, s)) {
		t.Fatal("external dataset was silently rebound to another project", err)
	}
	if _, err = s.ImportCognee(ctx, k); err == nil || !reflect.DeepEqual(before, cogneeImportCounts(t, s)) {
		t.Fatal("cross-project source changed facts", err)
	}
	k = cogneeKnowledge(a, ra)
	foreignSession := k
	foreignSession.ExternalID = strings.Replace(k.ExternalID, "/synthetic-session/", "/unrelated-session/", 1)
	if _, err = s.ImportCognee(ctx, foreignSession); err == nil || !reflect.DeepEqual(before, cogneeImportCounts(t, s)) {
		t.Fatal("unrelated Cognee session used as knowledge provenance", err)
	}
	foreignDataset := k
	foreignDataset.ExternalID = "cognee://" + uuid.NewString() + "/sessions/synthetic-session/data/" + uuid.NewString()
	if _, err = s.ImportCognee(ctx, foreignDataset); err == nil || !reflect.DeepEqual(before, cogneeImportCounts(t, s)) {
		t.Fatal("unrelated Cognee dataset used as knowledge provenance", err)
	}
	ordinary, err := s.Apply(ctx, Mutation{OperationID: uuid.NewString(), Kind: "save", Dataset: a.Project, Memory: uuid.NewString(), Text: a.Text, Origin: "raw", Source: "codex", SourceDetail: &records.AgentSource{References: []string{a.ExternalID}}})
	mustMerge(t, err)
	ordinarySource := k
	ordinarySource.Sources = []records.MergeSource{{Memory: ordinary.Memory, Revision: 1, Quote: "Ticket 仅接受 str"}}
	before = cogneeImportCounts(t, s)
	if _, err = s.ImportCognee(ctx, ordinarySource); err == nil || !reflect.DeepEqual(before, cogneeImportCounts(t, s)) {
		t.Fatal("ordinary Agent citation became official Cognee provenance", err)
	}
	_, err = s.DB.Exec(`CREATE TRIGGER synthetic_import_fault BEFORE INSERT ON organization_sources BEGIN SELECT RAISE(FAIL,'synthetic import fault'); END`)
	mustMerge(t, err)
	if _, err = s.ImportCognee(ctx, k); err == nil || !reflect.DeepEqual(before, cogneeImportCounts(t, s)) {
		t.Fatal("source failure left knowledge/version/job/operation behind", err)
	}
	_, err = s.DB.Exec(`DROP TRIGGER synthetic_import_fault`)
	mustMerge(t, err)
	_, err = s.Apply(ctx, Mutation{OperationID: uuid.NewString(), Kind: "edit", Dataset: a.Project, Memory: ra.Record.Memory, ExpectedRevision: 1, Text: "新来源版本"})
	mustMerge(t, err)
	before = cogneeImportCounts(t, s)
	if _, err = s.ImportCognee(ctx, k); err == nil || !reflect.DeepEqual(before, cogneeImportCounts(t, s)) {
		t.Fatal("old source revision accepted", err)
	}
	mustMerge(t, s.Disconnect(ctx, a.ConnectionID))
	a.ExternalID = "cognee://" + uuid.NewString() + "/sessions/synthetic-session/qa/" + uuid.NewString()
	before = cogneeImportCounts(t, s)
	if _, err = s.ImportCognee(ctx, a); err == nil || !reflect.DeepEqual(before, cogneeImportCounts(t, s)) {
		t.Fatal("late result crossed disconnected authorization", err)
	}
}
