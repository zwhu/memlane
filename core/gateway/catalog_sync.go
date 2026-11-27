package gateway

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

// catalogSyncCoordinator serializes same-dataset snapshot+publish end-to-end while
// allowing cross-dataset publishes to run in parallel and full catalog sync to run exclusively.
type catalogSyncCoordinator struct {
	fullMu  sync.RWMutex
	dsMu    sync.Mutex
	dsLocks map[string]*sync.Mutex
}

func (c *catalogSyncCoordinator) lockFull() func() {
	c.fullMu.Lock()
	return c.fullMu.Unlock
}

func (c *catalogSyncCoordinator) lockDataset(datasetID string) func() {
	c.fullMu.RLock()
	c.dsMu.Lock()
	if c.dsLocks == nil {
		c.dsLocks = make(map[string]*sync.Mutex)
	}
	mu := c.dsLocks[datasetID]
	if mu == nil {
		mu = &sync.Mutex{}
		c.dsLocks[datasetID] = mu
	}
	c.dsMu.Unlock()
	mu.Lock()
	return func() {
		mu.Unlock()
		c.fullMu.RUnlock()
	}
}

type managedCatalogTarget struct {
	Target    engine.CatalogDatasetTarget
	ProjectID string
}

func canonicalDatasetID(raw string) string {
	raw = strings.TrimSpace(raw)
	if id, err := uuid.Parse(raw); err == nil {
		return id.String()
	}
	return raw
}

