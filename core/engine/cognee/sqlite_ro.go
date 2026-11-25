package cognee

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/core/domain"
	"modernc.org/sqlite"
	sqliteLib "modernc.org/sqlite/lib"
)

func openOfficialSQLiteRO(ctx context.Context, path string) (*sql.DB, error) {
	return retryReadSQLite(ctx, func() (*sql.DB, error) {
		return openOfficialSQLiteOnce(ctx, path)
	})
}

func openOfficialSQLiteOnce(ctx context.Context, path string) (*sql.DB, error) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Lstat(path + suffix)
		if suffix != "" && errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("official SQLite path is not a regular file: %s", path+suffix)
		}
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	values := url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout=2000", "query_only=ON", "cache_size=-512"}}
	dsn := url.URL{Scheme: "file", Path: filepath.ToSlash(absPath), RawQuery: values.Encode()}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	var page string
	if err := db.QueryRowContext(ctx, "SELECT name FROM sqlite_master LIMIT 1").Scan(&page); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, errors.Join(err, db.Close())
	}
	return db, nil
}

func readSQLiteSnapshot[T any](ctx context.Context, path string, read func(*sql.Tx) (T, error)) (T, error) {
	return retryReadSQLite(ctx, func() (T, error) {
		return readSQLiteOnce(ctx, path, read)
	})
}

func retryReadSQLite[T any](ctx context.Context, run func() (T, error)) (T, error) {
	var value T
	var err error
	for _, delay := range []time.Duration{0, 50 * time.Millisecond, 150 * time.Millisecond, 300 * time.Millisecond} {
		if delay != 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				var zero T
				return zero, ctx.Err()
			case <-timer.C:
			}
		}
		value, err = run()
		if err == nil || !transientSQLiteError(err) {
			break
		}
	}
	return value, err
}

func readSQLiteOnce[T any](ctx context.Context, path string, read func(*sql.Tx) (T, error)) (value T, err error) {
	db, err := openOfficialSQLiteOnce(ctx, path)
	if err != nil {
		return value, err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return value, err
	}
	defer func() {
		if rollbackErr := tx.Rollback(); !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, rollbackErr)
		}
	}()
	return read(tx)
}

func transientSQLiteError(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	switch sqliteErr.Code() & 0xff {
	case sqliteLib.SQLITE_BUSY, sqliteLib.SQLITE_LOCKED, sqliteLib.SQLITE_IOERR, sqliteLib.SQLITE_CANTOPEN, sqliteLib.SQLITE_PROTOCOL:
		return true
	}
	return false
}

func columns(ctx context.Context, tx *sql.Tx, table string) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, "SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

func textColumns(cols map[string]bool, names ...string) string {
	expressions := make([]string, len(names))
	for i, name := range names {
		expressions[i] = "''"
		if cols[name] {
			expressions[i] = "COALESCE(CAST(" + name + " AS TEXT),'')"
		}
	}
	return strings.Join(expressions, ",")
}

func placeholders(n int) string {
	return strings.Repeat("?,", n-1) + "?"
}

func uuidText(raw string) string {
	raw = strings.TrimSpace(raw)
	if id, err := uuid.Parse(raw); err == nil {
		return id.String()
	}
	return raw
}

func uuidHex(raw string) string {
	if id, err := uuid.Parse(strings.TrimSpace(raw)); err == nil {
		return strings.ReplaceAll(id.String(), "-", "")
	}
	return ""
}

func idVariants(raw string) []string {
	raw = strings.TrimSpace(raw)
	var variants []string
	for _, candidate := range []string{uuidHex(raw), uuidText(raw), raw} {
		if candidate != "" && !slices.Contains(variants, candidate) {
			variants = append(variants, candidate)
		}
	}
	return variants
}

func isoText(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 19 && raw[10] == ' ' {
		return raw[:10] + "T" + raw[11:]
	}
	return raw
}

func parseTimestamp(raw string) time.Time {
	if len(raw) < 19 || raw[10] != 'T' || strings.Contains(raw, ",") {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05"} {
		if stamp, err := time.Parse(layout, raw); err == nil && stamp.Year() > 0 {
			return stamp
		}
	}
	return time.Time{}
}

