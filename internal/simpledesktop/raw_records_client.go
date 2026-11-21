package simpledesktop

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

type rawRecordsResponse struct {
	DatasetID  string            `json:"dataset_id"`
	CacheReady bool              `json:"cache_ready"`
	Documents  []Document        `json:"documents"`
	Sessions   []rawSessionEntry `json:"sessions"`
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

func (c ProjectControlClient) rawRecords(ctx context.Context, dataset, kind string) (rawRecordsResponse, error) {
	var out rawRecordsResponse
	path := fmt.Sprintf("/v1/raw-records?dataset_id=%s&kind=%s", url.QueryEscape(dataset), url.QueryEscape(rawRecordsAPIKind(kind)))
	if err := c.requestWithLimit(ctx, "GET", path, nil, &out, 8*1024*1024); err != nil {
		return rawRecordsResponse{}, err
	}
	if out.DatasetID != dataset {
		return rawRecordsResponse{}, fmt.Errorf("官方数据组不匹配")
	}
	for _, s := range out.Sessions {
		if s.ID == "" || (s.Dataset != "" && s.Dataset != dataset) {
			return rawRecordsResponse{}, fmt.Errorf("官方会话身份不匹配")
		}
	}
	for _, d := range out.Documents {
		if d.ID == "" || d.Dataset != dataset {
			return rawRecordsResponse{}, fmt.Errorf("官方内容身份不匹配")
		}
	}
	return out, nil
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

func buildRawRecordsFromFast(dataset string, resp rawRecordsResponse) ([]Item, bool) {
	rows := make([]Item, 0, len(resp.Documents)+len(resp.Sessions))
	seen := make(map[string]bool, len(resp.Documents)+len(resp.Sessions))
	for _, d := range resp.Documents {
		if d.Dataset != dataset || d.knowledge() || derivedMaterial(d) {
			continue
		}
		copyDoc := d
		item := Item{ID: d.ID, Dataset: d.Dataset, Kind: "文档", Title: d.title(), Date: d.date(), Document: &copyDoc}
		if !seen[item.key()] {
			rows = append(rows, item)
			seen[item.key()] = true
		}
	}
	for _, entry := range resp.Sessions {
		if entry.ID == "" || (entry.Dataset != "" && entry.Dataset != dataset) {
			continue
		}
		sess := entry.Session
		if sess.Dataset == "" {
			sess.Dataset = dataset
		}
		title := entry.ID
		var summary, readingState string
		if resp.CacheReady {
			readingState = "ready"
			summary = sessionCoverage(SessionDetail{Count: entry.MsgCount, ToolCount: entry.ToolCalls})
			if strings.TrimSpace(entry.Label) != "" {
				title = sessionTitle(entry.Label)
			}
		}
		item := Item{
			ID:             entry.ID,
			Dataset:        sess.Dataset,
			Kind:           "会话",
			Title:          title,
			SessionSummary: summary,
			Date:           entry.Updated,
			Session:        &sess,
			ReadingState:   readingState,
		}
		if !seen[item.key()] {
			rows = append(rows, item)
			seen[item.key()] = true
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, _ := officialTime(rows[i].Date)
		b, _ := officialTime(rows[j].Date)
		if a.Equal(b) {
			return rows[i].key() < rows[j].key()
		}
		return a.After(b)
	})
	return rows, resp.CacheReady
}

func shouldFallbackFromRawControlError(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	var ce *ControlResponseError
	if errors.As(err, &ce) && (ce.Status == 401 || ce.Status == 403) {
		return false
	}
	return true
}
