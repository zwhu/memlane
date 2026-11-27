package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

const catalogGeneration int64 = 1

type catalogItem struct {
	ProjectID      string `json:"project_id,omitempty"`
	DatasetID      string `json:"dataset_id"`
	DocID          string `json:"doc_id"`
	DocumentID     string `json:"document_id"`
	Kind           string `json:"kind"`
	Title          string `json:"title"`
	DocName        string `json:"doc_name"`
	DocLabel       string `json:"doc_label"`
	EffectiveDate  string `json:"effective_date"`
	Category       string `json:"category"`
	CommonCategory string `json:"common_category"`
	RecallCount    int    `json:"recall_count"`
	IsUnread       bool   `json:"is_unread"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	LessonType     string `json:"lesson_type"`
	StateKey       string `json:"state_key"`
	SessionID      string `json:"session_id"`
	ContentHash    string `json:"content_hash"`
	PipelineStatus string `json:"pipeline_status"`
	location       string
}

const docFields = `d.dataset_id,d.doc_id,d.kind,COALESCE(NULLIF(d.doc_label,''),d.doc_name),
	d.doc_name,d.doc_label,d.effective_date,d.category,d.common_category,COALESCE(rc.total_hits,d.recall_count),
	d.created_at_raw,d.updated_at_ns,d.lesson_type,d.state_key,d.session_id,d.content_hash,d.raw_data_location,d.pipeline_status`

const projectField = `CASE WHEN json_extract(ref.value,'$.kind')='common' THEN '' ELSE json_extract(ref.value,'$.project') END`

const recallCountJoin = ` LEFT JOIN recall_doc_counts rc ON rc.dataset_id=d.dataset_id AND rc.document_id=d.doc_id `

// Match the domain's explicit override, invalid date and horizon precedence.
const unreadExpr = `CASE WHEN rs.initialized=1 THEN COALESCE(1-ri.is_read,
	(d.created_at_invalid=1 OR d.created_at_ns>rs.read_horizon_at_ns)) ELSE 0 END`
const readingJoins = ` LEFT JOIN ui_read_scopes rs ON rs.scope_hash=json_extract(ref.value,'$.scope')
	AND rs.dataset_id=d.dataset_id AND rs.project_id=json_extract(ref.value,'$.project')
	LEFT JOIN ui_read_items ri ON ri.scope_hash=rs.scope_hash AND ri.doc_id=d.doc_id `

func scanCatalog(row rowScanner, tail ...any) (catalogItem, error) {
	var item catalogItem
	values := []any{&item.DatasetID, &item.DocID, &item.Kind, &item.Title, &item.DocName, &item.DocLabel,
		&item.EffectiveDate, &item.Category, &item.CommonCategory, &item.RecallCount, &item.CreatedAt, &item.UpdatedAt,
		&item.LessonType, &item.StateKey, &item.SessionID, &item.ContentHash, &item.location, &item.PipelineStatus, &item.IsUnread, &item.ProjectID}
	err := row.Scan(append(values, tail...)...)
	item.DocumentID = item.DocID
	return item, err
}

func scanCatalogPage(row rowScanner) (pageRow[catalogItem], error) {
	var k pageKey
	item, err := scanCatalog(row, &k.first, &k.second, &k.dataset, &k.id)
	return pageRow[catalogItem]{item, k}, err
}

type knowledgeOptions struct {
	project, kind, sort, category, day, agent, event, identity string
	unread                                                     bool
}

func (s *Server) knowledge(w http.ResponseWriter, r *http.Request) {
	s.knowledgePage(w, r, "knowledge", "")
}

func (s *Server) knowledgePage(w http.ResponseWriter, r *http.Request, kind, day string) {
	q := r.URL.Query()
	in := knowledgeOptions{project: r.PathValue("id"), kind: kind, sort: q.Get("sort"), category: q.Get("category"),
		day: day, agent: q.Get("recall_agent_id"), event: q.Get("recall_event_id"), identity: readingIdentity(r),
		unread: parseBool(q.Get("unread_only"))}
	if in.project == "" {
		in.project = q.Get("project_id")
	}
	if in.sort == "" {
		in.sort = "date"
	}
	switch kind {
	case "common":
		in.project, in.sort, in.category = "", "date", q.Get("common_category")
		if in.category == "all" {
			in.category = ""
		}
		if in.category != "" && !validCommonCategory(in.category) {
			writeDomainError(w, domain.ErrInvalidArgument)
			return
		}
	}
	switch strings.ToLower(q.Get("unread_only")) {
	case "", "0", "1", "true", "false":
	default:
		writeDomainError(w, domain.ErrInvalidArgument)
		return
	}
	if in.sort != "date" && in.sort != "recall" || (in.agent == "") != (in.event == "") {
		writeDomainError(w, domain.ErrInvalidArgument)
		return
	}
	ctx := r.Context()
	var result map[string]any
	err := snapshot(ctx, s.cfg.Store, func(tx *sql.Tx) error {
		refs, err := datasetRefs(ctx, tx, in.identity)
		if err != nil {
			return err
		}
		refs = slices.DeleteFunc(refs, func(ref datasetRef) bool {
			if in.kind == "common" {
				return ref.Kind != "common"
			}
			if in.day != "" {
				return ref.Kind == "code"
			}
			return ref.Kind != "project" || in.project != "" && ref.ProjectID != in.project
		})
		if len(refs) == 0 && (in.project != "" || kind == "common") {
			return domain.ErrNotFound
		}
		result, err = s.queryKnowledge(ctx, tx, r, in, refs)
		return err
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, 200, result)
}

func (s *Server) queryKnowledge(ctx context.Context, db sqlRead, r *http.Request, in knowledgeOptions, refs []datasetRef) (map[string]any, error) {
	raw, err := json.Marshal(refs)
	if err != nil {
		return nil, err
	}
	from := ` FROM json_each(?) ref JOIN catalog_docs d ON d.dataset_id=json_extract(ref.value,'$.id') ` +
		readingJoins + recallCountJoin + ` WHERE d.is_knowledge=1`
	args := []any{string(raw)}
	category := "d.category"
	first, second := "d.day_desc_key", "0"
	if in.kind == "common" {
		from += " AND d.kind='common'"
		category, first = "d.common_category", "d.sort_ts_desc_key"
	} else if in.day == "" {
		from += " AND d.kind<>'common'"
	}
	if in.sort == "recall" {
		first, second = "-COALESCE(rc.total_hits,d.recall_count)", "d.day_desc_key"
	}
	if in.day != "" {
		from += " AND d.effective_date=?"
		args = append(args, in.day)
	}
	if in.agent != "" {
		from += ` AND EXISTS(SELECT 1 FROM recall_hits h WHERE h.agent_id=? AND h.event_id=? AND h.dataset_id=d.dataset_id AND h.document_id=d.doc_id)`
		args = append(args, in.agent, in.event)
	}
	if in.unread {
		from += " AND (" + unreadExpr + ")=1"
	}
	type count struct {
		category string
		count    int
	}
	groups, err := readRows(ctx, db, "SELECT "+category+",COUNT(*)"+from+" GROUP BY "+category, args, func(row rowScanner) (count, error) {
		var group count
		err := row.Scan(&group.category, &group.count)
		return group, err
	})
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	if in.kind == "common" {
		counts["preference"], counts["experience"] = 0, 0
	}
	total := 0
	for _, g := range groups {
		counts[g.category] = g.count
		if in.category == "" || g.category == in.category {
			total += g.count
		}
	}
	if in.category != "" {
		from += " AND " + category + "=?"
		args = append(args, in.category)
	}
	binding := cursorBinding{Endpoint: r.URL.Path, Dataset: digest(raw), Sort: in.sort,
		Filter: tupleDigest(in.kind, in.category, in.day, in.agent, in.event, r.URL.Query().Get("unread_only"))}
	opts, err := newPageOptions(r, binding)
	if err != nil {
		return nil, err
	}
	query := "SELECT " + docFields + "," + unreadExpr + "," + projectField + "," + first + " AS k1," + second + " AS k2,d.dataset_id AS k3,d.doc_id AS k4" + from
	result, err := page(ctx, db, s.cursors, opts, query, args, scanCatalogPage)
	if err != nil {
		return nil, err
	}
	var unread int
	var revision int64
	var version string
	for _, ref := range refs {
		state, err := readStateFor(ctx, db, ref)
		if err != nil {
			return nil, err
		}
		unread += state.UnreadCount
		revision = max(revision, state.StateRev)
	}
	dataset := ""
	if len(refs) == 1 {
		dataset = refs[0].ID
		err := db.QueryRowContext(ctx, `SELECT catalog_version FROM catalog_sync_state WHERE dataset_id=?`, dataset).Scan(&version)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	return map[string]any{"generation": catalogGeneration, "common_category": in.category, "project_id": in.project, "dataset_id": dataset, "sort": in.sort, "day": in.day,
		"items": result.Items, "next_cursor": result.NextCursor, "prev_cursor": result.PrevCursor,
		"has_more": result.HasMore, "has_prev": result.HasPrev, "total_filtered": total, "total_count": total,
		"category_counts": counts, "unread_count": unread, "unread_state_rev": revision, "catalog_version": version}, nil
}

const maxDocumentBody = 1 << 20

func (s *Server) loadDocument(ctx context.Context, ds, doc string) (catalogItem, string, string, error) {
	query := "SELECT " + docFields + ",0,COALESCE((SELECT id FROM projects WHERE dataset_id=d.dataset_id ORDER BY id LIMIT 1),'') FROM catalog_docs d" + recallCountJoin + " WHERE d.dataset_id=? AND d.doc_id=?"
	item, err := scanCatalog(s.cfg.Store.DB().QueryRowContext(ctx, query, ds, doc))
	missing := errors.Is(err, sql.ErrNoRows)
	if err != nil && !missing {
		return item, "", "", err
	}
	mime := "text/plain"
	var content string
	if s.cfg.Projector != nil && item.location != "" {
		content, err = s.cfg.Projector.ReadLocalBody(ctx, engine.LocalBodyRef{DatasetID: ds, DocumentID: doc,
			Location: item.location, MimeType: mime, MaxBytes: maxDocumentBody})
	} else {
		var record engine.Item
		record, err = s.cfg.Engine.GetItem(ctx, engine.Scope{DatasetID: ds}, doc)
		if err == nil && (record.DatasetID != "" && record.DatasetID != ds || record.ID != "" && record.ID != doc) {
			return item, "", "", engine.ErrForbidden
		}
		if missing {
			item = catalogItem{DatasetID: ds, DocID: doc, DocumentID: doc, Kind: record.Kind, Title: record.Label,
				DocLabel: record.Label, DocName: record.Name, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt, ContentHash: record.ContentHash}
			if item.Title == "" {
				item.Title = record.Name
			}
		}
		content = record.Content
		if record.MimeType != "" {
			mime = record.MimeType
		}
	}
	if err == nil && len(content) > maxDocumentBody {
		err = engine.ErrContentTooLarge
	}
	return item, content, mime, err
}

func (s *Server) serveDocument(w http.ResponseWriter, r *http.Request, ds, doc string) {
	item, content, mime, err := s.loadDocument(r.Context(), ds, doc)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	if strings.HasSuffix(r.Pattern, "/body") {
		writeJSON(w, 200, map[string]any{"dataset_id": ds, "document_id": doc, "content": content, "mime_type": mime,
			"id": doc, "body": content, "content_hash": item.ContentHash, "bytes_total": len(content), "truncated": false})
		return
	}
	writeJSON(w, 200, struct {
		catalogItem
		Content  string `json:"content"`
		MimeType string `json:"mime_type"`
		ID       string `json:"id"`
		Dataset  string `json:"datasetId"`
		Name     string `json:"name"`
		Label    string `json:"label"`
		Created  string `json:"createdAt"`
		Updated  string `json:"updatedAt"`
		Hash     string `json:"contentHash"`
	}{item, content, mime, doc, ds, item.DocName, item.DocLabel, item.CreatedAt, item.UpdatedAt, item.ContentHash})
}

func (s *Server) document(w http.ResponseWriter, r *http.Request) {
	ds, doc := r.PathValue("ds"), r.PathValue("doc")
	if err := s.managedDataset(r.Context(), ds); err != nil {
		writeDomainError(w, err)
		return
	}
	s.serveDocument(w, r, ds, doc)
}

func (s *Server) removeDocument(ctx context.Context, ds, doc string) error {
	if err := s.cfg.Engine.DeleteItem(ctx, engine.Scope{DatasetID: ds}, doc); err != nil && !errors.Is(err, engine.ErrNotFound) {
		return err
	}
	return s.syncDocumentDeletion(ctx, ds, doc)
}

func (s *Server) syncDocumentDeletion(ctx context.Context, ds, doc string) error {
	if s.cfg.Projector != nil {
		_, err := s.cfg.Projector.SyncCatalog(ctx, s.cfg.Store)
		return err
	}
	return s.cfg.Store.DeleteCatalogDoc(ctx, ds, doc)
}

func (s *Server) deleteDocument(w http.ResponseWriter, r *http.Request) {
	s.commonMu.Lock()
	defer s.commonMu.Unlock()
	ds := r.PathValue("ds")
	if err := s.managedDataset(r.Context(), ds); err != nil {
		writeDomainError(w, err)
		return
	}
	if err := s.removeDocument(r.Context(), ds, r.PathValue("doc")); err != nil {
		writeEngineError(w, err)
		return
	}
	w.WriteHeader(204)
}

type pageOptions struct {
	binding        cursorBinding
	cursor, anchor string
	previous       bool
	limit          int
}

func newPageOptions(r *http.Request, binding cursorBinding) (pageOptions, error) {
	q := r.URL.Query()
	direction := q.Get("direction")
	if direction != "" && direction != "next" && direction != "prev" {
		return pageOptions{}, domain.ErrInvalidArgument
	}
	p := pageOptions{binding: binding, cursor: q.Get("cursor"), anchor: q.Get("anchor_doc_id"),
		previous: direction == "prev", limit: parseLimit(q.Get("limit"), 30, 50)}
	if p.anchor != "" && p.cursor != "" {
		return p, domain.ErrInvalidArgument
	}
	return p, nil
}

type pageKey struct {
	first, second int64
	dataset, id   string
}

const keyColumns = "k1,k2,k3,k4"

func (k pageKey) args() []any { return []any{k.first, k.second, k.dataset, k.id} }

func (k pageKey) strings() []string {
	return []string{strconv.FormatInt(k.first, 10), strconv.FormatInt(k.second, 10), k.dataset, k.id}
}
func (k *pageKey) scan(row rowScanner) error { return row.Scan(&k.first, &k.second, &k.dataset, &k.id) }

type pageRow[T any] struct {
	item T
	key  pageKey
}
type pageResult[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor"`
	PrevCursor string `json:"prev_cursor"`
	HasMore    bool   `json:"has_more"`
	HasPrev    bool   `json:"has_prev"`
}

