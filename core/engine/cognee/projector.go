package cognee

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/core/engine"
)

type Projector struct {
	storePath  string
	loc        *time.Location
	bodyReader *BodyReader
}

func NewProjector(storePath string, loc *time.Location) *Projector {
	if loc == nil {
		loc = time.Local
	}
	return &Projector{storePath: storePath, loc: loc, bodyReader: NewBodyReader(storePath, nil)}
}

func (p *Projector) database(name string) string {
	return filepath.Join(p.storePath, "system", "databases", name)
}

type DatasetInfo struct {
	ID, Name  string
	Protected bool
}

func isProtectedDatasetName(name string) bool {
	return name == "ml-user-profiles"
}

func (p *Projector) DiscoverDatasets(ctx context.Context) ([]DatasetInfo, error) {
	return readSQLiteSnapshot(ctx, p.database("cognee_db"), func(tx *sql.Tx) ([]DatasetInfo, error) {
		return discoverDatasets(ctx, tx)
	})
}

func discoverDatasets(ctx context.Context, tx *sql.Tx) ([]DatasetInfo, error) {
	cols, err := columns(ctx, tx, "datasets")
	if err != nil || len(cols) == 0 {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+textColumns(cols, "id", "name")+" FROM datasets ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	datasets := make([]DatasetInfo, 0)
	for rows.Next() {
		var ds DatasetInfo
		if err := rows.Scan(&ds.ID, &ds.Name); err != nil {
			return nil, err
		}
		ds.ID = uuidText(ds.ID)
		ds.Protected = isProtectedDatasetName(ds.Name)
		if ds.ID != "" {
			datasets = append(datasets, ds)
		}
	}
	return datasets, rows.Err()
}

type targetDataset struct {
	variants  []string
	protected bool
}

func normalizeTargets(targets []engine.CatalogDatasetTarget) map[string]targetDataset {
	datasets := make(map[string]targetDataset, len(targets))
	for _, target := range targets {
		if id := uuidText(target.DatasetID); id != "" {
			ds := datasets[id]
			ds.protected = ds.protected || target.Protected
			for _, v := range idVariants(target.DatasetID) {
				if !slices.Contains(ds.variants, v) {
					ds.variants = append(ds.variants, v)
				}
			}
			datasets[id] = ds
		}
	}
	return datasets
}

func (p *Projector) SnapshotCatalog(ctx context.Context, targets []engine.CatalogDatasetTarget, opts engine.CatalogSnapshotOptions) (*engine.CatalogSnapshot, error) {
	datasets := normalizeTargets(targets)
	if len(datasets) == 0 {
		return &engine.CatalogSnapshot{}, nil
	}
	snapshot, err := readSQLiteSnapshot(ctx, p.database("cognee_db"), func(tx *sql.Tx) (*engine.CatalogSnapshot, error) {
		return p.readSnapshot(ctx, tx, datasets, opts.SessionsOnly)
	})
	if err != nil {
		return nil, fmt.Errorf("read official catalog snapshot: %w", err)
	}
	if !opts.SessionsOnly {
		if err := p.resolveDocumentTitles(ctx, snapshot, opts.TitleHints); err != nil {
			return nil, fmt.Errorf("resolve catalog titles: %w", err)
		}
	}
	return snapshot, nil
}

func (p *Projector) readSnapshot(ctx context.Context, tx *sql.Tx, datasets map[string]targetDataset, sessionsOnly bool) (*engine.CatalogSnapshot, error) {
	discovered, err := discoverDatasets(ctx, tx)
	if err != nil {
		return nil, err
	}
	sources := make(map[string]bool, len(discovered))
	for _, source := range discovered {
		sources[source.ID] = sources[source.ID] || source.Protected
	}
	var dataCols map[string]bool
	if !sessionsOnly {
		dataCols, err = columns(ctx, tx, "data")
		if err != nil {
			return nil, err
		}
	}
	sessionCols, err := columns(ctx, tx, "session_records")
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(datasets))
	for id := range datasets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := &engine.CatalogSnapshot{Datasets: make([]engine.CatalogDatasetSnapshot, 0, len(ids))}
	for _, id := range ids {
		ds := datasets[id]
		args := make([]any, len(ds.variants))
		for i, variant := range ds.variants {
			args[i] = variant
		}
		var docs []engine.CatalogSnapshotDoc
		if !sessionsOnly {
			docs, err = p.readDocuments(ctx, tx, id, ds.protected || sources[id], dataCols, args)
			if err != nil {
				return nil, err
			}
		}
		sessions, err := readSessions(ctx, tx, id, sessionCols, args)
		if err != nil {
			return nil, err
		}
		_, inDiscovered := sources[id]
		exists := discovered == nil || inDiscovered || len(docs)+len(sessions) != 0
		result.Datasets = append(result.Datasets, engine.CatalogDatasetSnapshot{
			DatasetID: id,
			Exists:    exists,
			Docs:      docs,
			Sessions:  sessions,
		})
	}
	return result, nil
}

