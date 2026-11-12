package store

// Synthetic API-shaped graph input verifies SQLite binding, projection and
// migration only. It is not evidence of official extraction or live Cognee.
import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func graphContractFixture(t *testing.T) (*Store, records.CogneeImport, records.CogneeBinding, records.CogneeGraph, records.SaveReceipt) {
	s, a, _ := cogneeImportFixture(t)
	ctx := context.Background()
	instance := uuid.NewString()
	a.Material = &records.CogneeOriginal{Instance: instance, Session: "synthetic-session", Kind: "qa", Sequence: 0, CapturedAt: "2026-11-08T00:00:00Z", OfficialState: "persisted"}
	raw, err := s.ImportCognee(ctx, a)
	mustMerge(t, err)
	k := cogneeKnowledge(a, raw)
	k.Material = &records.CogneeOriginal{Instance: instance, Session: "synthetic-session", Kind: "data", Sequence: 0, OfficialState: "persisted"}
	learned, err := s.ImportCognee(ctx, k)
	mustMerge(t, err)
	u, err := url.Parse(k.ExternalID)
	mustMerge(t, err)
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	b := records.CogneeBinding{Operation: uuid.NewString(), Instance: instance, Dataset: u.Host, Project: a.Project, Endpoint: "http://127.0.0.1:18099", ConnectionID: a.ConnectionID, Generation: a.Generation, Workspace: a.Workspace}
	_, err = s.BindCognee(ctx, b)
	mustMerge(t, err)
	first, second := uuid.NewString(), uuid.NewString()
	source := records.CogneeGraphSource{Document: parts[3], Chunk: uuid.NewString(), Quote: k.Text}
	g := records.CogneeGraph{Instance: b.Instance, Dataset: b.Dataset, CogneeVersion: "1.6.7", Entities: []records.CogneeEntity{{ID: first, Name: "Ticket"}, {ID: second, Name: "str"}}, Mentions: []records.CogneeMention{{Entity: first, CogneeGraphSource: source}, {Entity: second, CogneeGraphSource: source}}, Relations: []records.CogneeRelation{{ID: uuid.NewString(), Source: first, Target: second, Label: "accepts", Sources: []records.CogneeGraphSource{source}}}}
	return s, a, b, g, learned
}

func TestCogneeGraphSQLiteProjectionReplayAndSpecificSource(t *testing.T) {
	s, _, b, g, knowledge := graphContractFixture(t)
	ctx := context.Background()
	out, err := s.ProjectCogneeGraph(ctx, b, g)
	mustMerge(t, err)
	if out.State != "available" || len(out.Entities) != 2 || len(out.Relations) != 1 || out.Mentions[0].Memory != knowledge.Record.Memory || out.Mentions[0].Revision != 1 {
		t.Fatal("projection lost actual source version", out)
	}
	before := restoreTables(t, s.DB)
	_, err = s.BindCognee(ctx, b)
	mustMerge(t, err)
	g.Mentions = append(g.Mentions, g.Mentions[0])
	g.Relations = append(g.Relations, g.Relations[0])
	out, err = s.ProjectCogneeGraph(ctx, b, g)
	mustMerge(t, err)
	if len(out.Mentions) != 2 || len(out.Relations) != 1 || !reflect.DeepEqual(before, restoreTables(t, s.DB)) {
		t.Fatal("replay mutated or duplicated facts")
	}
	b.Endpoint = "http://127.0.0.1:18098"
	if _, err = s.BindCognee(ctx, b); err == nil {
		t.Fatal("binding operation changed identity")
	}
}

func TestCogneeGraphSQLiteInvalidEvidenceRevocationAndVersions(t *testing.T) {
	s, a, b, g, k := graphContractFixture(t)
	ctx := context.Background()
	wrong := g
	wrong.Instance = uuid.NewString()
	if _, err := s.ProjectCogneeGraph(ctx, b, wrong); err == nil {
		t.Fatal("different service accepted")
	}
	g.Relations[0].Sources[0].Quote = "unsupported relation quote"
	out, err := s.ProjectCogneeGraph(ctx, b, g)
	mustMerge(t, err)
	if len(out.Relations) != 0 {
		t.Fatal("unsupported relation inferred")
	}
	g.Mentions[0].Document = uuid.NewString()
	g.Mentions[1].Document = uuid.NewString()
	out, err = s.ProjectCogneeGraph(ctx, b, g)
	mustMerge(t, err)
	if len(out.Entities) != 0 {
		t.Fatal("unimported material exposed")
	}
	_, err = s.Apply(ctx, Mutation{OperationID: uuid.NewString(), Kind: "edit", Memory: k.Record.Memory, Dataset: a.Project, ExpectedRevision: 1, Text: "用户当前 v2"})
	mustMerge(t, err)
	out, err = s.ProjectCogneeGraph(ctx, b, g)
	mustMerge(t, err)
	if out.State != "empty" {
		t.Fatal("old official graph became current edited knowledge")
	}
	mustMerge(t, s.Disconnect(ctx, a.ConnectionID))
	if _, err = s.ProjectCogneeGraph(ctx, b, g); err == nil {
		t.Fatal("late graph crossed revoked connection")
	}
	q := records.SourceRead{Project: a.Project, Owner: k.Record.Memory, OwnerRevision: 2, Revision: 1}
	// The raw identity is recovered from the real frozen source table.
	refs, err := s.OrganizationSources(ctx, k.Record.Memory, 1)
	mustMerge(t, err)
	q.Memory = refs[0].Memory
	if _, err = s.ReadSource(ctx, q); err == nil {
		t.Fatal("raw reader crossed revoked official binding")
	}
}

