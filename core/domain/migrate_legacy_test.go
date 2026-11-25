package domain

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const migrationNS = "2026-11-01T01:02:03.123456789Z"

var excludedLegacyFiles = []string{"backlog/jobs.json", "incremental-processing.json", "cold-pending-watch.json", "catalog-index.sqlite3"}

func TestImportLegacyShadowClone(t *testing.T) {
	cwd, err := os.Getwd()
	must(t, err)
	for {
		if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(cwd)
		if parent == cwd {
			t.Fatal("cannot locate repository root")
		}
		cwd = parent
	}
	live := filepath.Join(cwd, ".local", "daily")
	if _, err := os.Lstat(filepath.Join(live, "project-registry.json")); errors.Is(err, os.ErrNotExist) {
		t.Skip("live .local/daily not present")
	} else {
		must(t, err)
	}
	clone := t.TempDir()
	must(t, os.Mkdir(filepath.Join(clone, "backlog"), 0o700))
	names, err := snapshotLegacyFiles(live, clone, append(legacySnapshotNames(), excludedLegacyFiles...))
	must(t, err)
	makeLegacyReadOnly(t, clone, names)
	target := filepath.Join(clone, "memlane.sqlite3")
	report := assertMigration(t, clone, target, names)
	t.Logf("MigrationReport counts: %+v", report.Counts)
	assertShadowSourceCounts(t, report)
	db := openReadOnlyTestDB(t, target)
	assertMigratedTemperature(t, db, "ollama-thinking", 1, nil)
	assertMigratedTemperature(t, db, "ollama-thinking", 3, new(float64))
}

func makeLegacyReadOnly(t *testing.T, dir string, names []string) {
	t.Helper()
	for _, name := range names {
		must(t, os.Chmod(filepath.Join(dir, name), 0o400))
	}
}

func assertMigration(t *testing.T, dir, target string, names []string) *MigrationReport {
	t.Helper()
	before := readMigrationFiles(t, dir, names)
	r, err := ImportLegacyDailyStore(dir, target)
	must(t, err)
	if r.AlreadyMigrated || !r.InvariantsVerified || r.LegacyDir != dir || r.TargetPath != target ||
		r.OwnerMarkerPath != filepath.Join(dir, V2OwnerMarkerName) || len(r.Counts) != 27 {
		t.Fatalf("unexpected report: %+v", r)
	}
	if !strings.HasPrefix(filepath.Base(r.BackupDir), "migration-backup-") {
		t.Fatalf("unexpected backup directory: %s", r.BackupDir)
	}
	checkFileMode(t, r.BackupDir, 0o700)
	checkFileMode(t, r.OwnerMarkerPath, 0o600)
	checkFileMode(t, target, 0o600)
	var marker struct {
		SchemaVersion int    `json:"schema_version"`
		Target        string `json:"target_db"`
		Backup        string `json:"backup_dir"`
		MigratedAt    string `json:"migrated_at"`
	}
	markerData := readMigrationFiles(t, dir, []string{V2OwnerMarkerName})[V2OwnerMarkerName]
	must(t, json.Unmarshal(markerData, &marker))
	if marker.SchemaVersion != SchemaVersion || marker.Target != target || marker.Backup != r.BackupDir || !IsValidUTCNS(marker.MigratedAt) {
		t.Fatalf("unexpected marker: %+v", marker)
	}
	for name, data := range before {
		current, err := os.ReadFile(filepath.Join(dir, name))
		must(t, err)
		checkFileMode(t, filepath.Join(dir, name), 0o400)
		if !bytes.Equal(current, data) {
			t.Fatalf("source %s changed", name)
		}
		if !isExcludedLegacyFile(name) {
			backupPath := filepath.Join(r.BackupDir, name)
			backedUp, err := os.ReadFile(backupPath)
			must(t, err)
			checkFileMode(t, backupPath, 0o600)
			if !bytes.Equal(backedUp, data) {
				t.Fatalf("backup lost bytes from %s", name)
			}
		}
	}
	for _, name := range excludedLegacyFiles {
		assertAbsent(t, filepath.Join(r.BackupDir, name))
	}
	for name, counts := range r.Counts {
		if counts.Source != counts.Imported {
			t.Fatalf("%s: %+v", name, counts)
		}
	}
	assertIdempotentMigration(t, dir, target)
	return r
}

