package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

const versionColumns = `m.id,m.dataset,m.current_revision,v.text,m.state,v.operation,v.title,md.created_at,v.created_at,md.source,original.text,s.name,s.kind,md.origin,CASE WHEN m.indexed_revision=m.current_revision AND m.state='active' THEN 'indexed' WHEN j.state='failed' THEN 'failed' WHEN j.state='indexing' THEN 'indexing' ELSE 'pending' END,md.source_detail`
const versionJoins = ` FROM memories m JOIN versions v ON v.memory=m.id AND v.revision=m.current_revision JOIN versions original ON original.memory=m.id AND original.revision=1 JOIN memory_metadata md ON md.memory=m.id JOIN scopes s ON s.id=m.dataset LEFT JOIN jobs j ON j.memory=m.id AND j.revision=m.current_revision `

type scanner interface{ Scan(...any) error }

func scanVersion(row scanner, extra ...any) (Version, error) {
	var v Version
	var detail string
	values := []any{&v.Memory, &v.Dataset, &v.Revision, &v.Text, &v.State, &v.Operation, &v.Title, &v.CreatedAt, &v.UpdatedAt, &v.Source, &v.Original, &v.ScopeName, &v.ScopeKind, &v.Origin, &v.IndexState, &detail}
	err := row.Scan(append(values, extra...)...)
	if err == nil {
		err = json.Unmarshal([]byte(detail), &v.SourceDetail)
	}
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
	path, common, err := repository(ctx, draft.Path)
	if err != nil {
		return records.Scope{}, err
	}
	// E02 preflight is advisory until the real identity is rechecked before
	// knownRepository can backfill its cache or registration writes anything.
	if draft.ExpectedRepository != "" {
		identity, identityErr := projectRepositoryIdentity(path, common)
		if identityErr != nil || identity != draft.ExpectedRepository {
			return records.Scope{}, access("repository_changed", "repository changed after inspection; check it again")
		}
	}
	if known, found, e := s.knownRepository(ctx, path); e != nil {
		return records.Scope{}, e
	} else if found {
		return known, nil
	}
	if name == "个人偏好" || name == "全局经验" {
		return records.Scope{}, errors.New("project name is reserved for a global scope")
	}
	scope := records.Scope{ID: uuid.NewString(), Name: name, Kind: "project", Path: path}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return records.Scope{}, err
	}
	defer tx.Rollback()
	if s.Schema >= 16 {
		var pending string
		e := tx.QueryRowContext(ctx, `SELECT i.pending_project FROM connection_intakes i JOIN connections c ON c.id=i.connection_id WHERE i.trusted_repository=? AND i.pending_project!='' AND c.enabled=1 ORDER BY c.id LIMIT 1`, common).Scan(&pending)
		if e != nil && e != sql.ErrNoRows {
			return records.Scope{}, e
		}
		if e == nil {
			scope.ID = pending
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO scopes(id,kind,name,path) VALUES(?,'project',?,?) ON CONFLICT(path) WHERE path!='' DO NOTHING`, scope.ID, name, path)
	if err != nil {
		return records.Scope{}, err
	}
	err = tx.QueryRowContext(ctx, `SELECT id,name,kind,path FROM scopes WHERE path=?`, path).Scan(&scope.ID, &scope.Name, &scope.Kind, &scope.Path)
	if err != nil {
		return scope, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO repositories(common_dir,scope) VALUES(?,?) ON CONFLICT(common_dir) DO NOTHING`, common, scope.ID); err != nil {
		return scope, err
	}
	return scope, tx.Commit()
}

type listCursor struct {
	Created, ID, Filter string
	Rank                int64
	AsOfMS              int64
}

