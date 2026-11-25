package cognee

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

type Projector struct {
	storePath  string
	loc        *time.Location
	bodyReader *BodyReader
	syncMu     sync.Mutex
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
		ds.Protected = domain.IsProtectedProject("", ds.Name)
		if ds.ID != "" {
			datasets = append(datasets, ds)
		}
	}
	return datasets, rows.Err()
}

type managedDataset struct {
	variants  []string
	protected bool
}

func managedDatasets(ctx context.Context, store *domain.Store) (map[string]managedDataset, error) {
	projects, err := store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	common, err := store.GetCommonConfig(ctx)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	datasets := map[string]managedDataset{}
	add := func(raw string, protected bool) {
		if id := uuidText(raw); id != "" {
			ds := datasets[id]
			ds.protected = ds.protected || protected
			for _, v := range idVariants(raw) {
				if !slices.Contains(ds.variants, v) {
					ds.variants = append(ds.variants, v)
				}
			}
			datasets[id] = ds
		}
	}
	for _, project := range projects {
		add(project.DatasetID, domain.IsProtectedProject(project.Name, project.DatasetName))
		add(project.CodeDatasetID, false)
	}
	add(common.DatasetID, domain.IsProtectedProject(common.Name, common.DatasetName))
	return datasets, nil
}

type projection struct {
	docs     []projectedDoc
	sessions []domain.CatalogSession
	states   []domain.CatalogSyncState
}

type projectedDoc struct {
	domain.CatalogDoc
	provenance *domain.DocumentProvenance
}

func (p *Projector) SyncCatalog(ctx context.Context, store *domain.Store) (*engine.SyncStats, error) {
	if store == nil {
		return nil, fmt.Errorf("catalog store: %w", domain.ErrInvalidArgument)
	}
	started := time.Now()
	p.syncMu.Lock()
	defer p.syncMu.Unlock()
	datasets, err := managedDatasets(ctx, store)
	if err != nil {
		return nil, fmt.Errorf("load managed datasets: %w", err)
	}
	snapshot := projection{}
	if len(datasets) != 0 {
		snapshot, err = readSQLiteSnapshot(ctx, p.database("cognee_db"), func(tx *sql.Tx) (projection, error) {
			return p.readProjection(ctx, tx, datasets)
		})
		if err != nil {
			return nil, fmt.Errorf("read official catalog snapshot: %w", err)
		}
	}
	if err := p.resolveDocumentTitles(ctx, store, snapshot.docs); err != nil {
		return nil, fmt.Errorf("resolve catalog titles: %w", err)
	}
	stats := &engine.SyncStats{
		Datasets: len(snapshot.states), DocsProjected: len(snapshot.docs), SessionsProjected: len(snapshot.sessions),
	}
	if stats.ProvenanceLocked, err = lockLessonProvenance(ctx, store, snapshot.docs); err != nil {
		return nil, fmt.Errorf("lock lesson provenance: %w", err)
	}
	counts, err := recallCounts(ctx, store, snapshot.states)
	if err != nil {
		return nil, fmt.Errorf("read catalog recall counts: %w", err)
	}
	if err := store.WithTx(ctx, func(tx *sql.Tx) error {
		return publishCatalog(ctx, tx, snapshot, counts, stats)
	}); err != nil {
		return nil, fmt.Errorf("publish catalog: %w", err)
	}
	users := map[string]struct{}{}
	for _, session := range snapshot.sessions {
		if session.UserID != "" {
			users[session.UserID] = struct{}{}
		}
	}
	stats.DistinctSessionUsers = len(users)
	stats.Duration = time.Since(started)
	return stats, nil
}

func (p *Projector) readProjection(ctx context.Context, tx *sql.Tx, datasets map[string]managedDataset) (projection, error) {
	discovered, err := discoverDatasets(ctx, tx)
	if err != nil {
		return projection{}, err
	}
	sources := make(map[string]bool, len(discovered))
	for _, source := range discovered {
		sources[source.ID] = sources[source.ID] || source.Protected
	}
	dataCols, err := columns(ctx, tx, "data")
	if err != nil {
		return projection{}, err
	}
	sessionCols, err := columns(ctx, tx, "session_records")
	if err != nil {
		return projection{}, err
	}
	ids := make([]string, 0, len(datasets))
	for id := range datasets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := projection{}
	for _, id := range ids {
		ds := datasets[id]
		args := make([]any, len(ds.variants))
		for i, variant := range ds.variants {
			args[i] = variant
		}
		docs, err := p.readDocuments(ctx, tx, id, ds.protected || sources[id], dataCols, args)
		if err != nil {
			return projection{}, err
		}
		sessions, err := readSessions(ctx, tx, id, sessionCols, args)
		if err != nil {
			return projection{}, err
		}
		result.docs = append(result.docs, docs...)
		result.sessions = append(result.sessions, sessions...)
		if _, exists := sources[id]; discovered == nil || exists || len(docs)+len(sessions) != 0 {
			result.states = append(result.states, syncState(id, docs, sessions))
		}
	}
	return result, nil
}