func readMigrationFiles(t *testing.T, dir string, names []string) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		must(t, err)
		files[name] = data
	}
	return files
}

func isExcludedLegacyFile(name string) bool {
	for _, excluded := range excludedLegacyFiles {
		if name == excluded {
			return true
		}
	}
	return false
}

func assertIdempotentMigration(t *testing.T, dir, target string) {
	t.Helper()
	name := filepath.Base(target)
	before := readMigrationFiles(t, filepath.Dir(target), []string{name})
	backups, err := filepath.Glob(filepath.Join(dir, "migration-backup-*"))
	must(t, err)
	info, err := os.Stat(target)
	must(t, err)
	marker := readMigrationFiles(t, dir, []string{V2OwnerMarkerName})
	again, err := ImportLegacyDailyStore(dir, target)
	must(t, err)
	want := &MigrationReport{AlreadyMigrated: true, LegacyDir: dir, TargetPath: target, InvariantsVerified: true}
	if !reflect.DeepEqual(again, want) {
		t.Fatalf("idempotent report: %+v", again)
	}
	after, err := os.Stat(target)
	must(t, err)
	newBackups, err := filepath.Glob(filepath.Join(dir, "migration-backup-*"))
	must(t, err)
	if !sameLegacyFile(info, after) || !reflect.DeepEqual(backups, newBackups) ||
		!reflect.DeepEqual(before, readMigrationFiles(t, filepath.Dir(target), []string{name})) ||
		!reflect.DeepEqual(marker, readMigrationFiles(t, dir, []string{V2OwnerMarkerName})) {
		t.Fatal("idempotent rerun changed the migration output")
	}
}

