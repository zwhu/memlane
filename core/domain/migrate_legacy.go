package domain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const V2OwnerMarkerName = ".memlane-v2-owner"

type TableCounts struct {
	Source   int
	Imported int
}

type MigrationReport struct {
	AlreadyMigrated    bool
	LegacyDir          string
	TargetPath         string
	BackupDir          string
	OwnerMarkerPath    string
	BackedUpFiles      []string
	Counts             map[string]TableCounts
	InvariantsVerified bool
}

var legacySQLiteFiles = []string{
	"registry-sessions.sqlite3", "lesson-provenance.sqlite3", "knowledge-recall.sqlite3",
	"ui-reading-state.sqlite3", "capture-receipts.sqlite3", "session-drift.sqlite3",
}

var legacyJSONFiles = []string{
	"project-registry.json", "generation.json", "generation-handoff-runtime.json", "user-profiles.json",
	"tool-graph-control-jobs.json", "tool-graph-service-jobs.json", "tool-graph-trace-watermarks.json",
}

var legacyCountQueries = strings.Fields(`
	projects project_aliases agents archived_agents project_acl_overrides session_bindings
	registry_connection_operations registry_connection_receipts keyed_provenance document_provenance
	recall_events recall_hits recall_doc_counts recall_query_stats ui_read_scopes ui_read_items
	capture_receipts session_project_hits session_drift_seen session_drift_disposition
	generation_profiles generation_validations generation_handoffs user_profiles user_profile_attempts
	tool_graph_jobs tool_graph_watermarks
`)

// ImportLegacyDailyStore snapshots the legacy files, builds a v2 database and publishes it.
func ImportLegacyDailyStore(legacyDir, targetDBPath string) (report *MigrationReport, err error) {
	if err := requireIDs(legacyDir, targetDBPath); err != nil {
		return nil, err
	}
	legacy, err := filepath.Abs(legacyDir)
	if err != nil {
		return nil, err
	}
	target, err := filepath.Abs(targetDBPath)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(legacy)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("legacy path must be a directory: %w", ErrInvalidArgument)
	}
	if err := checkLegacyTargetPath(legacy, target); err != nil {
		return nil, err
	}
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, err
	}
	lock, err := os.Open(parent)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	// Importers sharing a target directory serialize the existence check and publication.
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}

	r := &MigrationReport{LegacyDir: legacy, TargetPath: target}
	if info, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return nil, err
		}
		if err := checkMigratedTarget(target, info); err != nil {
			return nil, err
		}
		r.AlreadyMigrated, r.InvariantsVerified = true, true
		return r, nil
	}

	tmp := target + ".tmp"
	if err := removeMigrationSQLite(tmp); err != nil {
		return nil, err
	}
	defer func() {
		if cleanupErr := removeMigrationSQLite(tmp); cleanupErr != nil {
			report, err = nil, errors.Join(err, cleanupErr)
		}
	}()
	now := time.Now().UTC()
	r.BackupDir = filepath.Join(legacy, "migration-backup-"+now.Format("20060102T150405.000000000Z"))
	if err := os.Mkdir(r.BackupDir, 0o700); err != nil {
		return nil, err
	}
	r.BackedUpFiles, err = snapshotLegacyFiles(legacy, r.BackupDir, legacySnapshotNames())
	if err != nil {
		return nil, errors.Join(err, os.RemoveAll(r.BackupDir))
	}
	if err := syncPath(r.BackupDir); err != nil {
		return nil, err
	}
	if err := syncPath(legacy); err != nil {
		return nil, err
	}
	if err := buildLegacyTarget(r, tmp); err != nil {
		return nil, err
	}
	r.OwnerMarkerPath = filepath.Join(legacy, V2OwnerMarkerName)
	if err := publishLegacyTarget(r, tmp, now); err != nil {
		return nil, err
	}
	r.InvariantsVerified = true
	return r, nil
}

func checkLegacyTargetPath(legacy, target string) error {
	// A target inside the legacy tree must not replace a source or its sidecars.
	legacyReal, err := filepath.EvalSymlinks(legacy)
	if err != nil {
		return err
	}
	parent := filepath.Dir(target)
	for {
		real, err := filepath.EvalSymlinks(parent)
		if err == nil {
			relative, err := filepath.Rel(parent, target)
			if err != nil {
				return err
			}
			target = filepath.Join(real, relative)
			break
		}
		if !errors.Is(err, os.ErrNotExist) || parent == filepath.Dir(parent) {
			return err
		}
		parent = filepath.Dir(parent)
	}
	protected := append(legacySnapshotNames(), V2OwnerMarkerName, "catalog-index.sqlite3",
		"catalog-index.sqlite3-wal", "catalog-index.sqlite3-shm", "incremental-processing.json", "cold-pending-watch.json", "backlog")
	for _, name := range protected {
		path := filepath.Join(legacyReal, name)
		if target == path || strings.HasPrefix(target, path+string(filepath.Separator)) {
			return fmt.Errorf("migration target overlaps a legacy path: %w", ErrConflict)
		}
	}
	return nil
}

