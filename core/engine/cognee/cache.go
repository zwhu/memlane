package cognee

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/zwhu/memlane/core/engine"
)

var (
	reUserRequestBlock        = regexp.MustCompile(`(?is)<USER_REQUEST>\s*(.*?)\s*</USER_REQUEST>`)
	reAdditionalMetadataBlock = regexp.MustCompile(`(?is)<ADDITIONAL_METADATA>.*?</ADDITIONAL_METADATA>`)
	reAdditionalMetadataOpen  = regexp.MustCompile(`(?is)<ADDITIONAL_METADATA>.*$`)
	reUserSettingsChangeBlock = regexp.MustCompile(`(?is)<USER_SETTINGS_CHANGE>.*?</USER_SETTINGS_CHANGE>`)
	reUserSettingsChangeOpen  = regexp.MustCompile(`(?is)<USER_SETTINGS_CHANGE>.*$`)
	reUserRequestTag          = regexp.MustCompile(`(?i)</?USER_REQUEST>`)
)

func cleanSessionLabel(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	if m := reUserRequestBlock.FindStringSubmatch(text); len(m) == 2 {
		text = m[1]
	}
	text = reAdditionalMetadataBlock.ReplaceAllString(text, "")
	text = reAdditionalMetadataOpen.ReplaceAllString(text, "")
	text = reUserSettingsChangeBlock.ReplaceAllString(text, "")
	text = reUserSettingsChangeOpen.ReplaceAllString(text, "")
	text = reUserRequestTag.ReplaceAllString(text, "")
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	return string(runes[:min(120, len(runes))])
}

type SessionKey = engine.SessionRef

type SessionRef = engine.SessionRef

type SessionCacheInfo = engine.SessionCacheInfo

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
			info.Label = cleanSessionLabel(metadataText(parseJSONDict(payload), "question"))
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

const maxSessionTextRunes = 32000

func (p *Projector) ReadSessionHistory(ctx context.Context, req engine.SessionHistoryRequest) (engine.SessionHistoryResult, error) {
	out := engine.SessionHistoryResult{
		QAs:    []engine.SessionQA{},
		Traces: []engine.SessionTrace{},
	}
	if strings.TrimSpace(req.SessionID) == "" {
		return out, nil
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	now := time.Now().UTC().Format("2006-01-02 15:04:05.000000")
	return readSQLiteSnapshot(ctx, p.database("cache.db"), func(tx *sql.Tx) (engine.SessionHistoryResult, error) {
		var whereParts []string
		var baseArgs []any
		if variants := idVariants(req.UserID); len(variants) > 0 {
			whereParts = append(whereParts, "user_id IN ("+placeholders(len(variants))+")")
			for _, v := range variants {
				baseArgs = append(baseArgs, v)
			}
		}
		whereParts = append(whereParts, "session_id=?")
		baseArgs = append(baseArgs, req.SessionID)
		whereParts = append(whereParts, "(expires_at IS NULL OR REPLACE(substr(expires_at,1,26),'T',' ')>?)")
		baseArgs = append(baseArgs, now)
		where := " WHERE " + strings.Join(whereParts, " AND ")

		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM cache_qa_entries"+where, baseArgs...).Scan(&out.MsgCount); err != nil {
			return out, err
		}
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM cache_trace_entries"+where, baseArgs...).Scan(&out.ToolCalls); err != nil {
			return out, err
		}

		labelRows, err := tx.QueryContext(ctx, "SELECT payload FROM cache_qa_entries"+where+" ORDER BY seq LIMIT 10", baseArgs...)
		if err != nil {
			return out, err
		}
		for labelRows.Next() {
			var payload string
			if err := labelRows.Scan(&payload); err != nil {
				labelRows.Close()
				return out, err
			}
			if label := cleanSessionLabel(metadataText(parseJSONDict(payload), "question")); label != "" {
				out.Label = label
				break
			}
		}
		if err := labelRows.Err(); err != nil {
			labelRows.Close()
			return out, err
		}
		labelRows.Close()

		qaArgs := append(slices.Clone(baseArgs), limit, max(0, req.QAOffset))
		qaRows, err := tx.QueryContext(ctx, "SELECT COALESCE(qa_id,''), payload FROM cache_qa_entries"+where+" ORDER BY seq LIMIT ? OFFSET ?", qaArgs...)
		if err != nil {
			return out, err
		}
		defer qaRows.Close()
		for qaRows.Next() {
			var qaID, payload string
			if err := qaRows.Scan(&qaID, &payload); err != nil {
				return out, err
			}
			dict := parseJSONDict(payload)
			q, qTrunc := truncateRunes(metadataText(dict, "question"), maxSessionTextRunes)
			a, aTrunc := truncateRunes(metadataText(dict, "answer"), maxSessionTextRunes)
			if qaID == "" {
				qaID = metadataText(dict, "qa_id")
			}
			out.QAs = append(out.QAs, engine.SessionQA{
				ID:                qaID,
				Question:          q,
				Answer:            a,
				QuestionTruncated: qTrunc,
				AnswerTruncated:   aTrunc,
			})
		}
		if err := qaRows.Err(); err != nil {
			return out, err
		}

		traceArgs := append(slices.Clone(baseArgs), limit, max(0, req.TraceOffset))
		traceRows, err := tx.QueryContext(ctx, "SELECT payload FROM cache_trace_entries"+where+" ORDER BY seq LIMIT ? OFFSET ?", traceArgs...)
		if err != nil {
			return out, err
		}
		defer traceRows.Close()
		for traceRows.Next() {
			var payload string
			if err := traceRows.Scan(&payload); err != nil {
				return out, err
			}
			out.Traces = append(out.Traces, parseSessionTrace(payload))
		}
		return out, traceRows.Err()
	})
}

func truncateRunes(text string, limit int) (string, bool) {
	runes := []rune(text)
	if len(runes) <= limit {
		return text, false
	}
	return string(runes[:limit]), true
}

func parseSessionTrace(payload string) engine.SessionTrace {
	var raw struct {
		ID     string          `json:"trace_id"`
		Name   string          `json:"origin_function"`
		Status string          `json:"status"`
		Params json.RawMessage `json:"method_params"`
		Result json.RawMessage `json:"method_return_value"`
		Error  string          `json:"error_message"`
	}
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return engine.SessionTrace{}
	}
	return engine.SessionTrace{
		ID:     raw.ID,
		Name:   raw.Name,
		Status: raw.Status,
		Params: raw.Params,
		Result: raw.Result,
		Error:  raw.Error,
	}
}
