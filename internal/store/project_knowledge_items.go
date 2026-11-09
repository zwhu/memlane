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

func (s *Store) PrepareProjectKnowledge(ctx context.Context, id string, attempt int64, p records.ProjectKnowledgeProposal, sources []records.ProjectRepositorySource) error {
	t, err := s.ProjectTask(ctx, id)
	if err != nil {
		return err
	}
	if t.AccessState != "available" {
		return access("project_scope_changed", "task repository or binding changed")
	}
	if err = validateProjectKnowledge(t, p, sources); err != nil {
		return err
	}
	sources = append([]records.ProjectRepositorySource(nil), sources...)
	sort.Slice(sources, func(i, j int) bool { return sources[i].ID < sources[j].ID })
	digest := UsageDigest(struct {
		Proposal records.ProjectKnowledgeProposal
		Sources  []records.ProjectRepositorySource
	}{p, sources})
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t, err = s.projectRunTx(ctx, tx, id, attempt, false)
	if err != nil {
		return err
	}
	var old, executionJSON string
	if err = tx.QueryRowContext(ctx, `SELECT result_digest,execution FROM project_task_runs WHERE task=? AND attempt=?`, id, attempt).Scan(&old, &executionJSON); err != nil {
		return err
	}
	if old != "" {
		if old != digest {
			return records.ErrConflict
		}
		return nil
	}
	var execution records.ProjectTaskExecution
	if json.Unmarshal([]byte(executionJSON), &execution) != nil || !execution.Terminal || !execution.Reaped || !execution.GroupGone || execution.Model != t.Snapshot.Executor.Model || execution.Provider == "" || execution.PermissionProfile != "memlane_project" || execution.Thread == "" || execution.Turn == "" {
		return access("project_model_incomplete", "matching model and cleanup receipts required")
	}
	if t.State != "organizing" {
		return access("project_task_closed", "task no longer accepting a model result")
	}
	for _, source := range sources {
		b, _ := json.Marshal(source)
		if _, err = tx.ExecContext(ctx, `INSERT INTO project_task_sources(task,source,payload) VALUES(?,?,?)`, id, source.ID, string(b)); err != nil {
			return err
		}
	}
	for position, item := range p.Items {
		b, _ := json.Marshal(item)
		if _, err = tx.ExecContext(ctx, `INSERT INTO project_task_items(task,position,payload) VALUES(?,?,?)`, id, position+1, string(b)); err != nil {
			return err
		}
	}
	b, _ := json.Marshal(p)
	if _, err = tx.ExecContext(ctx, `UPDATE project_task_runs SET result_digest=?,proposal=? WHERE task=? AND attempt=?`, digest, string(b), id, attempt); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE project_tasks SET state='saving' WHERE id=? AND attempt=?`, id, attempt); err != nil {
		return err
	}
	return tx.Commit()
}

type projectItemReceipt struct {
	Added   *records.ProjectTaskSaved  `json:"added,omitempty"`
	Sources []records.ProjectTaskSaved `json:"sources,omitempty"`
}

func projectReceiptTx(ctx context.Context, tx *sql.Tx, id string, attempt int64) (records.ProjectTaskReceipt, error) {
	r := records.ProjectTaskReceipt{Task: id, Attempt: attempt, Persisted: true, Added: []records.ProjectTaskSaved{}, Sources: []records.ProjectTaskSaved{}}
	rows, err := tx.QueryContext(ctx, `SELECT state,receipt FROM project_task_items WHERE task=? ORDER BY position`, id)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var state, raw string
		if err = rows.Scan(&state, &raw); err != nil {
			return r, err
		}
		switch state {
		case "skipped":
			r.Skipped++
		case "failed":
			r.SaveFailed++
		case "applied":
			var receipt projectItemReceipt
			if err = json.Unmarshal([]byte(raw), &receipt); err != nil {
				return r, err
			}
			if receipt.Added != nil {
				r.Added = append(r.Added, *receipt.Added)
			}
			for _, source := range receipt.Sources {
				if !seen[source.Memory] {
					seen[source.Memory] = true
					r.Sources = append(r.Sources, source)
				}
			}
		}
	}
	sort.Slice(r.Sources, func(i, j int) bool { return r.Sources[i].Memory < r.Sources[j].Memory })
	return r, rows.Err()
}
func updateProjectReceipt(ctx context.Context, tx *sql.Tx, id string, attempt int64) (records.ProjectTaskReceipt, error) {
	r, err := projectReceiptTx(ctx, tx, id, attempt)
	if err != nil {
		return r, err
	}
	b, _ := json.Marshal(r)
	_, err = tx.ExecContext(ctx, `UPDATE project_task_runs SET receipt=? WHERE task=? AND attempt=?`, string(b), id, attempt)
	return r, err
}

func (s *Store) applyProjectKnowledgeItem(ctx context.Context, id string, attempt int64, position int) error {
	current, err := s.ProjectTask(ctx, id)
	if err != nil {
		return err
	}
	if current.AccessState != "available" {
		return access("project_scope_changed", "repository task scope changed")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t, err := s.projectRunTx(ctx, tx, id, attempt, false)
	if err != nil {
		return err
	}
	if t.State != "saving" {
		return access("project_task_closed", "task not saving knowledge")
	}
	var state, payload string
	if err = tx.QueryRowContext(ctx, `SELECT state,payload FROM project_task_items WHERE task=? AND position=?`, id, position).Scan(&state, &payload); err != nil {
		return err
	}
	if state != "pending" {
		return nil
	}
	var item records.ProjectKnowledgeItem
	if err = json.Unmarshal([]byte(payload), &item); err != nil {
		return err
	}
	memory := projectKnowledgeMemory(t.Snapshot.Project, item)
	var existingProject string
	err = tx.QueryRowContext(ctx, `SELECT dataset FROM memories WHERE id=?`, memory).Scan(&existingProject)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil {
		if existingProject != t.Snapshot.Project {
			return access("scope_denied", "knowledge identity is outside project")
		}
		if _, err = tx.ExecContext(ctx, `UPDATE project_task_items SET state='skipped' WHERE task=? AND position=?`, id, position); err != nil {
			return err
		}
		if _, err = updateProjectReceipt(ctx, tx, id, attempt); err != nil {
			return err
		}
		return tx.Commit()
	}
	receipt := projectItemReceipt{}
	byID := map[string]records.ProjectRepositorySource{}
	for _, quote := range item.Sources {
		var raw string
		if err = tx.QueryRowContext(ctx, `SELECT payload FROM project_task_sources WHERE task=? AND source=?`, id, quote.Source).Scan(&raw); err != nil {
			return err
		}
		var source records.ProjectRepositorySource
		if err = json.Unmarshal([]byte(raw), &source); err != nil {
			return err
		}
		byID[source.ID] = source
		sourceMemory := projectSourceMemory(t.Snapshot.Project, source)
		var text, dataset, origin, sourceState string
		var revision int64
		err = tx.QueryRowContext(ctx, `SELECT v.text,m.dataset,md.origin,m.state,m.current_revision FROM memories m JOIN versions v ON v.memory=m.id AND v.revision=m.current_revision JOIN memory_metadata md ON md.memory=m.id WHERE m.id=?`, sourceMemory).Scan(&text, &dataset, &origin, &sourceState, &revision)
		if err == sql.ErrNoRows {
			v, e := s.applyTx(ctx, tx, Mutation{Kind: "save", OperationID: "project-source:" + sourceMemory, Memory: sourceMemory, Dataset: t.Snapshot.Project, Title: filepath.Base(source.Path), Text: source.Text, Origin: "raw", Source: t.Snapshot.Executor.Agent + "：仓库出处", SourceDetail: &records.AgentSource{References: []string{"repo://" + t.Snapshot.Project + "/" + source.Path + "?sha256=" + source.SHA256}}})
			if e != nil {
				return e
			}
			revision = v.Revision
		} else if err != nil {
			return err
		} else if text != source.Text || dataset != t.Snapshot.Project || origin != "raw" || sourceState != "active" || revision != 1 {
			return access("project_source_changed", "existing original changed; never overwrite it")
		}
		receipt.Sources = append(receipt.Sources, records.ProjectTaskSaved{Memory: sourceMemory, Revision: revision})
	}
	refs := []string{}
	for _, q := range item.Sources {
		refs = append(refs, projectSourceMemory(t.Snapshot.Project, byID[q.Source])+"@v1")
	}
	v, err := s.applyTx(ctx, tx, Mutation{Kind: "save", OperationID: "project-knowledge:" + memory, Memory: memory, Dataset: t.Snapshot.Project, Title: item.Title, Text: item.Text, Origin: "knowledge", Source: t.Snapshot.Executor.Agent + "：项目知识整理", SourceDetail: &records.AgentSource{References: refs}})
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO derived_memories(memory,revision,state) VALUES(?,?,'valid')`, memory, v.Revision); err != nil {
		return err
	}
	for _, quote := range item.Sources {
		source := byID[quote.Source]
		sourceMemory := projectSourceMemory(t.Snapshot.Project, source)
		snapshot := records.OrganizationInput{MergeInput: records.MergeInput{Memory: sourceMemory, Revision: 1, Title: source.Path, Text: source.Text, Source: t.Snapshot.Executor.Agent + "：仓库出处"}, Origin: "raw", Roots: []records.MergeTarget{{Memory: sourceMemory, Revision: 1}}}
		b, _ := json.Marshal(snapshot)
		if _, err = tx.ExecContext(ctx, `INSERT INTO organization_sources(memory,revision,source_memory,source_revision,project,snapshot,quote) VALUES(?,?,?,?,?,?,?)`, memory, v.Revision, sourceMemory, 1, t.Snapshot.Project, string(b), quote.Quote); err != nil {
			return err
		}
	}
	receipt.Added = &records.ProjectTaskSaved{Memory: memory, Revision: v.Revision}
	b, _ := json.Marshal(receipt)
	if _, err = tx.ExecContext(ctx, `UPDATE project_task_items SET state='applied',receipt=? WHERE task=? AND position=?`, string(b), id, position); err != nil {
		return err
	}
	if _, err = updateProjectReceipt(ctx, tx, id, attempt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ApplyProjectKnowledge(ctx context.Context, id string, attempt int64) (records.ProjectTaskReceipt, error) {
	var empty records.ProjectTaskReceipt
	rows, err := s.DB.QueryContext(ctx, `SELECT position FROM project_task_items WHERE task=? AND state='pending' ORDER BY position`, id)
	if err != nil {
		return empty, err
	}
	var positions []int
	for rows.Next() {
		var position int
		if err = rows.Scan(&position); err != nil {
			rows.Close()
			return empty, err
		}
		positions = append(positions, position)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return empty, err
	}
	for _, position := range positions {
		if err = s.applyProjectKnowledgeItem(ctx, id, attempt, position); err == nil {
			continue
		}
		if ctx.Err() != nil {
			return empty, ctx.Err()
		}
		var denied *records.AccessError
		if errors.As(err, &denied) && (denied.Code == "project_scope_changed" || denied.Code == "binding_changed" || denied.Code == "project_run_invalid" || denied.Code == "project_task_closed") {
			return empty, err
		}
		tx, e := s.DB.BeginTx(ctx, nil)
		if e != nil {
			return empty, errors.Join(err, e)
		}
		if _, e = s.projectRunTx(ctx, tx, id, attempt, false); e == nil {
			code := "project_save_failed"
			if denied != nil {
				code = denied.Code
			}
			_, e = tx.ExecContext(ctx, `UPDATE project_task_items SET state='failed',error_code=? WHERE task=? AND position=? AND state='pending'`, code, id, position)
		}
		if e == nil {
			_, e = updateProjectReceipt(ctx, tx, id, attempt)
		}
		if e == nil {
			e = tx.Commit()
		}
		tx.Rollback()
		if e != nil {
			return empty, errors.Join(err, e)
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	t, err := s.projectRunTx(ctx, tx, id, attempt, false)
	if err != nil {
		return empty, err
	}
	if t.State != "saving" && t.State != "indexing" {
		return empty, access("project_task_closed", "task no longer saving")
	}
	r, err := updateProjectReceipt(ctx, tx, id, attempt)
	if err != nil {
		return empty, err
	}
	code := ""
	if r.SaveFailed > 0 {
		code = "project_save_partial"
	}
	if _, err = tx.ExecContext(ctx, `UPDATE project_tasks SET state='indexing',error_code=? WHERE id=? AND attempt=?`, code, id, attempt); err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return r, nil
}
