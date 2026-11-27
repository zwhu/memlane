package domain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func wantError(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func assertAbsent(t *testing.T, path string) {
	t.Helper()
	_, err := os.Lstat(path)
	wantError(t, err, os.ErrNotExist)
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "store.sqlite3"))
	must(t, err)
	t.Cleanup(func() { must(t, store.Close()) })
	return store
}

func openReadOnlyTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := openMigrationSQLite(path, "ro")
	must(t, err)
	t.Cleanup(func() { must(t, db.Close()) })
	return db
}

func assertMigratedTemperature(t *testing.T, db *sql.DB, id string, revision int, want *float64) {
	t.Helper()
	var got *float64
	must(t, db.QueryRow("SELECT temperature FROM generation_profiles WHERE profile_id=? AND revision=?", id, revision).Scan(&got))
	if (got == nil) != (want == nil) || (got != nil && *got != *want) {
		t.Fatalf("%s revision %d temperature %v, want %v", id, revision, got, want)
	}
}

func TestStoreSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.sqlite3")
	store, err := OpenStore(path)
	must(t, err)
	checkFileMode(t, path, 0o600)
	version, err := store.SchemaVersion()
	must(t, err)
	if version != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, SchemaVersion)
	}
	ctx := context.Background()
	must(t, store.UpsertProject(ctx, Project{ID: "project", Name: "project"}))
	must(t, store.Close())
	store, err = OpenStore(path)
	must(t, err)
	_, err = store.GetProject(ctx, "project")
	must(t, err)
	futureVersion := SchemaVersion + 1
	_, err = store.DB().Exec("UPDATE schema_meta SET schema_version=?", futureVersion)
	must(t, err)
	must(t, store.Close())
	_, err = OpenStore(path)
	wantError(t, err, ErrConflict)
	var future int
	must(t, openReadOnlyTestDB(t, path).QueryRow("SELECT schema_version FROM schema_meta").Scan(&future))
	if future != futureVersion {
		t.Fatalf("future database changed to version %d, want %d", future, futureVersion)
	}
}

func TestConcurrentStoreReadWrites(t *testing.T) {
	s := newTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const workers, writes = 8, 25
	start := make(chan struct{})
	var group sync.WaitGroup
	for worker := range workers {
		group.Go(func() {
			<-start
			for n := range writes {
				key := fmt.Sprintf("%d-%d", worker, n)
				if err := s.UpsertSessionBinding(ctx, key, "project", float64(n)); err != nil {
					t.Error(err)
					return
				}
				event := RecallEventInput{AgentID: "agent", EventID: key, Channel: "automatic", Hits: []RecallHit{{"dataset", "doc"}}}
				if recorded, err := s.RecordRecallEvent(ctx, event); err != nil || !recorded {
					t.Errorf("recall event %s = %v, %v", key, recorded, err)
					return
				}
				if binding, err := s.GetSessionBinding(ctx, key); err != nil || binding.UpdatedAt != float64(n) {
					t.Errorf("binding %s = %+v, %v", key, binding, err)
					return
				}
				if count, err := s.GetRecallDocCount(ctx, "dataset", "doc"); err != nil || count.TotalHits < 1 {
					t.Errorf("recall count = %+v, %v", count, err)
					return
				}
			}
		})
	}
	close(start)
	group.Wait()
	bindings, err := s.ListProjectSessions(ctx, "project")
	must(t, err)
	count, err := s.GetRecallDocCount(ctx, "dataset", "doc")
	must(t, err)
	if len(bindings) != workers*writes || count.TotalHits != workers*writes {
		t.Fatalf("bindings=%d hits=%d, want %d each", len(bindings), count.TotalHits, workers*writes)
	}
}

