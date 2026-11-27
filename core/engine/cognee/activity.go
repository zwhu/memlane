package cognee

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/zwhu/memlane/core/engine"
)

func (p *Projector) ProjectCodeActivity(ctx context.Context, datasetID string) (engine.CodeActivitySnapshot, error) {
	canonicalID := uuidText(datasetID)
	if canonicalID == "" {
		return engine.CodeActivitySnapshot{}, invalidArgument("code dataset id required", nil)
	}
	variants := idVariants(datasetID)
	if len(variants) == 0 {
		return engine.CodeActivitySnapshot{}, invalidArgument("code dataset id required", nil)
	}
	return readSQLiteSnapshot(ctx, p.database("cognee_db"), func(tx *sql.Tx) (engine.CodeActivitySnapshot, error) {
		return readCodeActivity(ctx, tx, canonicalID, variants)
	})
}

func readCodeActivity(ctx context.Context, tx *sql.Tx, canonicalID string, variants []string) (engine.CodeActivitySnapshot, error) {
	dsCols, err := columns(ctx, tx, "datasets")
	if err != nil {
		return engine.CodeActivitySnapshot{}, err
	}
	if len(dsCols) == 0 {
		return engine.CodeActivitySnapshot{}, engine.ErrNotFound
	}
	args := make([]any, len(variants))
	for i, v := range variants {
		args[i] = v
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+textColumns(dsCols, "id", "created_at")+" FROM datasets WHERE id IN ("+placeholders(len(args))+")", args...)
	if err != nil {
		return engine.CodeActivitySnapshot{}, err
	}
	var createdRaw string
	foundCount := 0
	for rows.Next() {
		var idRaw, created string
		if err := rows.Scan(&idRaw, &created); err != nil {
			_ = rows.Close()
			return engine.CodeActivitySnapshot{}, err
		}
		foundCount++
		createdRaw = created
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return engine.CodeActivitySnapshot{}, err
	}
	_ = rows.Close()
	if foundCount == 0 {
		return engine.CodeActivitySnapshot{}, engine.ErrNotFound
	}
	if foundCount > 1 {
		return engine.CodeActivitySnapshot{}, fmt.Errorf("duplicate code dataset identity: %w", engine.ErrConflict)
	}
	createdISO := isoText(createdRaw)
	generation, ok := parseActivityTimestamp(createdISO)
	if !ok {
		return engine.CodeActivitySnapshot{}, fmt.Errorf("invalid dataset created_at timestamp: %w", engine.ErrUnavailable)
	}

	snap := engine.CodeActivitySnapshot{
		CodeDatasetID:    canonicalID,
		DatasetCreatedAt: createdISO,
	}
	runCols, err := columns(ctx, tx, "pipeline_runs")
	if err != nil || len(runCols) == 0 {
		return snap, err
	}

	query := "SELECT " + textColumns(runCols,
		"id", "pipeline_run_id", "dataset_id", "kind", "pipeline_name", "operation_name",
		"status", "outcome", "created_at", "started_at", "ended_at") +
		" FROM pipeline_runs WHERE dataset_id IN (" + placeholders(len(args)) + ")"
	if runCols["created_at"] {
		query += " ORDER BY created_at DESC, id DESC"
	} else {
		query += " ORDER BY rowid DESC"
	}
	runRows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return engine.CodeActivitySnapshot{}, err
	}
	defer runRows.Close()

	seen := make(map[string]bool)
	for runRows.Next() {
		var id, runID, dsID, kind, pipeline, operation, status, outcome, created, started, ended string
		if err := runRows.Scan(&id, &runID, &dsID, &kind, &pipeline, &operation, &status, &outcome, &created, &started, &ended); err != nil {
			return engine.CodeActivitySnapshot{}, err
		}
		if pipeline != "code_graph_pipeline" && operation != "remember" {
			continue
		}
		kind = deriveRunKind(kind, pipeline)
		created = isoText(created)
		started = isoText(started)
		ended = isoText(ended)
		before, err := codeRunPredatesGeneration(created, started, generation)
		if err != nil {
			return engine.CodeActivitySnapshot{}, err
		}
		if before {
			continue
		}
		id = uuidText(id)
		runID = uuidText(runID)
		key := runID
		if key == "" {
			key = id
		}
		if key != "" && seen[key] {
			continue
		}
		if key != "" {
			seen[key] = true
		}
		if snap.RunID == "" && snap.Status == "" {
			snap.RunID = runID
			snap.Status = status
			if snap.Status == "" {
				snap.Status = outcome
			}
			snap.LatestCreatedAt = created
			snap.LatestStartedAt = started
			snap.LatestEndedAt = ended
		}
		if isActiveCodeRun(status, kind, outcome, ended) {
			snap.Active = true
			snap.RunID = runID
			snap.Status = "running"
			if snap.LatestCreatedAt == "" {
				snap.LatestCreatedAt = created
			}
			if snap.LatestStartedAt == "" {
				snap.LatestStartedAt = started
			}
			snap.LatestEndedAt = ended
		}
	}
	return snap, runRows.Err()
}