func dayToDescKey(day string) int64 {
	if day == "" {
		return 0
	}
	key, _ := strconv.ParseInt(strings.ReplaceAll(day, "-", ""), 10, 64)
	return -key
}

func parseJSONDict(raw string) map[string]any {
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil
	}
	if nested, ok := parsed.(string); ok {
		if err := json.Unmarshal([]byte(nested), &parsed); err != nil {
			return nil
		}
	}
	result, _ := parsed.(map[string]any)
	return result
}

func decodeNodeSet(value any) []string {
	for range 2 {
		raw, ok := value.(string)
		if !ok {
			break
		}
		var parsed any
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil || parsed == nil {
			break
		}
		value = parsed
	}
	var set []string
	switch v := value.(type) {
	case string:
		if tag := strings.TrimSpace(v); tag != "" {
			set = append(set, tag)
		}
	case []string:
		return cleanedNodeSet(v)
	case []any:
		tags := make([]string, 0, len(v))
		for _, item := range v {
			if tag, ok := item.(string); ok {
				tags = append(tags, tag)
			}
		}
		return cleanedNodeSet(tags)
	}
	return set
}

func cleanedNodeSet(tags []string) []string {
	set := make([]string, 0, len(tags))
	for _, tag := range tags {
		if tag = strings.TrimSpace(tag); tag != "" && !slices.Contains(set, tag) {
			set = append(set, tag)
		}
	}
	return set
}

func metadataText(meta map[string]any, key string) string {
	text, _ := meta[key].(string)
	return text
}

func hasSystemMetadata(raw string) bool {
	raw = strings.TrimSpace(raw)
	return raw != "" && raw != "{}"
}

func extractTrustedMetadata(system, external, columnTags string) map[string]any {
	meta := parseJSONDict(external)
	if meta == nil {
		meta = map[string]any{}
	}
	tags := decodeNodeSet(columnTags)
	if hasSystemMetadata(system) {
		for _, key := range []string{"lesson_type", "state_key", "first_source_date", "session_date",
			"source_date_status", "source_qa_ids", "session_id", "seen_in_sessions", "seen_sessions",
			"memlane_kind", "category", "common_category", "node_set"} {
			delete(meta, key)
		}
		systemMeta := parseJSONDict(system)
		for key, value := range systemMeta {
			meta[key] = value
		}
		if sysTags := decodeNodeSet(systemMeta["node_set"]); len(sysTags) != 0 {
			tags = sysTags
		}
	} else if len(tags) == 0 {
		tags = decodeNodeSet(meta["node_set"])
	}
	if len(tags) != 0 {
		meta["node_set"] = tags
	}
	return meta
}

func extractCognifyStatus(raw, dataset string) string {
	slots, _ := parseJSONDict(raw)["cognify_pipeline"].(map[string]any)
	for _, variant := range []string{dataset, uuidHex(dataset)} {
		switch slot := slots[variant].(type) {
		case map[string]any:
			return metadataText(slot, "status")
		case string:
			return slot
		}
	}
	return ""
}

var lessonLabels = map[string]string{
	"state": "项目状态", "pitfall": "踩坑经验", "convention": "项目约定",
	"location": "代码位置与关联", "change": "代码变更", "constraint": "约束条件", "other": "其他",
}

func classifyDocument(d *projectedDoc, meta map[string]any, tags []string) {
	d.Kind = "raw_doc"
	if metadataText(meta, "memlane_kind") == "common_knowledge" {
		if cat := metadataText(meta, "category"); cat == "preference" || cat == "experience" {
			d.Kind, d.CommonCategory = "common", cat
		}
	}
	sessionLesson := slices.Contains(tags, "session_learnings")
	directMemory := slices.Contains(tags, "user_context") || slices.Contains(tags, "memory")
	if d.Kind != "common" && (sessionLesson || directMemory) {
		d.Kind = "knowledge"
	}
	d.IsKnowledge = d.Kind != "raw_doc"
	if !d.IsKnowledge {
		return
	}
	d.LessonType = strings.TrimSpace(metadataText(meta, "lesson_type"))
	d.StateKey = strings.TrimSpace(metadataText(meta, "state_key"))
	d.SessionID = uniqueLearningSession(meta)
	d.Category = knowledgeCategoryLabel(meta)
}

