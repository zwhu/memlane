package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

type searchInput struct {
	Query     string `json:"q"`
	Filter    string `json:"filter"`
	ProjectID string `json:"project_id"`
	DatasetID string `json:"dataset_id"`
	Limit     int    `json:"limit"`
	Semantic  bool   `json:"semantic"`
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	in := searchInput{Query: q.Get("q"), Filter: q.Get("filter"), ProjectID: q.Get("project_id"), DatasetID: q.Get("dataset_id"),
		Limit: parseLimit(q.Get("limit"), 30, 50), Semantic: parseBool(q.Get("semantic"))}
	if r.Method == http.MethodPost && !decodeJSON(w, r, s.cfg.MaxBodyBytes, &in) {
		return
	}
	if in.Filter == "" {
		in.Filter = "all"
	}
	in.Query = strings.TrimSpace(in.Query)
	if in.Query == "" || utf8.RuneCountInString(in.Query) > 256 ||
		!slices.Contains([]string{"all", "knowledge", "common", "raw"}, in.Filter) {
		writeDomainError(w, domain.ErrInvalidArgument)
		return
	}
	if in.Limit < 1 {
		in.Limit = 30
	}
	in.Limit = min(in.Limit, 50)
	ctx := r.Context()
	var items []catalogItem
	var sessions []rawItem
	var refs []datasetRef
	err := snapshot(ctx, s.cfg.Store, func(tx *sql.Tx) error {
		var err error
		refs, err = datasetRefs(ctx, tx, readingIdentity(r))
		if err != nil {
			return err
		}
		refs = slices.DeleteFunc(refs, func(ref datasetRef) bool {
			return in.ProjectID != "" && ref.ProjectID != in.ProjectID || in.DatasetID != "" && ref.ID != in.DatasetID
		})
		if len(refs) == 0 && (in.ProjectID != "" || in.DatasetID != "") {
			return domain.ErrForbidden
		}
		refs = slices.DeleteFunc(refs, func(ref datasetRef) bool {
			switch in.Filter {
			case "common":
				return ref.Kind != "common"
			case "knowledge", "raw":
				return ref.Kind == "common"
			default:
				return false
			}
		})
		items, sessions, err = catalogSearch(ctx, tx, in, refs)
		return err
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	hits := []engine.SearchHit{}
	if in.Semantic && len(refs) > 0 {
		scopes := make([]engine.Scope, 0, len(refs))
		for _, ref := range refs {
			scopes = append(scopes, engine.Scope{DatasetID: ref.ID})
		}
		searchCtx, cancel := context.WithTimeout(ctx, s.cfg.SearchTimeout)
		defer cancel()
		res, err := s.cfg.Engine.Search(searchCtx, engine.SearchRequest{Scopes: scopes, Query: in.Query, Limit: in.Limit,
			Filters: engine.SearchFilters{Kind: in.Filter, Limit: in.Limit}})
		if err == nil {
			err = searchCtx.Err()
		}
		if err == nil && !res.OK {
			err = engine.ErrUnavailable
		}
		if err != nil {
			writeEngineError(w, err)
			return
		}
		hits = authorizedHits(res.Hits, scopes, in.Limit)
		hits, err = s.filterSemanticHits(searchCtx, in.Filter, hits)
		if err != nil {
			writeDomainError(w, err)
			return
		}
	}
	s.searches.add(searchEntry{in.Query, in.Filter, in.ProjectID, in.DatasetID, len(items) + len(sessions) + len(hits), domain.NowUTCNS()})
	combined := make([]any, 0, len(items)+len(sessions))
	for _, item := range items {
		combined = append(combined, item)
	}
	for _, session := range sessions {
		combined = append(combined, session)
	}
	writeJSON(w, 200, map[string]any{"ok": true, "query": in.Query, "filter": in.Filter, "items": combined, "semantic_hits": hits})
}

func (s *Server) filterSemanticHits(ctx context.Context, filter string, hits []engine.SearchHit) ([]engine.SearchHit, error) {
	if filter == "all" || len(hits) == 0 {
		return hits, nil
	}
	ids := make([][2]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, [2]string{hit.DatasetID, hit.DocumentID})
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	// A search adapter can search whole datasets. Qualify each result using the catalog.
	query := `SELECT CAST(ref.key AS INTEGER) FROM json_each(?) ref
		JOIN catalog_docs d ON d.dataset_id=json_extract(ref.value,'$[0]') AND d.doc_id=json_extract(ref.value,'$[1]')
		WHERE d.kind=? ORDER BY ref.key`
	kind := filter
	if filter == "raw" {
		kind = "raw_doc"
	}
	rows, err := readRows(ctx, s.cfg.Store.DB(), query, []any{string(raw), kind}, func(row rowScanner) (int, error) {
		var index int
		err := row.Scan(&index)
		return index, err
	})
	if err != nil {
		return nil, err
	}
	out := make([]engine.SearchHit, 0, len(rows))
	for _, index := range rows {
		out = append(out, hits[index])
	}
	return out, nil
}

func catalogSearch(ctx context.Context, db sqlRead, in searchInput, refs []datasetRef) ([]catalogItem, []rawItem, error) {
	raw, err := json.Marshal(refs)
	if err != nil {
		return nil, nil, err
	}
	match := `(instr(lower(d.doc_name),lower(?2))>0 OR instr(lower(d.doc_label),lower(?2))>0 OR instr(lower(d.category),lower(?2))>0
		OR instr(lower(d.common_category),lower(?2))>0 OR d.doc_id=?2 OR d.session_id=?2)`
	query := "SELECT " + docFields + "," + unreadExpr + "," + projectField + ` FROM json_each(?1) ref JOIN catalog_docs d ON d.dataset_id=json_extract(ref.value,'$.id')` +
		readingJoins + recallCountJoin + " WHERE " + match
	switch in.Filter {
	case "knowledge":
		query += " AND d.is_knowledge=1 AND d.kind<>'common'"
	case "common":
		query += " AND d.kind='common'"
	case "raw":
		query += " AND d.kind='raw_doc'"
	}
	items, err := readRows(ctx, db, query+" ORDER BY d.sort_ts_desc_key,d.dataset_id,d.doc_id LIMIT ?3",
		[]any{string(raw), in.Query, in.Limit}, func(row rowScanner) (catalogItem, error) { return scanCatalog(row) })
	if err != nil {
		return nil, nil, err
	}
	sessions := []rawItem{}
	if in.Filter == "raw" || in.Filter == "all" {
		scope := ` JOIN (SELECT json_extract(value,'$.project') AS project_id,json_extract(value,'$.id') AS dataset
			FROM json_each(?1) WHERE json_extract(value,'$.kind')='project') p ON p.dataset=x.dataset_id
			WHERE (instr(lower(x.session_id),lower(?2))>0 OR instr(lower(x.agent_id),lower(?2))>0)`
		sessions, err = rawSessionSearch(ctx, db, scope, []any{string(raw), in.Query}, in.Limit-len(items))
	}
	return items, sessions, err
}

func (s *Server) searchRecent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit := parseLimit(r.URL.Query().Get("limit"), 20, 50)
	var items []catalogItem
	err := snapshot(ctx, s.cfg.Store, func(tx *sql.Tx) error {
		refs, err := datasetRefs(ctx, tx, readingIdentity(r))
		if err != nil {
			return err
		}
		items, err = recentKnowledge(ctx, tx, refs, limit)
		return err
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"recent_searches": s.searches.recent(limit), "recent_items": items})
}

type readingStateView struct {
	ProjectID   string `json:"project_id"`
	DatasetID   string `json:"dataset_id"`
	ScopeHash   string `json:"scope_hash"`
	UnreadCount int    `json:"unread_count"`
	StateRev    int64  `json:"state_rev"`
	ReadHorizon string `json:"read_horizon_at_ns"`
}

func readStateFor(ctx context.Context, db sqlRead, ref datasetRef) (readingStateView, error) {
	v := readingStateView{ProjectID: ref.ProjectID, DatasetID: ref.ID, ScopeHash: ref.ScopeHash, ReadHorizon: domain.FormatUTCNS(time.Time{})}
	if ref.ScopeHash == "" {
		return v, nil
	}
	err := db.QueryRowContext(ctx, `SELECT state_rev,COALESCE(NULLIF(read_horizon_at_ns,''),?),
		CASE WHEN initialized=1 THEN
		 (SELECT count(*) FROM catalog_docs d WHERE d.dataset_id=rs.dataset_id AND d.is_knowledge=1
		  AND d.created_at_invalid=0 AND d.created_at_ns>rs.read_horizon_at_ns
		  AND NOT EXISTS(SELECT 1 FROM ui_read_items i WHERE i.scope_hash=rs.scope_hash AND i.doc_id=d.doc_id))
		 +(SELECT count(*) FROM catalog_docs d WHERE d.dataset_id=rs.dataset_id AND d.is_knowledge=1
		  AND d.created_at_invalid=1
		  AND NOT EXISTS(SELECT 1 FROM ui_read_items i WHERE i.scope_hash=rs.scope_hash AND i.doc_id=d.doc_id))
		 +(SELECT count(*) FROM ui_read_items i JOIN catalog_docs d ON d.dataset_id=rs.dataset_id AND d.doc_id=i.doc_id
		  WHERE i.scope_hash=rs.scope_hash AND i.is_read=0 AND d.is_knowledge=1)
		ELSE 0 END
		FROM ui_read_scopes rs WHERE rs.scope_hash=? AND rs.project_id=? AND rs.dataset_id=?`,
		v.ReadHorizon, ref.ScopeHash, ref.ProjectID, ref.ID).Scan(&v.StateRev, &v.ReadHorizon, &v.UnreadCount)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return v, err
}

func (s *Server) readingState(w http.ResponseWriter, r *http.Request) {
	identity := readingIdentity(r)
	if identity == "" {
		writeError(w, 400, "reading_identity_required", "reading identity required")
		return
	}
	ctx := r.Context()
	out := []readingStateView{}
	err := snapshot(ctx, s.cfg.Store, func(tx *sql.Tx) error {
		refs, err := datasetRefs(ctx, tx, identity)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			if ref.Kind != "project" || r.URL.Query().Get("project_id") != "" && ref.ProjectID != r.URL.Query().Get("project_id") {
				continue
			}
			state, err := readStateFor(ctx, tx, ref)
			if err != nil {
				return err
			}
			out = append(out, state)
		}
		return nil
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"projects": out})
}

