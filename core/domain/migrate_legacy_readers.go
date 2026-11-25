package domain

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

type legacyObject map[string]json.RawMessage

type legacyImport struct {
	tx           *sql.Tx
	dir, readDir string
	counts       map[string]int
	err          error
}

func (m *legacyImport) run() error {
	m.registry()
	for _, group := range legacyTableGroups {
		m.withSQLite(group.file, func(db *sql.DB) error {
			for _, table := range group.tables {
				if err := m.copyTable(db, table); err != nil {
					return fmt.Errorf("copy %s: %w", table.target, err)
				}
			}
			return nil
		})
	}
	m.generation()
	m.userProfiles()
	m.toolGraph()
	return m.err
}

func (m *legacyImport) fail(err error) {
	if m.err == nil {
		m.err = err
	}
}

func legacyDecode[T any](m *legacyImport, raw json.RawMessage) (value T) {
	if len(raw) != 0 && m.err == nil {
		if err := json.Unmarshal(raw, &value); err != nil {
			m.fail(fmt.Errorf("decode legacy value: %w", errors.Join(ErrInvalidArgument, err)))
		}
	}
	return value
}

func (m *legacyImport) readObject(name string, generation bool) (legacyObject, float64) {
	if m.err != nil {
		return nil, 0
	}
	path := filepath.Join(m.dir, name)
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0
	}
	if err != nil {
		m.fail(err)
		return nil, 0
	}
	raw, err := os.ReadFile(path)
	if err == nil && generation {
		err = validateGenerationPayload(string(raw))
	}
	data := legacyDecode[legacyObject](m, raw)
	if err == nil && data == nil {
		err = fmt.Errorf("expected a JSON object: %w", ErrInvalidArgument)
	}
	if err != nil {
		m.fail(fmt.Errorf("read %s: %w", name, err))
	}
	mtime := info.ModTime()
	return data, float64(mtime.Unix()) + float64(mtime.Nanosecond())/1e9
}

func (m *legacyImport) object(raw json.RawMessage) legacyObject {
	return legacyDecode[legacyObject](m, raw)
}

func (m *legacyImport) extra(obj legacyObject, exclude string) string {
	extra := make(legacyObject, len(obj))
	for k, v := range obj {
		extra[k] = v
	}
	for _, key := range strings.Fields(exclude) {
		delete(extra, key)
	}
	raw, err := json.Marshal(extra)
	m.fail(err)
	return string(raw)
}

