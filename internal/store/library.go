package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

const versionColumns = `m.id,m.dataset,m.current_revision,v.text,m.state,v.operation,v.title,md.created_at,v.created_at,md.source,original.text,s.name,s.kind,md.origin,CASE WHEN m.indexed_revision=m.current_revision AND m.state='active' THEN 'indexed' WHEN j.state='failed' THEN 'failed' WHEN j.state='indexing' THEN 'indexing' ELSE 'pending' END`
const versionJoins = ` FROM memories m JOIN versions v ON v.memory=m.id AND v.revision=m.current_revision JOIN versions original ON original.memory=m.id AND original.revision=1 JOIN memory_metadata md ON md.memory=m.id JOIN scopes s ON s.id=m.dataset LEFT JOIN jobs j ON j.memory=m.id AND j.revision=m.current_revision `

type scanner interface{ Scan(...any) error }

func scanVersion(row scanner) (Version, error) {
	var v Version
	err := row.Scan(&v.Memory, &v.Dataset, &v.Revision, &v.Text, &v.State, &v.Operation, &v.Title, &v.CreatedAt, &v.UpdatedAt, &v.Source, &v.Original, &v.ScopeName, &v.ScopeKind, &v.Origin, &v.IndexState)
	return v, err
}

func (s *Store) Scopes(ctx context.Context) ([]records.Scope, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,kind,path FROM scopes ORDER BY CASE kind WHEN 'preference' THEN 0 WHEN 'experience' THEN 1 ELSE 2 END,name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []records.Scope{}
	for rows.Next() {
		var v records.Scope
		if err = rows.Scan(&v.ID, &v.Name, &v.Kind, &v.Path); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) RegisterProject(ctx context.Context, draft records.ScopeDraft) (records.Scope, error) {
	name := strings.TrimSpace(draft.Name)
	if name == "" || len([]rune(name)) > 80 || !filepath.IsAbs(draft.Path) {
		return records.Scope{}, errors.New("project name and absolute repository path required")
	}
	path, err := filepath.EvalSymlinks(filepath.Clean(draft.Path))
	if err != nil {
		return records.Scope{}, err
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return records.Scope{}, errors.New("repository directory does not exist")
	}
	if _, err = os.Stat(filepath.Join(path, ".git")); err != nil {
		return records.Scope{}, errors.New("choose a Git repository directory")
	}
	// Workspace/common-dir binding for Agent connections belongs to P2.
	if name == "个人偏好" || name == "全局经验" {
		return records.Scope{}, errors.New("project name is reserved for a global scope")
	}
	scope := records.Scope{ID: uuid.NewString(), Name: name, Kind: "project", Path: path}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO scopes(id,kind,name,path) VALUES(?,'project',?,?) ON CONFLICT(path) WHERE path!='' DO NOTHING`, scope.ID, name, path)
	if err != nil {
		return records.Scope{}, err
	}
	err = s.DB.QueryRowContext(ctx, `SELECT id,name,kind,path FROM scopes WHERE path=?`, path).Scan(&scope.ID, &scope.Name, &scope.Kind, &scope.Path)
	return scope, err
}

type listCursor struct{ Created, ID string }

func (s *Store) List(ctx context.Context, q records.ListQuery) (records.Page, error) {
	page := records.Page{Items: []records.Version{}}
	if q.Limit == 0 {
		q.Limit = 50
	}
	if q.Limit < 1 || q.Limit > 100 {
		return page, errors.New("list limit must be between 1 and 100")
	}
	where := []string{"1=1"}
	args := []any{}
	if q.Date == "" {
		where = append(where, "m.state='active'")
	}
	if len(q.Datasets) > 0 {
		marks := []string{}
		for _, id := range q.Datasets {
			parsed, err := uuid.Parse(id)
			if err != nil {
				return page, errors.New("invalid dataset")
			}
			marks = append(marks, "?")
			args = append(args, parsed.String())
		}
		where = append(where, "m.dataset IN ("+strings.Join(marks, ",")+")")
	}
	switch q.Kind {
	case "", "knowledge":
		where = append(where, "md.origin!='raw'")
	case "raw":
		where = append(where, "md.origin='raw'")
	default:
		return page, errors.New("invalid record kind")
	}
	if q.Source != "" {
		where = append(where, "md.source=?")
		args = append(args, q.Source)
	}
	if q.Date != "" {
		loc, err := timezone(q.Timezone)
		if err != nil {
			return page, err
		}
		date, err := time.ParseInLocation("2006-01-02", q.Date, loc)
		if err != nil {
			return page, errors.New("invalid activity date")
		}
		where = append(where, "EXISTS (SELECT 1 FROM activities a WHERE a.memory=m.id AND a.kind='created' AND a.occurred_at>=? AND a.occurred_at<?)")
		args = append(args, date.UTC().Format("2006-01-02T15:04:05.000Z"), date.AddDate(0, 0, 1).UTC().Format("2006-01-02T15:04:05.000Z"))
	}
	filter := strings.Join(where, " AND ")
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*)`+versionJoins+`WHERE `+filter, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	if q.Cursor != "" {
		if len(q.Cursor) > 512 {
			return page, errors.New("invalid page cursor")
		}
		data, err := base64.RawURLEncoding.DecodeString(q.Cursor)
		if err != nil {
			return page, errors.New("invalid page cursor")
		}
		var cursor listCursor
		if json.Unmarshal(data, &cursor) != nil || cursor.Created == "" || cursor.ID == "" {
			return page, errors.New("invalid page cursor")
		}
		filter += " AND (md.created_at<? OR (md.created_at=? AND m.id<?))"
		args = append(args, cursor.Created, cursor.Created, cursor.ID)
	}
	args = append(args, q.Limit+1)
	rows, err := s.DB.QueryContext(ctx, `SELECT `+versionColumns+versionJoins+`WHERE `+filter+` ORDER BY md.created_at DESC,m.id DESC LIMIT ?`, args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, v)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > q.Limit {
		page.Items = page.Items[:q.Limit]
		last := page.Items[len(page.Items)-1]
		data, _ := json.Marshal(listCursor{last.CreatedAt, last.Memory})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	return page, nil
}

func (s *Store) History(ctx context.Context, id string, before int64, limit int) (records.HistoryPage, error) {
	out := records.HistoryPage{Items: []records.History{}}
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 || before < 0 {
		return out, errors.New("invalid history page")
	}
	if _, err := s.Current(ctx, id); err != nil {
		return out, err
	}
	if before == 0 {
		before = 1 << 62
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT revision,title,text,operation,created_at FROM versions WHERE memory=? AND revision<? ORDER BY revision DESC LIMIT ?`, id, before, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v records.History
		if err = rows.Scan(&v.Revision, &v.Title, &v.Text, &v.Operation, &v.CreatedAt); err != nil {
			return out, err
		}
		out.Items = append(out.Items, v)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextRevision = out.Items[len(out.Items)-1].Revision
	}
	return out, nil
}

func timezone(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return time.Local, nil
	}
	return time.LoadLocation(name)
}
func parseTime(value string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02 15:04:05", value)
}
func (s *Store) Activities(ctx context.Context, zone string) (records.ActivitySummary, error) {
	out := records.ActivitySummary{Days: map[string]int{}, Recent: []records.Version{}}
	loc, err := timezone(zone)
	if err != nil {
		return out, err
	}
	today := time.Now().In(loc)
	cutoff := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc).AddDate(-1, 0, 1)
	rows, err := s.DB.QueryContext(ctx, `SELECT kind,occurred_at FROM activities`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var kind, date string
		if err = rows.Scan(&kind, &date); err != nil {
			rows.Close()
			return out, err
		}
		if kind == "imported" {
			out.Imported++
			continue
		}
		if kind != "created" {
			continue
		}
		t, err := parseTime(date)
		if err != nil {
			rows.Close()
			return out, err
		}
		if t.Before(cutoff) || t.After(today) {
			continue
		}
		key := t.In(loc).Format("2006-01-02")
		out.Days[key]++
		out.Total++
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	rows, err = s.DB.QueryContext(ctx, `SELECT `+versionColumns+versionJoins+` JOIN activities a ON a.memory=m.id WHERE a.kind='created' ORDER BY a.occurred_at DESC,m.id DESC LIMIT 4`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return out, err
		}
		out.Recent = append(out.Recent, v)
	}
	return out, rows.Err()
}

// Requeue uses the current revision and leaves immutable versions and activity
// facts untouched. The daemon's persistent replay performs the actual work.
func (s *Store) Requeue(ctx context.Context, id string, expected int64) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE jobs SET state='pending',error='' WHERE memory=? AND revision=? AND EXISTS(SELECT 1 FROM memories WHERE id=? AND current_revision=? AND state='active')`, id, expected, id, expected)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) IndexStarted(ctx context.Context, id string, revision int64) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE jobs SET state='indexing',error='' WHERE memory=? AND revision=? AND state!='superseded' AND EXISTS(SELECT 1 FROM memories WHERE id=? AND current_revision=? AND state='active')`, id, revision, id, revision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
