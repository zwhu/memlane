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

	"github.com/zwhu/memlane/core/engine"
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

func execProjectionTestSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func findDatasetSnapshot(t *testing.T, snap *engine.CatalogSnapshot, datasetID string) engine.CatalogDatasetSnapshot {
	t.Helper()
	for _, ds := range snap.Datasets {
		if ds.DatasetID == datasetID {
			return ds
		}
	}
	t.Fatalf("dataset %s not found in snapshot", datasetID)
	return engine.CatalogDatasetSnapshot{}
}

func TestProjectorSnapshotCatalogAndProvenance(t *testing.T) {
	ctx := context.Background()
	p := testProjector(t)
	src := openProjectionTestDB(t, p.database("cognee_db"), `
		CREATE TABLE datasets(id TEXT,name TEXT);
		CREATE TABLE data(id TEXT,dataset_id TEXT,name TEXT,system_metadata TEXT,external_metadata TEXT,node_set TEXT,
			pipeline_status TEXT,created_at DATETIME,updated_at DATETIME);
		CREATE TABLE session_records(dataset_id TEXT,session_id TEXT,user_id TEXT,status TEXT,started_at DATETIME,
			last_activity_at DATETIME, parent_session_id TEXT);
	`)
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

	discovered, err := p.DiscoverDatasets(ctx)
	if err != nil || len(discovered) != 4 || discovered[1] != (DatasetInfo{ID: profileDS, Name: "ml-user-profiles", Protected: true}) {
		t.Fatalf("dataset discovery = %+v, %v", discovered, err)
	}

	targets := []engine.CatalogDatasetTarget{
		{DatasetID: projectDS},
		{DatasetID: profileDS},
		{DatasetID: commonDS},
	}
	snap, err := p.SnapshotCatalog(ctx, targets, engine.CatalogSnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Datasets) != 3 {
		t.Fatalf("snapshot datasets = %d, want 3", len(snap.Datasets))
	}

	projSnap := findDatasetSnapshot(t, snap, projectDS)
	if !projSnap.Exists || len(projSnap.Docs) != 2 || len(projSnap.Sessions) != 2 {
		t.Fatalf("project snapshot = %+v", projSnap)
	}
	var lesson engine.CatalogSnapshotDoc
	var rawProfile engine.CatalogSnapshotDoc
	for _, d := range projSnap.Docs {
		switch d.DocID {
		case firstDoc:
			lesson = d
		case "raw-profile":
			rawProfile = d
		}
	}
	if lesson.DocID != firstDoc || lesson.LessonType != "state" || lesson.StateKey != "stable" ||
		lesson.Category != "项目状态" || lesson.SessionID != "root" ||
		lesson.CreatedAtNS != "2026-11-16T20:08:09.123456789Z" || lesson.UpdatedAtNS != "" ||
		lesson.EffectiveDate != "2026-10-01" || lesson.DayDescKey != -20261001 ||
		lesson.Provenance == nil || lesson.Provenance.SourceDateStatus != "linked_qa" {
		t.Fatalf("projected lesson = %+v", lesson)
	}
	if rawProfile.PipelineStatus != "not_applicable" || rawProfile.EffectiveDate != "2026-11-17" {
		t.Fatalf("raw profile doc = %+v", rawProfile)
	}

	profSnap := findDatasetSnapshot(t, snap, profileDS)
	if !profSnap.Exists || len(profSnap.Docs) != 1 || profSnap.Docs[0].PipelineStatus != "not_applicable" {
		t.Fatalf("profile snapshot = %+v", profSnap)
	}

	commonSnap := findDatasetSnapshot(t, snap, commonDS)
	if !commonSnap.Exists || len(commonSnap.Docs) != 1 || commonSnap.Docs[0].Kind != "common" || commonSnap.Docs[0].CommonCategory != "preference" {
		t.Fatalf("common snapshot = %+v", commonSnap)
	}

	// SessionsOnly must skip reading docs while returning sessions.
	sessionsSnap, err := p.SnapshotCatalog(ctx, []engine.CatalogDatasetTarget{{DatasetID: projectDS}}, engine.CatalogSnapshotOptions{SessionsOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessionsSnap.Datasets) != 1 || len(sessionsSnap.Datasets[0].Docs) != 0 || len(sessionsSnap.Datasets[0].Sessions) != 2 {
		t.Fatalf("sessions-only snapshot = %+v", sessionsSnap)
	}
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
	if len(got) != 3 || got[first] != (SessionCacheInfo{Label: "user one", MsgCount: 2, ToolCalls: 1}) ||
		got[second] != (SessionCacheInfo{Label: "user two", MsgCount: 1, ToolCalls: 1}) || got[missing] != (SessionCacheInfo{}) {
		t.Fatalf("cache hydrated = %+v", got)
	}
}

func TestProjectorReadOnlyAndOptionalTables(t *testing.T) {
	ctx := context.Background()
	p := testProjector(t)
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
	snap, err := p.SnapshotCatalog(ctx, []engine.CatalogDatasetTarget{{DatasetID: "dataset"}}, engine.CatalogSnapshotOptions{})
	if err != nil || len(snap.Datasets) != 1 || len(snap.Datasets[0].Docs) != 1 {
		t.Fatalf("optional tables snapshot = %+v, %v", snap, err)
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
	targets := make([]engine.CatalogDatasetTarget, 0, len(datasets))
	protectedSet := map[string]bool{}
	for _, dataset := range datasets {
		targets = append(targets, engine.CatalogDatasetTarget{DatasetID: dataset.ID, Protected: dataset.Protected})
		if dataset.Protected {
			protectedSet[dataset.ID] = true
		}
	}
	snap, err := p.SnapshotCatalog(ctx, targets, engine.CatalogSnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var docsProjected, sessionsProjected, protectedRows int
	users := map[string]struct{}{}
	for _, ds := range snap.Datasets {
		docsProjected += len(ds.Docs)
		sessionsProjected += len(ds.Sessions)
		for _, s := range ds.Sessions {
			if s.UserID != "" {
				users[s.UserID] = struct{}{}
			}
		}
		if protectedSet[ds.DatasetID] {
			for _, doc := range ds.Docs {
				if doc.PipelineStatus != "not_applicable" {
					t.Fatalf("protected doc %s has pipeline_status=%q", doc.DocID, doc.PipelineStatus)
				}
				protectedRows++
			}
		}
	}
	if docsProjected == 0 || sessionsProjected == 0 || len(users) <= 1 {
		t.Fatalf("shadow snapshot counts docs=%d sessions=%d users=%d", docsProjected, sessionsProjected, len(users))
	}
	if protectedRows == 0 {
		t.Fatal("shadow projection contains no protected profile documents")
	}
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
	p := testProjector(t)
	src := openProjectionTestDB(t, p.database("cognee_db"), `
		CREATE TABLE datasets(id TEXT,name TEXT);
		CREATE TABLE data(id TEXT,dataset_id TEXT,name TEXT,label TEXT,raw_data_location TEXT,content_hash TEXT,
			system_metadata TEXT,external_metadata TEXT,node_set TEXT);
	`)
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
	hints := map[engine.CatalogDocKey]engine.CatalogTitleHint{}
	check := func() {
		t.Helper()
		snap, err := p.SnapshotCatalog(ctx, []engine.CatalogDatasetTarget{{DatasetID: projectDS}}, engine.CatalogSnapshotOptions{TitleHints: hints})
		if err != nil {
			t.Fatal(err)
		}
		docs := findDatasetSnapshot(t, snap, projectDS).Docs
		if len(docs) != len(tests) {
			t.Fatalf("projected %d titles, want %d", len(docs), len(tests))
		}
		hints = map[engine.CatalogDocKey]engine.CatalogTitleHint{}
		for _, d := range docs {
			if d.DocLabel != "" {
				hints[engine.CatalogDocKey{DatasetID: d.DatasetID, DocID: d.DocID}] = engine.CatalogTitleHint{
					DocLabel:        d.DocLabel,
					ContentHash:     d.ContentHash,
					RawDataLocation: d.RawDataLocation,
				}
			}
			for _, tc := range tests {
				if d.DocID == tc.id && (d.DocName != tc.name || d.DocLabel != tc.want) {
					t.Errorf("%s: name=%q label=%q, want %q and %q", tc.id, d.DocName, d.DocLabel, tc.name, tc.want)
				}
			}
		}
	}
	check()

	// An unchanged hash and location must reuse the hinted label even without the body.
	if err := os.Remove(wrapper); err != nil {
		t.Fatal(err)
	}
	check()

	writeBody(t, wrapper, "# Updated title")
	execProjectionTestSQL(t, src, "UPDATE data SET content_hash='changed' WHERE id='wrapper'")
	tests[1].want = "Updated title"
	check()
}
