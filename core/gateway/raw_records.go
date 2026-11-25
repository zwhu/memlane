package gateway

import (
	"context"
	"database/sql"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/zwhu/memlane/core/domain"
)

type rawItem struct {
	DatasetID       string `json:"dataset_id"`
	ProjectID       string `json:"project_id,omitempty"`
	Kind            string `json:"kind"`
	DocID           string `json:"doc_id,omitempty"`
	DocumentID      string `json:"document_id,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	UserID          string `json:"user_id,omitempty"`
	AgentID         string `json:"agent_id,omitempty"`
	Title           string `json:"title"`
	Label           string `json:"label,omitempty"`
	IsSubagent      bool   `json:"is_subagent,omitempty"`
	DocName         string `json:"doc_name,omitempty"`
	DocLabel        string `json:"doc_label,omitempty"`
	Status          string `json:"status,omitempty"`
	StartedAt       string `json:"started_at,omitempty"`
	LastActivityAt  string `json:"last_activity_at,omitempty"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
	RootSessionID   string `json:"root_session_id,omitempty"`
	SubagentRole    string `json:"subagent_role,omitempty"`
	NestingDepth    int    `json:"nesting_depth,omitempty"`
	IsTopLevel      bool   `json:"is_top_level"`
	ChildCount      int    `json:"child_count"`
	ChildrenHasMore bool   `json:"children_has_more"`
	EffectiveDate   string `json:"effective_date,omitempty"`
	CreatedAt       string `json:"created_at,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
}

const rawSessions = `SELECT x.dataset_id,project_id,'session','','',x.session_id,x.user_id,x.agent_id,x.session_id,'','',
	x.status,x.started_at,x.last_activity_at,x.parent_session_id,x.root_session_id,x.subagent_role,x.nesting_depth,x.is_top_level,
	x.child_count,'','','',x.sort_ts_desc_key AS k1,0 AS k2,json_array(x.dataset_id,x.user_id) AS k3,x.session_id AS k4
	FROM catalog_sessions x`

const rawDocuments = `SELECT x.dataset_id,project_id,'document',x.doc_id,x.doc_id,x.session_id,'','',
	COALESCE(NULLIF(x.doc_label,''),x.doc_name),x.doc_name,x.doc_label,x.pipeline_status,'','','','','',0,0,0,
	x.effective_date,x.created_at_raw,x.updated_at_ns,x.sort_ts_desc_key AS k1,1 AS k2,json_array(x.dataset_id) AS k3,x.doc_id AS k4
	FROM catalog_docs x`

func scanRaw(row rowScanner) (pageRow[rawItem], error) {
	var i rawItem
	var k pageKey
	err := row.Scan(&i.DatasetID, &i.ProjectID, &i.Kind, &i.DocID, &i.DocumentID, &i.SessionID, &i.UserID, &i.AgentID,
		&i.Title, &i.DocName, &i.DocLabel, &i.Status, &i.StartedAt, &i.LastActivityAt, &i.ParentSessionID, &i.RootSessionID,
		&i.SubagentRole, &i.NestingDepth, &i.IsTopLevel, &i.ChildCount, &i.EffectiveDate, &i.CreatedAt, &i.UpdatedAt,
		&k.first, &k.second, &k.dataset, &k.id)
	i.Label = i.Title
	i.ChildrenHasMore = i.ChildCount > 0
	i.IsSubagent = i.Kind == "session" && !i.IsTopLevel
	return pageRow[rawItem]{i, k}, err
}

func (s *Server) rawRecords(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	project, parent, kind := r.PathValue("id"), r.PathValue("sid"), q.Get("kind")
	if project == "" {
		project = q.Get("project_id")
	}
	if kind == "" {
		kind = "all"
	}
	if parent != "" {
		kind = "session"
	}
	if kind != "all" && kind != "session" && kind != "document" {
		writeDomainError(w, domain.ErrInvalidArgument)
		return
	}
	ctx := r.Context()
	var out pageResult[rawItem]
	var dataset string
	var total int
	err := snapshot(ctx, s.cfg.Store, func(tx *sql.Tx) error {
		args := []any{}
		var cursorDatasets []string
		scope := ` JOIN (SELECT dataset_id,id AS project_id FROM projects WHERE dataset_id<>''`
		if project != "" {
			if err := tx.QueryRowContext(ctx, "SELECT dataset_id FROM projects WHERE id=?", project).Scan(&dataset); err != nil {
				return err
			}
			if dataset == "" {
				return domain.ErrNotFound
			}
			scope += " AND id=? AND dataset_id=?"
			args = append(args, project, dataset)
			cursorDatasets = []string{project, dataset}
		} else {
			var err error
			cursorDatasets, err = readRows(ctx, tx,
				"SELECT dataset_id FROM projects WHERE dataset_id<>'' ORDER BY dataset_id,id", nil,
				func(row rowScanner) (string, error) {
					var dataset string
					err := row.Scan(&dataset)
					return dataset, err
				})
			if err != nil {
				return err
			}
		}
		scope += ") p ON p.dataset_id=x.dataset_id"
		sessions, docs := rawSessions+scope+` WHERE x.is_top_level=1`, rawDocuments+scope+` WHERE x.kind='raw_doc'`
		source := docs
		switch kind {
		case "all":
			source = sessions + " UNION ALL " + docs
			args = append(args, slices.Clone(args)...)
		case "session":
			source = sessions
			if parent != "" {
				source = rawSessions + scope + " WHERE x.parent_session_id=?"
				args = append(args, parent)
				if userID := q.Get("user_id"); userID != "" {
					source += " AND x.user_id=?"
					args = append(args, userID)
				}
			}
		}
		opts, err := newPageOptions(r, cursorBinding{Endpoint: r.URL.Path, Dataset: tupleDigest(cursorDatasets...), Sort: "date",
			Filter: tupleDigest(kind, parent, q.Get("user_id"))})
		if err != nil {
			return err
		}
		if parent != "" {
			if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM ("+source+")", args...).Scan(&total); err != nil {
				return err
			}
		}
		out, err = page(ctx, tx, s.cursors, opts, source, args, scanRaw)
		return err
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/v1/") {
		writeJSON(w, 200, out)
		return
	}
	if parent != "" {
		writeJSON(w, 200, struct {
			pageResult[rawItem]
			ProjectID string `json:"project_id"`
			DatasetID string `json:"dataset_id"`
			Parent    string `json:"parent_session_id"`
			Total     int    `json:"total_count"`
		}{out, project, dataset, parent, total})
		return
	}
	type desktopEntry struct {
		rawItem
		ItemKind string         `json:"item_kind"`
		SortTS   int64          `json:"sort_ts_ns"`
		Session  *rawItem       `json:"session,omitempty"`
		Document map[string]any `json:"document,omitempty"`
	}
	entries := make([]desktopEntry, 0, len(out.Items))
	for _, item := range out.Items {
		entry := desktopEntry{rawItem: item, ItemKind: item.Kind}
		timestamp := item.LastActivityAt
		if item.Kind == "session" {
			entry.Session = &item
		} else {
			timestamp = item.UpdatedAt
			if timestamp == "" {
				timestamp = item.CreatedAt
			}
			entry.Document = map[string]any{"id": item.DocID, "datasetId": item.DatasetID, "label": item.DocLabel,
				"name": item.DocName, "createdAt": item.CreatedAt, "updatedAt": item.UpdatedAt}
		}
		if at, err := time.Parse(time.RFC3339Nano, timestamp); err == nil {
			entry.SortTS = at.UnixNano()
		}
		entries = append(entries, entry)
	}
	var ready bool
	if dataset != "" {
		if err := s.cfg.Store.DB().QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM catalog_sync_state WHERE dataset_id=?)", dataset).Scan(&ready); err != nil {
			writeDomainError(w, err)
			return
		}
	}
	writeJSON(w, 200, map[string]any{"project_id": project, "dataset_id": dataset, "kind": kind, "cache_ready": ready,
		"items": entries, "next_cursor": out.NextCursor, "has_more": out.HasMore})
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	ds, sid := r.PathValue("ds"), r.PathValue("sid")
	ctx := r.Context()
	if err := s.managedDataset(ctx, ds); err != nil {
		writeDomainError(w, err)
		return
	}
	query := rawSessions + " CROSS JOIN (SELECT '' AS project_id) WHERE x.dataset_id=? AND x.session_id=?"
	args := []any{ds, sid}
	if user := r.URL.Query().Get("user_id"); user != "" {
		query += " AND x.user_id=?"
		args = append(args, user)
	}
	item, err := scanRaw(s.cfg.Store.DB().QueryRowContext(ctx, query+" ORDER BY x.user_id LIMIT 1", args...))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, 200, item.item)
}

func rawSessionSearch(ctx context.Context, db sqlRead, scope string, args []any, limit int) ([]rawItem, error) {
	rows, err := readRows(ctx, db, rawSessions+scope+" ORDER BY k1,k3,k4 LIMIT ?", append(args, limit), scanRaw)
	if err != nil {
		return nil, err
	}
	items := make([]rawItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, row.item)
	}
	return items, nil
}
