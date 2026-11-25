package overview

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zwhu/memlane/app/api"
	"github.com/zwhu/memlane/app/shell"
)

func FirstUseDays(clientStateFile, configFile string, now time.Time) (int, error) {
	path := clientStateFile
	if path == "" {
		path = filepath.Join(filepath.Dir(configFile), "client-state.json")
	}
	if configFile == "" && clientStateFile == "" {
		return 0, fmt.Errorf("尚未完成初始化")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("初始化记录不可用")
	}
	var state FirstUseReceipt
	if json.Unmarshal(b, &state) != nil || state.Version != 1 || state.Zone == "" {
		return 0, fmt.Errorf("初始化记录无效")
	}
	if _, err := time.Parse(time.RFC3339Nano, state.Completed); err != nil {
		return 0, fmt.Errorf("初始化记录无效")
	}
	first, err := time.Parse("2006-01-02", state.Date)
	if err != nil {
		return 0, fmt.Errorf("初始化日期无效")
	}
	// UTC here is a calendar ordinal, not elapsed local hours across DST.
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	days := int(today.Sub(first)/(24*time.Hour)) + 1
	if days < 1 {
		days = 1
	}
	return days, nil
}

// Upstream writes datetime.now(timezone.utc); SQLite DTOs may omit its offset.
func OfficialTime(value string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

const activityLimit = 50

func catalogItem(row api.CatalogItem) Item {
	id := row.DocID
	if id == "" {
		id = row.DocumentID
	}
	return Item{ID: id, ProjectID: row.ProjectID, Dataset: row.DatasetID, Kind: row.Kind,
		Title: row.Title, Date: row.EffectiveDate, SourceDocument: row.DocName,
		Unread: row.IsUnread, RecallCount: row.RecallCount, Category: row.Category}
}

func catalogItems[T any](rows []api.CatalogItem, convert func(Item) T) []T {
	items := make([]T, 0, len(rows))
	for _, row := range rows {
		if item := catalogItem(row); item.ID != "" {
			items = append(items, convert(item))
		}
	}
	return items
}

func SearchEvents(rows []api.SearchHistoryEntry) []Event {
	events := make([]Event, 0, len(rows))
	for n, row := range rows {
		at, _ := OfficialTime(row.SearchedAt)
		events = append(events, Event{Key: fmt.Sprint("manual-search/", n), Title: row.Query,
			State: "搜索记录", Project: "手动搜索", At: at, Record: ActivityRow{Dataset: row.DatasetID}})
	}
	return events
}

func FromBootstrap(source api.OverviewSnapshot, projects []shell.ProjectRef) Hydration[Item] {
	return fromBootstrap(source, projects, func(i Item) Item { return i })
}

func fromBootstrap[T any](source api.OverviewSnapshot, projects []shell.ProjectRef, convert func(Item) T) Hydration[T] {
	buckets := make(map[string]int, len(source.DayBuckets))
	for _, b := range source.DayBuckets {
		if b.Day != "" && b.Count > 0 {
			buckets[b.Day] = b.Count
		}
	}
	recalls := make([]RecallEvent, 0, len(source.RecallEvents))
	for _, event := range source.RecallEvents {
		recall := RecallEvent{ID: event.EventID, AgentID: event.AgentID, AgentName: event.AgentName,
			AgentKind: event.AgentKind, Channel: event.Channel, Received: event.ReceivedAt}
		for _, hit := range event.Hits {
			recall.Hits = append(recall.Hits, RecallHit{Dataset: hit.DatasetID, Document: hit.DocumentID})
		}
		recalls = append(recalls, recall)
	}
	events, more := MergeRecallActivity(nil, recalls, projects, activityLimit)
	return Hydration[T]{
		Snapshot:       SnapshotOf[T]{Knowledge: catalogItems(source.RecentItems, convert), Raw: source.TotalRaw},
		TotalKnowledge: source.TotalKnowledge, Buckets: buckets, RecallStartedAt: source.RecallStartedAt,
		Projects: source.Projects, Events: events, More: more || source.RecallEventsMore,
		History: SearchEvents(source.Searches), HistoryLoaded: true,
	}
}

func (a *ControllerOf[T]) fetchBootstrap(projects []shell.ProjectRef) func(context.Context) (Hydration[T], error) {
	if fetch := a.FetchBootstrap; fetch != nil {
		return fetch()
	}
	factory, convert := a.clientFactory(), a.FromItem
	return func(ctx context.Context) (Hydration[T], error) {
		client, err := factory()
		if err != nil {
			return Hydration[T]{}, err
		}
		source, err := client.OverviewBootstrap(ctx, api.OverviewOptions{Days: 365, RecentLimit: 20, RecallLimit: 50, SearchLimit: 50})
		if err != nil {
			return Hydration[T]{}, err
		}
		return fromBootstrap(source, projects, convert), nil
	}
}

func (a *ControllerOf[T]) fetchDay() func(context.Context, string, string) (DayPage[T], error) {
	if fetch := a.FetchDay; fetch != nil {
		return fetch()
	}
	factory, convert := a.clientFactory(), a.FromItem
	return func(ctx context.Context, day, cursor string) (DayPage[T], error) {
		client, err := factory()
		if err != nil {
			return DayPage[T]{}, err
		}
		page, err := client.OverviewDay(ctx, day, api.KnowledgePageOptions{Cursor: cursor, Limit: 30})
		if err != nil {
			return DayPage[T]{}, err
		}
		return DayPage[T]{Items: catalogItems(page.Items, convert), Total: page.TotalCount,
			NextCursor: page.NextCursor, HasMore: page.HasMore}, nil
	}
}

func (a *ControllerOf[T]) fetchHistory() func(context.Context) ([]Event, error) {
	if fetch := a.FetchSearchHistory; fetch != nil {
		return fetch()
	}
	factory := a.clientFactory()
	return func(ctx context.Context) ([]Event, error) {
		client, err := factory()
		if err != nil {
			return nil, err
		}
		history, err := client.RecentSearch(ctx, 50)
		return SearchEvents(history.RecentSearches), err
	}
}
