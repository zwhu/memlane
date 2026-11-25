package domain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const provenanceDDL = `
CREATE TABLE IF NOT EXISTS keyed_provenance (
 dataset_id TEXT NOT NULL, lesson_type TEXT NOT NULL, state_key TEXT NOT NULL, first_source_date TEXT NOT NULL,
 source_date_status TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(dataset_id,lesson_type,state_key)
);
CREATE TABLE IF NOT EXISTS document_provenance (
 dataset_id TEXT NOT NULL, document_id TEXT NOT NULL, lesson_type TEXT NOT NULL, state_key TEXT NOT NULL,
 first_source_date TEXT NOT NULL, source_date_status TEXT NOT NULL, source_qa_ids_json TEXT NOT NULL,
 updated_at TEXT NOT NULL, sync_seq INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(dataset_id,document_id)
);
CREATE INDEX IF NOT EXISTS idx_document_provenance_sync_seq ON document_provenance(sync_seq);
`

type DateEvidence struct{ Date, Status string }

type KeyedProvenance struct {
	DatasetID, LessonType, StateKey, FirstSourceDate, SourceDateStatus, UpdatedAt string
}

type DocumentProvenance struct {
	DatasetID, DocumentID, LessonType, StateKey, FirstSourceDate, SourceDateStatus string
	SourceQAIDs                                                                    []string
	UpdatedAt                                                                      string
	SyncSeq                                                                        int64
}

type ResolveLessonDateInput struct {
	DatasetID, DocumentID, LessonType, StateKey, CreatedAt string
	SystemMetadata, ExternalMetadata                       map[string]any
	Location                                               *time.Location
}

type ResolvedLessonDate struct {
	Date, Status string
	SourceQAIDs  []string
}

func ProvenanceStatusRank(status string) int {
	switch status {
	case "created_at", "created_at_fallback":
		return 1
	case "legacy_json_locked":
		return 2
	case "session_date":
		return 3
	case "linked_qa":
		return 4
	default:
		return 0
	}
}

func parseProvenanceDate(raw string) (time.Time, string) {
	raw = strings.TrimSpace(raw)
	if len(raw) < 10 {
		return time.Time{}, ""
	}
	head := raw[:10]
	day, err := time.Parse(time.DateOnly, head)
	if err != nil || day.Year() == 0 {
		return time.Time{}, ""
	}
	if len(raw) == 10 {
		return day, head
	}
	// Legacy inputs include UTC offsets, a space separator and naive UTC timestamps.
	_, separatorSize := utf8.DecodeRuneInString(raw[10:])
	raw = head + "T" + raw[10+separatorSize:]
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05Z0700", "2006-01-02T15:04:05", "2006-01-02T15:04Z07:00", "2006-01-02T15:04", "2006-01-02T15"} {
		if stamp, err := time.Parse(layout, raw); err == nil {
			return stamp, head
		}
	}
	return time.Time{}, ""
}

func CalendarDate(raw string) string {
	_, date := parseProvenanceDate(raw)
	return date
}

func LocalCalendarDate(raw string, loc *time.Location) string {
	stamp, date := parseProvenanceDate(raw)
	if date == "" || len(strings.TrimSpace(raw)) == 10 {
		return date
	}
	if loc == nil {
		loc = time.Local
	}
	return stamp.In(loc).Format(time.DateOnly)
}

func SelectBestDateEvidence(pairs []DateEvidence) (string, string) {
	date, status := "", "unconfirmed"
	for _, pair := range pairs {
		day := CalendarDate(pair.Date)
		if day == "" {
			continue
		}
		candidate := pair.Status
		if ProvenanceStatusRank(candidate) == 0 {
			candidate = "created_at"
		}
		if date == "" || day < date || day == date && ProvenanceStatusRank(candidate) > ProvenanceStatusRank(status) {
			date, status = day, candidate
		}
	}
	return date, status
}

