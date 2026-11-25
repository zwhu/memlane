package app

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

type v2RawSessionEntry struct {
	Session
	Label           string            `json:"label"`
	MsgCount        int               `json:"msg_count"`
	ToolCalls       int               `json:"tool_calls"`
	ChildCount      int               `json:"child_count"`
	ChildrenPreview []rawSessionEntry `json:"children_preview"`
	ChildrenHasMore bool              `json:"children_has_more"`
}

type v2RawRecordItem struct {
	ItemKind string             `json:"item_kind"`
	SortTSNS int64              `json:"sort_ts_ns"`
	Session  *v2RawSessionEntry `json:"session,omitempty"`
	Document *Document          `json:"document,omitempty"`
}

type v2RawRecordsResponse struct {
	ProjectID  string            `json:"project_id"`
	DatasetID  string            `json:"dataset_id"`
	Kind       string            `json:"kind"`
	CacheReady bool              `json:"cache_ready"`
	Items      []v2RawRecordItem `json:"items"`
	NextCursor string            `json:"next_cursor"`
	HasMore    bool              `json:"has_more"`
}

func (c ProjectControlClient) rawRecordsV2(ctx context.Context, projectID, kind string, limit int, cursor string) (v2RawRecordsResponse, error) {
	var out v2RawRecordsResponse
	q := url.Values{}
	q.Set("kind", rawRecordsAPIKind(kind))
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	path := "/v1/projects/" + url.PathEscape(projectID) + "/raw-records?" + q.Encode()
	if err := c.requestWithLimit(ctx, "GET", path, nil, &out, 512*1024); err != nil {
		return v2RawRecordsResponse{}, err
	}
	return out, nil
}

type v2SessionChildrenResponse struct {
	ProjectID       string            `json:"project_id"`
	DatasetID       string            `json:"dataset_id"`
	ParentSessionID string            `json:"parent_session_id"`
	TotalCount      int               `json:"total_count"`
	Items           []rawSessionEntry `json:"items"`
	NextCursor      string            `json:"next_cursor"`
	HasMore         bool              `json:"has_more"`
}

func (c ProjectControlClient) sessionChildrenV2(ctx context.Context, projectID, sessionID, userID string, limit int, cursor string) (v2SessionChildrenResponse, error) {
	var out v2SessionChildrenResponse
	q := url.Values{}
	if userID != "" {
		q.Set("user_id", userID)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	path := "/v1/projects/" + url.PathEscape(projectID) + "/raw-records/sessions/" + url.PathEscape(sessionID) + "/children"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	if err := c.requestWithLimit(ctx, "GET", path, nil, &out, 512*1024); err != nil {
		return v2SessionChildrenResponse{}, err
	}
	return out, nil
}

func v2RawRecordsToItems(dataset string, resp v2RawRecordsResponse) ([]Item, error) {
	if resp.DatasetID != "" && dataset != "" && resp.DatasetID != dataset {
		return nil, fmt.Errorf("官方数据组不匹配")
	}
	ds := dataset
	if ds == "" {
		ds = resp.DatasetID
	}
	out := make([]Item, 0, len(resp.Items))
	for _, entry := range resp.Items {
		switch entry.ItemKind {
		case "session":
			if entry.Session == nil || entry.Session.ID == "" {
				continue
			}
			if entry.Session.Dataset != "" && ds != "" && entry.Session.Dataset != ds {
				return nil, fmt.Errorf("官方会话身份不匹配")
			}
			base := rawSessionEntry{
				Session:   entry.Session.Session,
				Label:     entry.Session.Label,
				MsgCount:  entry.Session.MsgCount,
				ToolCalls: entry.Session.ToolCalls,
			}
			item := rawSessionEntryToItem(ds, base, resp.CacheReady)
			item.SubagentTotalCount = entry.Session.ChildCount
			item.SubagentsHasMore = entry.Session.ChildrenHasMore
			if len(entry.Session.ChildrenPreview) > 0 {
				parentTitle := sessionReadableParentTitle(item)
				kids := make([]Item, 0, len(entry.Session.ChildrenPreview))
				for _, childEntry := range entry.Session.ChildrenPreview {
					if childEntry.ID == "" {
						continue
					}
					child := rawSessionEntryToItem(ds, childEntry, resp.CacheReady)
					child.ParentTitle = parentTitle
					kids = append(kids, child)
				}
				item.Subagents = kids
				if item.SubagentTotalCount < len(kids) {
					item.SubagentTotalCount = len(kids)
				}
			}
			out = append(out, item)
		case "document":
			if entry.Document == nil || entry.Document.ID == "" {
				continue
			}
			docCopy := *entry.Document
			if docCopy.Dataset == "" {
				docCopy.Dataset = ds
			}
			if ds != "" && docCopy.Dataset != ds {
				return nil, fmt.Errorf("官方内容身份不匹配")
			}
			if documentKnowledge(docCopy) || derivedMaterial(docCopy) {
				continue
			}
			out = append(out, Item{
				ID:       docCopy.ID,
				Dataset:  docCopy.Dataset,
				Kind:     "文档",
				Title:    documentTitle(docCopy),
				Date:     documentDate(docCopy),
				Document: &docCopy,
			})
		}
	}
	return out, nil
}