func (s *Server) listManagedCatalogTargets(ctx context.Context) ([]managedCatalogTarget, error) {
	projects, err := s.cfg.Store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	common, err := s.cfg.Store.GetCommonConfig(ctx)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	byID := make(map[string]managedCatalogTarget)
	order := make([]string, 0, len(projects)+1)
	add := func(rawDS, projectID string, protected bool) {
		id := canonicalDatasetID(rawDS)
		if id == "" {
			return
		}
		existing, ok := byID[id]
		if !ok {
			order = append(order, id)
			existing = managedCatalogTarget{
				Target:    engine.CatalogDatasetTarget{DatasetID: id, Protected: protected},
				ProjectID: projectID,
			}
		} else {
			existing.Target.Protected = existing.Target.Protected || protected
			if existing.ProjectID == "" {
				existing.ProjectID = projectID
			}
		}
		byID[id] = existing
	}
	for _, p := range projects {
		add(p.DatasetID, p.ID, domain.IsProtectedProject(p.Name, p.DatasetName))
		add(p.CodeDatasetID, p.ID, false)
	}
	add(common.DatasetID, "", domain.IsProtectedProject(common.Name, common.DatasetName))
	out := make([]managedCatalogTarget, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out, nil
}

func (s *Server) resolveManagedDatasetTarget(ctx context.Context, datasetID string) (managedCatalogTarget, bool, error) {
	want := canonicalDatasetID(datasetID)
	if want == "" {
		return managedCatalogTarget{}, false, nil
	}
	targets, err := s.listManagedCatalogTargets(ctx)
	if err != nil {
		return managedCatalogTarget{}, false, err
	}
	for _, t := range targets {
		if t.Target.DatasetID == want {
			return t, true, nil
		}
	}
	return managedCatalogTarget{}, false, nil
}

func toEngineTitleHints(hints map[domain.CatalogDocKey]domain.CatalogTitleHint) map[engine.CatalogDocKey]engine.CatalogTitleHint {
	if len(hints) == 0 {
		return nil
	}
	out := make(map[engine.CatalogDocKey]engine.CatalogTitleHint, len(hints))
	for k, v := range hints {
		out[engine.CatalogDocKey{DatasetID: k.DatasetID, DocID: k.DocID}] = engine.CatalogTitleHint{
			DocLabel:        v.DocLabel,
			ContentHash:     v.ContentHash,
			RawDataLocation: v.RawDataLocation,
		}
	}
	return out
}

func toDomainPublishInput(ds engine.CatalogDatasetSnapshot) domain.CatalogPublishInput {
	docs := make([]domain.CatalogPublishDoc, 0, len(ds.Docs))
	for _, d := range ds.Docs {
		pubDoc := domain.CatalogPublishDoc{
			CatalogDoc: domain.CatalogDoc{
				DatasetID:           d.DatasetID,
				DocID:               d.DocID,
				Kind:                d.Kind,
				IsKnowledge:         d.IsKnowledge,
				CommonCategory:      d.CommonCategory,
				LessonType:          d.LessonType,
				StateKey:            d.StateKey,
				Category:            d.Category,
				SessionID:           d.SessionID,
				DocName:             d.DocName,
				DocLabel:            d.DocLabel,
				RawDataLocation:     d.RawDataLocation,
				ContentHash:         d.ContentHash,
				CreatedAtRaw:        d.CreatedAtRaw,
				CreatedAtNS:         d.CreatedAtNS,
				CreatedAtInvalid:    d.CreatedAtInvalid,
				UpdatedAtNS:         d.UpdatedAtNS,
				EffectiveDate:       d.EffectiveDate,
				EffectiveDateSource: d.EffectiveDateSource,
				DayDescKey:          d.DayDescKey,
				SortTSDescKey:       d.SortTSDescKey,
				PipelineStatus:      d.PipelineStatus,
				SourceUpdatedAt:     d.SourceUpdatedAt,
			},
		}
		if d.Provenance != nil {
			pubDoc.Provenance = &domain.DocumentProvenance{
				DatasetID:        d.Provenance.DatasetID,
				DocumentID:       d.Provenance.DocumentID,
				LessonType:       d.Provenance.LessonType,
				StateKey:         d.Provenance.StateKey,
				FirstSourceDate:  d.Provenance.FirstSourceDate,
				SourceDateStatus: d.Provenance.SourceDateStatus,
				SourceQAIDs:      d.Provenance.SourceQAIDs,
			}
		}
		docs = append(docs, pubDoc)
	}
	return domain.CatalogPublishInput{
		DatasetID: ds.DatasetID,
		Exists:    ds.Exists,
		Docs:      docs,
		Sessions:  toDomainSessions(ds.Sessions),
	}
}

func toDomainSessions(in []engine.CatalogSnapshotSession) []domain.CatalogSession {
	if len(in) == 0 {
		return nil
	}
	out := make([]domain.CatalogSession, 0, len(in))
	for _, s := range in {
		out = append(out, domain.CatalogSession{
			DatasetID:       s.DatasetID,
			UserID:          s.UserID,
			SessionID:       s.SessionID,
			AgentID:         s.AgentID,
			Status:          s.Status,
			StartedAt:       s.StartedAt,
			LastActivityAt:  s.LastActivityAt,
			SortTSDescKey:   s.SortTSDescKey,
			ParentSessionID: s.ParentSessionID,
			RootSessionID:   s.RootSessionID,
			SubagentRole:    s.SubagentRole,
			NestingDepth:    s.NestingDepth,
			IsTopLevel:      s.IsTopLevel,
			ChildCount:      s.ChildCount,
		})
	}
	return out
}

func toEngineSyncStats(stats *domain.CatalogPublishStats, dur time.Duration) *engine.SyncStats {
	if stats == nil {
		return &engine.SyncStats{Duration: dur}
	}
	return &engine.SyncStats{
		Datasets:             stats.Datasets,
		DocsProjected:        stats.DocsProjected,
		KnowledgeCount:       stats.KnowledgeCount,
		CommonCount:          stats.CommonCount,
		RawDocCount:          stats.RawDocCount,
		PendingRawCount:      stats.PendingRawCount,
		SessionsProjected:    stats.SessionsProjected,
		DistinctSessionUsers: stats.DistinctSessionUsers,
		ProvenanceLocked:     stats.ProvenanceLocked,
		Duration:             dur,
	}
}

// SyncCatalog projects all currently managed datasets and cleans up orphan catalog entries.
func (s *Server) SyncCatalog(ctx context.Context) (*engine.SyncStats, error) {
	if s.cfg.Projector == nil {
		return &engine.SyncStats{}, nil
	}
	defer s.catalogSync.lockFull()()
	started := time.Now()
	managed, err := s.listManagedCatalogTargets(ctx)
	if err != nil {
		return nil, err
	}
	if len(managed) == 0 {
		stats, err := s.cfg.Store.PublishFullCatalog(ctx, nil)
		if err != nil {
			return nil, err
		}
		return toEngineSyncStats(stats, time.Since(started)), nil
	}
	targets := make([]engine.CatalogDatasetTarget, 0, len(managed))
	for _, m := range managed {
		targets = append(targets, m.Target)
	}
	hints, err := s.cfg.Store.ListCatalogTitleHints(ctx, nil)
	if err != nil {
		return nil, err
	}
	snapshotAt := domain.NowUTCMicro()
	snap, err := s.cfg.Projector.SnapshotCatalog(ctx, targets, engine.CatalogSnapshotOptions{
		TitleHints: toEngineTitleHints(hints),
	})
	if err != nil {
		return nil, err
	}
	if snap == nil || len(snap.Datasets) == 0 {
		return &engine.SyncStats{Duration: time.Since(started)}, nil
	}
	inputs := make([]domain.CatalogPublishInput, 0, len(snap.Datasets))
	for _, ds := range snap.Datasets {
		in := toDomainPublishInput(ds)
		in.SnapshotAt = snapshotAt
		inputs = append(inputs, in)
	}
	stats, err := s.cfg.Store.PublishFullCatalog(ctx, inputs)
	if err != nil {
		return nil, err
	}
	return toEngineSyncStats(stats, time.Since(started)), nil
}

// SyncDatasetCatalog incrementally refreshes a single dataset's catalog projection.
// If the dataset is no longer managed (e.g. project removed while a job was in flight),
// it cleans up any residual catalog rows instead of resurrecting a ghost catalog.
func (s *Server) SyncDatasetCatalog(ctx context.Context, datasetID string) (*engine.SyncStats, error) {
	if s.cfg.Projector == nil {
		return &engine.SyncStats{}, nil
	}
	dsID := canonicalDatasetID(datasetID)
	if dsID == "" {
		return &engine.SyncStats{}, nil
	}
	defer s.catalogSync.lockDataset(dsID)()
	started := time.Now()
	target, managed, err := s.resolveManagedDatasetTarget(ctx, dsID)
	if err != nil {
		return nil, err
	}
	if !managed {
		if err := s.cfg.Store.DeleteCatalogForDataset(ctx, dsID); err != nil {
			return nil, err
		}
		return &engine.SyncStats{Duration: time.Since(started)}, nil
	}
	hints, err := s.cfg.Store.ListCatalogTitleHints(ctx, []string{target.Target.DatasetID})
	if err != nil {
		return nil, err
	}
	snapshotAt := domain.NowUTCMicro()
	snap, err := s.cfg.Projector.SnapshotCatalog(ctx, []engine.CatalogDatasetTarget{target.Target}, engine.CatalogSnapshotOptions{
		TitleHints: toEngineTitleHints(hints),
	})
	if err != nil {
		return nil, err
	}
	// Re-check management status after engine I/O to avoid resurrecting a removed project's catalog.
	targetAfter, stillManaged, err := s.resolveManagedDatasetTarget(ctx, target.Target.DatasetID)
	if err != nil {
		return nil, err
	}
	if !stillManaged {
		if err := s.cfg.Store.DeleteCatalogForDataset(ctx, target.Target.DatasetID); err != nil {
			return nil, err
		}
		return &engine.SyncStats{Duration: time.Since(started)}, nil
	}
	if snap == nil || len(snap.Datasets) == 0 {
		if err := s.cfg.Store.MarkCatalogPublishOKAfterSnapshot(ctx, targetAfter.Target.DatasetID, targetAfter.ProjectID, snapshotAt); err != nil {
			return nil, err
		}
		return &engine.SyncStats{Duration: time.Since(started)}, nil
	}
	var pubInput domain.CatalogPublishInput
	found := false
	for _, ds := range snap.Datasets {
		if canonicalDatasetID(ds.DatasetID) == targetAfter.Target.DatasetID {
			pubInput = toDomainPublishInput(ds)
			pubInput.DatasetID = targetAfter.Target.DatasetID
			found = true
			break
		}
	}
	if !found {
		pubInput = domain.CatalogPublishInput{DatasetID: targetAfter.Target.DatasetID, Exists: false}
	}
	pubInput.SnapshotAt = snapshotAt
	stats, err := s.cfg.Store.PublishDatasetCatalog(ctx, pubInput)
	if err != nil {
		return nil, err
	}
	return toEngineSyncStats(stats, time.Since(started)), nil
}

// SyncDatasetSessions refreshes only catalog_sessions for a single dataset without rewriting catalog_docs.
func (s *Server) SyncDatasetSessions(ctx context.Context, datasetID string) (*engine.SyncStats, error) {
	if s.cfg.Projector == nil {
		return &engine.SyncStats{}, nil
	}
	dsID := canonicalDatasetID(datasetID)
	if dsID == "" {
		return &engine.SyncStats{}, nil
	}
	defer s.catalogSync.lockDataset(dsID)()
	started := time.Now()
	target, managed, err := s.resolveManagedDatasetTarget(ctx, dsID)
	if err != nil {
		return nil, err
	}
	if !managed {
		if err := s.cfg.Store.DeleteCatalogForDataset(ctx, dsID); err != nil {
			return nil, err
		}
		return &engine.SyncStats{Duration: time.Since(started)}, nil
	}
	snap, err := s.cfg.Projector.SnapshotCatalog(ctx, []engine.CatalogDatasetTarget{target.Target}, engine.CatalogSnapshotOptions{
		SessionsOnly: true,
	})
	if err != nil {
		return nil, err
	}
	targetAfter, stillManaged, err := s.resolveManagedDatasetTarget(ctx, target.Target.DatasetID)
	if err != nil {
		return nil, err
	}
	if !stillManaged {
		if err := s.cfg.Store.DeleteCatalogForDataset(ctx, target.Target.DatasetID); err != nil {
			return nil, err
		}
		return &engine.SyncStats{Duration: time.Since(started)}, nil
	}
	if snap == nil || len(snap.Datasets) == 0 {
		return &engine.SyncStats{Duration: time.Since(started)}, nil
	}
	var sessions []domain.CatalogSession
	for _, ds := range snap.Datasets {
		if canonicalDatasetID(ds.DatasetID) == targetAfter.Target.DatasetID {
			sessions = toDomainSessions(ds.Sessions)
			break
		}
	}
	stats, err := s.cfg.Store.PublishDatasetSessions(ctx, targetAfter.Target.DatasetID, sessions)
	if err != nil {
		return nil, err
	}
	return toEngineSyncStats(stats, time.Since(started)), nil
}

type linkableDatasetView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func normalizedDatasetKey(raw string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(raw), "-", ""))
}