func TestClearCatalogProjection(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, err := s.DB().Exec(`
		INSERT INTO projects(id,name) VALUES('p','p');
		INSERT INTO project_aliases VALUES('p','/p');
		INSERT INTO agents(id,name) VALUES('a','a');
		INSERT INTO project_acl_overrides(target_kind,target_id,agent_id) VALUES('project','p','a');
		INSERT INTO common_config(singleton,id,name) VALUES(1,'c','common');
		INSERT INTO session_bindings VALUES('s','p',1);
		INSERT INTO registry_connection_operations VALUES('op','{}',0);
		INSERT INTO registry_connection_receipts VALUES('a','{}',0);
		INSERT INTO keyed_provenance VALUES('d','lesson','key','2026-01-01','session_date','');
		INSERT INTO document_provenance VALUES('d','doc','lesson','key','2026-01-01','session_date','[]','',1);
		INSERT INTO recall_events VALUES('a','e','explicit','','hash');
		INSERT INTO recall_hits VALUES('a','e','d','doc');
		INSERT INTO recall_doc_counts(dataset_id,document_id) VALUES('d','doc');
		INSERT INTO recall_query_stats(channel) VALUES('explicit');
		INSERT INTO ui_read_scopes(scope_hash,project_id,dataset_id,initialized,enabled_at_ns,read_horizon_at_ns,updated_at)
			VALUES('hash','p','d',1,'','','');
		INSERT INTO ui_read_items VALUES('hash','doc',1,'');
		INSERT INTO capture_receipts(capture_key,user_id,dataset_id,session_id,message_id,payload_sha256,entry_type,status,updated_at)
			VALUES('key','u','d','s','m','sha','qa','pending','');
		INSERT INTO capture_inbox(id,project_id,dataset_id,agent_id,session_id,message_id,kind,payload_json,status,created_at,updated_at)
			VALUES('in','p','d','a','s','m','qa','{}','pending','','');
		INSERT INTO session_flush_jobs(id,project_id,dataset_id,session_id,status,created_at,updated_at)
			VALUES('flush:1','p','d','s','pending','','');
		INSERT INTO session_project_hits(bound_dataset_id,session_id,target_project_id,updated_at) VALUES('d','s','p','');
		INSERT INTO removed_session_owners(dataset_id,session_id,removed_at) VALUES('d-removed','s-removed','');
		INSERT INTO session_drift_seen VALUES('d','s','m','');
		INSERT INTO session_drift_disposition VALUES('d','s','dismissed','p','');
		INSERT INTO generation_profiles(profile_id,revision,digest,mode,model,created_at) VALUES('gp',1,'digest','provider','model','');
		INSERT INTO generation_validations(operation_id,digest,status,updated_at) VALUES('v','digest','passed','');
		INSERT INTO generation_handoffs(operation_id) VALUES('h');
		INSERT INTO user_profiles(instance_id) VALUES('source');
		INSERT INTO user_profile_attempts(operation_id) VALUES('attempt');
		INSERT INTO tool_graph_jobs(scope,operation_id) VALUES('control','op');
		INSERT INTO tool_graph_watermarks(user_id,session_id) VALUES('u','s');`)
	must(t, err)
	must(t, s.UpsertSessionRelation(ctx, SessionRelation{DatasetID: "d", SessionID: "child", ParentSessionID: "parent", SubagentRole: "worker"}))
	_, err = s.RecordCaptureFilterEvent(ctx, CaptureFilterEvent{ProjectID: "p", Rule: "*.key"})
	must(t, err)
	must(t, s.UpsertCatalogDoc(ctx, CatalogDoc{DatasetID: "d", DocID: "doc", Kind: "knowledge", IsKnowledge: true}))
	must(t, s.UpsertCatalogSession(ctx, CatalogSession{DatasetID: "d", SessionID: "s"}))
	must(t, s.UpsertCatalogSyncState(ctx, CatalogSyncState{DatasetID: "d"}))
	must(t, s.ClearCatalogProjection(ctx))
	must(t, s.ClearCatalogProjection(ctx))
	rows, err := s.DB().Query("SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	must(t, err)
	var tables []string
	for rows.Next() {
		var name string
		must(t, rows.Scan(&name))
		tables = append(tables, name)
	}
	must(t, rows.Err())
	must(t, rows.Close())
	for _, name := range tables {
		want := 1
		if strings.HasPrefix(name, "catalog_") {
			want = 0
		}
		var got int
		must(t, s.DB().QueryRow("SELECT COUNT(*) FROM "+name).Scan(&got))
		if got != want {
			t.Errorf("%s count = %d, want %d", name, got, want)
		}
	}
}

func TestRecallMaterialization(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a, b := RecallHit{"dataset", "a"}, RecallHit{"dataset", "b"}
	in := RecallEventInput{AgentID: "agent", EventID: "one", Channel: "automatic", ReceivedAt: "2026-01-01", Hits: []RecallHit{b, a, a}}
	added, err := s.RecordRecallEvent(ctx, in)
	must(t, err)
	if !added {
		t.Fatal("first event was not recorded")
	}
	in.Hits = []RecallHit{a, b}
	added, err = s.RecordRecallEvent(ctx, in)
	must(t, err)
	if added {
		t.Fatal("reordered duplicate was recorded")
	}
	in.Hits = []RecallHit{a}
	_, err = s.RecordRecallEvent(ctx, in)
	wantError(t, err, ErrConflict)
	in.EventID, in.Channel, in.ReceivedAt = "two", "explicit", "2026-02-01"
	_, err = s.RecordRecallEvent(ctx, in)
	must(t, err)
	count, err := s.GetRecallDocCount(ctx, a.DatasetID, a.DocumentID)
	must(t, err)
	if count.TotalHits != 2 || count.AutoHits != 1 || count.ExplicitHits != 1 || count.LastRecalledAt != in.ReceivedAt || count.Rev != 2 {
		t.Fatalf("recall counts: %+v", count)
	}
	must(t, s.RecordRecallQueryStat(ctx, "explicit", 0, "first"))
	must(t, s.RecordRecallQueryStat(ctx, "explicit", 2, "last"))
	stats, err := s.GetRecallQueryStats(ctx)
	must(t, err)
	if len(stats) != 1 || stats[0] != (RecallQueryStat{"explicit", 2, 1, 1, 2, "last"}) {
		t.Fatalf("recall query stats: %+v", stats)
	}
}

func TestReadingScopeAndUnreadBranches(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	scopeHash := KnowledgeReadScopeHash("identity/", "project", "dataset")
	if scopeHash != "ba9e583bce83771f0adeaf1a2c48f618d49f9f6b865f610d7cc081b4e81d50d0" {
		t.Fatalf("legacy double slash hash = %s", scopeHash)
	}
	for _, stamp := range []string{"2026-11-01T01:02:03.123456Z", "2026-11-01T01:02:03.123456789+00:00", "2026-02-30T01:02:03.123456789Z"} {
		if IsValidUTCNS(stamp) {
			t.Errorf("accepted invalid ns timestamp %s", stamp)
		}
	}
	if !IsValidUTCNS(migrationNS) {
		t.Fatal("rejected ns timestamp")
	}
	scope := ReadScope{ScopeHash: scopeHash, ProjectID: "project", DatasetID: "dataset", Initialized: true,
		EnabledAtNS: migrationNS, ReadHorizonAtNS: migrationNS}
	must(t, s.UpsertReadScope(ctx, scope))
	scope.EnabledAtNS = "2026-01-01"
	wantError(t, s.UpsertReadScope(ctx, scope), ErrInvalidArgument)
	newer := "2026-11-01T01:02:03.123456790Z"
	add := func(id, stamp string, invalid bool) {
		t.Helper()
		must(t, s.UpsertCatalogDoc(ctx, CatalogDoc{DatasetID: "dataset", DocID: id, Kind: "knowledge", IsKnowledge: true,
			CreatedAtNS: stamp, CreatedAtInvalid: invalid}))
	}
	unread := func(want int) {
		t.Helper()
		got, err := s.CountUnreadInScope(ctx, scopeHash)
		must(t, err)
		if got != want {
			t.Fatalf("unread = %d, want %d", got, want)
		}
	}
	add("horizon", migrationNS, false)
	add("explicit-unread", migrationNS, false)
	unread(0)
	add("new", newer, false)
	unread(1)
	add("invalid", "", true)
	unread(2)
	must(t, s.SetReadItem(ctx, scopeHash, "explicit-unread", false))
	unread(3)
	must(t, s.SetReadItem(ctx, scopeHash, "new", true))
	must(t, s.SetReadItem(ctx, scopeHash, "invalid", true))
	unread(1)
	must(t, s.MarkAllReadInScope(ctx, scopeHash, newer, 20))
	unread(0)
}

func TestSessionDrift(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	input := []DriftHitDelta{{TargetProjectID: "target", ReadCount: 2, WriteCount: 3, CommandCount: 1}}
	for _, message := range []string{" m ", "m", "other"} {
		got, err := s.RecordSessionDriftHits(ctx, "dataset", "session", message, input)
		must(t, err)
		if got != (message != "m") {
			t.Fatalf("unexpected drift dedup for %q: %v", message, got)
		}
	}
	hits, err := s.ListSessionDriftHits(ctx, "dataset", "session")
	must(t, err)
	if len(hits) != 1 || hits[0].ReadCount != 4 || hits[0].WriteCount != 6 || hits[0].CommandCount != 2 {
		t.Fatalf("drift hits: %+v", hits)
	}
	_, err = s.GetSessionDriftDisposition(ctx, "dataset", "session")
	wantError(t, err, ErrNotFound)
	must(t, s.DismissSessionDrift(ctx, "dataset", "session", "target"))
	disposition, err := s.GetSessionDriftDisposition(ctx, "dataset", "session")
	must(t, err)
	if disposition.Status != "dismissed" || disposition.TargetProjectID != "target" {
		t.Fatalf("drift disposition: %+v", disposition)
	}
	_, err = s.GetSessionDriftDisposition(ctx, "other-dataset", "session")
	wantError(t, err, ErrNotFound)
}

func TestUserProfilesAndToolGraph(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	profile := UserProfile{InstanceID: "codex", Kind: "codex", Document: DocumentRef{"profile-dataset", "codex-doc"},
		Previous: DocumentRef{"old-dataset", "old-doc"}}
	must(t, s.UpsertUserProfile(ctx, profile))
	other := profile
	other.InstanceID, other.Kind, other.Document.DataID = "pi", "pi", "pi-doc"
	must(t, s.UpsertUserProfile(ctx, other))
	attempt := UserProfileAttempt{OperationID: "attempt", InstanceID: "pi", Document: other.Document, Previous: other.Previous, ExitCode: new(int)}
	must(t, s.UpsertUserProfileAttempt(ctx, attempt))
	got, err := s.GetUserProfile(ctx, profile.InstanceID)
	must(t, err)
	if got.Document != profile.Document || got.Previous != profile.Previous {
		t.Fatalf("profile refs changed across sources: %+v", got)
	}
	profiles, err := s.ListUserProfiles(ctx)
	must(t, err)
	unrelated, err := s.ListUserProfileAttempts(ctx, profile.InstanceID)
	must(t, err)
	savedAttempt, err := s.GetUserProfileAttempt(ctx, attempt.OperationID)
	must(t, err)
	if len(profiles) != 2 || len(unrelated) != 0 || savedAttempt.Document != attempt.Document ||
		savedAttempt.Previous != attempt.Previous || savedAttempt.ExitCode == nil || *savedAttempt.ExitCode != 0 {
		t.Fatalf("profiles=%+v unrelated=%+v attempt=%+v", profiles, unrelated, savedAttempt)
	}
	must(t, s.DeleteUserProfileAttempt(ctx, "attempt"))
	must(t, s.DeleteUserProfile(ctx, "pi"))
	_, err = s.GetUserProfileAttempt(ctx, "attempt")
	wantError(t, err, ErrNotFound)
	_, err = s.GetUserProfile(ctx, "pi")
	wantError(t, err, ErrNotFound)
	_, err = s.GetUserProfile(ctx, "codex")
	must(t, err)

	for _, scope := range []string{"control", "service"} {
		must(t, s.UpsertToolGraphJob(ctx, ToolGraphJob{Scope: scope, OperationID: "op", Status: scope}))
	}
	must(t, s.UpsertToolGraphJob(ctx, ToolGraphJob{Scope: "control", OperationID: "op", Status: "updated"}))
	job, err := s.GetToolGraphJob(ctx, "control", "op")
	must(t, err)
	jobs, err := s.ListToolGraphJobs(ctx, "")
	must(t, err)
	if job.Status != "updated" || len(jobs) != 2 || jobs[1].Status != "service" {
		t.Fatalf("jobs=%+v updated=%+v", jobs, job)
	}
	must(t, s.DeleteToolGraphJob(ctx, "control", "op"))
	_, err = s.GetToolGraphJob(ctx, "control", "op")
	wantError(t, err, ErrNotFound)
	_, err = s.GetToolGraphJob(ctx, "service", "op")
	must(t, err)

	for _, user := range []string{"one", "two"} {
		must(t, s.UpsertToolGraphWatermark(ctx, ToolGraphWatermark{UserID: user, SessionID: "s", Count: 2, MinSeq: 1, MaxSeq: 3, PersistedMaxSeq: 2}))
	}
	must(t, s.UpsertToolGraphWatermark(ctx, ToolGraphWatermark{UserID: "one", SessionID: "s", Count: 3, MinSeq: 1, MaxSeq: 4, PersistedMaxSeq: 3}))
	watermarks, err := s.ListToolGraphWatermarks(ctx, "one")
	must(t, err)
	if len(watermarks) != 1 || watermarks[0].Count != 3 || watermarks[0].PersistedMaxSeq != 3 {
		t.Fatalf("watermarks: %+v", watermarks)
	}
	must(t, s.DeleteToolGraphWatermark(ctx, "one", "s"))
	_, err = s.GetToolGraphWatermark(ctx, "one", "s")
	wantError(t, err, ErrNotFound)
	wm, err := s.GetToolGraphWatermark(ctx, "two", "s")
	must(t, err)
	if wm.PersistedMaxSeq != 2 {
		t.Fatalf("other user's watermark: %+v", wm)
	}
}

func TestSessionRelationsCRUDMigrationAndProjectDelete(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v3.sqlite3")
	db, err := OpenPrivateSQLite(path)
	must(t, err)
	v3Catalog := strings.ReplaceAll(catalogDDL, sessionRelationsDDL, "")
	v3Schema := strings.ReplaceAll(schemaDDL, catalogDDL, v3Catalog)
	must(t, execSQL(ctx, db, v3Schema+`
		INSERT INTO schema_meta VALUES(1,3,'created','updated');
		INSERT INTO projects(id,name,dataset_id) VALUES('p1','Project 1','ds-1');
	`))
	must(t, db.Close())

	for range 2 {
		store, err := OpenStore(path)
		must(t, err)
		version, err := store.SchemaVersion()
		must(t, err)
		if version != SchemaVersion {
			t.Fatalf("migrated version = %d, want %d", version, SchemaVersion)
		}
		must(t, store.Close())
	}

	s, err := OpenStore(path)
	must(t, err)
	defer func() { must(t, s.Close()) }()

	wantError(t, s.UpsertSessionRelation(ctx, SessionRelation{DatasetID: "", SessionID: "c1", ParentSessionID: "p1"}), ErrInvalidArgument)
	wantError(t, s.UpsertSessionRelation(ctx, SessionRelation{DatasetID: "ds-1", SessionID: "", ParentSessionID: "p1"}), ErrInvalidArgument)
	wantError(t, s.UpsertSessionRelation(ctx, SessionRelation{DatasetID: "ds-1", SessionID: "same", ParentSessionID: "same"}), ErrInvalidArgument)

	must(t, s.UpsertSessionRelation(ctx, SessionRelation{
		DatasetID: "ds-1", SessionID: "child-1", ParentSessionID: "root-1", RootSessionID: "root-1", SubagentRole: "research",
	}))
	// Updating without subagent_role must preserve the existing non-empty subagent_role.
	must(t, s.UpsertSessionRelation(ctx, SessionRelation{
		DatasetID: "ds-1", SessionID: "child-1", ParentSessionID: "root-1", SubagentRole: "",
	}))
	must(t, s.UpsertSessionRelation(ctx, SessionRelation{
		DatasetID: "ds-2", SessionID: "child-2", ParentSessionID: "root-2", SubagentRole: "worker",
	}))

	ds1Rels, err := s.ListSessionRelations(ctx, "ds-1")
	must(t, err)
	if len(ds1Rels) != 1 || ds1Rels[0].SessionID != "child-1" || ds1Rels[0].ParentSessionID != "root-1" ||
		ds1Rels[0].RootSessionID != "root-1" || ds1Rels[0].SubagentRole != "research" || ds1Rels[0].UpdatedAt == "" {
		t.Fatalf("ds-1 relations = %+v", ds1Rels)
	}
	allRels, err := s.ListSessionRelations(ctx, "")
	must(t, err)
	if len(allRels) != 2 {
		t.Fatalf("all relations = %+v", allRels)
	}

	// Updating with a new non-empty subagent_role overwrites it.
	must(t, s.UpsertSessionRelation(ctx, SessionRelation{
		DatasetID: "ds-1", SessionID: "child-1", ParentSessionID: "root-1", SubagentRole: "reviewer",
	}))
	ds1Rels, err = s.ListSessionRelations(ctx, "ds-1")
	must(t, err)
	if len(ds1Rels) != 1 || ds1Rels[0].SubagentRole != "reviewer" {
		t.Fatalf("updated relation = %+v", ds1Rels)
	}

	// Deleting p1 removes ds-1 relations while preserving ds-2.
	must(t, s.DeleteProject(ctx, "p1"))
	ds1Rels, err = s.ListSessionRelations(ctx, "ds-1")
	must(t, err)
	ds2Rels, err := s.ListSessionRelations(ctx, "ds-2")
	must(t, err)
	if len(ds1Rels) != 0 || len(ds2Rels) != 1 {
		t.Fatalf("after delete: ds1=%+v ds2=%+v", ds1Rels, ds2Rels)
	}
}
