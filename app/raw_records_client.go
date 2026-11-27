package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

type rawSessionEntry struct {
	Session
	Label     string `json:"label"`
	MsgCount  int    `json:"msg_count"`
	ToolCalls int    `json:"tool_calls"`
}

func rawRecordsAPIKind(kind string) string {
	switch kind {
	case "会话":
		return "session"
	case "文档":
		return "document"
	default:
		return "all"
	}
}

func (c ProjectControlClient) rawSessionHistory(ctx context.Context, item Item, qa, trace int) (SessionHistoryPage, error) {
	var page SessionHistoryPage
	if item.Session == nil || item.Dataset == "" || item.ID == "" || qa < 0 || trace < 0 {
		return page, fmt.Errorf("来源会话身份无效")
	}
	path := fmt.Sprintf("/v1/raw-records/session-history?dataset_id=%s&session_id=%s&qa_offset=%d&trace_offset=%d&limit=20",
		url.QueryEscape(item.Dataset), url.QueryEscape(item.ID), qa, trace)
	if item.Session.User != "" {
		path += "&user_id=" + url.QueryEscape(item.Session.User)
	}
	if err := c.requestWithLimit(ctx, "GET", path, nil, &page, 8*1024*1024); err != nil {
		return page, err
	}
	if err := validateSessionHistoryPage(page, item.Dataset, item.ID, item.Session.User, qa, trace); err != nil {
		return SessionHistoryPage{}, err
	}
	return page, nil
}

func rawSessionEntryToItem(dataset string, entry rawSessionEntry, cacheReady bool) Item {
	sess := entry.Session
	if sess.Dataset == "" {
		sess.Dataset = dataset
	}
	title := entry.ID
	var summary, readingState string
	if cacheReady {
		readingState = "ready"
		summary = sessionCoverage(SessionDetail{Count: entry.MsgCount, ToolCount: entry.ToolCalls})
		if strings.TrimSpace(entry.Label) != "" {
			title = sessionTitle(entry.Label)
		}
	}
	return Item{
		ID:             entry.ID,
		Dataset:        sess.Dataset,
		Kind:           "会话",
		Title:          title,
		SessionSummary: summary,
		Date:           entry.Updated,
		Session:        &sess,
		ReadingState:   readingState,
	}
}

func rawSessionEntriesToItems(dataset string, entries []rawSessionEntry, cacheReady bool) []Item {
	out := make([]Item, 0, len(entries))
	for _, entry := range entries {
		if entry.ID == "" {
			continue
		}
		out = append(out, rawSessionEntryToItem(dataset, entry, cacheReady))
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, _ := officialTime(out[i].Date)
		b, _ := officialTime(out[j].Date)
		if a.Equal(b) {
			return out[i].Key() < out[j].Key()
		}
		return a.After(b)
	})
	return out
}

func shouldFallbackFromRawControlError(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	var ce *ControlResponseError
	if errors.As(err, &ce) && (ce.Status == 401 || ce.Status == 403 || ce.Status >= 500) {
		return false
	}
	return true
}