func isActiveCodeRun(status, kind, outcome, ended string) bool {
	return status == "DATASET_PROCESSING_STARTED" ||
		status == "DATASET_PROCESSING_INITIATED" ||
		(kind == "operation" && outcome == "" && ended == "")
}

func parseActivityTimestamp(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05.999999999",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	} {
		if parsed, err := time.Parse(layout, raw); err == nil && parsed.Year() > 0 {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

func deriveRunKind(kind, pipelineName string) string {
	if trimmed := strings.TrimSpace(kind); trimmed != "" {
		return trimmed
	}
	if strings.TrimSpace(pipelineName) == "" {
		return "operation"
	}
	return "pipeline"
}

func codeRunPredatesGeneration(created, started string, generation time.Time) (bool, error) {
	rowCreated, ok := parseActivityTimestamp(created)
	if !ok {
		return false, fmt.Errorf("invalid pipeline run created_at timestamp: %w", engine.ErrUnavailable)
	}
	if strings.TrimSpace(started) == "" {
		return rowCreated.Before(generation), nil
	}
	runStarted, ok := parseActivityTimestamp(started)
	if !ok {
		return false, fmt.Errorf("invalid pipeline run started_at timestamp: %w", engine.ErrUnavailable)
	}
	return rowCreated.Before(generation) && runStarted.Before(generation), nil
}

func (p *Projector) ListPipelineRuns(ctx context.Context, query engine.PipelineRunsQuery) ([]engine.PipelineRun, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}
	if offset > 100000 {
		offset = 100000
	}

	var filterVariants []string
	if strings.TrimSpace(query.DatasetID) != "" {
		filterVariants = idVariants(query.DatasetID)
		if len(filterVariants) == 0 {
			return []engine.PipelineRun{}, nil
		}
	} else if query.AllowedDatasetIDs != nil {
		if len(query.AllowedDatasetIDs) == 0 {
			return []engine.PipelineRun{}, nil
		}
		for _, ds := range query.AllowedDatasetIDs {
			for _, v := range idVariants(ds) {
				if !slices.Contains(filterVariants, v) {
					filterVariants = append(filterVariants, v)
				}
			}
		}
		if len(filterVariants) == 0 {
			return []engine.PipelineRun{}, nil
		}
	}

	runs, err := readSQLiteSnapshot(ctx, p.database("cognee_db"), func(tx *sql.Tx) ([]engine.PipelineRun, error) {
		return readPipelineRuns(ctx, tx, filterVariants, limit, offset)
	})
	if errors.Is(err, os.ErrNotExist) {
		return []engine.PipelineRun{}, nil
	}
	if err != nil {
		return nil, err
	}
	if runs == nil {
		return []engine.PipelineRun{}, nil
	}
	return runs, nil
}

func readPipelineRuns(ctx context.Context, tx *sql.Tx, filterVariants []string, limit, offset int) ([]engine.PipelineRun, error) {
	cols, err := columns(ctx, tx, "pipeline_runs")
	if err != nil || len(cols) == 0 {
		return []engine.PipelineRun{}, err
	}
	sqlText := "SELECT " + textColumns(cols,
		"id", "pipeline_run_id", "parent_operation_id", "dataset_id", "kind",
		"pipeline_name", "operation_name", "status", "outcome", "background",
		"error_class", "origin", "created_at", "started_at", "ended_at") +
		" FROM pipeline_runs"
	args := make([]any, 0, len(filterVariants)+2)
	if len(filterVariants) > 0 {
		sqlText += " WHERE dataset_id IN (" + placeholders(len(filterVariants)) + ")"
		for _, v := range filterVariants {
			args = append(args, v)
		}
	}
	if cols["created_at"] {
		sqlText += " ORDER BY created_at DESC, id DESC"
	} else {
		sqlText += " ORDER BY rowid DESC"
	}
	sqlText += " LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := tx.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]engine.PipelineRun, 0)
	for rows.Next() {
		var r engine.PipelineRun
		var bgRaw string
		if err := rows.Scan(
			&r.ID, &r.PipelineRunID, &r.ParentOperationID, &r.DatasetID, &r.Kind,
			&r.PipelineName, &r.OperationName, &r.Status, &r.Outcome, &bgRaw,
			&r.ErrorClass, &r.Origin, &r.CreatedAt, &r.StartedAt, &r.EndedAt,
		); err != nil {
			return nil, err
		}
		r.ID = uuidText(r.ID)
		r.PipelineRunID = uuidText(r.PipelineRunID)
		r.ParentOperationID = uuidText(r.ParentOperationID)
		r.DatasetID = uuidText(r.DatasetID)
		r.Kind = deriveRunKind(r.Kind, r.PipelineName)
		r.Background = parseBoolText(bgRaw)
		r.CreatedAt = isoText(r.CreatedAt)
		r.StartedAt = isoText(r.StartedAt)
		r.EndedAt = isoText(r.EndedAt)
		out = append(out, r)
	}
	return out, rows.Err()
}

func parseBoolText(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "t", "yes":
		return true
	default:
		return false
	}
}