func openMigrationSQLite(path, mode string) (*sql.DB, error) {
	q := url.Values{"mode": {mode}, "_pragma": {"busy_timeout=5000"}}
	if mode == "ro" {
		q.Add("_pragma", "query_only=ON")
	}
	dsn := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: q.Encode()}
	db, err := sql.Open("sqlite", dsn.String())
	if err == nil {
		db.SetMaxOpenConns(1)
	}
	return db, err
}

func checkMigratedTarget(path string, info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("migration target is not a regular file: %w", ErrConflict)
	}
	db, err := openMigrationSQLite(path, "ro")
	if err != nil {
		return errors.Join(ErrConflict, err)
	}
	defer db.Close()
	var version int
	err = db.QueryRow("SELECT schema_version FROM schema_meta WHERE singleton=1").Scan(&version)
	if err != nil || version != SchemaVersion {
		return fmt.Errorf("existing migration target schema version %d: %w", version, errors.Join(ErrConflict, err))
	}
	return nil
}

func legacySnapshotNames() []string {
	names := append([]string(nil), legacyJSONFiles...)
	for _, db := range legacySQLiteFiles {
		names = append(names, db, db+"-wal", db+"-shm")
	}
	return names
}

// Check the whole file set before and after the copy to catch a changed source.
func snapshotLegacyFiles(source, destination string, names []string) ([]string, error) {
	stats := make(map[string]os.FileInfo)
	for _, name := range names {
		if info, err := os.Lstat(filepath.Join(source, name)); err == nil {
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("legacy file %s is not regular: %w", name, ErrInvalidArgument)
			}
			stats[name] = info
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	var copied []string
	for _, name := range names {
		if info := stats[name]; info != nil {
			if err := copyLegacyFile(filepath.Join(source, name), filepath.Join(destination, name), info); err != nil {
				return nil, fmt.Errorf("copy legacy file %s: %w", name, err)
			}
			copied = append(copied, name)
		}
	}
	for _, name := range names {
		before := stats[name]
		after, err := os.Lstat(filepath.Join(source, name))
		if before == nil && errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || before == nil || !sameLegacyFile(before, after) {
			return nil, fmt.Errorf("legacy file %s changed during snapshot: %w", name, errors.Join(ErrConflict, err))
		}
	}
	return copied, nil
}

func sameLegacyFile(first, second os.FileInfo) bool {
	return os.SameFile(first, second) && first.Size() == second.Size() && first.ModTime().Equal(second.ModTime())
}

func copyLegacyFile(source, destination string, before os.FileInfo) error {
	fd, err := syscall.Open(source, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	in := os.NewFile(uintptr(fd), source)
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || !sameLegacyFile(before, info) {
		return ErrConflict
	}
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	return errors.Join(copyErr, out.Chmod(0o600), os.Chtimes(destination, info.ModTime(), info.ModTime()), out.Sync(), out.Close())
}

func removeMigrationSQLite(path string) error {
	var result error
	for _, suffix := range []string{"-wal", "-shm", ""} {
		// Unlink only files, never directories, even for stale temporary paths.
		if err := syscall.Unlink(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, err)
		}
	}
	return result
}

func checkpointMigrationDB(db *sql.DB) error {
	var busy, frames, checkpointed int
	if err := db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &frames, &checkpointed); err != nil {
		return err
	}
	if busy != 0 || frames != checkpointed {
		return fmt.Errorf("SQLite checkpoint incomplete: %w", ErrConflict)
	}
	return nil
}

func syncPath(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}

func publishLegacyTarget(r *MigrationReport, tmp string, now time.Time) error {
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	if err := syncPath(tmp); err != nil {
		return err
	}
	file, err := os.CreateTemp(r.LegacyDir, ".memlane-v2-owner-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	data := map[string]any{"schema_version": SchemaVersion, "target_db": r.TargetPath,
		"migrated_at": FormatUTCNS(now), "backup_dir": r.BackupDir}
	if err := errors.Join(file.Chmod(0o600), json.NewEncoder(file).Encode(data), file.Sync(), file.Close()); err != nil {
		return err
	}
	if err := os.Rename(tmp, r.TargetPath); err != nil {
		return err
	}
	parent := filepath.Dir(r.TargetPath)
	rollback := func(err error) error {
		return errors.Join(err, os.Remove(r.TargetPath), syncPath(parent))
	}
	if err := syncPath(parent); err != nil {
		return rollback(err)
	}
	if err := os.Rename(file.Name(), r.OwnerMarkerPath); err != nil {
		return rollback(err)
	}
	return syncPath(r.LegacyDir)
}

