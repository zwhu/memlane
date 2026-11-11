package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zwhu/memlane/internal/records"
)

type homeActivityCursor struct {
	MS      int64  `json:"ms"`
	ID      string `json:"id"`
	Project string `json:"project"`
	Kind    string `json:"kind"`
}

// HomeActivities reads durable version facts and successfully committed daily
// Agent usage. App browsing and connection probes never become daily activity.
// It does not prune activity/version history to the usage retention window.
func (s *Store) HomeActivities(ctx context.Context, q records.HomeActivityQuery) (records.HomeActivityPage, error) {
	out := records.HomeActivityPage{Items: []records.HomeActivity{}}
	if q.Limit == 0 {
		q.Limit = 8
	}
	if q.Limit < 1 || q.Limit > 100 || len(q.Before) > 4096 {
		return out, access("invalid_request", "bounded activity query required")
	}
	switch q.Kind {
	case "", "save", "edit", "restore", "delete", "read", "return", "adopt":
	default:
		return out, access("invalid_request", "unknown activity filter")
	}
	// Keep the event's exact stored version/title. Resolving an event opens the
	// current knowledge explicitly, without claiming that it is the viewed version.

	dataset := "m.dataset"
	if s.Schema >= 5 {
		dataset = "COALESCE((SELECT sh.previous_scope FROM scope_history sh WHERE sh.memory=m.id AND sh.revision>v.revision ORDER BY sh.revision LIMIT 1),m.dataset)"
	}
	union := `SELECT 'version:'||v.memory||':'||v.revision AS event_id,v.operation AS kind,'saved' AS channel,CASE WHEN v.operation='save' THEN md.source ELSE '' END AS agent,v.memory,` + dataset + ` AS dataset,sc.name AS scope_name,sc.kind AS scope_kind,v.revision,v.title,md.origin,CAST(ROUND((julianday(v.created_at)-2440587.5)*86400000) AS INTEGER) AS occurred_ms,m.dataset AS current_dataset,current_scope.kind AS current_scope_kind FROM versions v JOIN memories m ON m.id=v.memory JOIN memory_metadata md ON md.memory=m.id JOIN scopes sc ON sc.id=` + dataset + ` JOIN scopes current_scope ON current_scope.id=m.dataset`
	if s.Schema >= 10 {
		union += ` UNION ALL SELECT 'usage:'||u.id||':'||u.memory||':'||u.revision,u.kind,u.channel,COALESCE(c.agent,'Agent'),u.memory,u.dataset,sc.name,sc.kind,u.revision,v.title,md.origin,u.observed_ms,m.dataset,current_scope.kind FROM usage_events u JOIN memories m ON m.id=u.memory JOIN versions v ON v.memory=u.memory AND v.revision=u.revision JOIN memory_metadata md ON md.memory=u.memory JOIN scopes sc ON sc.id=u.dataset JOIN scopes current_scope ON current_scope.id=m.dataset LEFT JOIN connections c ON c.id=u.connection WHERE u.channel='daily' AND u.kind IN ('read','return','adopt')`
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()

	// Classify only exact immutable first-save identities. A verification read
	// of ordinary knowledge never removes that record's real activity facts.
	synthetic, err := syntheticConnectionActivityMemories(ctx, tx)
	if err != nil {
		return out, err
	}
	excluded, err := json.Marshal(synthetic)
	if err != nil {
		return out, err
	}
	where := []string{"occurred_ms IS NOT NULL", "memory NOT IN (SELECT value FROM json_each(?))"}
	args := []any{string(excluded)}
	if q.Project != "" {
		where = append(where, "dataset=?")
		args = append(args, q.Project)
	}
	if q.Kind != "" {
		where = append(where, "kind=?")
		args = append(args, q.Kind)
	}
	base := ` WITH facts AS (` + union + `) `
	filter := strings.Join(where, " AND ")
	if err := tx.QueryRowContext(ctx, base+`SELECT count(*) FROM facts WHERE `+filter, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	if q.Before != "" {
		raw, err := base64.RawURLEncoding.DecodeString(q.Before)
		var cursor homeActivityCursor
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.ID == "" || cursor.Project != q.Project || cursor.Kind != q.Kind {
			return out, access("invalid_request", "activity cursor does not match filters")
		}
		where = append(where, "(occurred_ms<? OR (occurred_ms=? AND event_id<?))")
		args = append(args, cursor.MS, cursor.MS, cursor.ID)
	}
	args = append(args, q.Limit+1)
	rows, err := tx.QueryContext(ctx, base+`SELECT event_id,kind,channel,agent,memory,dataset,scope_name,scope_kind,revision,title,origin,occurred_ms,current_dataset,current_scope_kind FROM facts WHERE `+strings.Join(where, " AND ")+` ORDER BY occurred_ms DESC,event_id DESC LIMIT ?`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var e records.HomeActivity
		if err = rows.Scan(&e.ID, &e.Kind, &e.Channel, &e.Agent, &e.Memory, &e.Dataset, &e.ScopeName, &e.ScopeKind, &e.Revision, &e.Title, &e.Origin, &e.OccurredMS, &e.CurrentDataset, &e.CurrentScopeKind); err != nil {
			return out, err
		}
		out.Items = append(out.Items, e)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > q.Limit {
		out.Items = out.Items[:q.Limit]
		last := out.Items[len(out.Items)-1]
		raw, err := json.Marshal(homeActivityCursor{MS: last.OccurredMS, ID: last.ID, Project: q.Project, Kind: q.Kind})
		if err != nil {
			return out, fmt.Errorf("activity cursor: %w", err)
		}
		out.Next = base64.RawURLEncoding.EncodeToString(raw)
	}
	return out, nil
}

// No content or origin is migrated. This projection excludes only known
// synthetic handshake identities and retains immutable facts for diagnostics.
func syntheticConnectionActivityMemories(ctx context.Context, tx *sql.Tx) ([]string, error) {
	out := []string{}
	rows, err := tx.QueryContext(ctx, `SELECT v.memory,o.id,md.origin,v.text,md.source_detail FROM versions v JOIN operations o ON o.memory=v.memory AND o.revision=v.revision JOIN memory_metadata md ON md.memory=v.memory WHERE v.revision=1 AND v.operation='save' AND md.origin='raw' AND CASE WHEN json_valid(md.source_detail) THEN json_extract(md.source_detail,'$.fragment') ELSE '' END=?`, records.ConnectionVerificationFragment)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var memory, operation, origin, text, raw string
		if err = rows.Scan(&memory, &operation, &origin, &text, &raw); err != nil {
			return nil, err
		}
		var source records.AgentSource
		if json.Unmarshal([]byte(raw), &source) != nil {
			continue
		}
		if records.IsSyntheticConnectionVerification(memory, operation, origin, text, &source) {
			out = append(out, memory)
		}
	}
	return out, rows.Err()
}