func (s *Server) markReading(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Identity  string   `json:"reading_identity"`
		ProjectID string   `json:"project_id"`
		DatasetID string   `json:"dataset_id"`
		DocIDs    []string `json:"doc_ids"`
	}
	if !decodeJSON(w, r, s.cfg.MaxBodyBytes, &in) {
		return
	}
	if in.Identity == "" {
		in.Identity = readingIdentity(r)
	}
	in.Identity = strings.TrimSpace(in.Identity)
	all := strings.HasSuffix(r.Pattern, "/mark-all-read")
	if in.Identity == "" || in.ProjectID == "" || in.DatasetID == "" || !all && (len(in.DocIDs) < 1 || len(in.DocIDs) > 200) {
		writeDomainError(w, domain.ErrInvalidArgument)
		return
	}
	ctx := r.Context()
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	p, err := s.cfg.Store.GetProject(ctx, in.ProjectID)
	if err == nil && p.DatasetID != in.DatasetID {
		err = domain.ErrForbidden
	}
	if err != nil {
		writeDomainError(w, err)
		return
	}
	slices.Sort(in.DocIDs)
	ids := slices.Compact(in.DocIDs)
	if !all {
		raw, err := json.Marshal(ids)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		var eligible int
		err = s.cfg.Store.DB().QueryRowContext(ctx, `SELECT count(*) FROM json_each(?) x JOIN catalog_docs d ON d.doc_id=x.value
			WHERE d.dataset_id=? AND d.is_knowledge=1`, string(raw), in.DatasetID).Scan(&eligible)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		if eligible != len(ids) {
			writeDomainError(w, domain.ErrNotFound)
			return
		}
	}
	scope, err := s.cfg.Store.EnsureReadScope(ctx, in.Identity, in.ProjectID, in.DatasetID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if all {
		var horizon string
		var rowID int64
		err = s.cfg.Store.DB().QueryRowContext(ctx, `SELECT MAX(?,COALESCE(MAX(created_at_ns) FILTER(WHERE created_at_invalid=0),'')),
			COALESCE(MAX(cognee_rowid),0) FROM catalog_docs WHERE dataset_id=? AND is_knowledge=1`,
			domain.NowUTCNS(), in.DatasetID).Scan(&horizon, &rowID)
		if err == nil {
			err = s.cfg.Store.MarkAllReadInScope(ctx, scope.ScopeHash, horizon, max(0, rowID))
		}
		if err != nil {
			writeDomainError(w, err)
			return
		}
	} else {
		for _, id := range ids {
			if err := s.cfg.Store.SetReadItem(ctx, scope.ScopeHash, id, true); err != nil {
				writeDomainError(w, err)
				return
			}
		}
	}
	view, err := readStateFor(ctx, s.cfg.Store.DB(), datasetRef{ID: in.DatasetID, ProjectID: in.ProjectID, ScopeHash: scope.ScopeHash})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, 200, struct {
		readingStateView
		Marked int `json:"marked"`
	}{view, len(ids)})
}

func (s *Server) graph(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ds := q.Get("dataset_id")
	ctx := r.Context()
	if id := q.Get("project_id"); id != "" {
		p, err := s.cfg.Store.GetProject(ctx, id)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			writeDomainError(w, err)
			return
		}
		if err != nil || ds != "" && ds != p.DatasetID && (!p.CodeEnabled || ds != p.CodeDatasetID) {
			writeDomainError(w, domain.ErrForbidden)
			return
		}
		if ds == "" {
			ds = p.DatasetID
		}
	}
	if err := s.managedDataset(ctx, ds); err != nil {
		writeDomainError(w, err)
		return
	}
	payload, err := s.cfg.Engine.Graph(ctx, engine.GraphRequest{Scope: engine.Scope{DatasetID: ds}, Filter: q.Get("filter")})
	if err == nil && !payload.OK {
		err = engine.ErrUnavailable
	}
	if err != nil {
		writeEngineError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "dataset_id": ds, "nodes": append([]engine.GraphNode{}, payload.Nodes...),
		"edges": append([]engine.GraphEdge{}, payload.Edges...)})
}