func page[T any](ctx context.Context, db sqlRead, codec cursorCodec, options pageOptions,
	source string, args []any, scan func(rowScanner) (pageRow[T], error)) (pageResult[T], error) {
	out := pageResult[T]{Items: []T{}}
	var boundary pageKey
	op := ""
	switch {
	case options.cursor != "":
		values, err := codec.decode(options.binding, options.cursor, 4)
		if err != nil {
			return out, err
		}
		first, e1 := strconv.ParseInt(values[0], 10, 64)
		second, e2 := strconv.ParseInt(values[1], 10, 64)
		if errors.Join(e1, e2) != nil {
			return out, domain.ErrInvalidArgument
		}
		boundary = pageKey{first, second, values[2], values[3]}
		op = ">"
		if options.previous {
			op = "<"
		}
	case options.anchor != "":
		err := boundary.scan(db.QueryRowContext(ctx, "SELECT "+keyColumns+" FROM ("+source+") WHERE k4=? ORDER BY "+keyColumns+" LIMIT 1",
			append(slices.Clone(args), options.anchor)...))
		if err != nil {
			return out, err
		}
		op = ">="
		if options.previous {
			op = "<="
		}
	}
	query := "SELECT * FROM (" + source + ")"
	pageArgs := slices.Clone(args)
	if op != "" {
		query += " WHERE (" + keyColumns + ") " + op + " (?,?,?,?)"
		pageArgs = append(pageArgs, boundary.args()...)
	}
	order := keyColumns
	if options.previous {
		order = "k1 DESC,k2 DESC,k3 DESC,k4 DESC"
	}
	rows, err := readRows(ctx, db, query+" ORDER BY "+order+" LIMIT ?", append(pageArgs, options.limit+1), scan)
	if err != nil || len(rows) == 0 {
		return out, err
	}
	more := len(rows) > options.limit
	rows = rows[:min(len(rows), options.limit)]
	if options.previous {
		slices.Reverse(rows)
	}
	first, last := rows[0].key, rows[len(rows)-1].key
	out.HasMore = more
	if options.previous {
		out.HasPrev, out.HasMore = more, false
	}
	if op != "" || options.previous {
		probe, cmp, dst := first, "<", &out.HasPrev
		if options.previous {
			probe, cmp, dst = last, ">", &out.HasMore
		}
		err = db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM ("+source+") WHERE ("+keyColumns+") "+cmp+" (?,?,?,?))",
			append(slices.Clone(args), probe.args()...)...).Scan(dst)
		if err != nil {
			return out, err
		}
	}
	if out.HasMore {
		out.NextCursor = codec.encode(options.binding, last.strings())
	}
	if out.HasPrev {
		out.PrevCursor = codec.encode(options.binding, first.strings())
	}
	for _, row := range rows {
		out.Items = append(out.Items, row.item)
	}
	return out, nil
}
