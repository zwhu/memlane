package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

func (s *Server) activityReader() engine.ActivityReader {
	reader, _ := engine.AsActivityReader(s.cfg.Engine)
	return reader
}

func (s *Server) codeIndexer() engine.CodeIndexer {
	indexer, _ := engine.AsCodeIndexer(s.cfg.Engine)
	return indexer
}

func (s *Server) projectCodeActivity(w http.ResponseWriter, r *http.Request) {
	reader := s.activityReader()
	if reader == nil {
		writeError(w, http.StatusServiceUnavailable, "activity_reader_unavailable", "engine activity reader unavailable")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	ctx := r.Context()
	project, err := s.cfg.Store.GetProject(ctx, id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	codeDS := strings.TrimSpace(project.CodeDatasetID)
	if codeDS == "" {
		writeError(w, http.StatusConflict, "code_dataset_not_configured", "project code dataset is not configured")
		return
	}
	snap, err := reader.ProjectCodeActivity(ctx, codeDS)
	if errors.Is(err, engine.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "code_dataset_not_found", "code dataset not found in official store")
		return
	}
	if err != nil {
		writeEngineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project_id":         project.ID,
		"code_dataset_id":    snap.CodeDatasetID,
		"dataset_created_at": snap.DatasetCreatedAt,
		"active":             snap.Active,
		"status":             snap.Status,
		"run_id":             snap.RunID,
		"latest_created_at":  snap.LatestCreatedAt,
		"latest_started_at":  snap.LatestStartedAt,
		"latest_ended_at":    snap.LatestEndedAt,
	})
}

func (s *Server) ensureProjectCodeDataset(w http.ResponseWriter, r *http.Request) {
	indexer := s.codeIndexer()
	if indexer == nil {
		writeError(w, http.StatusServiceUnavailable, "code_indexer_unavailable", "engine code indexer unavailable")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	ctx := r.Context()

	s.registryMu.Lock()
	project, err := s.cfg.Store.GetProject(ctx, id)
	if err != nil {
		s.registryMu.Unlock()
		writeDomainError(w, err)
		return
	}
	if existing := domain.CanonicalUUID(strings.TrimSpace(project.CodeDatasetID)); existing != "" {
		if existing == domain.CanonicalUUID(project.DatasetID) {
			s.registryMu.Unlock()
			writeError(w, http.StatusConflict, "code_dataset_conflict", "code dataset id conflicts with project memory dataset")
			return
		}
		conflict, err := s.codeDatasetConflictsLocked(ctx, project.ID, existing)
		if err != nil {
			s.registryMu.Unlock()
			writeDomainError(w, err)
			return
		}
		if conflict {
			s.registryMu.Unlock()
			writeError(w, http.StatusConflict, "code_dataset_conflict", "code dataset id conflicts with another managed dataset")
			return
		}
		if !project.CodeConfigured || project.CodeDatasetID != existing {
			project.CodeDatasetID = existing
			project.CodeConfigured = true
			if err := s.cfg.Store.UpsertProject(ctx, project); err != nil {
				s.registryMu.Unlock()
				writeDomainError(w, err)
				return
			}
		}
		s.registryMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"id":         existing,
			"name":       project.ID + "-code",
			"project_id": project.ID,
		})
		return
	}
	s.registryMu.Unlock()

	name := project.ID + "-code"
	rec, err := indexer.EnsureDataset(ctx, name)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	codeDS := domain.CanonicalUUID(strings.TrimSpace(rec.ID))
	if codeDS == "" {
		writeError(w, http.StatusBadGateway, "engine_error", "engine returned empty dataset id")
		return
	}

	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	project, err = s.cfg.Store.GetProject(ctx, id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if existing := domain.CanonicalUUID(strings.TrimSpace(project.CodeDatasetID)); existing != "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"id":         existing,
			"name":       project.ID + "-code",
			"project_id": project.ID,
		})
		return
	}
	if codeDS == domain.CanonicalUUID(project.DatasetID) {
		writeError(w, http.StatusConflict, "code_dataset_conflict", "code dataset id conflicts with project memory dataset")
		return
	}
	conflict, err := s.codeDatasetConflictsLocked(ctx, project.ID, codeDS)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if conflict {
		writeError(w, http.StatusConflict, "code_dataset_conflict", "code dataset id conflicts with another managed dataset")
		return
	}
	project.CodeDatasetID = codeDS
	project.CodeConfigured = true
	if err := s.cfg.Store.UpsertProject(ctx, project); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":         codeDS,
		"name":       rec.Name,
		"project_id": project.ID,
	})
}

