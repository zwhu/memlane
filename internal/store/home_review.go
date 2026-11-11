package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/zwhu/memlane/internal/records"
)

type homeReviewCursor struct{ ID, Project, Action string }

func (s *Store) HomeReviews(ctx context.Context, q records.HomeReviewQuery) (records.HomeReviewPage, error) {
	out := records.HomeReviewPage{Items: []records.HomeReview{}}
	if q.Limit == 0 {
		q.Limit = 8
	}
	if q.Limit < 1 || q.Limit > 100 || len(q.Before) > 4096 {
		return out, access("invalid_request", "bounded review query required")
	}
	switch q.Action {
	case "", "merge", "overwrite", "delete", "move", "duplicate", "conflict", "global_experience":
	default:
		return out, access("invalid_request", "unknown review filter")
	}
	if s.Schema < 3 {
		return out, nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	// Summaries do not change the queue. Sources and destinations are checked
	// against the same transaction; expired suggestions are not counted as ready.
	union := `SELECT p.id,t.id AS task,'merge' AS owner,t.project,sc.name AS scope_name,'merge' AS action,COALESCE(json_extract(p.payload,'$.title'),''),t.created_at,p.payload,a.snapshot FROM merge_proposals p JOIN merge_tasks t ON t.id=p.task JOIN merge_attempts a ON a.task=t.id AND a.attempt=p.attempt JOIN scopes sc ON sc.id=t.project WHERE p.state='pending' AND t.cancelled=0`
	if s.Schema >= 5 {
		for _, table := range []string{"organization_reviews", "global_experience_reviews"} {
			if table == "global_experience_reviews" && s.Schema < 6 {
				continue
			}
			union += ` UNION ALL SELECT r.id,t.id,'organization',t.project,sc.name,r.action,COALESCE(json_extract(r.payload,'$.title'),''),t.created_at,r.payload,a.snapshot FROM ` + table + ` r JOIN organization_tasks t ON t.id=r.task JOIN organization_attempts a ON a.task=t.id AND a.attempt=t.attempt JOIN scopes sc ON sc.id=t.project WHERE r.state='review_pending' AND t.cancelled=0`
		}
	}
	where := []string{"1=1"}
	args := []any{}
	if q.Project != "" {
		where = append(where, "project=?")
		args = append(args, q.Project)
	}
	if q.Action != "" {
		where = append(where, "action=?")
		args = append(args, q.Action)
	}
	rows, err := tx.QueryContext(ctx, `WITH suggestions AS (`+union+`) SELECT * FROM suggestions WHERE `+strings.Join(where, " AND ")+` ORDER BY created_at DESC,id DESC`, args...)
	if err != nil {
		return out, err
	}
	type candidate struct {
		summary           records.HomeReview
		payload, snapshot string
	}
	candidates := []candidate{}
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.summary.ID, &c.summary.Task, &c.summary.Owner, &c.summary.Project, &c.summary.ScopeName, &c.summary.Action, &c.summary.Title, &c.summary.CreatedAt, &c.payload, &c.snapshot); err != nil {
			rows.Close()
			return out, err
		}
		candidates = append(candidates, c)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	valid := []records.HomeReview{}
	for _, c := range candidates {
		current := false
		if c.summary.Owner == "organization" {
			var snapshot records.OrganizationSnapshot
			var item records.OrganizationItem
			if err = json.Unmarshal([]byte(c.snapshot), &snapshot); err != nil {
				return out, err
			}
			if err = json.Unmarshal([]byte(c.payload), &item); err != nil {
				return out, err
			}
			current, err = organizationItemCurrent(ctx, tx, snapshot, item)
		} else {
			var snapshot records.MergeSnapshot
			if err = json.Unmarshal([]byte(c.snapshot), &snapshot); err != nil {
				return out, err
			}
			sources := make([]records.MergeSource, 0, len(snapshot.Inputs))
			for _, input := range snapshot.Inputs {
				sources = append(sources, records.MergeSource{Memory: input.Memory, Revision: input.Revision})
			}
			if s.Schema >= 4 {
				current, err = organizationCurrent(ctx, tx, snapshot.Project, sources)
			} else {
				current = true
				for _, source := range sources {
					var count int
					err = tx.QueryRowContext(ctx, `SELECT count(*) FROM memories WHERE id=? AND dataset=? AND current_revision=? AND state='active'`, source.Memory, snapshot.Project, source.Revision).Scan(&count)
					if err != nil || count != 1 {
						current = false
						break
					}
				}
			}
		}
		if err != nil {
			return out, err
		}
		if current {
			valid = append(valid, c.summary)
		}
	}
	out.Total = len(valid)
	start := 0
	if q.Before != "" {
		raw, e := base64.RawURLEncoding.DecodeString(q.Before)
		var cursor homeReviewCursor
		if e != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Project != q.Project || cursor.Action != q.Action {
			return out, access("invalid_request", "review cursor does not match filters")
		}
		found := false
		for i, item := range valid {
			if item.ID == cursor.ID {
				start = i + 1
				found = true
				break
			}
		}
		if !found {
			return out, access("invalid_request", "review queue changed; refresh required")
		}
	}
	end := min(start+q.Limit, len(valid))
	out.Items = valid[start:end]
	if end < len(valid) {
		raw, _ := json.Marshal(homeReviewCursor{ID: out.Items[len(out.Items)-1].ID, Project: q.Project, Action: q.Action})
		out.Next = base64.RawURLEncoding.EncodeToString(raw)
	}
	return out, nil
}
