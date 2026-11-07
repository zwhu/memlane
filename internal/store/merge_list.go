package store

import (
	"context"
	"database/sql"
	"github.com/zwhu/memlane/internal/records"
)

// MergeList reads summaries only. Details revalidate pending sources through
// MergeTask; a list snapshot never authorizes review or claims index readiness.
func (s *Store) MergeList(ctx context.Context, q records.MergeListQuery) (records.MergePage, error) {
	out := records.MergePage{Items: []records.MergeSummary{}}
	if q.Limit == 0 {
		q.Limit = 30
	}
	if q.Limit < 1 || q.Limit > 100 {
		return out, records.MergeError("invalid_request")
	}
	if q.Project != "" {
		var kind string
		if err := s.DB.QueryRowContext(ctx, `SELECT kind FROM scopes WHERE id=?`, q.Project).Scan(&kind); err != nil || kind != "project" {
			return out, records.MergeError("scope_denied")
		}
	}
	beforeDate := ""
	if q.Before != "" {
		err := s.DB.QueryRowContext(ctx, `SELECT created_at FROM merge_tasks WHERE id=? AND (?='' OR project=?)`, q.Before, q.Project, q.Project).Scan(&beforeDate)
		if err == sql.ErrNoRows {
			return out, records.MergeError("invalid_request")
		}
		if err != nil {
			return out, err
		}
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT t.id,t.project,t.state,CASE WHEN p.state='pending' AND EXISTS (SELECT 1 FROM merge_attempts a, json_each(a.snapshot,'$.inputs') j LEFT JOIN memories m ON m.id=json_extract(j.value,'$.memory') WHERE a.task=t.id AND a.attempt=p.attempt AND (m.id IS NULL OR m.state<>'active' OR m.dataset<>t.project OR m.current_revision<>json_extract(j.value,'$.revision'))) THEN 'expired' ELSE COALESCE(p.state,'none') END,COALESCE(json_extract(p.payload,'$.title'),''),t.created_at
 FROM merge_tasks t LEFT JOIN merge_proposals p ON p.task=t.id
 WHERE (?='' OR EXISTS (SELECT 1 FROM merge_attempts a,json_each(a.snapshot,'$.inputs') j WHERE a.task=t.id AND json_extract(j.value,'$.memory')=?) OR EXISTS (SELECT 1 FROM merge_receipts r WHERE r.proposal=p.id AND r.memory=?)) AND (?='' OR t.project=?) AND (?='' OR t.created_at<? OR (t.created_at=? AND t.id<?))
 ORDER BY t.created_at DESC,t.id DESC LIMIT ?`, q.Memory, q.Memory, q.Memory, q.Project, q.Project, q.Before, beforeDate, beforeDate, q.Before, q.Limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v records.MergeSummary
		if err = rows.Scan(&v.ID, &v.Project, &v.State, &v.ReviewState, &v.Title, &v.CreatedAt); err != nil {
			return out, err
		}
		out.Items = append(out.Items, v)
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