func (s *Server) codeDatasetConflictsLocked(ctx context.Context, projectID, codeDS string) (bool, error) {
	projects, err := s.cfg.Store.ListProjects(ctx)
	if err != nil {
		return false, err
	}
	for _, p := range projects {
		if domain.CanonicalUUID(p.DatasetID) == codeDS {
			return true, nil
		}
		if p.ID != projectID && domain.CanonicalUUID(p.CodeDatasetID) == codeDS {
			return true, nil
		}
	}
	common, err := s.cfg.Store.GetCommonConfig(ctx)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return false, err
	}
	if err == nil && domain.CanonicalUUID(common.DatasetID) == codeDS {
		return true, nil
	}
	return false, nil
}

func (s *Server) triggerProjectCodeIndex(w http.ResponseWriter, r *http.Request) {
	indexer := s.codeIndexer()
	reader := s.activityReader()
	if indexer == nil || reader == nil {
		writeError(w, http.StatusServiceUnavailable, "code_indexer_unavailable", "engine code indexer unavailable")
		return
	}
	var req struct {
		RepoPath string `json:"repo_path"`
	}
	if r.Body != nil {
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes))
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			writeError(w, http.StatusRequestEntityTooLarge, "request_body_too_large", "request exceeds the body limit")
			return
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", "failed to read request body")
			return
		}
		raw = bytes.TrimSpace(raw)
		if len(raw) > 0 {
			if raw[0] != '{' || json.Unmarshal(raw, &req) != nil {
				writeError(w, http.StatusBadRequest, "invalid_json", "valid JSON object required")
				return
			}
		}
	}

	id := strings.TrimSpace(r.PathValue("id"))
	ctx := r.Context()
	project, err := s.cfg.Store.GetProject(ctx, id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	codeDS := domain.CanonicalUUID(strings.TrimSpace(project.CodeDatasetID))
	if !project.CodeEnabled {
		writeError(w, http.StatusConflict, "code_indexing_disabled", "code indexing is disabled for this project")
		return
	}
	if codeDS == "" {
		writeError(w, http.StatusConflict, "code_dataset_not_configured", "project code dataset is not configured")
		return
	}

	repoPath, ok := resolveAuthorizedRepoPath(strings.TrimSpace(req.RepoPath), project.Aliases)
	if !ok {
		if len(project.Aliases) == 0 && strings.TrimSpace(req.RepoPath) == "" {
			writeError(w, http.StatusBadRequest, "invalid_repo_path", "project has no registered alias path")
			return
		}
		writeError(w, http.StatusForbidden, "repo_path_not_allowed", "repo_path must match a registered local project directory")
		return
	}

	if !s.tryStartCodeIndex(codeDS) {
		writeError(w, http.StatusConflict, "code_index_already_active", "code indexing is already running for this project")
		return
	}

	act, err := reader.ProjectCodeActivity(ctx, codeDS)
	if errors.Is(err, engine.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		s.finishCodeIndex(codeDS)
		writeError(w, http.StatusNotFound, "code_dataset_not_found", "code dataset not found in official store")
		return
	}
	if err != nil {
		s.finishCodeIndex(codeDS)
		writeEngineError(w, err)
		return
	}
	if act.Active {
		s.finishCodeIndex(codeDS)
		writeError(w, http.StatusConflict, "code_index_already_active", "code indexing is already running for this project")
		return
	}

	type indexOutcome struct {
		res engine.CodeIndexResult
		err error
	}
	done := make(chan indexOutcome, 1)
	indexCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Minute)
	go func() {
		defer cancel()
		defer s.finishCodeIndex(codeDS)
		res, err := indexer.IndexCode(indexCtx, engine.CodeIndexRequest{
			DatasetID: codeDS,
			RepoPath:  repoPath,
		})
		done <- indexOutcome{res: res, err: err}
	}()

	var out indexOutcome
	select {
	case out = <-done:
	case <-ctx.Done():
		// Keep codeIndexInFlight held until the detached engine call actually finishes.
		writeEngineError(w, ctx.Err())
		return
	}
	res, err := out.res, out.err
	if err != nil {
		writeEngineError(w, err)
		return
	}
	if res.DatasetID != "" && domain.CanonicalUUID(res.DatasetID) != domain.CanonicalUUID(codeDS) {
		writeError(w, http.StatusBadGateway, "engine_error", "code index dataset mismatch")
		return
	}
	status := strings.ToLower(strings.TrimSpace(res.Status))
	if status != "completed" && status != "errored" {
		writeError(w, http.StatusBadGateway, "engine_error", "code index returned non-terminal status")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":          status,
		"dataset_id":      codeDS,
		"pipeline_run_id": res.PipelineRunID,
		"items_processed": res.ItemsProcessed,
	})
}

