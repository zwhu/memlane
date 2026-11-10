package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"

	"github.com/zwhu/memlane/internal/records"
)

func (s *Store) ProjectTaskForIntent(ctx context.Context, d records.ProjectTaskStart) (records.ProjectTask, bool, error) {
	var out records.ProjectTask
	if !projectUUID(d.OperationID) || !projectUUID(d.Project) {
		return out, false, access("invalid_request", "canonical task intent required")
	}
	var id, digest string
	err := s.DB.QueryRowContext(ctx, `SELECT id,request_digest FROM project_tasks WHERE operation_id=?`, d.OperationID).Scan(&id, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	d.Sessions = append([]records.ProjectSessionSelection(nil), d.Sessions...)
	sort.Slice(d.Sessions, func(i, j int) bool { return d.Sessions[i].ID < d.Sessions[j].ID })
	if digest != UsageDigest(d) {
		return out, true, records.ErrConflict
	}
	out, err = s.ProjectTask(ctx, id)
	return out, true, err
}

func projectOriginalPresentation(t records.ProjectTask, source records.ProjectRepositorySource) (string, string, *records.AgentSource) {
	if source.History != nil {
		original := *source.History
		return "Codex 历史会话 · " + source.History.CapturedAt, t.Snapshot.Executor.Agent + "：历史会话", &records.AgentSource{History: &original, Fragment: "历史会话", References: []string{"codex://" + source.History.Session + "?sha256=" + source.History.Digest}}
	}
	return filepath.Base(source.Path), t.Snapshot.Executor.Agent + "：仓库出处", &records.AgentSource{References: []string{"repo://" + t.Snapshot.Project + "/" + source.Path + "?sha256=" + source.SHA256}}
}

func migrateProjectHistories(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE project_task_histories(
 task TEXT NOT NULL REFERENCES project_tasks(id),session TEXT NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(task,session));
CREATE TRIGGER project_history_immutable BEFORE UPDATE ON project_task_histories
 BEGIN SELECT RAISE(ABORT,'project history source is immutable'); END;
PRAGMA user_version=21;`)
	return err
}

// Permission is the current project binding and computer-level connection grant.
// Listing never invokes a model; user confirmation is checked by the daemon.
func (s *Store) ProjectHistoryAccess(ctx context.Context, q records.ProjectHistoryQuery) (records.ProjectBinding, string, string, error) {
	var b records.ProjectBinding
	if !projectUUID(q.Project) || q.BindingRevision < 1 {
		return b, "", "", access("invalid_request", "explicit project history scope required")
	}
	b, err := s.ProjectBinding(ctx, q.Project)
	if err != nil {
		return b, "", "", err
	}
	if b.State != "available" || b.Agent != "codex" || b.Revision != q.BindingRevision {
		return b, "", "", access("history_scope_denied", "current verified Codex project grant required")
	}
	workspace, identity, err := s.projectTaskWorkspace(ctx, q.Project)
	return b, workspace, identity, err
}

func validateFrozenProjectHistory(d records.ProjectTaskStart, sources []records.ProjectRepositorySource) error {
	if !d.IncludeHistory {
		if len(sources) != 0 || d.HistoryWorkspaceID != "" {
			return access("history_scope_denied", "unchecked task cannot carry historical material")
		}
		return nil
	}
	if !projectDigest(d.HistoryWorkspaceID) || len(sources) != len(d.Sessions) || len(sources) == 0 {
		return access("history_scope_required", "all selected history versions must be frozen")
	}
	selected := map[string]string{}
	for _, choice := range d.Sessions {
		selected[choice.ID] = choice.Digest
	}
	total := 0
	for _, source := range sources {
		if source.History == nil || source.History.Agent != "codex" || source.History.Session == "" || selected[source.History.Session] != source.History.Digest || source.History.Digest == "" || source.History.CLIVersion != "0.167.0-alpha.3.2" {
			return access("history_scope_denied", "selected canonical Codex history version required")
		}
		delete(selected, source.History.Session)
		total += len(source.Text)
		if len(source.Text) == 0 || len(source.Text) > 4*1024*1024 || total > 32*1024*1024 {
			return access("history_limit", "selected history exceeds source budget; nothing truncated")
		}
	}
	if len(selected) != 0 {
		return access("history_scope_required", "selected historical scope incomplete")
	}
	return nil
}

// The frozen material is only released to the live model owner of this task.
// A UI/model cannot choose another task, project, attempt, session or version.
func (s *Store) ProjectTaskHistory(ctx context.Context, id string, attempt int64, session string) (records.ProjectRepositorySource, error) {
	var out records.ProjectRepositorySource
	current, err := s.ProjectTask(ctx, id)
	if err != nil {
		return out, err
	}
	if current.AccessState != "available" || current.Attempt != attempt || !current.Snapshot.IncludeHistory {
		return out, access("history_scope_denied", "current authorized historical task required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	task, err := s.projectRunTx(ctx, tx, id, attempt, false)
	if err != nil {
		return out, err
	}
	var pid int
	var gone bool
	if err = tx.QueryRowContext(ctx, `SELECT process_pid,group_gone FROM project_task_runs WHERE task=? AND attempt=?`, id, attempt).Scan(&pid, &gone); err != nil {
		return out, err
	}
	if task.State != "organizing" || pid <= 1 || gone {
		return out, access("project_task_closed", "live historical model owner required")
	}
	var raw string
	if err = tx.QueryRowContext(ctx, `SELECT payload FROM project_task_histories WHERE task=? AND session=?`, id, session).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, access("history_scope_denied", "session not selected in this task")
		}
		return out, err
	}
	if err = json.Unmarshal([]byte(raw), &out); err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	return out, nil
}