// put uses fixed column names and SQL values. A leading '=' replaces a seeded singleton.
func (m *legacyImport) put(table, columns string, values ...any) {
	if m.err != nil {
		return
	}
	verb := "INSERT INTO "
	if strings.HasPrefix(table, "=") {
		verb, table = "INSERT OR REPLACE INTO ", table[1:]
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")
	_, err := m.tx.Exec(verb+table+"("+columns+") VALUES("+placeholders+")", values...)
	if err != nil {
		m.fail(fmt.Errorf("import %s: %w", table, err))
		return
	}
	m.counts[table]++
}

// Map legacy JSON fields to columns while keeping the schema defaults for omitted values.
// Field tags are s (text), n (integer), b (boolean), r (real), j (raw JSON), and t (text or numeric timestamp).
func (m *legacyImport) mapped(table, columns string, values []any, obj legacyObject, fields string) {
	for _, spec := range strings.Fields(fields) {
		key, column, renamed := strings.Cut(spec[1:], "=")
		if !renamed {
			column = key
		}
		raw := obj[key]
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var value any
		switch spec[0] {
		case 's':
			value = legacyDecode[string](m, raw)
		case 'n':
			value = legacyDecode[int64](m, raw)
		case 'b':
			value = legacyDecode[bool](m, raw)
		case 'r':
			value = legacyDecode[float64](m, raw)
		case 'j':
			value = string(raw)
		case 't':
			if raw[0] == '"' {
				value = legacyDecode[string](m, raw)
			} else {
				value = string(legacyDecode[json.Number](m, raw))
			}
		}
		columns += "," + column
		values = append(values, value)
	}
	m.put(table, columns, values...)
}

func (m *legacyImport) overrides(kind, id string, raw json.RawMessage) {
	for agent, override := range m.object(raw) {
		m.mapped("project_acl_overrides", "target_kind,target_id,agent_id", []any{kind, id, agent},
			m.object(override), "saccess bcapture bauto_process")
	}
}

func (m *legacyImport) hasTimestamp(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	if raw[0] == '"' {
		return strings.TrimSpace(legacyDecode[string](m, raw)) != ""
	}
	return true
}

func (m *legacyImport) retirementTimestamp(raw json.RawMessage) json.RawMessage {
	retirement := m.object(raw)
	for _, key := range []string{"completed_at", "started_at"} {
		if timestamp := retirement[key]; m.hasTimestamp(timestamp) {
			return timestamp
		}
	}
	return json.RawMessage(`"retiring"`)
}

func (m *legacyImport) registry() {
	root, mtime := m.readObject("project-registry.json", false)
	for id, raw := range m.object(root["projects"]) {
		obj := m.object(raw)
		m.mapped("projects", "id,name,extra_json", []any{id, legacyDecode[string](m, obj["name"]), m.extra(obj,
			"id name dataset_id dataset_name code_dataset_id code_enabled code_configured auto_capture auto_capture_version auto_capture_updated_at source status desired_revision operation_id import_acl tool_graph applied aliases overrides")},
			obj, "sdataset_id sdataset_name scode_dataset_id bcode_enabled bcode_configured bauto_capture nauto_capture_version tauto_capture_updated_at ssource sstatus ndesired_revision soperation_id bimport_acl jtool_graph=tool_graph_json japplied=applied_json")
		aliases := make(map[string]bool)
		for _, alias := range legacyDecode[[]string](m, obj["aliases"]) {
			cleaned := filepath.Clean(alias)
			if !aliases[cleaned] {
				m.put("project_aliases", "project_id,alias_path", id, cleaned)
				aliases[cleaned] = true
			}
		}
		m.overrides("project", id, obj["overrides"])
	}
	if obj := m.object(root["common"]); obj != nil {
		id, name := legacyDecode[string](m, obj["id"]), legacyDecode[string](m, obj["name"])
		m.mapped("common_config", "singleton,id,name,extra_json", []any{1, id, name, m.extra(obj,
			"id name kind dataset_id dataset_name code_enabled status desired_revision operation_id applied overrides")},
			obj, "skind sdataset_id sdataset_name bcode_enabled sstatus ndesired_revision soperation_id japplied=applied_json")
		m.overrides("common", id, obj["overrides"])
	}
	retirements := m.object(root["agent_retirements"])
	for archived, key := range []string{"agents", "archived_agents"} {
		for id, raw := range m.object(root[key]) {
			obj := m.object(raw)
			if archived == 0 && obj != nil {
				if len(obj["default_access"]) == 0 || string(obj["default_access"]) == "null" {
					// Legacy agents without a default grant field inherited registry defaults.
					obj["default_access"] = json.RawMessage(`"maintain"`)
				}
				if rawRetirement, found := retirements[id]; found && !m.hasTimestamp(obj["retired_at"]) {
					obj["retired_at"] = m.retirementTimestamp(rawRetirement)
				}
			}
			m.mapped("agents", "id,name,archived,extra_json", []any{id, legacyDecode[string](m, obj["name"]), archived, m.extra(obj,
				"id name kind principal_id key_file control_token_file default_access host_profile host_profile_fingerprint capabilities entrypoints installation retired_at archived")},
				obj, "skind sprincipal_id skey_file scontrol_token_file sdefault_access shost_profile shost_profile_fingerprint jcapabilities=capabilities_json jentrypoints=entrypoints_json jinstallation=installation_json tretired_at")
		}
	}
	m.counts["archived_agents"] = len(m.object(root["archived_agents"]))
	m.counts["agents"] -= m.counts["archived_agents"]
	if root != nil {
		defaults := m.object(root["defaults"])
		if defaults == nil {
			defaults = make(legacyObject)
		}
		for _, k := range strings.Fields("revision service_namespace runtime_catalog_imported reserved_datasets tool_graph host_targets") {
			defaults[k] = root[k]
		}
		m.mapped("=defaults", "singleton,extra_json", []any{1, m.extra(root,
			"projects agents archived_agents common defaults revision service_namespace runtime_catalog_imported reserved_datasets tool_graph host_targets sessions connection_operations connection_receipts")},
			defaults, "saccess bcapture bauto_process nrevision sservice_namespace bruntime_catalog_imported jreserved_datasets=reserved_datasets_json jtool_graph=tool_graph_json jhost_targets=host_targets_json")
	}
	m.registryDynamic(root, mtime)
}

type legacyDynamicTable struct{ key, source, target, columns string }

func (m *legacyImport) registryDynamic(root legacyObject, mtime float64) {
	tables := []legacyDynamicTable{
		{"sessions", "registry_sessions", "session_bindings", "session_key,project_id,updated_at"},
		{"connection_operations", "registry_connection_operations", "registry_connection_operations", "operation_id,payload_json,updated_at"},
		{"connection_receipts", "registry_connection_receipts", "registry_connection_receipts", "agent_id,payload_json,updated_at"},
	}
	jsonRev, dbRev := legacyDecode[int](m, root["revision"]), -1
	m.withSQLite("registry-sessions.sqlite3", func(db *sql.DB) error {
		var value string
		err := db.QueryRow("SELECT value FROM registry_meta WHERE key='revision'").Scan(&value)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if rev, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			dbRev = rev
		}
		for _, table := range tables {
			m.mergeDynamic(db, table, m.object(root[table.key]), jsonRev, dbRev, mtime)
		}
		return m.err
	})
	// Without a SQLite file the JSON maps are the entire source.
	if _, err := os.Stat(filepath.Join(m.readDir, "registry-sessions.sqlite3")); errors.Is(err, os.ErrNotExist) {
		for _, table := range tables {
			m.mergeDynamic(nil, table, m.object(root[table.key]), jsonRev, dbRev, mtime)
		}
	}
}