func (s *Store) List(ctx context.Context, q records.ListQuery) (records.Page, error) {
	page := records.Page{Items: []records.Version{}}
	if q.Limit == 0 {
		q.Limit = 50
	}
	if q.Limit < 1 || q.Limit > 100 {
		return page, errors.New("list limit must be between 1 and 100")
	}
	sortMode := q.Sort
	switch sortMode {
	case "", "updated", "frequent", "important":
	default:
		return page, errors.New("invalid list sort")
	}
	if sortMode != "" && s.Schema < 10 {
		return page, errors.New("sorted usage list unavailable")
	}
	fingerprintQuery := q
	fingerprintQuery.Cursor = ""
	fingerprintQuery.Limit = 0
	fingerprint := UsageDigest(fingerprintQuery)
	asOf := time.Now().UnixMilli()
	var cursor listCursor
	if q.Cursor != "" {
		if len(q.Cursor) > 1024 {
			return page, errors.New("invalid page cursor")
		}
		data, e := base64.RawURLEncoding.DecodeString(q.Cursor)
		if e != nil || json.Unmarshal(data, &cursor) != nil || cursor.Created == "" || cursor.ID == "" {
			return page, errors.New("invalid page cursor")
		}
		// Historical created-order cursors remain accepted only in their old mode.
		if cursor.Filter != fingerprint && !(cursor.Filter == "" && sortMode == "" && len(q.Datasets) == 0 && q.Kind == "" && q.Timezone == "" && q.Text == "" && q.Source == "" && q.Date == "" && q.ScopeKind == "" && q.UpdatedSinceMS == 0) {
			return page, errors.New("page cursor scope or sort changed")
		}
		if cursor.AsOfMS > 0 {
			asOf = cursor.AsOfMS
		}
	}
	dateColumn := "md.created_at"
	if sortMode != "" {
		dateColumn = "v.created_at"
	}
	rankColumn := "CAST(0 AS INTEGER)"
	if sortMode == "important" {
		rankColumn = "COALESCE((SELECT important FROM memory_importance WHERE memory=m.id),0)"
	}
	if sortMode == "frequent" {
		rankColumn = fmt.Sprintf("(SELECT count(*) FROM usage_events u WHERE u.memory=m.id AND u.dataset=m.dataset AND u.channel='daily' AND u.kind='read' AND u.observed_ms BETWEEN %d AND %d)", asOf-int64(30*24*time.Hour/time.Millisecond), asOf)
	}
	where := []string{"1=1"}
	args := []any{}
	if q.Date == "" {
		where = append(where, "m.state='active'")
		if s.Schema >= 4 {
			where = append(where, "NOT EXISTS(SELECT 1 FROM derived_memories d WHERE d.memory=m.id AND d.revision=m.current_revision AND d.state='stale')")
		}
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
	// Scope totals precede content-kind, source, date and cursor filters.
	countWhere := append([]string{"m.state='active'"}, where[1:]...)
	if q.Date != "" && s.Schema >= 4 {
		countWhere = append(countWhere, "NOT EXISTS(SELECT 1 FROM derived_memories d WHERE d.memory=m.id AND d.revision=m.current_revision AND d.state='stale')")
	}
	var knowledge, raw int
	if err := s.DB.QueryRowContext(ctx, `SELECT coalesce(sum(CASE WHEN md.origin!='raw' THEN 1 ELSE 0 END),0),coalesce(sum(CASE WHEN md.origin='raw' THEN 1 ELSE 0 END),0)`+versionJoins+`WHERE `+strings.Join(countWhere, " AND "), args...).Scan(&knowledge, &raw); err != nil {
		return page, err
	}
	page.KnowledgeTotal, page.RawTotal = &knowledge, &raw
	sourceRows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT md.source`+versionJoins+`WHERE `+strings.Join(countWhere, " AND ")+` ORDER BY md.source LIMIT 1001`, args...)
	if err != nil {
		return page, err
	}
	for sourceRows.Next() {
		var source string
		if err = sourceRows.Scan(&source); err != nil {
			sourceRows.Close()
			return page, err
		}
		page.Sources = append(page.Sources, source)
	}
	err = sourceRows.Err()
	sourceRows.Close()
	if err != nil {
		return page, err
	}
	if len(page.Sources) > 1000 {
		page.Sources = page.Sources[:1000]
		page.SourcesTruncated = true
	}
	if q.ScopeKind != "" {
		switch q.ScopeKind {
		case "project", "preference", "experience":
		default:
			return page, errors.New("invalid content category")
		}
		where = append(where, "s.kind=?")
		args = append(args, q.ScopeKind)
	}
	if q.UpdatedSinceMS < 0 {
		return page, errors.New("invalid saved-version date range")
	}
	if q.UpdatedSinceMS > 0 {
		where = append(where, "julianday(v.created_at)>=julianday(?)")
		args = append(args, time.UnixMilli(q.UpdatedSinceMS).UTC().Format(time.RFC3339Nano))
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
	if q.Text != "" {
		if len(q.Text) > 1024 {
			return page, errors.New("content filter too long")
		}
		where = append(where, "(instr(lower(v.title),lower(?))>0 OR instr(lower(v.text),lower(?))>0)")
		args = append(args, q.Text, q.Text)
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
		filter += " AND (" + rankColumn + "<? OR (" + rankColumn + "=? AND (" + dateColumn + "<? OR (" + dateColumn + "=? AND m.id<?))))"
		args = append(args, cursor.Rank, cursor.Rank, cursor.Created, cursor.Created, cursor.ID)
	}
	args = append(args, q.Limit+1)
	rows, err := s.DB.QueryContext(ctx, `SELECT `+versionColumns+","+rankColumn+versionJoins+`WHERE `+filter+` ORDER BY `+rankColumn+` DESC,`+dateColumn+` DESC,m.id DESC LIMIT ?`, args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	ranks := []int64{}
	for rows.Next() {
		var rank int64
		v, err := scanVersion(rows, &rank)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, v)
		ranks = append(ranks, rank)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > q.Limit {
		page.Items = page.Items[:q.Limit]
		last := page.Items[len(page.Items)-1]
		date := last.CreatedAt
		if sortMode != "" {
			date = last.UpdatedAt
		}
		data, _ := json.Marshal(listCursor{Created: date, ID: last.Memory, Filter: fingerprint, Rank: ranks[q.Limit-1], AsOfMS: asOf})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	rows.Close()
	versions := []*records.Version{}
	for i := range page.Items {
		versions = append(versions, &page.Items[i])
	}
	if err = s.enrichVersions(ctx, versions); err != nil {
		return page, err
	}
	if s.Schema >= 10 {
		for _, v := range versions {
			u, e := s.UsageForVersion(ctx, *v)
			if e != nil {
				return page, e
			}
			v.Usage = &u
		}
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
	query := `SELECT revision,title,text,operation,created_at,'','' FROM versions WHERE memory=? AND revision<? ORDER BY revision DESC LIMIT ?`
	if s.Schema >= 6 {
		query = `SELECT v.revision,v.title,v.text,v.operation,v.created_at,coalesce(g.kind,''),coalesce(g.conditions,'') FROM versions v LEFT JOIN global_experiences g ON g.memory=v.memory AND g.revision=(SELECT max(revision) FROM global_experiences x WHERE x.memory=v.memory AND x.revision<=v.revision) WHERE v.memory=? AND v.revision<? ORDER BY v.revision DESC LIMIT ?`
	}
	rows, err := s.DB.QueryContext(ctx, query, id, before, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v records.History
		var kind, conditions string
		if err = rows.Scan(&v.Revision, &v.Title, &v.Text, &v.Operation, &v.CreatedAt, &kind, &conditions); err != nil {
			return out, err
		}
		if kind != "" {
			v.Experience = &records.ExperienceInfo{Kind: kind, Conditions: conditions}
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
	// Count identities, not versions or graph nodes. Raw records remain part of
	// the cumulative receipt count after archival; knowledge is the effective
	// current set, using the same stale-derived exclusion as the normal list.
	raw, knowledge := 0, 0
	knowledgeWhere := "md.origin!='raw' AND m.state='active'"
	if s.Schema >= 4 {
		knowledgeWhere += " AND NOT EXISTS(SELECT 1 FROM derived_memories d WHERE d.memory=m.id AND d.revision=m.current_revision AND d.state='stale')"
	}
	err = s.DB.QueryRowContext(ctx, `SELECT count(CASE WHEN md.origin='raw' THEN 1 END),count(CASE WHEN `+knowledgeWhere+` THEN 1 END) FROM memories m JOIN memory_metadata md ON md.memory=m.id`).Scan(&raw, &knowledge)
	if err != nil {
		return out, err
	}
	out.RawTotal, out.KnowledgeTotal = &raw, &knowledge
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
	if s.Schema >= 25 {
		return s.requeueIndex(ctx, id, expected)
	}
	query := `UPDATE jobs SET state='pending',error='' WHERE memory=? AND revision=? AND EXISTS(SELECT 1 FROM memories WHERE id=? AND current_revision=? AND state='active')`
	args := []any{id, expected, id, expected}
	if s.Schema >= 4 {
		query += ` AND NOT EXISTS(SELECT 1 FROM derived_memories WHERE memory=? AND revision=? AND state='stale')`
		args = append(args, id, expected)
	}
	result, err := s.DB.ExecContext(ctx, query, args...)
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