func TestCogneeBrowsingSchema14WALBackupPreservesEveryOldTable(t *testing.T) {
	s, a, _ := cogneeImportFixture(t)
	_, err := s.ImportCognee(context.Background(), a)
	mustMerge(t, err)
	_, err = s.DB.Exec(`DROP TABLE official_code_scopes;DROP TABLE coding_memory_grants; DROP TABLE pi_capture_read_grants; DROP TABLE pi_capture_consents; DROP TABLE official_capture_receipts; DROP TABLE session_processing_tasks; DROP TABLE processing_validation_evidence; DROP TABLE delivery_assignments; DROP TABLE project_preference_decisions; DROP TABLE cognee_submissions; DROP TABLE cognee_submission_tasks; DROP TABLE cognee_library_identity; DROP TABLE processing_http_receipts; DROP TABLE processing_suppliers; DROP TABLE processing_validation_diagnostics; DROP TABLE index_rebuild_controls; DROP TABLE index_attempts; DROP TABLE agent_connection_batch_head; DROP TABLE agent_connection_batches; DROP TABLE organization_start_intents; DROP TABLE organization_previews; DROP TABLE organization_preview_head; DROP TABLE experience_previews; DROP TABLE experience_preview_head; DROP TABLE project_task_histories; DROP TABLE project_index_recoveries; DROP TABLE project_task_items; DROP TABLE project_task_sources; DROP TABLE project_task_runs; DROP TABLE project_tasks; DROP TABLE project_bindings; DROP TABLE processing_operations; DROP TABLE processing_validations; DROP TABLE processing_saved; DROP TABLE processing_current; DROP TABLE processing_revisions; DROP TABLE pending_cognee_bindings; DROP TABLE connection_intakes; DROP TABLE cognee_bindings; PRAGMA user_version=14; PRAGMA wal_autocheckpoint=0`)
	mustMerge(t, err)
	s.Schema = 14
	before := restoreTables(t, s.DB)
	var path string
	mustMerge(t, s.DB.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path))
	reader, err := sql.Open("sqlite", path)
	mustMerge(t, err)
	hold, err := reader.Begin()
	mustMerge(t, err)
	var count int
	mustMerge(t, hold.QueryRow(`SELECT count(*) FROM versions`).Scan(&count))
	mustMerge(t, s.Close())
	current, err := Open(path)
	mustMerge(t, err)
	defer current.Close()
	mustMerge(t, hold.Rollback())
	mustMerge(t, reader.Close())
	paths, err := filepath.Glob(filepath.Join(filepath.Dir(path), "backups/schema-v14-*.sqlite"))
	mustMerge(t, err)
	if len(paths) != 1 {
		t.Fatal("migration backup missing", paths)
	}
	backup, err := sql.Open("sqlite", paths[0])
	mustMerge(t, err)
	defer backup.Close()
	for table, rows := range before {
		if !reflect.DeepEqual(rows, sqlRows(t, current.DB, table)) || !reflect.DeepEqual(rows, sqlRows(t, backup, table)) {
			t.Fatal("migration changed old table", table)
		}
	}
	var version int
	mustMerge(t, backup.QueryRow(`PRAGMA user_version`).Scan(&version))
	if version != 14 || current.Schema != SchemaVersion {
		t.Fatal("backup/schema mismatch", version, current.Schema)
	}
}

func TestCogneeGraphExplicitTypesSurviveAuthorizedProjection(t *testing.T) {
	s, _, b, g, _ := graphContractFixture(t)
	g.Entities[0].Types = []string{"Package", "Tool"}
	out, err := s.ProjectCogneeGraph(context.Background(), b, g)
	mustMerge(t, err)
	found := false
	for _, entity := range out.Entities {
		if entity.ID == g.Entities[0].ID {
			found = reflect.DeepEqual(entity.Types, g.Entities[0].Types)
		}
	}
	if !found {
		t.Fatal("explicit graph types dropped from authorized entity")
	}
	g.Entities[0].Types = []string{" "}
	if _, err = s.ProjectCogneeGraph(context.Background(), b, g); err == nil {
		t.Fatal("invalid type label accepted")
	}
}