func (m *legacyImport) mergeDynamic(db *sql.DB, table legacyDynamicTable, entries legacyObject, jsonRev, dbRev int, mtime float64) {
	payload := table.key != "sessions"
	decode := func(raw json.RawMessage) (string, any) {
		if payload {
			return string(raw), legacyDecode[any](m, raw)
		}
		value := legacyDecode[string](m, raw)
		return value, value
	}
	if db != nil {
		rows, err := db.Query("SELECT " + table.columns + " FROM " + table.source)
		if err != nil {
			m.fail(err)
			return
		}
		defer rows.Close()
		for rows.Next() && m.err == nil {
			var key, value string
			var updated float64
			if err := rows.Scan(&key, &value, &updated); err != nil {
				m.fail(err)
				return
			}
			var dbValue any = value
			if payload {
				var object map[string]any
				if json.Unmarshal([]byte(value), &object) != nil || object == nil {
					continue // The legacy loader ignores invalid SQLite payload rows.
				}
				dbValue = object
			}
			if raw, found := entries[key]; found {
				jsonText, jsonValue := decode(raw)
				if prefersLegacyJSON(jsonValue, dbValue, jsonRev, dbRev, mtime, updated, payload) {
					value, updated = jsonText, mtime
				}
				delete(entries, key)
			}
			m.put(table.target, table.columns, key, value, updated)
		}
		m.fail(rows.Err())
	}
	for key, raw := range entries {
		value, _ := decode(raw)
		m.put(table.target, table.columns, key, value, mtime)
	}
}

type legacyTable struct{ source, target, columns string }

var legacyTableGroups = []struct {
	file   string
	tables []legacyTable
}{
	{"lesson-provenance.sqlite3", []legacyTable{
		{"keyed_provenance", "keyed_provenance", "dataset_id,lesson_type,state_key,first_source_date,source_date_status,updated_at"},
		{"document_provenance", "document_provenance", "dataset_id,document_id,lesson_type,state_key,first_source_date,source_date_status,source_qa_ids_json,updated_at,sync_seq"},
	}},
	{"knowledge-recall.sqlite3", []legacyTable{
		{"recall_event", "recall_events", "agent_id,event_id,channel,received_at,payload_hash"},
		{"recall_hit", "recall_hits", "agent_id,event_id,dataset_id,document_id"},
		{"recall_doc_counts", "recall_doc_counts", recallDocColumns},
		{"recall_query_stats", "recall_query_stats", "channel,total_queries,hit_queries,empty_queries,total_hits,last_query_at"},
	}},
	{"ui-reading-state.sqlite3", []legacyTable{
		{"ui_read_scopes", "ui_read_scopes", readScopeColumns},
		{"ui_read_items", "ui_read_items", "scope_hash,doc_id,is_read,updated_at"},
	}},
	{"capture-receipts.sqlite3", []legacyTable{{"capture_receipts", "capture_receipts", receiptColumns}}},
	{"session-drift.sqlite3", []legacyTable{
		{"session_project_hits", "session_project_hits", "bound_dataset_id,session_id,target_project_id,read_count,write_count,command_count,updated_at"},
		{"session_drift_seen", "session_drift_seen", "bound_dataset_id,session_id,message_id,recorded_at"},
		{"session_drift_disposition", "session_drift_disposition", "bound_dataset_id,session_id,status,target_project_id,updated_at"},
	}},
}