func buildLegacyTarget(r *MigrationReport, tmp string) error {
	readDir, err := os.MkdirTemp("", "memlane-legacy-read-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(readDir)
	var sqliteNames []string
	for _, name := range legacySnapshotNames() {
		if strings.Contains(name, ".sqlite3") {
			sqliteNames = append(sqliteNames, name)
		}
	}
	if _, err := snapshotLegacyFiles(r.BackupDir, readDir, sqliteNames); err != nil {
		return err
	}
	store, err := OpenStore(tmp)
	if err != nil {
		return err
	}
	ctx := context.Background()
	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		m := legacyImport{tx: tx, dir: r.BackupDir, readDir: readDir, counts: make(map[string]int)}
		if err := m.run(); err != nil {
			return err
		}
		r.Counts, err = m.verify()
		return err
	})
	if err == nil {
		err = checkpointMigrationDB(store.DB())
	}
	if err := errors.Join(err, store.Close()); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(tmp + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

var legacyScopeHash = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (m *legacyImport) verify() (map[string]TableCounts, error) {
	report := make(map[string]TableCounts, len(legacyCountQueries))
	for _, name := range legacyCountQueries {
		query := "SELECT COUNT(*) FROM " + name
		if name == "agents" || name == "archived_agents" {
			query = "SELECT COUNT(*) FROM agents WHERE archived=" + map[string]string{"agents": "0", "archived_agents": "1"}[name]
		}
		var count int
		if err := m.tx.QueryRow(query).Scan(&count); err != nil {
			return nil, err
		}
		if count != m.counts[name] {
			return nil, fmt.Errorf("migration parity for %s: imported %d of %d", name, count, m.counts[name])
		}
		report[name] = TableCounts{Source: m.counts[name], Imported: count}
	}
	rows, err := m.tx.Query(`SELECT 'scope',scope_hash,enabled_at_ns,read_horizon_at_ns,'' FROM ui_read_scopes
		UNION ALL SELECT 'date',first_source_date,'','','' FROM keyed_provenance
		UNION ALL SELECT 'date',first_source_date,'','','' FROM document_provenance
		UNION ALL SELECT 'generation',payload_json,'','','' FROM generation_validations
		UNION ALL SELECT 'generation',payload_json,runtime_payload_json,'','' FROM generation_handoffs
		UNION ALL SELECT 'generation',connected_agent_selections_json,operations_json,removed_profiles_json,''
			FROM generation_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var fields [4]string
		if err := rows.Scan(&kind, &fields[0], &fields[1], &fields[2], &fields[3]); err != nil {
			return nil, err
		}
		if err := verifyLegacyFields(kind, fields); err != nil {
			return nil, err
		}
	}
	return report, rows.Err()
}

func verifyLegacyFields(kind string, fields [4]string) error {
	if kind == "scope" && !legacyScopeHash.MatchString(fields[0]) {
		return fmt.Errorf("invalid legacy scope hash: %w", ErrInvalidArgument)
	}
	for i, value := range fields {
		if value == "" || (kind == "scope" && i == 0) {
			continue
		}
		switch kind {
		case "scope":
			if !IsValidUTCNS(value) {
				return fmt.Errorf("invalid legacy reading timestamp: %w", ErrInvalidArgument)
			}
		case "date":
			if CalendarDate(value) != value {
				return fmt.Errorf("invalid legacy provenance date: %w", ErrInvalidArgument)
			}
		case "generation":
			if err := validateGenerationPayload(value); err != nil {
				return err
			}
		}
	}
	return nil
}

func prefersLegacyJSON(j, d any, jsonRev, dbRev int, mtime, updated float64, payload bool) bool {
	if reflect.DeepEqual(j, d) {
		return false
	}
	if dbRev < 0 {
		return true
	}
	if payload {
		jts, dts := legacyPayloadTime(j), legacyPayloadTime(d)
		if jts != dts {
			return jts > dts
		}
	}
	return jsonRev > dbRev || (jsonRev == dbRev && mtime >= updated)
}

func legacyPayloadTime(value any) float64 {
	obj, _ := value.(map[string]any)
	for _, key := range strings.Fields("received_at completed_at updated_at at") {
		if ts, ok := obj[key].(float64); ok && ts > 0 {
			return ts
		}
	}
	return 0
}

// These columns were added by the legacy stores when opening an older database.
func legacySelectColumns(db *sql.DB, table legacyTable) (string, error) {
	var defaults map[string]string
	switch table.source {
	case "document_provenance":
		defaults = map[string]string{"sync_seq": "rowid"}
	case "ui_read_scopes":
		defaults = map[string]string{"horizon_max_rowid": "0", "state_rev": "1", "is_auto_baseline": "0"}
	default:
		return table.columns, nil
	}
	columns := strings.Split(table.columns, ",")
	for i, name := range columns {
		fallback, optional := defaults[name]
		if !optional {
			continue
		}
		var exists bool
		if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM pragma_table_info(?) WHERE name=?)", table.source, name).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			columns[i] = fallback
		}
	}
	return strings.Join(columns, ","), nil
}
