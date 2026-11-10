package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/zwhu/memlane/internal/records"
)

func (s *Store) ListOrganizations(ctx context.Context, q records.OrganizationListQuery) (records.OrganizationPage, error) {
	out := records.OrganizationPage{Items: []records.OrganizationSummary{}}
	if q.Limit == 0 {
		q.Limit = 30
	}
	if q.Limit < 1 || q.Limit > 100 || q.Origin != "" && q.Origin != "manual" && q.Origin != "schedule" || q.SinceMS < 0 || q.SinceMS > 253402300799000 {
		return out, records.MergeError("invalid_request")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if q.Project != "" {
		var kind string
		if e := tx.QueryRowContext(ctx, `SELECT kind FROM scopes WHERE id=?`, q.Project).Scan(&kind); e != nil || kind != "project" {
			return out, records.MergeError("scope_denied")
		}
	}
	origin := `'manual'`
	if s.Schema >= 7 {
		origin = `CASE WHEN EXISTS(SELECT 1 FROM organization_plan_runs p WHERE p.task=t.id) THEN 'schedule' ELSE 'manual' END`
	}
	filters, args := []string{"1=1"}, []any{}
	if q.Project != "" {
		filters = append(filters, "t.project=?")
		args = append(args, q.Project)
	}
	if q.Origin != "" {
		filters = append(filters, origin+"=?")
		args = append(args, q.Origin)
	}
	if q.SinceMS > 0 {
		filters = append(filters, "julianday(t.created_at)>=julianday(?)")
		args = append(args, time.UnixMilli(q.SinceMS).UTC().Format(time.RFC3339Nano))
	}
	if q.Memory != "" {
		filters = append(filters, `EXISTS(SELECT 1 FROM organization_items i WHERE i.task=t.id AND json_extract(i.receipt,'$.memory')=?)`)
		args = append(args, q.Memory)
	}
	if q.ActiveOnly {
		filters = append(filters, `t.state IN ('queued','running','applying')`)
	}
	where := strings.Join(filters, " AND ")
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM organization_tasks t WHERE "+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	if q.Before != "" {
		var before string
		cursorArgs := append(append([]any{}, args...), q.Before)
		err = tx.QueryRowContext(ctx, "SELECT t.created_at FROM organization_tasks t WHERE "+where+" AND t.id=?", cursorArgs...).Scan(&before)
		if err == sql.ErrNoRows {
			return out, records.MergeError("invalid_request")
		}
		if err != nil {
			return out, err
		}
		where += " AND (t.created_at<? OR (t.created_at=? AND t.id<?))"
		args = append(args, before, before, q.Before)
	}
	order := "t.created_at DESC,t.id DESC"
	if q.ActiveOnly {
		order = "CASE WHEN t.state IN ('running','applying') THEN 0 ELSE 1 END," + order
	}
	query := `SELECT t.id,t.project,t.state,COALESCE(NULLIF(json_extract(r.payload,'$.items[0].title'),''),(SELECT json_extract(a.snapshot,'$.inputs[0].title') FROM organization_attempts a WHERE a.task=t.id ORDER BY a.attempt DESC LIMIT 1),''),t.created_at,` + origin + `,COALESCE((SELECT json_extract(a.snapshot,'$.processing.revision.spec') FROM organization_attempts a WHERE a.task=t.id ORDER BY a.attempt DESC LIMIT 1),'null') FROM organization_tasks t LEFT JOIN organization_results r ON r.task=t.id AND r.attempt=(SELECT max(attempt) FROM organization_results WHERE task=t.id) WHERE ` + where + " ORDER BY " + order + " LIMIT ?"
	rows, err := tx.QueryContext(ctx, query, append(args, q.Limit+1)...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v records.OrganizationSummary
		var processing string
		if err = rows.Scan(&v.ID, &v.Project, &v.State, &v.Title, &v.CreatedAt, &v.Origin, &processing); err != nil {
			return out, err
		}
		if err = json.Unmarshal([]byte(processing), &v.Processing); err != nil {
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