func TestImportLegacyDailyStore(t *testing.T) {
	dir := t.TempDir()
	writeLegacyJSON(t, dir, "project-registry.json", `{
		"schema_version":2,"revision":7,"service_namespace":"dev","runtime_catalog_imported":true,
		"reserved_datasets":["protected"],"tool_graph":{"active":true},"host_targets":{"os":"mac"},
		"enrollments":{"agent":{"status":"ready"}},"defaults":{"access":"read","capture":false,"auto_process":false},
		"projects":{"p":{"id":"p","name":"Example","dataset_id":"d","aliases":["/home/p","/home//p/.","/home/other"],
		"overrides":{"agent":{"access":"maintain","capture":true}}},"off":{"name":"disabled","auto_capture":false,"auto_capture_version":4}},
		"common":{"id":"common","name":"Common","overrides":{"agent":{"access":"read","auto_process":false}}},
		"agents":{"agent":{"name":"Agent","kind":"codex","capabilities":{"collect":true}}},
		"archived_agents":{"old":{"name":"Old agent","retired_at":123,"project_policies":{"p":"read"}}},
		"sessions":{"sqlite":"json-older","json":"json-newer","equal":"same","json-only":"p"},
		"connection_operations":{"op":{"updated_at":30,"which":"json"},"op-ts":{"received_at":40,"updated_at":999,"which":"json"}},
		"connection_receipts":{"agent":{"completed_at":10,"which":"json"}}
	}`)
	writeLegacyJSON(t, dir, "generation.json", `{
		"schema_version":2,"revision":8,"active":{"id":"ollama","revision":2},"next":{"id":"ollama","revision":1},
		"startup_error":"startup_failed","connected_agent_selections":{"codex":{"enabled":true}},
		"operations":{"old":{"state":"done"}},"removed_profiles":{"removed":{"revision":1}},
		"profiles":{
			"ollama":[
				{"revision":1,"digest":"d1","profile":{"mode":"provider","provider":"ollama","model":"x","endpoint":"http://localhost:11434"}},
				{"revision":2,"digest":"d2","profile":{"mode":"provider","provider":"ollama","model":"x","endpoint":"http://localhost:11434","temperature":0,"max_output_tokens":64,"max_concurrent_requests":1}}
			],"removed":[{"revision":1,"digest":"r1","profile":{"mode":"agent_bridge","bridge_kind":"codex","model":"x"}}]},
		"validations":{"v":{"reference":{"id":"ollama","revision":2},"digest":"d2","registered_revision":8,"status":"passed","plain_passed":true,"schema_passed":true,"quality_passed":true,"structured_mode":"native","extra":[1]}},
		"handoffs":{"h":{"reference":{"id":"ollama","revision":2},"previous":{"id":"ollama","revision":1},"status":"applied","active_committed":true,"pending_sessions":2,"started_at":12.5}}
	}`)
	writeLegacyJSON(t, dir, "generation-handoff-runtime.json", `{
		"h":{"status":"staging","runtime_field":"kept"},"runtime-only":{"status":"failed","cancelled_sessions":[{"id":"s"}]}
	}`)
	writeLegacyJSON(t, dir, "user-profiles.json", `{
		"profiles":{"source":{"kind":"codex","operation_id":"profile","document":{"dataset_id":"d","data_id":"new"},"previous":{"dataset_id":"prior","data_id":"old"},"progress":{"phase":"saved"},"unknown":true}},
		"attempts":{"attempt":{"instance_id":"source","document":{"dataset_id":"d","data_id":"try"},"previous":{"dataset_id":"d","data_id":"new"},"error_code":"failed","exit_code":0,"failure_reason":"x","progress":{"phase":"failed"},"extra":5}}
	}`)
	writeLegacyJSON(t, dir, "tool-graph-control-jobs.json", `{"op":{"dataset_id":"d","status":"running","result":{"count":3}}}`)
	writeLegacyJSON(t, dir, "tool-graph-service-jobs.json", `{"jobs":{"op":{"user_id":"u","status":"done","signature":"sig"}}}`)
	writeLegacyJSON(t, dir, "tool-graph-trace-watermarks.json", `{"entries":{"[\"u\",\"s\"]":{"count":3,"min_seq":1,"max_seq":5,"persisted_max_seq":4}}}`)
	must(t, os.Mkdir(filepath.Join(dir, "backlog"), 0o700))
	for _, name := range excludedLegacyFiles {
		writeLegacyJSON(t, dir, name, "excluded bytes "+name)
	}
	hash := KnowledgeReadScopeHash("identity/", "p", "d")
	legacySQLiteHarness(t, dir, hash)
	names := append(legacySnapshotNames(), excludedLegacyFiles...)
	makeLegacyReadOnly(t, dir, names)
	target := filepath.Join(dir, "target #1.sqlite3")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		writeLegacyJSON(t, dir, filepath.Base(target)+".tmp"+suffix, "stale")
	}
	report := assertMigration(t, dir, target, names)
	if len(report.BackedUpFiles) != len(legacySnapshotNames()) {
		t.Fatalf("backup file list: %v", report.BackedUpFiles)
	}
	want := map[string]int{"projects": 2, "project_aliases": 2, "project_acl_overrides": 2,
		"session_bindings": 5, "registry_connection_operations": 2, "generation_profiles": 3,
		"generation_handoffs": 2, "tool_graph_jobs": 2}
	for name, count := range report.Counts {
		expected := 1
		if n, ok := want[name]; ok {
			expected = n
		}
		if count.Source != expected || count.Imported != expected {
			t.Errorf("%s: %+v, want %d", name, count, expected)
		}
	}
	db := openReadOnlyTestDB(t, target)
	assertMigratedTemperature(t, db, "ollama", 1, nil)
	assertMigratedTemperature(t, db, "ollama", 2, new(float64))
	for _, check := range []struct{ query, want string }{
		{`SELECT project_id FROM session_bindings WHERE session_key='sqlite'`, "db-newer"},
		{`SELECT project_id FROM session_bindings WHERE session_key='json'`, "json-newer"},
		{`SELECT updated_at FROM session_bindings WHERE session_key='equal'`, "200"},
		{`SELECT json_extract(payload_json,'$.which') FROM registry_connection_operations WHERE operation_id='op'`, "json"},
		{`SELECT json_extract(payload_json,'$.which') FROM registry_connection_operations WHERE operation_id='op-ts'`, "db"},
		{`SELECT json_extract(payload_json,'$.which') FROM registry_connection_receipts WHERE agent_id='agent'`, "db"},
		{`SELECT auto_capture || ':' || auto_capture_version FROM projects WHERE id='p'`, "1:1"},
		{`SELECT auto_capture || ':' || auto_capture_version FROM projects WHERE id='off'`, "0:4"},
		{`SELECT retired_at || ':' || json_extract(extra_json,'$.project_policies.p') FROM agents WHERE id='old'`, "123:read"},
		{`SELECT access || ':' || revision || ':' || json_extract(extra_json,'$.enrollments.agent.status') FROM defaults`, "read:7:ready"},
		{`SELECT enabled_at_ns || ':' || read_horizon_at_ns || ':' || horizon_max_rowid || ':' || state_rev || ':' || is_auto_baseline || ':' || legacy_file_sha256 || ':' || legacy_mtime_ns || ':' || updated_at FROM ui_read_scopes`,
			migrationNS + ":" + migrationNS + ":20:4:1:checksum:123:legacy-update"},
		{`SELECT source_qa_ids_json || ':' || sync_seq || ':' || first_source_date || ':' || source_date_status FROM document_provenance`, `["qa"]:18:2026-01-01:linked_qa`},
		{`SELECT entry_id IS NULL AND baseline_count IS NULL FROM capture_receipts`, "1"},
		{`SELECT last_recalled_at IS NULL FROM recall_doc_counts`, "1"},
		{`SELECT last_query_at IS NULL FROM recall_query_stats`, "1"},
		{`SELECT max_output_tokens || ':' || max_concurrent_requests FROM generation_profiles WHERE profile_id='ollama' AND revision=2`, "64:1"},
		{`SELECT max_output_tokens IS NULL AND max_concurrent_requests IS NULL FROM generation_profiles WHERE profile_id='ollama' AND revision=1`, "1"},
		{`SELECT removed FROM generation_profiles WHERE profile_id='removed'`, "1"},
		{`SELECT active_revision || ':' || next_revision || ':' || startup_error FROM generation_state`, "2:1:startup_failed"},
		{`SELECT profile_id || ':' || profile_revision || ':' || registered_revision || ':' || json_extract(payload_json,'$.extra[0]') FROM generation_validations`, "ollama:2:8:1"},
		{`SELECT status || ':' || active_committed || ':' || json_extract(runtime_payload_json,'$.runtime_field') FROM generation_handoffs WHERE operation_id='h'`, "applied:1:kept"},
		{`SELECT cancelled_sessions FROM generation_handoffs WHERE operation_id='runtime-only'`, "1"},
		{`SELECT document_data_id || ':' || previous_data_id || ':' || json_extract(progress_json,'$.phase') FROM user_profiles`, "new:old:saved"},
		{`SELECT exit_code || ':' || failure_reason || ':' || json_extract(extra_json,'$.extra') FROM user_profile_attempts`, "0:x:5"},
		{`SELECT json_extract(payload_json,'$.result.count') FROM tool_graph_jobs WHERE scope='control'`, "3"},
		{`SELECT persisted_max_seq FROM tool_graph_watermarks WHERE user_id='u' AND session_id='s'`, "4"},
	} {
		var got string
		must(t, db.QueryRow(check.query).Scan(&got))
		if got != check.want {
			t.Errorf("%s = %q, want %q", check.query, got, check.want)
		}
	}
}