func knowledgeCategoryLabel(meta map[string]any) string {
	if metadataText(meta, "memlane_kind") == "common_knowledge" {
		if cat := metadataText(meta, "category"); cat == "preference" || cat == "experience" {
			return ""
		}
	}
	tags := decodeNodeSet(meta["node_set"])
	if !slices.Contains(tags, "session_learnings") {
		if slices.Contains(tags, "user_context") || slices.Contains(tags, "memory") {
			return "直接保存"
		}
		return ""
	}
	kind := strings.TrimSpace(metadataText(meta, "lesson_type"))
	if kind == "" {
		return "未记录"
	}
	if label := lessonLabels[kind]; label != "" {
		return label
	}
	return "未识别分类（" + kind + "）"
}

func uniqueLearningSession(meta map[string]any) string {
	session := ""
	for _, tag := range decodeNodeSet(meta["node_set"]) {
		id, ok := strings.CutPrefix(tag, "session_learnings:")
		if !ok {
			continue
		}
		if id == "" || session != "" && id != session {
			return ""
		}
		session = id
	}
	if metaID := metadataText(meta, "session_id"); metaID != "" && metaID != session {
		return ""
	}
	return session
}

func applyLessonEvidence(d *projectedDoc, meta map[string]any, systemPresent, sessionLesson bool) {
	firstDay := domain.CalendarDate(metadataText(meta, "first_source_date"))
	sessionDay := domain.CalendarDate(metadataText(meta, "session_date"))
	status := d.EffectiveDateSource
	var qaIDs []string
	switch {
	case systemPresent && firstDay != "":
		d.EffectiveDate, d.EffectiveDateSource = firstDay, "meta"
		qaIDs = sourceQAIDs(meta)
		declared := metadataText(meta, "source_date_status")
		switch {
		case len(qaIDs) != 0:
			status = "linked_qa"
		case declared == "created_at" || declared == "session_date":
			status = declared
		case sessionDay == firstDay:
			status = "session_date"
		default:
			status = "unconfirmed"
		}
	case sessionDay != "":
		d.EffectiveDate, d.EffectiveDateSource, status = sessionDay, "meta", "session_date"
		if systemPresent {
			qaIDs = sourceQAIDs(meta)
		}
	case d.EffectiveDate == "" && !systemPresent && firstDay != "":
		d.EffectiveDate, d.EffectiveDateSource, status = firstDay, "meta", "unconfirmed"
	}
	if sessionLesson || d.LessonType != "" || firstDay != "" || metadataText(meta, "source_date_status") != "" {
		d.provenance = &domain.DocumentProvenance{DatasetID: d.DatasetID, DocumentID: d.DocID, LessonType: d.LessonType,
			StateKey: d.StateKey, FirstSourceDate: d.EffectiveDate, SourceDateStatus: status, SourceQAIDs: qaIDs}
	}
}

func sourceQAIDs(meta map[string]any) []string {
	rawIDs, _ := meta["source_qa_ids"].([]any)
	var ids []string
	for _, rawID := range rawIDs {
		if id, ok := rawID.(string); ok && id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func normalizeUTCNS(raw string) (string, int64, bool) {
	stamp := parseTimestamp(isoText(raw))
	if stamp.IsZero() {
		return "", 0, true
	}
	epoch := stamp.UnixNano()
	if stamp.Before(time.Unix(0, math.MinInt64+1)) {
		epoch = math.MinInt64 + 1
	}
	if stamp.After(time.Unix(0, math.MaxInt64)) {
		epoch = math.MaxInt64
	}
	return domain.FormatUTCNS(stamp), epoch, false
}

func localDayFromUTCNS(raw string, loc *time.Location) string {
	stamp := parseTimestamp(raw)
	if stamp.IsZero() {
		return ""
	}
	if loc == nil {
		loc = time.Local
	}
	return stamp.In(loc).Format(time.DateOnly)
}