func (p *Projector) readDocuments(ctx context.Context, tx *sql.Tx, id string, protected bool, cols map[string]bool, args []any) ([]engine.CatalogSnapshotDoc, error) {
	if len(cols) == 0 {
		return nil, nil
	}
	query := "SELECT " + textColumns(cols, "id", "name", "label", "raw_data_location", "content_hash",
		"created_at", "updated_at", "system_metadata", "external_metadata", "node_set", "pipeline_status")
	rows, err := tx.QueryContext(ctx, query+" FROM data WHERE dataset_id IN ("+placeholders(len(args))+") ORDER BY rowid", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	docs := []engine.CatalogSnapshotDoc{}
	positions := map[string]int{}
	for rows.Next() {
		d := engine.CatalogSnapshotDoc{DatasetID: id}
		var sys, ext, tags, pipeline string
		if err := rows.Scan(&d.DocID, &d.DocName, &d.DocLabel, &d.RawDataLocation, &d.ContentHash,
			&d.CreatedAtRaw, &d.SourceUpdatedAt, &sys, &ext, &tags, &pipeline); err != nil {
			return nil, err
		}
		d.DocID = uuidText(d.DocID)
		if d.DocID == "" || !p.projectDocument(&d, protected, sys, ext, tags, pipeline) {
			continue
		}
		if previous, exists := positions[d.DocID]; exists {
			docs[previous] = d
		} else {
			positions[d.DocID] = len(docs)
			docs = append(docs, d)
		}
	}
	return docs, rows.Err()
}

func (p *Projector) projectDocument(d *engine.CatalogSnapshotDoc, protected bool, sys, ext, tags, pipeline string) bool {
	meta := extractTrustedMetadata(sys, ext, tags)
	nodeSet := decodeNodeSet(meta["node_set"])
	d.PipelineStatus = extractCognifyStatus(pipeline, d.DatasetID)
	if protected || metadataText(meta, "memlane_kind") == "user_profile" || metadataText(parseJSONDict(ext), "memlane_kind") == "user_profile" {
		d.PipelineStatus = "not_applicable"
	}
	if !readableTitle(d.DocLabel) {
		d.DocLabel = ""
		if title := cleanTitle(metadataText(meta, "title")); readableTitle(title) {
			d.DocLabel = title
		}
	}
	classifyDocument(d, meta, nodeSet)
	if d.Kind == "raw_doc" && (slices.Contains(nodeSet, "user_sessions_from_cache") || slices.Contains(nodeSet, "agent_trace_feedbacks")) {
		return false
	}
	d.CreatedAtRaw, d.SourceUpdatedAt = isoText(d.CreatedAtRaw), isoText(d.SourceUpdatedAt)
	createdNS, createdEpoch, createdInvalid := normalizeUTCNS(d.CreatedAtRaw)
	updatedNS, updatedEpoch, updatedInvalid := normalizeUTCNS(d.SourceUpdatedAt)
	d.CreatedAtNS, d.UpdatedAtNS, d.CreatedAtInvalid = createdNS, updatedNS, createdInvalid
	if !createdInvalid {
		d.EffectiveDate, d.EffectiveDateSource = localDayFromUTCNS(createdNS, p.loc), "created_at"
	}
	if !updatedInvalid && !d.IsKnowledge {
		d.EffectiveDate, d.EffectiveDateSource = localDayFromUTCNS(updatedNS, p.loc), "updated_at"
	}
	d.SortTSDescKey = -updatedEpoch
	if updatedInvalid {
		d.SortTSDescKey = -createdEpoch
	}
	if d.IsKnowledge {
		applyLessonEvidence(d, meta, hasSystemMetadata(sys), slices.Contains(nodeSet, "session_learnings"))
	}
	d.DayDescKey = dayToDescKey(d.EffectiveDate)
	return true
}

var machineDocName = regexp.MustCompile(`(?i)^(text_[0-9a-f]{32}(\.(md|txt))?|(user_context|memory)-[0-9a-f]{32}(\.txt)?)$`)
var orderedTitleMarker = regexp.MustCompile(`^[0-9]+[.)][[:space:]]+`)

func readableTitle(title string) bool {
	title = strings.TrimSpace(title)
	if title == "" || machineDocName.MatchString(title) {
		return false
	}
	_, err := uuid.Parse(title)
	return err != nil
}

func cleanTitle(line string) string {
	line = strings.TrimLeft(strings.TrimSpace(line), "#>*-+ \t")
	line = orderedTitleMarker.ReplaceAllString(line, "")
	title := strings.Join(strings.Fields(line), " ")
	if utf8.RuneCountInString(title) > 80 {
		title = strings.TrimSpace(string([]rune(title)[:79])) + "…"
	}
	return title
}

func bodyTitle(body string) string {
	var fence byte
	var fenceSize int
	for line := range strings.SplitSeq(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			size := len(trimmed) - len(strings.TrimLeft(trimmed, string(trimmed[0])))
			if fence == 0 {
				fence, fenceSize = trimmed[0], size
			} else if trimmed[0] == fence && size >= fenceSize && strings.TrimSpace(trimmed[size:]) == "" {
				fence = 0
			}
			continue
		}
		if fence != 0 || strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") ||
			strings.HasPrefix(trimmed, "![") && strings.HasSuffix(trimmed, ")") {
			continue
		}
		title := cleanTitle(trimmed)
		if strings.HasPrefix(title, "Lesson-Type:") || strings.HasPrefix(title, "State-Key:") ||
			strings.HasPrefix(title, "Session learning (session ") {
			continue
		}
		if readableTitle(title) {
			return title
		}
	}
	return ""
}