func TestImportLegacyAgentRetirements(t *testing.T) {
	dir := t.TempDir()
	writeLegacyJSON(t, dir, "project-registry.json", `{
		"defaults":{"access":"maintain","capture":true,"auto_process":true},
		"projects":{"p":{"name":"Project","overrides":{"pending":{"access":"maintain"}}}},
		"common":{"id":"common","name":"Common","overrides":{"pending":{"access":"maintain"}}},
		"agents":{
			"pending":{"name":"Pending"},
			"complete":{"name":"Complete","retired_at":" "},
			"undated":{"name":"Undated"},
			"already-retired":{"name":"Already retired","retired_at":"2026-11-25T00:00:00Z"},
			"active":{"name":"Active"}
		},
		"agent_retirements":{
			"pending":{"started_at":123.5},
			"complete":{"started_at":123,"completed_at":456},
			"undated":{"started_at":null,"completed_at":""},
			"already-retired":{"completed_at":456}
		}
	}`)
	target := filepath.Join(dir, "target.sqlite3")
	_, err := ImportLegacyDailyStore(dir, target)
	must(t, err)
	s := Store{db: openReadOnlyTestDB(t, target)}
	ctx := context.Background()
	for id, want := range map[string]string{
		"pending": "123.5", "complete": "456", "undated": "retiring", "already-retired": "2026-11-25T00:00:00Z",
	} {
		t.Run(id, func(t *testing.T) {
			agent, err := s.GetAgent(ctx, id)
			must(t, err)
			if agent.Archived || agent.RetiredAt != want {
				t.Fatalf("agent archived = %t, retired_at = %q, want active record with retired_at = %q", agent.Archived, agent.RetiredAt, want)
			}
			_, err = s.EvaluateProjectACL(ctx, "p", id)
			wantError(t, err, ErrForbidden)
			_, err = s.EvaluateCommonACL(ctx, id)
			wantError(t, err, ErrForbidden)
		})
	}
	active, err := s.EvaluateProjectACL(ctx, "p", "active")
	must(t, err)
	if active.Access != AccessMaintain {
		t.Fatalf("active migrated agent received access %q, want %q", active.Access, AccessMaintain)
	}
}