func (m *legacyImport) withSQLite(name string, read func(*sql.DB) error) {
	if m.err != nil {
		return
	}
	path := filepath.Join(m.readDir, name)
	if _, err := os.Stat(path); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			m.fail(err)
		}
		return
	}
	db, err := openMigrationSQLite(path, "rw")
	if err != nil {
		m.fail(err)
		return
	}
	if err = checkpointMigrationDB(db); err == nil {
		err = read(db)
	}
	if err = errors.Join(err, db.Close()); err != nil {
		m.fail(fmt.Errorf("read %s: %w", name, err))
	}
}

func (m *legacyImport) copyTable(db *sql.DB, table legacyTable) error {
	var sourceCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table.source).Scan(&sourceCount); err != nil {
		return err
	}
	columns, err := legacySelectColumns(db, table)
	if err != nil {
		return err
	}
	rows, err := db.Query("SELECT " + columns + " FROM " + table.source)
	if err != nil {
		return err
	}
	defer rows.Close()
	values := make([]any, len(strings.Split(table.columns, ",")))
	destinations := make([]any, len(values))
	for i := range values {
		destinations[i] = &values[i]
	}
	for rows.Next() && m.err == nil {
		if err := rows.Scan(destinations...); err != nil {
			return err
		}
		m.put(table.target, table.columns, values...)
	}
	m.counts[table.target] = sourceCount
	return errors.Join(m.err, rows.Err())
}

func (m *legacyImport) generation() {
	root, _ := m.readObject("generation.json", true)
	runtime, _ := m.readObject("generation-handoff-runtime.json", true)
	removed := m.object(root["removed_profiles"])
	for id, raw := range m.object(root["profiles"]) {
		for _, row := range legacyDecode[[]legacyObject](m, raw) {
			p := legacyDecode[GenerationProfile](m, row["profile"])
			p.ProfileID, p.Revision = id, legacyDecode[int](m, row["revision"])
			p.Digest = legacyDecode[string](m, row["digest"])
			_, p.Removed = removed[id]
			m.generationProfile(p)
		}
	}
	for id, raw := range m.object(root["validations"]) {
		obj := m.object(raw)
		ref := m.object(obj["reference"])
		m.mapped("generation_validations", "operation_id,profile_id,profile_revision,payload_json,updated_at",
			[]any{id, legacyDecode[string](m, ref["id"]), legacyDecode[int](m, ref["revision"]), string(raw), NowUTCMicro()}, obj,
			"sdigest nregistered_revision sstatus bplain_passed bschema_passed bquality_passed sstructured_mode serror_code")
	}
	if root != nil {
		active, next := m.object(root["active"]), m.object(root["next"])
		m.mapped("=generation_state", "singleton,active_profile_id,active_revision,next_profile_id,next_revision,updated_at",
			[]any{1, legacyDecode[string](m, active["id"]), legacyDecode[int](m, active["revision"]),
				legacyDecode[string](m, next["id"]), legacyDecode[int](m, next["revision"]), NowUTCMicro()},
			root, "nschema_version nrevision sstartup_error jconnected_agent_selections=connected_agent_selections_json joperations=operations_json jremoved_profiles=removed_profiles_json")
	}
	handoffs := m.object(root["handoffs"])
	for id, raw := range handoffs {
		m.handoff(id, raw, runtime[id])
	}
	for id, raw := range runtime {
		if _, found := handoffs[id]; !found {
			m.handoff(id, nil, raw)
		}
	}
}

