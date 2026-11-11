package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

func migrateProjectPreferenceDecisions(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE project_preference_decisions(
 operation TEXT PRIMARY KEY,digest TEXT NOT NULL,library TEXT NOT NULL,
 project TEXT NOT NULL REFERENCES scopes(id),
 project_memory TEXT NOT NULL,project_revision INTEGER NOT NULL,
 preference_memory TEXT NOT NULL,preference_revision INTEGER NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('confirmed','retired')),payload TEXT NOT NULL,confirmed_ms INTEGER NOT NULL,
 FOREIGN KEY(project_memory,project_revision) REFERENCES versions(memory,revision),
 FOREIGN KEY(preference_memory,preference_revision) REFERENCES versions(memory,revision));
 CREATE INDEX project_preference_current ON project_preference_decisions(library,project,project_memory,project_revision,preference_memory,preference_revision,state,confirmed_ms);
 PRAGMA user_version=29;`)
	return err
}

func preferenceScope(q records.ProjectPreferenceQuery) error {
	if q.Project == "" || q.ProjectRecord.Memory == "" || q.PreferenceRecord.Memory == "" || q.ProjectRecord.Memory == q.PreferenceRecord.Memory || q.ProjectRecord.Dataset != q.Project || q.PreferenceRecord.Dataset == "" || q.ProjectRecord.Revision < 1 || q.PreferenceRecord.Revision < 1 || len(q.Datasets) == 0 || len(q.Datasets) > 256 {
		return access("invalid_request", "two exact current records and explicit read scopes required")
	}
	for _, s := range []string{q.Project, q.ProjectRecord.Memory, q.PreferenceRecord.Memory, q.PreferenceRecord.Dataset} {
		if len(s) > 128 {
			return access("invalid_request", "record identity exceeds limit")
		}
	}
	allowed := map[string]bool{}
	for _, id := range q.Datasets {
		if id == "" || len(id) > 128 {
			return access("invalid_request", "invalid read scope")
		}
		allowed[id] = true
	}
	if !allowed[q.Project] || !allowed[q.PreferenceRecord.Dataset] {
		return access("scope_denied", "both record scopes must be authorized")
	}
	return nil
}

func (s *Store) projectPreferenceTx(ctx context.Context, tx *sql.Tx, q records.ProjectPreferenceQuery) (records.ProjectPreferenceComparison, error) {
	out := records.ProjectPreferenceComparison{}
	if err := preferenceScope(q); err != nil {
		return out, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT identity FROM cognee_library_identity WHERE id=1`).Scan(&out.LibraryIdentity); err != nil {
		return out, err
	}
	if q.LibraryIdentity != "" && q.LibraryIdentity != out.LibraryIdentity {
		return out, access("library_changed", "comparison library changed")
	}
	if err := tx.QueryRowContext(ctx, `SELECT id,name,kind,path FROM scopes WHERE id=?`, q.Project).Scan(&out.Project.ID, &out.Project.Name, &out.Project.Kind, &out.Project.Path); err != nil {
		return out, err
	}
	if out.Project.Kind != "project" || out.Project.Path == "" {
		return out, access("scope_denied", "a registered project is required")
	}
	refs := []records.UsageTarget{q.ProjectRecord, q.PreferenceRecord}
	cards := []*records.Version{&out.ProjectRecord, &out.PreferenceRecord}
	for i, ref := range refs {
		v, err := scanVersion(tx.QueryRowContext(ctx, `SELECT `+versionColumns+versionJoins+` WHERE m.id=?`, ref.Memory))
		if err != nil {
			return out, err
		}
		if v.Dataset != ref.Dataset || v.Revision != ref.Revision {
			return out, access("version_changed", "comparison record version or scope changed")
		}
		if v.State != "active" || v.Origin != "knowledge" {
			return out, access("record_unavailable", "comparison requires active knowledge records")
		}
		if i == 0 && v.ScopeKind != "project" || i == 1 && v.ScopeKind != "preference" {
			return out, access("scope_denied", "comparison requires project knowledge and personal preference")
		}
		var stale int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM derived_memories WHERE memory=? AND revision=? AND state='stale'`, ref.Memory, ref.Revision).Scan(&stale); err != nil {
			return out, err
		}
		if stale != 0 {
			return out, access("source_stale", "comparison source has changed")
		}
		var kind, conditions string
		conditionErr := tx.QueryRowContext(ctx, `SELECT kind,conditions FROM global_experiences WHERE memory=? AND revision=(SELECT max(revision) FROM global_experiences WHERE memory=? AND revision<=?)`, ref.Memory, ref.Memory, ref.Revision).Scan(&kind, &conditions)
		if conditionErr == nil {
			v.Experience = &records.ExperienceInfo{Kind: kind, Conditions: conditions}
			if i == 0 {
				out.ProjectConditions = conditions
				out.ProjectConditionsRecorded = true
			} else {
				out.PreferenceConditions = conditions
				out.PreferenceConditionsRecorded = true
			}
		} else if !errors.Is(conditionErr, sql.ErrNoRows) {
			return out, conditionErr
		}
		if err = filterVersionProvenance(ctx, tx, &v, q.Datasets); err != nil {
			return out, err
		}
		*cards[i] = v
	}
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT payload FROM project_preference_decisions WHERE library=? AND project=? AND project_memory=? AND project_revision=? AND preference_memory=? AND preference_revision=? AND state='confirmed' ORDER BY confirmed_ms DESC,rowid DESC LIMIT 1`, out.LibraryIdentity, q.Project, q.ProjectRecord.Memory, q.ProjectRecord.Revision, q.PreferenceRecord.Memory, q.PreferenceRecord.Revision).Scan(&raw)
	if err == nil {
		var d records.ProjectPreferenceDecision
		if err = json.Unmarshal([]byte(raw), &d); err != nil {
			return out, err
		}
		out.Confirmation = &d
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	return out, nil
}

