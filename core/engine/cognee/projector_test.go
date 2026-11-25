package cognee

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zwhu/memlane/core/domain"
)

const (
	projectDS = "11111111-1111-4111-8111-111111111111"
	profileDS = "22222222-2222-4222-8222-222222222222"
	commonDS  = "33333333-3333-4333-8333-333333333333"
	unmanaged = "44444444-4444-4444-8444-444444444444"
	firstDoc  = "55555555-5555-4555-8555-555555555555"
	nextDoc   = "66666666-6666-4666-8666-666666666666"
	userOne   = "77777777-7777-4777-8777-777777777777"
	userTwo   = "88888888-8888-4888-8888-888888888888"
)

func testProjector(t *testing.T) *Projector {
	t.Helper()
	p := NewProjector(t.TempDir(), time.FixedZone("Asia/Shanghai", 8*60*60))
	if err := os.MkdirAll(filepath.Dir(p.database("cognee_db")), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func openProjectionTestDB(t *testing.T, path string, ddl string) *sql.DB {
	t.Helper()
	dsn := url.URL{Scheme: "file", Path: path}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	execProjectionTestSQL(t, db, ddl)
	return db
}

func openProjectionStore(t *testing.T) *domain.Store {
	t.Helper()
	store, err := domain.OpenStore(filepath.Join(t.TempDir(), "memlane.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store
}

func execProjectionTestSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func registerProjectionProject(t *testing.T, store *domain.Store, id, datasetID, name string) {
	t.Helper()
	if err := store.UpsertProject(context.Background(), domain.Project{ID: id, Name: name, DatasetID: datasetID}); err != nil {
		t.Fatal(err)
	}
}

func TestProjectorCatalogAndProvenance(t *testing.T) {
	ctx := context.Background()
	p, store := testProjector(t), openProjectionStore(t)
	src := openProjectionTestDB(t, p.database("cognee_db"), `
		CREATE TABLE datasets(id TEXT,name TEXT);
		CREATE TABLE data(id TEXT,dataset_id TEXT,name TEXT,system_metadata TEXT,external_metadata TEXT,node_set TEXT,
			pipeline_status TEXT,created_at DATETIME,updated_at DATETIME);
		CREATE TABLE session_records(dataset_id TEXT,session_id TEXT,user_id TEXT,status TEXT,started_at DATETIME,
			last_activity_at DATETIME, parent_session_id TEXT);
	`)
	registerProjectionProject(t, store, "project", projectDS, "project")
	registerProjectionProject(t, store, "profile-alias", profileDS, "alias")
	if err := store.UpsertCommonConfig(ctx, domain.CommonConfig{ID: "common", Name: "common", DatasetID: commonDS}); err != nil {
		t.Fatal(err)
	}
	for id, name := range map[string]string{projectDS: "project", profileDS: "ml-user-profiles", commonDS: "common", unmanaged: "unmanaged"} {
		execProjectionTestSQL(t, src, "INSERT INTO datasets VALUES(?,?)", uuidHex(id), name)
	}
	insertDoc := func(id, dataset, system, external, tags string) {
		t.Helper()
		const insert = `INSERT INTO data VALUES(?,?,?, ?,?,?, '{}','2026-11-16 20:08:09.123456789','invalid')`
		execProjectionTestSQL(t, src, insert, id, dataset, "document", system, external, tags)
	}
	insertDoc(uuidHex(firstDoc), uuidHex(projectDS), `{"lesson_type":"state","state_key":"stable","first_source_date":"2026-10-01","source_qa_ids":["qa"]}`,
		`{"lesson_type":"pitfall","first_source_date":"2025-01-01"}`, `"[\"session_learnings\",\"session_learnings:root\"]"`)
	insertDoc("protected", profileDS, "", "", "[]")
	insertDoc("raw-profile", projectDS, "{broken", `{"memlane_kind":"user_profile","node_set":["memory"]}`, "")
	insertDoc("common-doc", uuidHex(commonDS), "", `{"memlane_kind":"common_knowledge","category":"preference"}`, "")
	insertDoc("orphan-document", unmanaged, "", "", "")
	insertDoc("derived", projectDS, "", "", `["user_sessions_from_cache"]`)
	execProjectionTestSQL(t, src, "INSERT INTO session_records VALUES(?,?,?,?,?,?,?)", uuidHex(projectDS), "root", uuidHex(userOne),
		"active", "2026-11-16 10:00:00", "2026-11-16 10:15:00", "")
	execProjectionTestSQL(t, src, "INSERT INTO session_records VALUES(?,?,?,?,?,?,?)", projectDS, "child", userTwo,
		"closed", "2026-11-16 10:00:00", "invalid", "root")
	if _, err := store.RecordRecallEvent(ctx, domain.RecallEventInput{AgentID: "agent", EventID: "event", Channel: "explicit",
		Hits: []domain.RecallHit{{DatasetID: uuidHex(projectDS), DocumentID: uuidHex(firstDoc)}}}); err != nil {
		t.Fatal(err)
	}

	discovered, err := p.DiscoverDatasets(ctx)
	if err != nil || len(discovered) != 4 || discovered[1] != (DatasetInfo{ID: profileDS, Name: "ml-user-profiles", Protected: true}) {
		t.Fatalf("dataset discovery = %+v, %v", discovered, err)
	}
	stats, err := p.SyncCatalog(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Datasets != 3 || stats.DocsProjected != 4 || stats.KnowledgeCount != 1 || stats.CommonCount != 1 ||
		stats.RawDocCount != 2 || stats.PendingRawCount != 0 || stats.SessionsProjected != 2 || stats.DistinctSessionUsers != 2 || stats.ProvenanceLocked != 1 {
		t.Fatalf("sync stats = %+v", stats)
	}
	knowledge := projectionDocs(t, store, projectDS, "knowledge")
	if len(knowledge) != 1 {
		t.Fatalf("knowledge = %+v", knowledge)
	}
	d := knowledge[0]
	if d.DocID != firstDoc || d.LessonType != "state" || d.Category != "项目状态" || d.SessionID != "root" ||
		d.CreatedAtNS != "2026-11-16T20:08:09.123456789Z" || d.UpdatedAtNS != "" || d.EffectiveDate != "2026-10-01" ||
		d.EffectiveDateSource != "provenance" || d.DayDescKey != -20261001 || d.RecallCount != 1 || d.RecallDescKey != -1 {
		t.Fatalf("projected lesson = %+v", d)
	}
	for _, id := range []string{profileDS, projectDS} {
		raw := projectionDocs(t, store, id, "raw_doc")
		if len(raw) != 1 || raw[0].PipelineStatus != "not_applicable" || raw[0].EffectiveDate != "2026-11-17" {
			t.Fatalf("profile raw = %+v", raw)
		}
	}
	sessions, err := store.ListCatalogSessions(ctx, projectDS, 10)
	if err != nil || len(sessions) != 2 || sessions[0].UserID != userOne || sessions[1].UserID != userTwo ||
		sessions[1].ParentSessionID != "root" || sessions[1].IsTopLevel || sessions[1].SortTSDescKey == 0 {
		t.Fatalf("sessions = %+v, %v", sessions, err)
	}
	provenance, err := store.GetDocumentProvenance(ctx, projectDS, firstDoc)
	if err != nil {
		t.Fatal(err)
	}
	stats, err = p.SyncCatalog(ctx, store)
	if err != nil || stats.ProvenanceLocked != 0 {
		t.Fatalf("unchanged catalog sync = %+v, %v", stats, err)
	}
	knowledge = projectionDocs(t, store, projectDS, "knowledge")
	if len(knowledge) != 1 || knowledge[0].EffectiveDate != d.EffectiveDate || knowledge[0].EffectiveDateSource != "provenance" {
		t.Fatalf("unchanged catalog knowledge = %+v", knowledge)
	}
	if got, err := store.GetDocumentProvenance(ctx, projectDS, firstDoc); err != nil || got.SyncSeq != provenance.SyncSeq {
		t.Fatalf("unchanged document provenance = %+v, %v", got, err)
	}

	execProjectionTestSQL(t, src, "DELETE FROM data WHERE id IN (?,?)", uuidHex(firstDoc), "protected")
	execProjectionTestSQL(t, src, "DELETE FROM datasets WHERE id=?", uuidHex(profileDS))
	insertDoc(nextDoc, projectDS, `{"lesson_type":"state","state_key":"stable","session_date":"2026-11-15"}`, "", `["session_learnings"]`)
	if _, err := p.SyncCatalog(ctx, store); err != nil {
		t.Fatal(err)
	}
	knowledge = projectionDocs(t, store, projectDS, "knowledge")
	if len(knowledge) != 1 || knowledge[0].DocID != nextDoc || knowledge[0].EffectiveDate != "2026-10-01" || knowledge[0].DocSeq <= d.DocSeq {
		t.Fatalf("replacement knowledge = %+v", knowledge)
	}
	if _, err := store.GetCatalogSyncState(ctx, profileDS); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted dataset checkpoint: %v", err)
	}
	if err := store.DeleteProject(ctx, "project"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.SyncCatalog(ctx, store); err != nil {
		t.Fatal(err)
	}
	if docs := projectionDocs(t, store, projectDS, "knowledge"); len(docs) != 0 {
		t.Fatalf("stale docs = %+v", docs)
	}
	if sessions, err := store.ListCatalogSessions(ctx, projectDS, 10); err != nil || len(sessions) != 0 {
		t.Fatalf("stale sessions = %+v, %v", sessions, err)
	}
}

func projectionDocs(t *testing.T, store *domain.Store, dataset, kind string) []domain.CatalogDoc {
	t.Helper()
	docs, err := store.ListCatalogDocs(context.Background(), dataset, kind, 100)
	if err != nil {
		t.Fatal(err)
	}
	return docs
}

func TestProjectorCacheUserScopeAndExpiration(t *testing.T) {
	p := testProjector(t)
	src := openProjectionTestDB(t, p.database("cache.db"), `
		CREATE TABLE cache_qa_entries(seq INTEGER PRIMARY KEY,user_id TEXT,session_id TEXT,payload TEXT,expires_at DATETIME);
		CREATE TABLE cache_trace_entries(seq INTEGER PRIMARY KEY,user_id TEXT,session_id TEXT,expires_at DATETIME);
	`)
	qa := "INSERT INTO cache_qa_entries(user_id,session_id,payload,expires_at) VALUES(?,?,?,?)"
	trace := "INSERT INTO cache_trace_entries(user_id,session_id,expires_at) VALUES(?,?,?)"
	execProjectionTestSQL(t, src, qa, userOne, "shared", `{"question":"expired label"}`, "2000-01-01T00:00:00")
	execProjectionTestSQL(t, src, qa, uuidHex(userOne), "shared", `{"question":"   "}`, nil)
	execProjectionTestSQL(t, src, qa, userTwo, "shared", `{"question":"user two"}`, nil)
	execProjectionTestSQL(t, src, qa, userOne, "shared", `{"question":" user one "}`, "2199-01-01 00:00:00")
	execProjectionTestSQL(t, src, trace, userOne, "shared", "2000-01-01 00:00:00")
	execProjectionTestSQL(t, src, trace, uuidHex(userOne), "shared", nil)
	execProjectionTestSQL(t, src, trace, userTwo, "shared", nil)
	first := SessionRef{UserID: userOne, SessionID: "shared"}
	second := SessionRef{UserID: userTwo, SessionID: "shared"}
	missing := SessionRef{UserID: userOne, SessionID: "missing"}
	got, err := p.HydrateSessionsFromCache(context.Background(), []SessionRef{first, second, missing, first})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[first] != (SessionCacheInfo{"user one", 2, 1}) ||
		got[second] != (SessionCacheInfo{"user two", 1, 1}) || got[missing] != (SessionCacheInfo{}) {
		t.Fatalf("cache hydrated = %+v", got)
	}
}

func TestProjectorReadOnlyAndOptionalTables(t *testing.T) {
	ctx := context.Background()
	p, store := testProjector(t), openProjectionStore(t)
	realDB := filepath.Join(t.TempDir(), "with?#url")
	openProjectionTestDB(t, realDB, "CREATE TABLE data(id TEXT,dataset_id TEXT); INSERT INTO data VALUES('incomplete', 'dataset');")
	if err := os.Symlink(realDB, p.database("cognee_db")); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DiscoverDatasets(ctx); err == nil {
		t.Fatal("symlink was accepted")
	}
	if err := os.Remove(p.database("cognee_db")); err != nil {
		t.Fatal(err)
	}
	if err := copyProjectionFile(realDB, p.database("cognee_db")); err != nil {
		t.Fatal(err)
	}
	db, err := openOfficialSQLiteRO(ctx, realDB)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var queryOnly, busyMS, cachePages int
	for i, result := range []*int{&queryOnly, &busyMS, &cachePages} {
		pragma := []string{"query_only", "busy_timeout", "cache_size"}[i]
		if err := db.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(result); err != nil {
			t.Fatal(err)
		}
	}
	if queryOnly != 1 || busyMS != 2000 || cachePages != -512 || db.Stats().MaxOpenConnections != 1 {
		t.Fatalf("read-only pragmas = %d, %d, %d, max connections = %d", queryOnly, busyMS, cachePages, db.Stats().MaxOpenConnections)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM data"); err == nil {
		t.Fatal("read-only mutation succeeded")
	}
	datasets, err := p.DiscoverDatasets(ctx)
	if err != nil || len(datasets) != 0 {
		t.Fatalf("optional datasets discovery = %+v, %v", datasets, err)
	}
	registerProjectionProject(t, store, "optional", "dataset", "optional")
	stats, err := p.SyncCatalog(ctx, store)
	if err != nil || stats.PendingRawCount != 1 || stats.Datasets != 1 {
		t.Fatalf("optional tables sync = %+v, %v", stats, err)
	}
}

func TestCogneeProjectorShadowClone(t *testing.T) {
	ctx := context.Background()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	src := ""
	for base := cwd; ; base = filepath.Dir(base) {
		candidate := filepath.Join(base, ".local", "daily", "official-store", "system", "databases")
		info, err := os.Lstat(filepath.Join(candidate, "cognee_db"))
		if err == nil && info.Mode().IsRegular() {
			src = candidate
			break
		}
		if base == filepath.Dir(base) {
			break
		}
	}
	if src == "" {
		t.Skip("daily official store is unavailable")
	}
	store := openProjectionStore(t)
	var p *Projector
	for attempt := 0; attempt < 3; attempt++ {
		p = testProjector(t)
		stable, err := copyProjectionSnapshot(src, p)
		if err == nil && stable {
			break
		}
		if attempt == 2 {
			t.Fatalf("daily SQLite files changed during shadow copy: %v", err)
		}
	}
	datasets, err := p.DiscoverDatasets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i, dataset := range datasets {
		if dataset.Name == "" {
			dataset.Name = dataset.ID
		}
		registerProjectionProject(t, store, fmt.Sprintf("shadow-%d", i), dataset.ID, dataset.Name)
	}
	stats, err := p.SyncCatalog(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("shadow projection %+v", stats)
	if stats.DocsProjected == 0 || stats.SessionsProjected == 0 || stats.DistinctSessionUsers <= 1 {
		t.Fatalf("shadow projection counts = %+v", stats)
	}
	protectedRows := 0
	for _, dataset := range datasets {
		if !dataset.Protected {
			continue
		}
		var total, invalid int
		err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*),
			COALESCE(SUM(pipeline_status<>'not_applicable'),0) FROM catalog_docs WHERE dataset_id=?`, dataset.ID).Scan(&total, &invalid)
		if err != nil || invalid != 0 {
			t.Fatalf("protected docs total %d, invalid %d: %v", total, invalid, err)
		}
		protectedRows += total
	}
	if protectedRows == 0 {
		t.Fatal("shadow projection contains no protected profile documents")
	}
	t.Logf("protected profiles: %d projected, 0 applicable to processing", protectedRows)
}

func copyProjectionSnapshot(src string, p *Projector) (bool, error) {
	names := []string{"cognee_db", "cognee_db-wal", "cognee_db-shm", "cache.db", "cache.db-wal", "cache.db-shm"}
	before := make([]os.FileInfo, len(names))
	for i, name := range names {
		info, err := os.Lstat(filepath.Join(src, name))
		if errors.Is(err, os.ErrNotExist) && (i%3 != 0) {
			continue
		}
		if err != nil {
			return false, err
		}
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("shadow source is not a regular file: %s", name)
		}
		before[i] = info
	}
	for i, name := range names {
		if before[i] != nil {
			if err := copyProjectionFile(filepath.Join(src, name), p.database(name)); err != nil {
				return false, err
			}
		}
	}
	for i, name := range names {
		info, err := os.Lstat(filepath.Join(src, name))
		if before[i] == nil && errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
		if before[i] == nil || !os.SameFile(before[i], info) || before[i].Size() != info.Size() ||
			!before[i].ModTime().Equal(info.ModTime()) {
			return false, fmt.Errorf("%s changed while copying", name)
		}
	}
	return true, nil
}

func copyProjectionFile(src, dst string) error {
	input, err := os.Open(src)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(output, input)
	return errors.Join(err, output.Close())
}

func TestProjectorReadableTitles(t *testing.T) {
	t.Setenv("FILE_ENCRYPTION_ENABLED", "")
	ctx := t.Context()
	p, store := testProjector(t), openProjectionStore(t)
	src := openProjectionTestDB(t, p.database("cognee_db"), `
		CREATE TABLE datasets(id TEXT,name TEXT);
		CREATE TABLE data(id TEXT,dataset_id TEXT,name TEXT,label TEXT,raw_data_location TEXT,content_hash TEXT,
			system_metadata TEXT,external_metadata TEXT,node_set TEXT);
	`)
	registerProjectionProject(t, store, "project", projectDS, "Project")
	execProjectionTestSQL(t, src, "INSERT INTO datasets VALUES(?,?)", projectDS, "Project")
	const machine = "text_1234567890abcdef1234567890abcdef.txt"
	data := filepath.Join(p.storePath, "data")
	mkdirBodyDir(t, data)
	wrapper := filepath.Join(data, "wrapper.md")
	writeBody(t, wrapper, "Lesson-Type: state\nState-Key: build\n\n# Session learning (session abc)\n"+
		"\n````markdown\n# Inside a fence\n```\n````\n~~~~\n# Also inside a fence\n~~~~\n"+
		"    Indented code\n![Diagram](chart.png)\n# Real Heading\nDetails")
	writeBody(t, filepath.Join(data, "summary.md"), "> - "+strings.Repeat("字", 81))
	tests := []struct{ id, name, label, location, system, external, want string }{
		{"metadata", machine, machine, "", `{"title":"#  Trusted  title"}`, `{"title":"External title"}`, "Trusted title"},
		{"wrapper", machine, "", "file:///old/official-store/data/wrapper.md", "", `{"title":"` + firstDoc + `"}`, "Real Heading"},
		{"summary", "memory-1234567890abcdef1234567890abcdef.txt", "", "summary.md", "", "", strings.Repeat("字", 79) + "…"},
		{"named", "Existing name.md", "", "wrapper.md", "", "", ""},
		{"labeled", machine, "Handwritten title", "wrapper.md", "", `{"title":"A different title"}`, "Handwritten title"},
	}
	for _, tc := range tests {
		execProjectionTestSQL(t, src, `INSERT INTO data VALUES(?,?,?,?,?,'hash',?,?,'["memory"]')`,
			tc.id, projectDS, tc.name, tc.label, tc.location, tc.system, tc.external)
	}
	check := func() {
		t.Helper()
		if _, err := p.SyncCatalog(ctx, store); err != nil {
			t.Fatal(err)
		}
		docs := projectionDocs(t, store, projectDS, "knowledge")
		if len(docs) != len(tests) {
			t.Fatalf("projected %d titles, want %d", len(docs), len(tests))
		}
		for _, d := range docs {
			for _, tc := range tests {
				if d.DocID == tc.id && (d.DocName != tc.name || d.DocLabel != tc.want) {
					t.Errorf("%s: name=%q label=%q, want %q and %q", tc.id, d.DocName, d.DocLabel, tc.name, tc.want)
				}
			}
		}
	}
	check()

	// An unchanged hash and location must reuse the published label even without the body.
	if err := os.Remove(wrapper); err != nil {
		t.Fatal(err)
	}
	check()

	writeBody(t, wrapper, "# Updated title")
	execProjectionTestSQL(t, src, "UPDATE data SET content_hash='changed' WHERE id='wrapper'")
	tests[1].want = "Updated title"
	check()
}