func writeLegacyJSON(t *testing.T, dir, name, data string) {
	t.Helper()
	must(t, os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600))
}

func legacySQLiteHarness(t *testing.T, dir, hash string) {
	t.Helper()
	registry := filepath.Join(dir, "project-registry.json")
	mtime := time.Unix(100, 0)
	must(t, os.Chtimes(registry, mtime, mtime))
	for _, fixture := range []struct{ name, schema, seed string }{
		{"registry-sessions.sqlite3", `
			CREATE TABLE registry_meta(key TEXT PRIMARY KEY,value TEXT);
			CREATE TABLE registry_sessions(session_key TEXT PRIMARY KEY,project_id TEXT,updated_at REAL);
			CREATE TABLE registry_connection_operations(operation_id TEXT PRIMARY KEY,payload_json TEXT,updated_at REAL);
			CREATE TABLE registry_connection_receipts(agent_id TEXT PRIMARY KEY,payload_json TEXT,updated_at REAL);`,
			`INSERT INTO registry_meta VALUES('revision','7');
			INSERT INTO registry_sessions VALUES('sqlite','db-newer',101),('json','db-older',100),
				('equal','same',200),('wal-only','p',90);
			INSERT INTO registry_connection_operations VALUES('op','{"updated_at":20,"which":"db"}',999),
				('op-ts','{"received_at":50,"which":"db"}',1);
			INSERT INTO registry_connection_receipts VALUES('agent','{"completed_at":20,"which":"db"}',1);`},
		{"lesson-provenance.sqlite3", provenanceDDL,
			`INSERT INTO keyed_provenance VALUES('d','lesson','key','2026-01-01','linked_qa','date');
			INSERT INTO document_provenance VALUES('d','doc','lesson','key','2026-01-01','linked_qa','["qa"]','date',18);`},
		{"knowledge-recall.sqlite3", strings.NewReplacer("recall_events", "recall_event", "recall_hits", "recall_hit").Replace(recallDDL),
			`INSERT INTO recall_event VALUES('agent','event','automatic','when','digest');
			INSERT INTO recall_hit VALUES('agent','event','d','doc');
			INSERT INTO recall_doc_counts VALUES('d','doc',1,1,0,NULL,9);
			INSERT INTO recall_query_stats VALUES('automatic',2,1,1,1,NULL);`},
		{"ui-reading-state.sqlite3", readingDDL, fmt.Sprintf(`
			INSERT INTO ui_read_scopes VALUES('%s','p','d',1,'%s','%s',20,4,1,'checksum',123,'legacy-update');
			INSERT INTO ui_read_items VALUES('%s','doc',0,'legacy-update');`, hash, migrationNS, migrationNS, hash)},
		{"capture-receipts.sqlite3", captureDDL, `INSERT INTO capture_receipts VALUES('capture','u','d','s','m','digest','qa',NULL,NULL,'pending','when');`},
		{"session-drift.sqlite3", driftDDL,
			`INSERT INTO session_project_hits VALUES('d','s','p',1,2,3,'when');
			INSERT INTO session_drift_seen VALUES('d','s','m','when');
			INSERT INTO session_drift_disposition VALUES('d','s','dismissed','p','when');`},
	} {
		path := filepath.Join(dir, fixture.name)
		db, err := OpenPrivateSQLite(path)
		must(t, err)
		t.Cleanup(func() { must(t, db.Close()) })
		_, err = db.Exec(fixture.schema)
		must(t, err)
		must(t, checkpointMigrationDB(db))
		_, err = db.Exec(fixture.seed)
		must(t, err)
		info, err := os.Stat(path + "-wal")
		must(t, err)
		if info.Size() == 0 {
			t.Fatalf("%s has no committed WAL frames", fixture.name)
		}
		if fixture.name == "registry-sessions.sqlite3" {
			tx, err := db.Begin()
			must(t, err)
			t.Cleanup(func() { must(t, tx.Rollback()) })
			_, err = tx.Exec(`INSERT INTO registry_sessions VALUES('uncommitted','p',0)`)
			must(t, err)
		}
	}
}