func mergeQAIDs(groups ...[]string) []string {
	ids := []string{}
	for _, group := range groups {
		for _, id := range group {
			if id != "" {
				ids = append(ids, id)
			}
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

func getDocumentProvenance(ctx context.Context, db sqlReader, datasetID, documentID string) (DocumentProvenance, error) {
	var p DocumentProvenance
	var qaJSON string
	err := db.QueryRowContext(ctx, `SELECT dataset_id,document_id,lesson_type,state_key,first_source_date,source_date_status,
		source_qa_ids_json,updated_at,sync_seq FROM document_provenance WHERE dataset_id=? AND document_id=?`, datasetID, documentID).
		Scan(&p.DatasetID, &p.DocumentID, &p.LessonType, &p.StateKey, &p.FirstSourceDate, &p.SourceDateStatus, &qaJSON, &p.UpdatedAt, &p.SyncSeq)
	if err != nil {
		return p, rowError(err)
	}
	return p, json.Unmarshal([]byte(qaJSON), &p.SourceQAIDs)
}

func getKeyedProvenance(ctx context.Context, db sqlReader, datasetID, lessonType, stateKey string) (KeyedProvenance, error) {
	var p KeyedProvenance
	err := db.QueryRowContext(ctx, `SELECT dataset_id,lesson_type,state_key,first_source_date,source_date_status,updated_at
		FROM keyed_provenance WHERE dataset_id=? AND lesson_type=? AND state_key=?`, datasetID, lessonType, stateKey).
		Scan(&p.DatasetID, &p.LessonType, &p.StateKey, &p.FirstSourceDate, &p.SourceDateStatus, &p.UpdatedAt)
	return p, rowError(err)
}

func (s *Store) GetDocumentProvenance(ctx context.Context, datasetID, documentID string) (DocumentProvenance, error) {
	return getDocumentProvenance(ctx, s.db, datasetID, documentID)
}

func (s *Store) GetKeyedProvenance(ctx context.Context, datasetID, lessonType, stateKey string) (KeyedProvenance, error) {
	return getKeyedProvenance(ctx, s.db, datasetID, lessonType, stateKey)
}

func (s *Store) LockDocumentProvenance(ctx context.Context, p DocumentProvenance) (DocumentProvenance, error) {
	if err := requireIDs(p.DatasetID, p.DocumentID); err != nil {
		return DocumentProvenance{}, err
	}
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		doc, err := getDocumentProvenance(ctx, tx, p.DatasetID, p.DocumentID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		p.LessonType, p.StateKey = defaultText(p.LessonType, doc.LessonType), defaultText(p.StateKey, doc.StateKey)
		var key KeyedProvenance
		if p.LessonType != "" && p.StateKey != "" {
			key, err = getKeyedProvenance(ctx, tx, p.DatasetID, p.LessonType, p.StateKey)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
		}
		p.FirstSourceDate, p.SourceDateStatus = SelectBestDateEvidence([]DateEvidence{
			{doc.FirstSourceDate, doc.SourceDateStatus}, {key.FirstSourceDate, key.SourceDateStatus}, {p.FirstSourceDate, p.SourceDateStatus},
		})
		p.SourceQAIDs = mergeQAIDs(doc.SourceQAIDs, p.SourceQAIDs)
		p.UpdatedAt = NowUTCMicro()
		if p.LessonType != "" && p.StateKey != "" && p.FirstSourceDate != "" &&
			(CalendarDate(key.FirstSourceDate) == "" || p.FirstSourceDate < key.FirstSourceDate ||
				p.FirstSourceDate == key.FirstSourceDate && ProvenanceStatusRank(p.SourceDateStatus) > ProvenanceStatusRank(key.SourceDateStatus)) {
			if err := execSQL(ctx, tx, `INSERT INTO keyed_provenance VALUES(?,?,?,?,?,?)
				ON CONFLICT(dataset_id,lesson_type,state_key) DO UPDATE SET first_source_date=excluded.first_source_date,
				source_date_status=excluded.source_date_status,updated_at=excluded.updated_at`,
				p.DatasetID, p.LessonType, p.StateKey, p.FirstSourceDate, p.SourceDateStatus, p.UpdatedAt); err != nil {
				return err
			}
		}
		qaJSON, err := json.Marshal(p.SourceQAIDs)
		if err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `INSERT INTO document_provenance VALUES(?,?,?,?,?,?,?,?,(SELECT COALESCE(MAX(sync_seq),0)+1 FROM document_provenance))
			ON CONFLICT(dataset_id,document_id) DO UPDATE SET lesson_type=excluded.lesson_type,state_key=excluded.state_key,
			first_source_date=excluded.first_source_date,source_date_status=excluded.source_date_status,source_qa_ids_json=excluded.source_qa_ids_json,
			updated_at=excluded.updated_at,sync_seq=excluded.sync_seq RETURNING sync_seq`,
			p.DatasetID, p.DocumentID, p.LessonType, p.StateKey, p.FirstSourceDate, p.SourceDateStatus, string(qaJSON), p.UpdatedAt).Scan(&p.SyncSeq)
	})
	if err != nil {
		return DocumentProvenance{}, err
	}
	return p, nil
}

func (s *Store) ResolveLessonDate(ctx context.Context, in ResolveLessonDateInput) (ResolvedLessonDate, error) {
	var result ResolvedLessonDate
	err := s.readTx(ctx, func(tx *sql.Tx) error {
		doc, err := getDocumentProvenance(ctx, tx, in.DatasetID, in.DocumentID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		meta := in.SystemMetadata
		if len(meta) == 0 {
			meta = in.ExternalMetadata
		}
		lt := defaultText(in.LessonType, defaultText(metadataString(meta, "lesson_type"), doc.LessonType))
		sk := defaultText(in.StateKey, defaultText(metadataString(meta, "state_key"), doc.StateKey))
		var key KeyedProvenance
		if lt != "" && sk != "" {
			key, err = getKeyedProvenance(ctx, tx, in.DatasetID, lt, sk)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
		}
		result.Date, result.Status = SelectBestDateEvidence([]DateEvidence{{doc.FirstSourceDate, doc.SourceDateStatus}, {key.FirstSourceDate, key.SourceDateStatus}})
		if result.Date != "" {
			result.SourceQAIDs = doc.SourceQAIDs
		} else {
			result = resolveMetadataDate(in)
		}
		return nil
	})
	return result, err
}

func metadataString(meta map[string]any, key string) string {
	value, _ := meta[key].(string)
	return strings.TrimSpace(value)
}

func metadataQAIDs(meta map[string]any) []string {
	switch ids := meta["source_qa_ids"].(type) {
	case []string:
		return mergeQAIDs(ids)
	case []any:
		list := make([]string, 0, len(ids))
		for _, id := range ids {
			if value, ok := id.(string); ok {
				list = append(list, value)
			}
		}
		return mergeQAIDs(list)
	default:
		return nil
	}
}

func resolveMetadataDate(in ResolveLessonDateInput) ResolvedLessonDate {
	system, external := in.SystemMetadata, in.ExternalMetadata
	qaIDs := metadataQAIDs(system)
	sessionDay := CalendarDate(metadataString(system, "session_date"))
	if date := CalendarDate(metadataString(system, "first_source_date")); date != "" {
		status := "unconfirmed"
		declared := metadataString(system, "source_date_status")
		switch {
		case len(qaIDs) > 0:
			status = "linked_qa"
		case declared == "session_date" || declared == "created_at":
			status = declared
		case sessionDay == date:
			status = "session_date"
		}
		return ResolvedLessonDate{date, status, qaIDs}
	}
	if sessionDay != "" {
		return ResolvedLessonDate{sessionDay, "session_date", qaIDs}
	}
	if len(system) == 0 {
		if date := CalendarDate(metadataString(external, "session_date")); date != "" {
			return ResolvedLessonDate{Date: date, Status: "session_date"}
		}
	}
	if date := LocalCalendarDate(in.CreatedAt, in.Location); date != "" {
		return ResolvedLessonDate{Date: date, Status: "created_at"}
	}
	if len(system) == 0 {
		return ResolvedLessonDate{Date: CalendarDate(metadataString(external, "first_source_date")), Status: "unconfirmed"}
	}
	return ResolvedLessonDate{Status: "unconfirmed"}
}
