package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

const projectTaskColumns = `id,attempt,state,cancel_requested,error_code,snapshot,created_ms`

func scanProjectTask(row scanner) (records.ProjectTask, error) {
	var t records.ProjectTask
	var snapshot string
	err := row.Scan(&t.ID, &t.Attempt, &t.State, &t.Cancelled, &t.Error, &snapshot, &t.CreatedMS)
	if err == nil {
		err = json.Unmarshal([]byte(snapshot), &t.Snapshot)
	}
	return t, err
}

func projectDigest(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32 && value == strings.ToLower(value)
}

func (s *Store) projectTaskWorkspace(ctx context.Context, project string) (string, string, error) {
	var path string
	if err := s.DB.QueryRowContext(ctx, `SELECT path FROM scopes WHERE id=? AND kind='project'`, project).Scan(&path); err != nil {
		return "", "", err
	}
	root, common, err := repository(ctx, path)
	if err != nil {
		return "", "", err
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		return "", "", err
	}
	commonInfo, err := os.Stat(common)
	if err != nil {
		return "", "", err
	}
	r, rootOK := rootInfo.Sys().(*syscall.Stat_t)
	c, commonOK := commonInfo.Sys().(*syscall.Stat_t)
	if !rootOK || !commonOK {
		return "", "", access("workspace_unavailable", "repository identity unavailable")
	}
	identity := UsageDigest(struct {
		Project, Root, Common                            string
		RootDevice, RootInode, CommonDevice, CommonInode uint64
	}{project, root, common, uint64(r.Dev), r.Ino, uint64(c.Dev), c.Ino})
	return root, identity, nil
}

func sameProjectExecutor(b records.ProjectBinding, e records.ProjectTaskExecutor) bool {
	return b.State == "available" && b.ConnectionID == e.ConnectionID && b.Generation == e.Generation && b.Agent == e.Agent
}