func TestImportLegacyRollback(t *testing.T) {
	for _, scenario := range []struct {
		name, file, data string
		want             error
	}{
		{"corrupt generation", "generation.json", "{", ErrInvalidArgument},
		{"generation secret", "generation.json", `{"operations":{"op":{"nested":[{"API-key":"raw"}]}}}`, ErrForbidden},
		{"duplicate key masks secret", "generation.json", `{"x":{"api_key":"raw"},"x":{}}`, ErrForbidden},
		{"runtime secret", "generation-handoff-runtime.json", `{"op":{"nested":{"authorization":"raw"}}}`, ErrForbidden},
		{"invalid provenance date", "", "", ErrInvalidArgument},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			dir := t.TempDir()
			writeLegacyJSON(t, dir, "project-registry.json", `{"projects":{"p":{"name":"written before failure"}}}`)
			if scenario.file != "" {
				writeLegacyJSON(t, dir, scenario.file, scenario.data)
			} else {
				db, err := OpenPrivateSQLite(filepath.Join(dir, "lesson-provenance.sqlite3"))
				must(t, err)
				_, err = db.Exec(provenanceDDL + `INSERT INTO keyed_provenance VALUES('d','lesson','k','2026-02-30','created_at','');`)
				must(t, err)
				must(t, db.Close())
			}
			target := filepath.Join(dir, "target.sqlite3")
			_, err := ImportLegacyDailyStore(dir, target)
			wantError(t, err, scenario.want)
			for _, path := range []string{target, target + ".tmp", target + ".tmp-wal", target + ".tmp-shm", filepath.Join(dir, V2OwnerMarkerName)} {
				assertAbsent(t, path)
			}
		})
	}
}

func TestImportLegacyRejectsExistingTargetAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.sqlite3")
	writeLegacyJSON(t, dir, filepath.Base(target), "foreign database")
	_, err := ImportLegacyDailyStore(dir, target)
	wantError(t, err, ErrConflict)
	if got := readMigrationFiles(t, dir, []string{filepath.Base(target)})[filepath.Base(target)]; string(got) != "foreign database" {
		t.Fatal("existing file changed")
	}
	link := filepath.Join(dir, "target-link")
	must(t, os.Symlink(target, link))
	_, err = ImportLegacyDailyStore(dir, link)
	wantError(t, err, ErrConflict)
	legacyLink := filepath.Join(t.TempDir(), "legacy-link")
	must(t, os.Symlink(dir, legacyLink))
	_, err = ImportLegacyDailyStore(legacyLink, target)
	wantError(t, err, ErrInvalidArgument)
	must(t, os.Symlink(target, filepath.Join(dir, "project-registry.json")))
	_, err = ImportLegacyDailyStore(dir, filepath.Join(dir, "fresh.sqlite3"))
	wantError(t, err, ErrInvalidArgument)
	backups, err := filepath.Glob(filepath.Join(dir, "migration-backup-*"))
	must(t, err)
	if len(backups) != 0 {
		t.Fatalf("unexpected backups: %v", backups)
	}
	assertAbsent(t, filepath.Join(dir, V2OwnerMarkerName))
}