func (s *Server) listLinkableDatasets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var discoverer engine.DatasetDiscoverer
	if d, ok := s.cfg.Projector.(engine.DatasetDiscoverer); ok && d != nil {
		discoverer = d
	} else if d, ok := engine.AsDatasetDiscoverer(s.cfg.Engine); ok && d != nil {
		discoverer = d
	}
	if discoverer == nil {
		writeJSON(w, http.StatusOK, []linkableDatasetView{})
		return
	}
	discovered, err := discoverer.DiscoverDatasets(ctx)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSON(w, http.StatusOK, []linkableDatasetView{})
			return
		}
		writeEngineError(w, err)
		return
	}
	managed, err := s.listManagedCatalogTargets(ctx)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	occupied := make(map[string]bool, len(managed))
	for _, m := range managed {
		if k := normalizedDatasetKey(m.Target.DatasetID); k != "" {
			occupied[k] = true
		}
	}
	out := make([]linkableDatasetView, 0, len(discovered))
	for _, ds := range discovered {
		id := canonicalDatasetID(ds.ID)
		if id == "" || ds.Protected || domain.IsProtectedProject(ds.Name, ds.Name) {
			continue
		}
		if occupied[normalizedDatasetKey(id)] {
			continue
		}
		name := strings.TrimSpace(ds.Name)
		if name == "" {
			name = id
		}
		out = append(out, linkableDatasetView{ID: id, Name: name})
	}
	writeJSON(w, http.StatusOK, out)
}