func (s *Server) tryStartCodeIndex(codeDS string) bool {
	s.codeIndexMu.Lock()
	defer s.codeIndexMu.Unlock()
	if s.codeIndexInFlight == nil {
		s.codeIndexInFlight = make(map[string]struct{})
	}
	if _, busy := s.codeIndexInFlight[codeDS]; busy {
		return false
	}
	s.codeIndexInFlight[codeDS] = struct{}{}
	return true
}

func (s *Server) finishCodeIndex(codeDS string) {
	s.codeIndexMu.Lock()
	delete(s.codeIndexInFlight, codeDS)
	s.codeIndexMu.Unlock()
}

func resolveAuthorizedRepoPath(requested string, aliases []string) (string, bool) {
	cleanedAliases := make([]string, 0, len(aliases))
	for _, a := range aliases {
		if trimmed := strings.TrimSpace(a); trimmed != "" {
			cleanedAliases = append(cleanedAliases, filepath.Clean(trimmed))
		}
	}
	if len(cleanedAliases) == 0 {
		return "", false
	}
	candidate := cleanedAliases[0]
	if requested != "" {
		cleanReq := filepath.Clean(requested)
		matched := ""
		for _, alias := range cleanedAliases {
			if cleanReq == alias {
				matched = alias
				break
			}
		}
		if matched == "" {
			return "", false
		}
		candidate = matched
	}
	if !filepath.IsAbs(candidate) {
		return "", false
	}
	info, err := os.Lstat(candidate)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", false
	}
	expectedCanonical := canonicalSystemPrefix(candidate)
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil || filepath.Clean(resolved) != expectedCanonical {
		return "", false
	}
	resolvedInfo, err := os.Stat(resolved)
	if err != nil || !resolvedInfo.IsDir() {
		return "", false
	}
	return resolved, true
}

func canonicalSystemPrefix(p string) string {
	clean := filepath.Clean(p)
	for _, prefix := range []string{"/var", "/tmp", "/etc"} {
		if clean == prefix || strings.HasPrefix(clean, prefix+"/") {
			if _, err := os.Stat("/private" + prefix); err == nil {
				return "/private" + clean
			}
		}
	}
	return clean
}

func (s *Server) listPipelineRuns(w http.ResponseWriter, r *http.Request) {
	reader := s.activityReader()
	if reader == nil {
		writeError(w, http.StatusServiceUnavailable, "activity_reader_unavailable", "engine activity reader unavailable")
		return
	}
	ctx := r.Context()
	q := r.URL.Query()
	datasetID := strings.TrimSpace(q.Get("dataset_id"))
	limit := parseLimit(q.Get("limit"), 100, 500)
	offset := 0
	if rawOffset := strings.TrimSpace(q.Get("offset")); rawOffset != "" {
		parsed, err := strconv.Atoi(rawOffset)
		if err != nil || parsed < 0 || parsed > 100000 {
			writeError(w, http.StatusBadRequest, "invalid_offset", "offset must be between 0 and 100000")
			return
		}
		offset = parsed
	}

	var allowed []string
	if datasetID != "" {
		if err := s.managedDatasetOrCode(ctx, datasetID); err != nil {
			writeDomainError(w, err)
			return
		}
	} else {
		var err error
		allowed, err = s.allManagedDatasetIDs(ctx)
		if err != nil {
			writeDomainError(w, err)
			return
		}
	}

	runs, err := reader.ListPipelineRuns(ctx, engine.PipelineRunsQuery{
		DatasetID:         datasetID,
		AllowedDatasetIDs: allowed,
		Limit:             limit,
		Offset:            offset,
	})
	if err != nil {
		writeEngineError(w, err)
		return
	}
	if runs == nil {
		runs = []engine.PipelineRun{}
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *Server) managedDatasetOrCode(ctx context.Context, ds string) error {
	if err := s.managedDataset(ctx, ds); err == nil {
		return nil
	}
	var found bool
	err := s.cfg.Store.DB().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE code_dataset_id=?1)`, ds).Scan(&found)
	if err != nil {
		return err
	}
	if !found {
		return domain.ErrForbidden
	}
	return nil
}

func (s *Server) allManagedDatasetIDs(ctx context.Context) ([]string, error) {
	rows, err := readRows(ctx, s.cfg.Store.DB(), `SELECT dataset_id FROM projects WHERE dataset_id<>''
		UNION SELECT code_dataset_id FROM projects WHERE code_dataset_id<>''
		UNION SELECT dataset_id FROM common_config WHERE dataset_id<>''`, nil, func(row rowScanner) (string, error) {
		var ds string
		err := row.Scan(&ds)
		return ds, err
	})
	if err != nil {
		return nil, err
	}
	if rows == nil {
		return []string{}, nil
	}
	return rows, nil
}