func (m *legacyImport) generationProfile(p GenerationProfile) {
	if err := ValidateGenerationProfile(p); err != nil {
		m.fail(err)
	}
	m.put("generation_profiles", generationProfileColumns, p.ProfileID, p.Revision, p.Digest, p.Mode,
		p.Provider, p.BridgeKind, p.BridgeProvider, p.Model, p.Endpoint, p.CredentialRef, p.CredentialVersion,
		p.Temperature, p.MaxOutputTokens, p.MaxConcurrentRequests, p.Removed, defaultText(p.CreatedAt, NowUTCMicro()))
	if m.err != nil {
		return
	}
	got, err := scanGenerationProfile(m.tx.QueryRow("SELECT "+generationProfileColumns+
		" FROM generation_profiles WHERE profile_id=? AND revision=?", p.ProfileID, p.Revision))
	if err == nil && (!reflect.DeepEqual(p.Temperature, got.Temperature) ||
		!reflect.DeepEqual(p.MaxOutputTokens, got.MaxOutputTokens) || !reflect.DeepEqual(p.MaxConcurrentRequests, got.MaxConcurrentRequests)) {
		err = fmt.Errorf("generation parameters changed in migration: %w", ErrConflict)
	}
	m.fail(errors.Join(err, ValidateGenerationProfile(got)))
}

func (m *legacyImport) handoff(id string, payload, runtime json.RawMessage) {
	obj := m.object(runtime)
	if obj == nil {
		obj = make(legacyObject)
	}
	// Runtime phase counters can be lists; the persisted handoff records final counts.
	if cancelled := obj["cancelled_sessions"]; len(cancelled) > 0 && cancelled[0] == '[' {
		obj["cancelled_sessions"] = json.RawMessage(strconv.Itoa(len(legacyDecode[[]json.RawMessage](m, cancelled))))
	}
	for key, value := range m.object(payload) {
		obj[key] = value
	}
	ref, previous := m.object(obj["reference"]), m.object(obj["previous"])
	m.mapped("generation_handoffs", "operation_id,profile_id,profile_revision,previous_profile_id,previous_revision,payload_json,runtime_payload_json",
		[]any{id, legacyDecode[string](m, ref["id"]), legacyDecode[int](m, ref["revision"]), legacyDecode[string](m, previous["id"]),
			legacyDecode[int](m, previous["revision"]), defaultText(string(payload), "{}"), defaultText(string(runtime), "{}")},
		obj, "sdigest sstatus sreason rstarted_at rended_at bactive_committed ncancelled_sessions npending_sessions nunconfirmed_sessions")
}

func (m *legacyImport) userProfiles() {
	root, _ := m.readObject("user-profiles.json", false)
	for attempt, key := range []string{"profiles", "attempts"} {
		for id, raw := range m.object(root[key]) {
			obj := m.object(raw)
			doc, prev := m.object(obj["document"]), m.object(obj["previous"])
			cols := "document_dataset_id,document_data_id,previous_dataset_id,previous_data_id,extra_json"
			values := []any{legacyDecode[string](m, doc["dataset_id"]), legacyDecode[string](m, doc["data_id"]),
				legacyDecode[string](m, prev["dataset_id"]), legacyDecode[string](m, prev["data_id"])}
			fields := "skind smethod sstatus sdataset_id scontent_hash rstarted_at rupdated_at jprogress=progress_json"
			exclude := "kind method status dataset_id content_hash started_at updated_at progress document previous instance_id operation_id"
			table := "user_profiles"
			if attempt == 0 {
				cols, fields = cols+",instance_id", fields+" soperation_id"
			} else {
				table, cols = "user_profile_attempts", cols+",operation_id"
				fields += " sinstance_id serror_code nexit_code sfailure_reason"
				exclude += " error_code exit_code failure_reason"
			}
			m.mapped(table, cols, append(values, m.extra(obj, exclude), id), obj, fields)
		}
	}
}

func (m *legacyImport) toolGraph() {
	for _, scope := range []string{"control", "service"} {
		root, _ := m.readObject("tool-graph-"+scope+"-jobs.json", false)
		if _, wrapped := root["jobs"]; wrapped || scope == "service" {
			root = m.object(root["jobs"])
		}
		for id, raw := range root {
			m.mapped("tool_graph_jobs", "scope,operation_id,payload_json", []any{scope, id, string(raw)}, m.object(raw),
				"sdataset_id suser_id sstatus ssignature serror_code tcreated_at tstarted_at tended_at")
		}
	}
	root, _ := m.readObject("tool-graph-trace-watermarks.json", false)
	for key, raw := range m.object(root["entries"]) {
		ids := legacyDecode[[]string](m, json.RawMessage(key))
		if len(ids) != 2 {
			m.fail(fmt.Errorf("invalid tool graph watermark key: %w", ErrInvalidArgument))
			continue
		}
		m.mapped("tool_graph_watermarks", "user_id,session_id", []any{ids[0], ids[1]}, m.object(raw),
			"ncount nmin_seq nmax_seq npersisted_max_seq tupdated_at")
	}
}
