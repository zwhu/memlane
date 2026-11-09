package store

import (
	"context"
	"encoding/json"

	"github.com/zwhu/memlane/internal/records"
)

// Only the live daemon task owner can read this page. Cursor and page size
// cannot select a dataset, raw record, historical version or another Agent.
func (s *Store) ProjectTaskKnowledge(ctx context.Context, id string, attempt int64, after string) (records.ProjectKnowledgeContextPage, error) {
	page := records.ProjectKnowledgeContextPage{Items: []records.ProjectKnowledgeContextItem{}}
	if after != "" && !projectUUID(after) {
		return page, access("invalid_request", "knowledge page cursor required")
	}
	current, err := s.ProjectTask(ctx, id)
	if err != nil {
		return page, err
	}
	if current.AccessState != "available" || current.Attempt != attempt {
		return page, access("project_task_closed", "current authorized repository task required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	task, err := s.projectRunTx(ctx, tx, id, attempt, false)
	if err != nil {
		return page, err
	}
	var pid int
	var gone bool
	if err = tx.QueryRowContext(ctx, `SELECT process_pid,group_gone FROM project_task_runs WHERE task=? AND attempt=?`, id, attempt).Scan(&pid, &gone); err != nil {
		return page, err
	}
	if task.State != "organizing" || pid <= 1 || gone {
		return page, access("project_task_closed", "running model knowledge context required")
	}
	const joins = ` FROM memories m JOIN versions v ON v.memory=m.id AND v.revision=m.current_revision JOIN memory_metadata md ON md.memory=m.id `
	const filter = `m.dataset=? AND m.state='active' AND md.origin!='raw' AND NOT EXISTS(SELECT 1 FROM derived_memories d WHERE d.memory=m.id AND d.revision=m.current_revision AND d.state='stale')`
	if err = tx.QueryRowContext(ctx, `SELECT count(*)`+joins+` WHERE `+filter, task.Snapshot.Project).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT m.id,m.current_revision,v.title,v.text`+joins+` WHERE `+filter+` AND (?='' OR m.id>?) ORDER BY m.id LIMIT 21`, task.Snapshot.Project, after, after)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	size := 0
	for rows.Next() {
		var item records.ProjectKnowledgeContextItem
		if err = rows.Scan(&item.Memory, &item.Revision, &item.Title, &item.Text); err != nil {
			return page, err
		}
		encoded, e := json.Marshal(item)
		if e != nil || len(encoded) > 1024*1024 {
			return page, access("project_context_limit", "individual knowledge context exceeded page budget")
		}
		if len(page.Items) == 20 || size+len(encoded) > 1024*1024 && len(page.Items) > 0 {
			page.Next = page.Items[len(page.Items)-1].Memory
			break
		}
		page.Items = append(page.Items, item)
		size += len(encoded)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	if err = ctx.Err(); err != nil {
		return page, err
	}
	return page, nil
}