func (s *Store) ProjectPreferenceComparison(ctx context.Context, q records.ProjectPreferenceQuery) (records.ProjectPreferenceComparison, error) {
	if s.Schema < 29 {
		return records.ProjectPreferenceComparison{}, access("comparison_unavailable", "comparison ledger unavailable")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return records.ProjectPreferenceComparison{}, err
	}
	defer tx.Rollback()
	out, err := s.projectPreferenceTx(ctx, tx, q)
	if err != nil {
		return records.ProjectPreferenceComparison{}, err
	}
	return out, tx.Commit()
}

// ConfirmProjectPreference writes only the separate user's applicability
// decision. It neither observes Agent use nor changes either knowledge record.
func (s *Store) ConfirmProjectPreference(ctx context.Context, r records.ProjectPreferenceConfirmation) (records.ProjectPreferenceDecision, error) {
	var out records.ProjectPreferenceDecision
	if s.Schema < 29 {
		return out, access("comparison_unavailable", "comparison ledger unavailable")
	}
	id, err := uuid.Parse(r.OperationID)
	if err != nil || id.String() != r.OperationID || r.LibraryIdentity == "" || !r.ConfirmProjectRule || r.Decision != "use_project" {
		return out, access("confirmation_required", "explicit user project-rule decision required")
	}
	for i, text := range []string{r.ProjectQuote, r.PreferenceQuote, r.Conditions, r.Reason} {
		if !utf8.ValidString(text) || len(text) > 4096 || i < 3 && strings.TrimSpace(text) == "" {
			return out, access("invalid_request", "bounded explicit quotations and applicability required")
		}
	}
	if err = preferenceScope(r.Query); err != nil {
		return out, err
	}
	// Read scope ordering is not a different semantic confirmation.
	scopes := map[string]bool{}
	for _, scope := range r.Query.Datasets {
		scopes[scope] = true
	}
	r.Query.Datasets = nil
	for scope := range scopes {
		r.Query.Datasets = append(r.Query.Datasets, scope)
	}
	sort.Strings(r.Query.Datasets)
	digest := records.MergeDigest(r)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var library string
	if err = tx.QueryRowContext(ctx, `SELECT identity FROM cognee_library_identity WHERE id=1`).Scan(&library); err != nil {
		return out, err
	}
	if library != r.LibraryIdentity {
		return out, access("library_changed", "library restored; review current records again")
	}
	var prior, raw, state string
	err = tx.QueryRowContext(ctx, `SELECT digest,payload,state FROM project_preference_decisions WHERE operation=?`, r.OperationID).Scan(&prior, &raw, &state)
	if err == nil {
		if prior != digest {
			return out, records.ErrConflict
		}
		if state != "confirmed" {
			return out, access("decision_retired", "restored decision is retired")
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	current, e := s.projectPreferenceTx(ctx, tx, r.Query)
	if e != nil {
		return out, e
	}
	if !strings.Contains(current.ProjectRecord.Text, r.ProjectQuote) || !strings.Contains(current.PreferenceRecord.Text, r.PreferenceQuote) {
		return out, access("quote_changed", "quotation does not match its exact current record")
	}
	if err == nil {
		if err = json.Unmarshal([]byte(raw), &out); err != nil {
			return out, err
		}
		return out, tx.Commit()
	}
	out = records.ProjectPreferenceDecision{ID: r.OperationID, LibraryIdentity: library, Project: r.Query.Project, ProjectRecord: r.Query.ProjectRecord, PreferenceRecord: r.Query.PreferenceRecord, ProjectQuote: r.ProjectQuote, PreferenceQuote: r.PreferenceQuote, Conditions: r.Conditions, Reason: r.Reason, Source: "user", Decision: "use_project", ConfirmedMS: time.Now().UnixMilli(), State: "confirmed"}
	data, err := json.Marshal(out)
	if err != nil {
		return records.ProjectPreferenceDecision{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO project_preference_decisions(operation,digest,library,project,project_memory,project_revision,preference_memory,preference_revision,state,payload,confirmed_ms) VALUES(?,?,?,?,?,?,?,?, 'confirmed',?,?)`, r.OperationID, digest, library, r.Query.Project, r.Query.ProjectRecord.Memory, r.Query.ProjectRecord.Revision, r.Query.PreferenceRecord.Memory, r.Query.PreferenceRecord.Revision, string(data), out.ConfirmedMS)
	if err != nil {
		return records.ProjectPreferenceDecision{}, err
	}
	return out, tx.Commit()
}

func (s *Store) ProjectPreferenceDecisions(ctx context.Context, q records.ProjectPreferenceDecisionsQuery) (records.ProjectPreferenceDecisionsPage, error) {
	out := records.ProjectPreferenceDecisionsPage{ProjectRecord: q.ProjectRecord, Decisions: []records.ProjectPreferenceDecision{}}
	if s.Schema < 29 {
		return out, access("comparison_unavailable", "comparison ledger unavailable")
	}
	if q.Project == "" || q.ProjectRecord.Dataset != q.Project || q.ProjectRecord.Memory == "" || q.ProjectRecord.Revision < 1 || len(q.Project) > 128 || len(q.ProjectRecord.Memory) > 128 || len(q.Datasets) == 0 || len(q.Datasets) > 256 {
		return out, access("invalid_request", "one exact project knowledge target and read scopes required")
	}
	allowed := map[string]bool{}
	for _, id := range q.Datasets {
		if id == "" || len(id) > 128 {
			return out, access("invalid_request", "invalid read scope")
		}
		allowed[id] = true
	}
	if !allowed[q.Project] {
		return out, access("scope_denied", "project read scope required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, `SELECT identity FROM cognee_library_identity WHERE id=1`).Scan(&out.LibraryIdentity); err != nil {
		return out, err
	}
	if q.LibraryIdentity != "" && q.LibraryIdentity != out.LibraryIdentity {
		return out, access("library_changed", "comparison library changed")
	}
	if err = tx.QueryRowContext(ctx, `SELECT id,name,kind,path FROM scopes WHERE id=?`, q.Project).Scan(&out.Project.ID, &out.Project.Name, &out.Project.Kind, &out.Project.Path); err != nil {
		return out, err
	}
	if out.Project.Kind != "project" || out.Project.Path == "" {
		return out, access("scope_denied", "registered project required")
	}
	v, err := scanVersion(tx.QueryRowContext(ctx, `SELECT `+versionColumns+versionJoins+` WHERE m.id=?`, q.ProjectRecord.Memory))
	if err != nil {
		return out, err
	}
	if v.Dataset != q.ProjectRecord.Dataset || v.Revision != q.ProjectRecord.Revision {
		return out, access("version_changed", "project knowledge version or scope changed")
	}
	if v.State != "active" || v.Origin != "knowledge" || v.ScopeKind != "project" {
		return out, access("record_unavailable", "active project knowledge required")
	}
	var stale int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM derived_memories WHERE memory=? AND revision=? AND state='stale'`, v.Memory, v.Revision).Scan(&stale); err != nil {
		return out, err
	}
	if stale != 0 {
		return out, access("source_stale", "project source changed")
	}
	scopeRows, e := tx.QueryContext(ctx, `SELECT id FROM scopes ORDER BY id`)
	if e != nil {
		return out, e
	}
	for scopeRows.Next() {
		var id string
		if e = scopeRows.Scan(&id); e != nil {
			scopeRows.Close()
			return out, e
		}
		if allowed[id] {
			out.CoverageDatasets = append(out.CoverageDatasets, id)
		}
	}
	e = scopeRows.Err()
	scopeRows.Close()
	if e != nil {
		return out, e
	}
	rows, err := tx.QueryContext(ctx, `SELECT payload FROM project_preference_decisions WHERE library=? AND project=? AND project_memory=? AND project_revision=? AND state='confirmed' ORDER BY confirmed_ms DESC,rowid DESC`, out.LibraryIdentity, q.Project, v.Memory, v.Revision)
	if err != nil {
		return out, err
	}
	candidates := []records.ProjectPreferenceDecision{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return out, err
		}
		var d records.ProjectPreferenceDecision
		if err = json.Unmarshal([]byte(raw), &d); err != nil {
			rows.Close()
			return out, err
		}
		candidates = append(candidates, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	seen := map[records.UsageTarget]bool{}
	for _, d := range candidates {
		if !allowed[d.PreferenceRecord.Dataset] || seen[d.PreferenceRecord] {
			continue
		}
		seen[d.PreferenceRecord] = true
		comparison, e := s.projectPreferenceTx(ctx, tx, records.ProjectPreferenceQuery{LibraryIdentity: out.LibraryIdentity, Project: q.Project, ProjectRecord: q.ProjectRecord, PreferenceRecord: d.PreferenceRecord, Datasets: q.Datasets})
		if e != nil {
			var denied *records.AccessError
			if errors.As(e, &denied) && (denied.Code == "version_changed" || denied.Code == "record_unavailable" || denied.Code == "source_stale" || denied.Code == "scope_denied") || errors.Is(e, sql.ErrNoRows) {
				continue
			}
			return out, e
		}
		if comparison.Confirmation == nil {
			continue
		}
		if len(out.Decisions) == 50 {
			out.Truncated = true
			break
		}
		out.Decisions = append(out.Decisions, *comparison.Confirmation)
	}
	return out, tx.Commit()
}
