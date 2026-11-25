package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"
)

type dayBucket struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

type overviewSnapshot struct {
	LocalTZName      string               `json:"local_tz_name"`
	TotalKnowledge   int                  `json:"total_knowledge_count"`
	TotalRaw         int                  `json:"total_raw_count"`
	TotalUnread      int                  `json:"total_unread_count"`
	DayBuckets       []dayBucket          `json:"day_buckets"`
	Projects         []projectSummary     `json:"projects"`
	Common           *projectSummary      `json:"common"`
	RecentItems      []catalogItem        `json:"recent_items"`
	RecallEvents     []overviewRecall     `json:"recall_events"`
	RecallStats      []overviewRecallStat `json:"recall_query_stats"`
	RecallEventsMore bool                 `json:"recall_events_more"`
	RecallStartedAt  string               `json:"recall_started_at"`
	Searches         []searchEntry        `json:"recent_searches"`
	Inbox            InboxSummary         `json:"inbox"`
}

func localZone(now time.Time) string {
	if name := now.Location().String(); name != "Local" {
		return name
	}
	if zone := os.Getenv("TZ"); zone != "" {
		return strings.TrimPrefix(zone, ":")
	}
	if path, err := os.Readlink("/etc/localtime"); err == nil {
		if _, name, ok := strings.Cut(path, "/zoneinfo/"); ok {
			return name
		}
	}
	name, _ := now.Zone()
	return name
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	days := parseLimit(q.Get("days"), 365, 3650)
	recent := parseLimit(q.Get("recent_limit"), 20, 50)
	recall := parseLimit(q.Get("recall_limit"), 20, 50)
	now := time.Now()
	out := overviewSnapshot{LocalTZName: localZone(now), Searches: s.searches.recent(parseLimit(q.Get("search_limit"), 20, 50))}
	err := snapshot(ctx, s.cfg.Store, func(tx *sql.Tx) error {
		refs, err := datasetRefs(ctx, tx, readingIdentity(r))
		if err != nil {
			return err
		}
		if err := out.loadProjects(ctx, tx, refs); err != nil {
			return err
		}
		out.Common, err = commonSummary(ctx, tx)
		if err != nil {
			return err
		}
		for _, p := range out.Projects {
			out.TotalKnowledge += p.KnowledgeCount
			out.TotalRaw += p.RawDocCount + p.SessionCount
			out.TotalUnread += p.UnreadCount
		}
		if out.Common != nil {
			out.TotalKnowledge += out.Common.KnowledgeCount
			out.TotalRaw += out.Common.RawDocCount
		}
		out.DayBuckets, err = readRows(ctx, tx, `SELECT effective_date,count(*) FROM catalog_docs d WHERE d.is_knowledge=1
			AND d.effective_date<>'' AND d.effective_date BETWEEN ? AND ? AND d.dataset_id IN (
				SELECT dataset_id FROM projects WHERE dataset_id<>'' UNION SELECT dataset_id FROM common_config WHERE dataset_id<>'')
			GROUP BY effective_date ORDER BY effective_date`,
			[]any{now.AddDate(0, 0, 1-days).Format(time.DateOnly), now.Format(time.DateOnly)}, func(row rowScanner) (dayBucket, error) {
				var bucket dayBucket
				err := row.Scan(&bucket.Day, &bucket.Count)
				return bucket, err
			})
		if err != nil {
			return err
		}
		out.RecentItems, err = recentKnowledge(ctx, tx, refs, recent)
		if err != nil {
			return err
		}
		if err := out.loadRecall(ctx, tx, recall); err != nil {
			return err
		}
		out.Inbox, err = s.inbox.summary(ctx, tx, "")
		return err
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (out *overviewSnapshot) loadProjects(ctx context.Context, tx *sql.Tx, refs []datasetRef) error {
	var err error
	out.Projects, err = readRows(ctx, tx, `SELECT p.id,p.name,p.dataset_id,p.auto_capture,p.auto_capture_version,
		(SELECT count(*) FROM capture_inbox i WHERE i.project_id=p.id AND i.status='pending'),
		(SELECT count(*) FROM capture_inbox i WHERE i.project_id=p.id AND i.status='failed'),
		COALESCE((SELECT i.last_error FROM capture_inbox i WHERE i.project_id=p.id AND i.status='failed'
		 ORDER BY i.updated_at DESC,i.id LIMIT 1),'') FROM projects p ORDER BY p.name,p.id`, nil, func(row rowScanner) (projectSummary, error) {
		var p projectSummary
		err := row.Scan(&p.ProjectID, &p.Name, &p.DatasetID, &p.AutoCapture, &p.AutoCaptureVersion, &p.InboxPending, &p.InboxFailed, &p.LastError)
		if p.LastError != "" {
			p.LastError = sanitizeError(errors.New(p.LastError))
		}
		return p, err
	})
	if err != nil {
		return err
	}
	scopeByProject := make(map[string]datasetRef)
	for _, ref := range refs {
		if ref.Kind == "project" {
			scopeByProject[ref.ProjectID] = ref
		}
	}
	for i := range out.Projects {
		p := &out.Projects[i]
		if err := catalogCounts(ctx, tx, p.DatasetID, p); err != nil {
			return err
		}
		state, err := readStateFor(ctx, tx, scopeByProject[p.ProjectID])
		if err != nil {
			return err
		}
		p.UnreadCount, p.UnreadStateRev = state.UnreadCount, state.StateRev
	}
	return nil
}

func recentKnowledge(ctx context.Context, db sqlRead, refs []datasetRef, limit int) ([]catalogItem, error) {
	raw, err := json.Marshal(refs)
	if err != nil {
		return nil, err
	}
	query := `WITH recent AS MATERIALIZED (
		SELECT ref.value,d.dataset_id,d.doc_id,d.day_desc_key,d.sort_ts_desc_key FROM json_each(?) ref
		JOIN catalog_docs d ON d.dataset_id=json_extract(ref.value,'$.id')
		WHERE d.is_knowledge=1 AND json_extract(ref.value,'$.kind')<>'code'
		ORDER BY d.day_desc_key,d.sort_ts_desc_key,d.dataset_id,d.doc_id LIMIT ?
	) SELECT ` + docFields + "," + unreadExpr + "," + projectField +
		` FROM recent ref JOIN catalog_docs d USING(dataset_id,doc_id)` + readingJoins + recallCountJoin +
		` ORDER BY ref.day_desc_key,ref.sort_ts_desc_key,ref.dataset_id,ref.doc_id`
	return readRows(ctx, db, query, []any{string(raw), limit}, func(row rowScanner) (catalogItem, error) { return scanCatalog(row) })
}

type overviewRecallHit struct {
	DatasetID  string `json:"dataset_id"`
	DocumentID string `json:"document_id"`
}

type overviewRecall struct {
	AgentName  string              `json:"agent_name"`
	AgentKind  string              `json:"agent_kind"`
	AgentID    string              `json:"agent_id"`
	EventID    string              `json:"event_id"`
	Channel    string              `json:"channel"`
	ReceivedAt string              `json:"received_at"`
	Hits       []overviewRecallHit `json:"hits"`
}

type overviewRecallStat struct {
	Channel      string `json:"channel"`
	TotalQueries int    `json:"total_queries"`
	HitQueries   int    `json:"hit_queries"`
	EmptyQueries int    `json:"empty_queries"`
	TotalHits    int    `json:"total_hits"`
	LastQueryAt  string `json:"last_query_at"`
}

func (out *overviewSnapshot) loadRecall(ctx context.Context, db sqlRead, limit int) error {
	var err error
	out.RecallEvents, err = readRows(ctx, db, `SELECT e.agent_id,e.event_id,e.channel,e.received_at,COALESCE(a.name,''),COALESCE(a.kind,''),
		(SELECT json_group_array(json_object('dataset_id',h.dataset_id,'document_id',h.document_id))
		 FROM recall_hits h WHERE h.agent_id=e.agent_id AND h.event_id=e.event_id)
		FROM recall_events e LEFT JOIN agents a ON a.id=e.agent_id ORDER BY e.received_at DESC,e.agent_id,e.event_id LIMIT ?`, []any{limit + 1},
		func(row rowScanner) (overviewRecall, error) {
			var e overviewRecall
			var hits string
			if err := row.Scan(&e.AgentID, &e.EventID, &e.Channel, &e.ReceivedAt, &e.AgentName, &e.AgentKind, &hits); err != nil {
				return e, err
			}
			err := json.Unmarshal([]byte(hits), &e.Hits)
			return e, err
		})
	if err != nil {
		return err
	}
	out.RecallEventsMore = len(out.RecallEvents) > limit
	out.RecallEvents = out.RecallEvents[:min(limit, len(out.RecallEvents))]
	err = db.QueryRowContext(ctx, `SELECT COALESCE(MIN(received_at),'') FROM recall_events`).Scan(&out.RecallStartedAt)
	if err != nil {
		return err
	}
	out.RecallStats, err = readRows(ctx, db, `SELECT channel,total_queries,hit_queries,empty_queries,total_hits,COALESCE(last_query_at,'')
		FROM recall_query_stats ORDER BY channel`, nil, func(row rowScanner) (overviewRecallStat, error) {
		var stat overviewRecallStat
		err := row.Scan(&stat.Channel, &stat.TotalQueries, &stat.HitQueries, &stat.EmptyQueries, &stat.TotalHits, &stat.LastQueryAt)
		return stat, err
	})
	return err
}

func (s *Server) overviewDay(w http.ResponseWriter, r *http.Request) {
	day := r.URL.Query().Get("day")
	if _, err := time.Parse(time.DateOnly, day); err != nil {
		writeError(w, 400, "invalid_day_parameter", "day must use YYYY-MM-DD")
		return
	}
	s.knowledgePage(w, r, "knowledge", day)
}