// The daemon supplies a resolved executor and authorized session attribution.
// This method persists intent only; it never starts a process, reads history,
// invokes global memory processing or declares a model/save/index result.
func (s *Store) StartProjectTask(ctx context.Context, d records.ProjectTaskStart, executor records.ProjectTaskExecutor, sessions []records.ProjectSessionSource) (records.ProjectTask, bool, error) {
	var empty records.ProjectTask
	if s.Schema < 18 || !projectUUID(d.OperationID) || !projectUUID(d.Project) || d.BindingRevision < 1 ||
		strings.TrimSpace(executor.Model) == "" || len(executor.Model) > 512 || strings.TrimSpace(executor.Version) == "" || len(executor.Version) > 512 || !projectDigest(executor.ConfigurationDigest) {
		return empty, false, access("invalid_request", "resolved current project executor required")
	}
	b, err := s.ProjectBinding(ctx, d.Project)
	if err != nil {
		return empty, false, err
	}
	if b.Revision != d.BindingRevision || !sameProjectExecutor(b, executor) {
		return empty, false, access("binding_changed", "project organizer changed; confirm its current scope")
	}
	if !d.IncludeHistory && (len(d.Sessions) != 0 || len(sessions) != 0) || d.IncludeHistory && len(d.Sessions) == 0 || len(d.Sessions) != len(sessions) {
		return empty, false, access("history_scope_required", "explicit authorized history selection required")
	}
	resolved := make(map[string]records.ProjectSessionSource, len(sessions))
	for _, source := range sessions {
		if source.ID == "" || len(source.ID) > 256 || strings.TrimSpace(source.ID) != source.ID || strings.ContainsAny(source.ID, "/\\\r\n") ||
			!projectDigest(source.Digest) || source.Project != d.Project || source.Agent != b.Agent {
			return empty, false, access("history_scope_denied", "history must belong to the bound Agent and project")
		}
		if _, exists := resolved[source.ID]; exists {
			return empty, false, access("invalid_request", "duplicate history selection")
		}
		resolved[source.ID] = source
	}
	d.Sessions = append([]records.ProjectSessionSelection(nil), d.Sessions...)
	sort.Slice(d.Sessions, func(i, j int) bool { return d.Sessions[i].ID < d.Sessions[j].ID })
	selected := make([]records.ProjectSessionSource, 0, len(d.Sessions))
	for i, choice := range d.Sessions {
		source, ok := resolved[choice.ID]
		if !ok || choice.Digest != source.Digest || i > 0 && d.Sessions[i-1].ID == choice.ID {
			return empty, false, access("history_changed", "selected session changed or was not authorized")
		}
		selected = append(selected, source)
	}
	digest := UsageDigest(d)
	prior, err := scanProjectTask(s.DB.QueryRowContext(ctx, `SELECT `+projectTaskColumns+` FROM project_tasks WHERE operation_id=?`, d.OperationID))
	if err == nil {
		var old string
		if err = s.DB.QueryRowContext(ctx, `SELECT request_digest FROM project_tasks WHERE id=?`, prior.ID).Scan(&old); err != nil {
			return empty, false, err
		}
		if old != digest {
			return empty, false, records.ErrConflict
		}
		prior, err = s.ProjectTask(ctx, prior.ID)
		if err == nil && prior.Snapshot.Executor != executor {
			prior.AccessState = "configuration_changed"
		}
		return prior, false, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return empty, false, err
	}
	root, identity, err := s.projectTaskWorkspace(ctx, d.Project)
	if err != nil {
		return empty, false, err
	}
	snapshot := records.ProjectTaskSnapshot{Project: d.Project, BindingRevision: b.Revision, Workspace: root, WorkspaceID: identity,
		Executor: executor, IncludeHistory: d.IncludeHistory, Sessions: selected}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return empty, false, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return empty, false, err
	}
	defer tx.Rollback()
	current, err := s.projectBindingTx(ctx, tx, d.Project)
	if err != nil {
		return empty, false, err
	}
	if current.Revision != b.Revision || !sameProjectExecutor(current, executor) {
		return empty, false, access("binding_changed", "project organizer changed during task preparation")
	}
	prior, err = scanProjectTask(tx.QueryRowContext(ctx, `SELECT `+projectTaskColumns+` FROM project_tasks WHERE operation_id=?`, d.OperationID))
	if err == nil {
		var old string
		if err = tx.QueryRowContext(ctx, `SELECT request_digest FROM project_tasks WHERE id=?`, prior.ID).Scan(&old); err != nil {
			return empty, false, err
		}
		if old != digest {
			return empty, false, records.ErrConflict
		}
		tx.Rollback()
		prior, err = s.ProjectTask(ctx, prior.ID)
		if err == nil && prior.Snapshot.Executor != executor {
			prior.AccessState = "configuration_changed"
		}
		return prior, false, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return empty, false, err
	}
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM project_tasks WHERE project=? AND state IN ('queued','starting','organizing','saving','indexing','cancelling')`, d.Project).Scan(&active); err != nil {
		return empty, false, err
	}
	if active != 0 {
		return empty, false, access("project_task_active", "return to the current project task")
	}
	id := uuid.NewString()
	_, err = tx.ExecContext(ctx, `INSERT INTO project_tasks(id,operation_id,request_digest,project,snapshot,state,created_ms) VALUES(?,?,?,?,?,'queued',?)`, id, d.OperationID, digest, d.Project, string(encoded), time.Now().UnixMilli())
	if err != nil {
		return empty, false, err
	}
	t, err := scanProjectTask(tx.QueryRowContext(ctx, `SELECT `+projectTaskColumns+` FROM project_tasks WHERE id=?`, id))
	if err != nil {
		return empty, false, err
	}
	t.AccessState = "available"
	return t, true, tx.Commit()
}

func (s *Store) ProjectTask(ctx context.Context, id string) (records.ProjectTask, error) {
	t, err := scanProjectTask(s.DB.QueryRowContext(ctx, `SELECT `+projectTaskColumns+` FROM project_tasks WHERE id=?`, id))
	if err != nil {
		return t, err
	}
	if err = s.projectTaskRuntime(ctx, &t); err != nil {
		return t, err
	}
	b, err := s.ProjectBinding(ctx, t.Snapshot.Project)
	if err != nil {
		return t, err
	}
	t.AccessState = b.State
	if b.Revision != t.Snapshot.BindingRevision || !sameProjectExecutor(b, t.Snapshot.Executor) {
		if b.State == "available" {
			t.AccessState = "binding_changed"
		}
		return t, nil
	}
	root, identity, err := s.projectTaskWorkspace(ctx, t.Snapshot.Project)
	if err != nil || root != t.Snapshot.Workspace || identity != t.Snapshot.WorkspaceID {
		t.AccessState = "workspace_changed"
	}
	return t, nil
}

func (s *Store) CancelProjectTask(ctx context.Context, id string) (records.ProjectTask, error) {
	_, err := s.DB.ExecContext(ctx, `UPDATE project_tasks SET cancel_requested=1,
 state=CASE WHEN state='queued' THEN 'cancelled' ELSE 'cancelling' END
 WHERE id=? AND state IN ('queued','starting','organizing','saving','indexing','cancelling')`, id)
	if err != nil {
		return records.ProjectTask{}, err
	}
	return s.ProjectTask(ctx, id)
}

func (s *Store) ListProjectTasks(ctx context.Context, q records.MergeListQuery) (records.ProjectTaskPage, error) {
	out := records.ProjectTaskPage{Items: []records.ProjectTaskSummary{}}
	if s.Schema < 18 || q.Project != "" && !projectUUID(q.Project) || q.Memory != "" {
		return out, access("invalid_request", "project task query required")
	}
	if q.Limit == 0 {
		q.Limit = 30
	}
	if q.Limit < 1 || q.Limit > 100 {
		return out, access("invalid_request", "bounded task page required")
	}
	if q.Project != "" {
		var kind string
		if err := s.DB.QueryRowContext(ctx, `SELECT kind FROM scopes WHERE id=?`, q.Project).Scan(&kind); err != nil {
			return out, err
		}
		if kind != "project" {
			return out, access("scope_denied", "project task scope required")
		}
	}
	before := int64(0)
	if q.Before != "" {
		if err := s.DB.QueryRowContext(ctx, `SELECT created_ms FROM project_tasks WHERE id=? AND (?='' OR project=?)`, q.Before, q.Project, q.Project).Scan(&before); err != nil {
			return out, err
		}
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,project,state,attempt,created_ms FROM project_tasks
 WHERE (?='' OR project=?) AND (?='' OR created_ms<? OR (created_ms=? AND id<?))
 AND (?=0 OR state IN ('queued','starting','organizing','saving','indexing','cancelling'))
 ORDER BY created_ms DESC,id DESC LIMIT ?`, q.Project, q.Project, q.Before, before, before, q.Before, q.ActiveOnly, q.Limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var item records.ProjectTaskSummary
		if err = rows.Scan(&item.ID, &item.Project, &item.State, &item.Attempt, &item.CreatedMS); err != nil {
			return out, err
		}
		out.Items = append(out.Items, item)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > q.Limit {
		out.Items = out.Items[:q.Limit]
		out.Next = out.Items[len(out.Items)-1].ID
	}
	return out, nil
}