func TestImportLegacyConcurrentPublication(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.sqlite3")
	start := make(chan struct{})
	results := make(chan bool, 4)
	errs := make(chan error, 4)
	for range 4 {
		go func() {
			<-start
			r, err := ImportLegacyDailyStore(dir, target)
			errs <- err
			results <- err == nil && !r.AlreadyMigrated
		}()
	}
	close(start)
	var fresh int
	for range 4 {
		must(t, <-errs)
		if <-results {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatalf("fresh migrations = %d", fresh)
	}
}

// Count legacy rows independently of the importer using only snapshot bytes.
func assertShadowSourceCounts(t *testing.T, report *MigrationReport) {
	t.Helper()
	want := make(map[string]int)
	load := func(name string) map[string]any {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(report.BackupDir, name))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		must(t, err)
		var root map[string]any
		must(t, json.Unmarshal(data, &root))
		return root
	}
	object := func(value any) map[string]any { obj, _ := value.(map[string]any); return obj }
	registry := load("project-registry.json")
	for _, name := range []string{"projects", "agents", "archived_agents"} {
		want[name] = len(object(registry[name]))
	}
	for _, raw := range object(registry["projects"]) {
		p := object(raw)
		aliases, _ := p["aliases"].([]any)
		unique := make(map[string]bool)
		for _, alias := range aliases {
			unique[filepath.Clean(alias.(string))] = true
		}
		want["project_aliases"] += len(unique)
		want["project_acl_overrides"] += len(object(p["overrides"]))
	}
	want["project_acl_overrides"] += len(object(object(registry["common"])["overrides"]))
	generation := load("generation.json")
	want["generation_validations"] = len(object(generation["validations"]))
	for _, raw := range object(generation["profiles"]) {
		want["generation_profiles"] += len(raw.([]any))
	}
	handoffs := object(generation["handoffs"])
	want["generation_handoffs"] = len(handoffs)
	for key := range load("generation-handoff-runtime.json") {
		if _, found := handoffs[key]; !found {
			want["generation_handoffs"]++
		}
	}
	users := load("user-profiles.json")
	want["user_profiles"], want["user_profile_attempts"] = len(object(users["profiles"])), len(object(users["attempts"]))
	control := load("tool-graph-control-jobs.json")
	if wrapped, found := control["jobs"]; found {
		control = object(wrapped)
	}
	want["tool_graph_jobs"] = len(control) + len(object(load("tool-graph-service-jobs.json")["jobs"]))
	want["tool_graph_watermarks"] = len(object(load("tool-graph-trace-watermarks.json")["entries"]))

	sqliteSnapshot := t.TempDir()
	var names []string
	for _, name := range report.BackedUpFiles {
		if strings.Contains(name, ".sqlite3") {
			names = append(names, name)
		}
	}
	_, err := snapshotLegacyFiles(report.BackupDir, sqliteSnapshot, names)
	must(t, err)
	for _, group := range legacyTableGroups {
		path := filepath.Join(sqliteSnapshot, group.file)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else {
			must(t, err)
		}
		db := openReadOnlyTestDB(t, path)
		for _, table := range group.tables {
			var count int
			must(t, db.QueryRow("SELECT count(*) FROM "+table.source).Scan(&count))
			want[table.target] = count
		}
	}
	registryDB := filepath.Join(sqliteSnapshot, "registry-sessions.sqlite3")
	var db *sql.DB
	if _, err := os.Stat(registryDB); !errors.Is(err, os.ErrNotExist) {
		must(t, err)
		db = openReadOnlyTestDB(t, registryDB)
	}
	for _, table := range []struct{ key, target, query string }{
		{"sessions", "session_bindings", "SELECT session_key FROM registry_sessions"},
		{"connection_operations", "registry_connection_operations",
			"SELECT operation_id FROM registry_connection_operations WHERE CASE WHEN json_valid(payload_json) THEN json_type(payload_json)='object' END"},
		{"connection_receipts", "registry_connection_receipts",
			"SELECT agent_id FROM registry_connection_receipts WHERE CASE WHEN json_valid(payload_json) THEN json_type(payload_json)='object' END"},
	} {
		keys := make(map[string]bool)
		for key := range object(registry[table.key]) {
			keys[key] = true
		}
		if db != nil {
			rows, err := db.Query(table.query)
			must(t, err)
			for rows.Next() {
				var key string
				must(t, rows.Scan(&key))
				keys[key] = true
			}
			must(t, rows.Err())
			must(t, rows.Close())
		}
		want[table.target] = len(keys)
	}
	for _, name := range legacyCountQueries {
		got, found := report.Counts[name]
		if !found || got != (TableCounts{Source: want[name], Imported: want[name]}) {
			t.Errorf("%s: %+v, independent source count = %d", name, got, want[name])
		}
	}
}