func (p *Projector) readDocuments(ctx context.Context, tx *sql.Tx, id string, protected bool, cols map[string]bool, args []any) ([]projectedDoc, error) {
	if len(cols) == 0 {
		return nil, nil
	}
	query := "SELECT rowid," + textColumns(cols, "id", "name", "label", "raw_data_location", "content_hash",
		"created_at", "updated_at", "system_metadata", "external_metadata", "node_set", "pipeline_status")
	rows, err := tx.QueryContext(ctx, query+" FROM data WHERE dataset_id IN ("+placeholders(len(args))+") ORDER BY rowid", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	docs := []projectedDoc{}
	positions := map[string]int{}
	for rows.Next() {
		d := projectedDoc{CatalogDoc: domain.CatalogDoc{DatasetID: id}}
		var sys, ext, tags, pipeline string
		if err := rows.Scan(&d.CogneeRowID, &d.DocID, &d.DocName, &d.DocLabel, &d.RawDataLocation, &d.ContentHash,
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

func (p *Projector) projectDocument(d *projectedDoc, protected bool, sys, ext, tags, pipeline string) bool {
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

func (p *Projector) resolveDocumentTitles(ctx context.Context, store *domain.Store, docs []projectedDoc) error {
	candidates := make(map[docKey]*projectedDoc)
	for i := range docs {
		d := &docs[i]
		if d.IsKnowledge && !readableTitle(d.DocLabel) && !readableTitle(d.DocName) && d.RawDataLocation != "" {
			candidates[docKey{d.DatasetID, d.DocID}] = d
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	if err := reuseDocumentTitles(ctx, store, candidates); err != nil {
		return err
	}
	for _, d := range candidates {
		result, err := p.bodyReader.ReadBody(ctx, engine.LocalBodyRef{Location: d.RawDataLocation, MaxBytes: 4096})
		if err == nil {
			d.DocLabel = bodyTitle(result.Body)
		}
	}
	return ctx.Err()
}

func reuseDocumentTitles(ctx context.Context, store *domain.Store, candidates map[docKey]*projectedDoc) error {
	rows, err := store.DB().QueryContext(ctx, `SELECT dataset_id,doc_id,doc_label,content_hash,raw_data_location
		FROM catalog_docs WHERE is_knowledge=1 AND doc_label<>''`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key docKey
		var label, hash, location string
		if err := rows.Scan(&key.dataset, &key.doc, &label, &hash, &location); err != nil {
			return err
		}
		if d := candidates[key]; d != nil && d.ContentHash == hash && d.RawDataLocation == location && readableTitle(label) {
			d.DocLabel = label
			delete(candidates, key)
		}
	}
	return rows.Err()
}

func readSessions(ctx context.Context, tx *sql.Tx, id string, cols map[string]bool, args []any) ([]domain.CatalogSession, error) {
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
	sessions := []domain.CatalogSession{}
	positions := map[SessionKey]int{}
	for rows.Next() {
		s := domain.CatalogSession{DatasetID: id}
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

func syncState(id string, docs []projectedDoc, sessions []domain.CatalogSession) domain.CatalogSyncState {
	state := domain.CatalogSyncState{DatasetID: id, DatasetHex: uuidHex(id), DocCount: len(docs),
		SessionCount: len(sessions), CatalogVersion: "1", SyncedAt: domain.NowUTCNS()}
	for _, d := range docs {
		state.MaxRowID = max(state.MaxRowID, d.CogneeRowID)
		state.SumRowID += d.CogneeRowID
		state.MaxUpdatedAt = max(state.MaxUpdatedAt, d.SourceUpdatedAt)
		switch d.Kind {
		case "knowledge":
			state.KnowledgeCount++
		case "common":
			state.CommonCount++
		case "raw_doc":
			state.RawDocCount++
		}
	}
	return state
}

type docKey struct{ dataset, doc string }

func publishCatalog(ctx context.Context, tx *sql.Tx, snapshot projection, counts map[docKey]int, stats *engine.SyncStats) error {
	sequences, maxSeq, err := existingSequences(ctx, tx)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM catalog_docs; DELETE FROM catalog_sessions; DELETE FROM catalog_sync_state;"); err != nil {
		return err
	}
	if err := publishDocs(ctx, tx, snapshot.docs, sequences, maxSeq, counts, stats); err != nil {
		return err
	}
	sessionStmt, err := tx.PrepareContext(ctx, `INSERT INTO catalog_sessions(dataset_id,user_id,session_id,agent_id,status,
		started_at,last_activity_at,sort_ts_desc_key,parent_session_id,root_session_id,subagent_role,nesting_depth,is_top_level,child_count)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer sessionStmt.Close()
	for _, s := range snapshot.sessions {
		if _, err := sessionStmt.ExecContext(ctx, s.DatasetID, s.UserID, s.SessionID, s.AgentID, s.Status,
			s.StartedAt, s.LastActivityAt, s.SortTSDescKey, s.ParentSessionID, s.RootSessionID, s.SubagentRole, s.NestingDepth, s.IsTopLevel, s.ChildCount); err != nil {
			return err
		}
	}
	for _, state := range snapshot.states {
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_sync_state(dataset_id,dataset_hex,doc_count,max_rowid,sum_rowid,
			max_updated_at,knowledge_count,raw_doc_count,common_count,session_count,catalog_version,synced_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			state.DatasetID, state.DatasetHex, state.DocCount, state.MaxRowID, state.SumRowID, state.MaxUpdatedAt,
			state.KnowledgeCount, state.RawDocCount, state.CommonCount, state.SessionCount, state.CatalogVersion, state.SyncedAt); err != nil {
			return err
		}
	}
	return nil
}

func existingSequences(ctx context.Context, tx *sql.Tx) (map[docKey]int64, int64, error) {
	rows, err := tx.QueryContext(ctx, "SELECT dataset_id,doc_id,doc_seq FROM catalog_docs")
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	sequences := map[docKey]int64{}
	var maxSeq int64
	for rows.Next() {
		var key docKey
		var seq int64
		if err := rows.Scan(&key.dataset, &key.doc, &seq); err != nil {
			return nil, 0, err
		}
		sequences[key] = seq
		maxSeq = max(maxSeq, seq)
	}
	return sequences, maxSeq, rows.Err()
}

func recallCounts(ctx context.Context, store *domain.Store, states []domain.CatalogSyncState) (map[docKey]int, error) {
	counts := map[docKey]int{}
	for _, state := range states {
		for _, ds := range idVariants(state.DatasetID) {
			items, err := store.ListRecallDocCounts(ctx, ds)
			if err != nil {
				return nil, err
			}
			for _, item := range items {
				counts[docKey{state.DatasetID, uuidText(item.DocumentID)}] += item.TotalHits
			}
		}
	}
	return counts, nil
}

func publishDocs(ctx context.Context, tx *sql.Tx, docs []projectedDoc, sequences map[docKey]int64, maxSeq int64, counts map[docKey]int, stats *engine.SyncStats) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO catalog_docs(dataset_id,doc_id,cognee_rowid,kind,is_knowledge,
		common_category,lesson_type,state_key,category,session_id,doc_name,doc_label,raw_data_location,content_hash,
		created_at_raw,created_at_ns,created_at_invalid,updated_at_ns,effective_date,effective_date_source,day_desc_key,
		sort_ts_desc_key,recall_count,recall_desc_key,pipeline_status,source_updated_at,doc_seq)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, d := range docs {
		key := docKey{d.DatasetID, d.DocID}
		d.DocSeq = sequences[key]
		if d.DocSeq == 0 {
			maxSeq++
			d.DocSeq = maxSeq
		}
		d.RecallCount = counts[key]
		d.RecallDescKey = -int64(d.RecallCount)
		if d.provenance != nil {
			if err := applyLockedDate(ctx, tx, &d); err != nil {
				return err
			}
		}
		switch d.Kind {
		case "knowledge":
			stats.KnowledgeCount++
		case "common":
			stats.CommonCount++
		case "raw_doc":
			stats.RawDocCount++
			if d.PipelineStatus != "DATA_ITEM_PROCESSING_COMPLETED" && d.PipelineStatus != "not_applicable" {
				stats.PendingRawCount++
			}
		}
		if _, err := stmt.ExecContext(ctx, d.DatasetID, d.DocID, d.CogneeRowID, d.Kind, d.IsKnowledge, d.CommonCategory,
			d.LessonType, d.StateKey, d.Category, d.SessionID, d.DocName, d.DocLabel, d.RawDataLocation, d.ContentHash,
			d.CreatedAtRaw, d.CreatedAtNS, d.CreatedAtInvalid, d.UpdatedAtNS, d.EffectiveDate, d.EffectiveDateSource,
			d.DayDescKey, d.SortTSDescKey, d.RecallCount, d.RecallDescKey, d.PipelineStatus, d.SourceUpdatedAt, d.DocSeq); err != nil {
			return err
		}
	}
	return nil
}

func applyLockedDate(ctx context.Context, tx *sql.Tx, d *projectedDoc) error {
	p := d.provenance
	day := p.FirstSourceDate
	if p.LessonType != "" && p.StateKey != "" {
		var keyDay string
		err := tx.QueryRowContext(ctx, "SELECT first_source_date FROM keyed_provenance WHERE dataset_id=? AND lesson_type=? AND state_key=?",
			p.DatasetID, p.LessonType, p.StateKey).Scan(&keyDay)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if keyDay != "" && (day == "" || keyDay < day) {
			day = keyDay
		}
	}
	if day != "" {
		d.EffectiveDate, d.EffectiveDateSource, d.DayDescKey = day, "provenance", dayToDescKey(day)
	}
	return nil
}