func (p *Projector) resolveDocumentTitles(ctx context.Context, snapshot *engine.CatalogSnapshot, hints map[engine.CatalogDocKey]engine.CatalogTitleHint) error {
	for i := range snapshot.Datasets {
		docs := snapshot.Datasets[i].Docs
		for j := range docs {
			d := &docs[j]
			if !d.IsKnowledge || readableTitle(d.DocLabel) || readableTitle(d.DocName) || d.RawDataLocation == "" {
				continue
			}
			key := engine.CatalogDocKey{DatasetID: d.DatasetID, DocID: d.DocID}
			if hint, ok := hints[key]; ok && d.ContentHash == hint.ContentHash && d.RawDataLocation == hint.RawDataLocation && readableTitle(hint.DocLabel) {
				d.DocLabel = hint.DocLabel
				continue
			}
			result, err := p.bodyReader.ReadBody(ctx, engine.LocalBodyRef{Location: d.RawDataLocation, MaxBytes: 4096})
			if err == nil {
				d.DocLabel = bodyTitle(result.Body)
			}
		}
	}
	return ctx.Err()
}

func readSessions(ctx context.Context, tx *sql.Tx, id string, cols map[string]bool, args []any) ([]engine.CatalogSnapshotSession, error) {
	if len(cols) == 0 {
		return nil, nil
	}
	query := "SELECT " + textColumns(cols, "session_id", "user_id", "agent_id", "status", "started_at", "last_activity_at",
		"parent_session_id", "root_session_id", "subagent_role") + "," + sessionRelationColumns(cols)
	rows, err := tx.QueryContext(ctx, query+" FROM session_records WHERE dataset_id IN ("+placeholders(len(args))+") ORDER BY rowid", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sessions := []engine.CatalogSnapshotSession{}
	positions := map[SessionKey]int{}
	for rows.Next() {
		s := engine.CatalogSnapshotSession{DatasetID: id}
		if err := rows.Scan(&s.SessionID, &s.UserID, &s.AgentID, &s.Status, &s.StartedAt, &s.LastActivityAt,
			&s.ParentSessionID, &s.RootSessionID, &s.SubagentRole, &s.NestingDepth, &s.IsTopLevel, &s.ChildCount); err != nil {
			return nil, err
		}
		s.SessionID, s.UserID = strings.TrimSpace(s.SessionID), uuidText(s.UserID)
		if s.SessionID == "" {
			continue
		}
		s.StartedAt, s.LastActivityAt = isoText(s.StartedAt), isoText(s.LastActivityAt)
		_, sortEpoch, invalid := normalizeUTCNS(s.LastActivityAt)
		if invalid {
			_, sortEpoch, _ = normalizeUTCNS(s.StartedAt)
		}
		s.SortTSDescKey = -sortEpoch
		key := SessionKey{UserID: s.UserID, SessionID: s.SessionID}
		if previous, exists := positions[key]; exists {
			sessions[previous] = s
		} else {
			positions[key] = len(sessions)
			sessions = append(sessions, s)
		}
	}
	return sessions, rows.Err()
}

func sessionRelationColumns(cols map[string]bool) string {
	expressions := []string{"0", "1", "0"}
	for i, name := range []string{"nesting_depth", "is_top_level", "child_count"} {
		if cols[name] {
			expressions[i] = "COALESCE(" + name + "," + expressions[i] + ")"
		}
	}
	if !cols["is_top_level"] && cols["parent_session_id"] {
		expressions[1] = "(COALESCE(parent_session_id,'')='')"
	}
	return strings.Join(expressions, ",")
}
