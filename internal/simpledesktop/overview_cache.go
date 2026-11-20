package simpledesktop

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type cachedOverviewItem struct {
	ID              string         `json:"id"`
	Dataset         string         `json:"dataset"`
	Title           string         `json:"title"`
	Date            string         `json:"date"`
	Source          string         `json:"source,omitempty"`
	ReadingState    string         `json:"reading_state,omitempty"`
	FirstSourceDate string         `json:"first_source_date,omitempty"`
	Created         string         `json:"created,omitempty"`
	Updated         string         `json:"updated,omitempty"`
	ContentHash     string         `json:"content_hash,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	RecallCount     int64          `json:"recall_count,omitempty"`
	HasRecallStats  bool           `json:"has_recall_stats,omitempty"`
}

type cachedOverviewSnapshot struct {
	Version   int                  `json:"version"`
	Scope     string               `json:"scope"`
	Raw       int                  `json:"raw"`
	RawLoaded bool                 `json:"raw_loaded"`
	Knowledge []cachedOverviewItem `json:"knowledge"`
}

func (a *App) overviewCachePath() (string, bool) {
	base := a.Config.ClientStateFile
	if base == "" {
		base = a.Config.ConfigFile
	}
	if base == "" {
		return "", false
	}
	identity, err := readingIdentity(a.Client)
	if err != nil || identity == "" {
		return "", false
	}
	name := fmt.Sprintf("overview-ui-cache-%x.json", sha256.Sum256([]byte(identity)))
	return filepath.Join(filepath.Dir(base), name), true
}

func (a *App) hydrateOverviewFromCache(scope string) bool {
	path, ok := a.overviewCachePath()
	if !ok {
		return false
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4*1024*1024 {
		return false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var cached cachedOverviewSnapshot
	if json.Unmarshal(b, &cached) != nil || cached.Version != 1 || cached.Scope != scope || len(cached.Knowledge) == 0 {
		return false
	}
	items := make([]Item, 0, len(cached.Knowledge))
	for _, row := range cached.Knowledge {
		doc := Document{
			ID:              row.ID,
			Dataset:         row.Dataset,
			Created:         row.Created,
			Updated:         row.Updated,
			ContentHash:     row.ContentHash,
			FirstSourceDate: row.FirstSourceDate,
			Metadata:        row.Metadata,
		}
		item := Item{
			ID:           row.ID,
			Dataset:      row.Dataset,
			Kind:         "知识",
			Title:        row.Title,
			Date:         row.Date,
			Source:       row.Source,
			ReadingState: row.ReadingState,
			Document:     &doc,
		}
		if row.HasRecallStats {
			item.RecallStats = &KnowledgeRecallStats{Count: row.RecallCount}
		}
		items = append(items, item)
	}
	a.overviewState.Snapshot = overviewSnapshot{Knowledge: items, Raw: cached.Raw}
	a.overviewState.RawLoaded = cached.RawLoaded
	a.overviewState.Loaded = true
	a.overviewState.Error = ""
	return true
}

func (a *App) saveOverviewCache(scope string, snapshot overviewSnapshot, rawLoaded bool) {
	path, ok := a.overviewCachePath()
	if !ok || scope == "" || len(snapshot.Knowledge) == 0 {
		return
	}
	rows := make([]cachedOverviewItem, 0, len(snapshot.Knowledge))
	for _, item := range snapshot.Knowledge {
		entry := cachedOverviewItem{
			ID:           item.ID,
			Dataset:      item.Dataset,
			Title:        item.Title,
			Date:         item.Date,
			Source:       item.Source,
			ReadingState: item.ReadingState,
		}
		if item.Document != nil {
			entry.FirstSourceDate = item.Document.FirstSourceDate
			entry.Created = item.Document.Created
			entry.Updated = item.Document.Updated
			entry.ContentHash = item.Document.ContentHash
			entry.Metadata = compactOverviewMetadata(item.Document.Metadata)
		}
		if item.RecallStats != nil {
			entry.HasRecallStats = true
			entry.RecallCount = item.RecallStats.Count
		}
		rows = append(rows, entry)
	}
	payload := cachedOverviewSnapshot{
		Version:   1,
		Scope:     scope,
		Raw:       snapshot.Raw,
		RawLoaded: rawLoaded,
		Knowledge: rows,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_ = writeNoticeAcknowledgements(path, b)
}

func compactOverviewMetadata(meta map[string]any) map[string]any {
	if len(meta) == 0 {
		return nil
	}
	keep := []string{"node_set", "lesson_type", "state_key", "session_date", "first_source_date", "session_id", "agent_kind", "source_agent"}
	out := map[string]any{}
	for _, k := range keep {
		if v, ok := meta[k]; ok {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
