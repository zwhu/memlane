package cognee

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"time"
)

type SessionKey struct {
	UserID, SessionID string
}

type SessionRef = SessionKey

type SessionCacheInfo struct {
	Label               string
	MsgCount, ToolCalls int
}

func (p *Projector) HydrateSessionsFromCache(ctx context.Context, refs []SessionRef) (map[SessionKey]SessionCacheInfo, error) {
	grouped := map[string][]string{}
	initial := map[SessionKey]SessionCacheInfo{}
	for _, ref := range refs {
		key := SessionKey{UserID: ref.UserID, SessionID: ref.SessionID}
		if key.UserID != "" && key.SessionID != "" && !slices.Contains(grouped[key.UserID], key.SessionID) {
			grouped[key.UserID] = append(grouped[key.UserID], key.SessionID)
		}
		initial[key] = SessionCacheInfo{}
	}
	if len(grouped) == 0 {
		return initial, nil
	}
	now := time.Now().UTC().Format("2006-01-02 15:04:05.000000")
	result, err := readSQLiteSnapshot(ctx, p.database("cache.db"), func(tx *sql.Tx) (map[SessionKey]SessionCacheInfo, error) {
		return hydrateCache(ctx, tx, initial, grouped, now)
	})
	if err != nil {
		return nil, fmt.Errorf("read session cache snapshot: %w", err)
	}
	return result, nil
}

func hydrateCache(ctx context.Context, tx *sql.Tx, initial map[SessionKey]SessionCacheInfo, grouped map[string][]string, now string) (map[SessionKey]SessionCacheInfo, error) {
	result := make(map[SessionKey]SessionCacheInfo, len(initial))
	for key := range initial {
		result[key] = SessionCacheInfo{}
	}
	for user, sessions := range grouped {
		variants := idVariants(user)
		if len(variants) == 0 {
			continue
		}
		for batch := range slices.Chunk(sessions, 200) {
			args := make([]any, 0, len(variants)+len(batch)+1)
			for _, variant := range variants {
				args = append(args, variant)
			}
			for _, session := range batch {
				args = append(args, session)
			}
			args = append(args, now)
			where := " WHERE user_id IN (" + placeholders(len(variants)) + ") AND session_id IN (" + placeholders(len(batch)) +
				") AND (expires_at IS NULL OR REPLACE(substr(expires_at,1,26),'T',' ')>?)"
			if err := hydrateQuestions(ctx, tx, user, where, args, result); err != nil {
				return nil, err
			}
			if err := hydrateTraces(ctx, tx, user, where, args, result); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

func hydrateQuestions(ctx context.Context, tx *sql.Tx, user, where string, args []any, result map[SessionKey]SessionCacheInfo) error {
	rows, err := tx.QueryContext(ctx, "SELECT session_id, payload FROM cache_qa_entries"+where+" ORDER BY seq", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		key := SessionKey{UserID: user}
		var payload string
		if err := rows.Scan(&key.SessionID, &payload); err != nil {
			return err
		}
		info := result[key]
		info.MsgCount++
		if info.Label == "" {
			label := strings.TrimSpace(metadataText(parseJSONDict(payload), "question"))
			runes := []rune(label)
			info.Label = string(runes[:min(120, len(runes))])
		}
		result[key] = info
	}
	return rows.Err()
}

func hydrateTraces(ctx context.Context, tx *sql.Tx, user, where string, args []any, result map[SessionKey]SessionCacheInfo) error {
	rows, err := tx.QueryContext(ctx, "SELECT session_id, COUNT(*) FROM cache_trace_entries"+where+" GROUP BY session_id", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		key := SessionKey{UserID: user}
		var count int
		if err := rows.Scan(&key.SessionID, &count); err != nil {
			return err
		}
		info := result[key]
		info.ToolCalls = count
		result[key] = info
	}
	return rows.Err()
}
