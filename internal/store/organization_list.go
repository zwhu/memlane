package store

import (
	"context"
	"database/sql"
	"github.com/zwhu/memlane/internal/records"
)

func (s *Store) ListOrganizations(ctx context.Context, q records.MergeListQuery) (records.OrganizationPage, error) {
	out := records.OrganizationPage{Items: []records.OrganizationSummary{}}
	if q.Limit == 0 {
		q.Limit = 30
	}
	if q.Limit < 1 || q.Limit > 100 {
		return out, records.MergeError("invalid_request")
	}
	if q.Project != "" {
		var kind string
		if e := s.DB.QueryRowContext(ctx, `SELECT kind FROM scopes WHERE id=?`, q.Project).Scan(&kind); e != nil || kind != "project" {
			return out, records.MergeError("scope_denied")
		}
	}
	before := ""
	if q.Before != "" {
		e := s.DB.QueryRowContext(ctx, `SELECT created_at FROM organization_tasks WHERE id=? AND (?='' OR project=?)`, q.Before, q.Project, q.Project).Scan(&before)
		if e == sql.ErrNoRows {
			return out, records.MergeError("invalid_request")
		}
		if e != nil {
			return out, e
		}
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT t.id,t.project,t.state,COALESCE(NULLIF(json_extract(r.payload,'$.items[0].title'),''),(SELECT json_extract(a.snapshot,'$.inputs[0].title') FROM organization_attempts a WHERE a.task=t.id ORDER BY a.attempt DESC LIMIT 1),''),t.created_at FROM organization_tasks t LEFT JOIN organization_results r ON r.task=t.id AND r.attempt=(SELECT max(attempt) FROM organization_results WHERE task=t.id) WHERE (?='' OR t.project=?) AND (?='' OR t.created_at<? OR (t.created_at=? AND t.id<?)) AND (?='' OR EXISTS(SELECT 1 FROM organization_items i WHERE i.task=t.id AND json_extract(i.receipt,'$.memory')=?)) AND (?=0 OR t.state IN ('queued','running','applying')) ORDER BY CASE WHEN ? AND t.state IN ('running','applying') THEN 0 ELSE 1 END,t.created_at DESC,t.id DESC LIMIT ?`, q.Project, q.Project, q.Before, before, before, q.Before, q.Memory, q.Memory, q.ActiveOnly, q.ActiveOnly, q.Limit+1)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var v records.OrganizationSummary
		if e = rows.Scan(&v.ID, &v.Project, &v.State, &v.Title, &v.CreatedAt); e != nil {
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	if len(out.Items) > q.Limit {
		out.Items = out.Items[:q.Limit]
		out.Next = out.Items[len(out.Items)-1].ID
	}
	return out, nil
}
