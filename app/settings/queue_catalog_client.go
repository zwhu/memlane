package settings

import (
	"context"
	"net/url"
	"strings"
)

type v2ProjectKnowledgeResponse struct {
	ProjectID      string            `json:"project_id"`
	DatasetID      string            `json:"dataset_id"`
	Generation     int64             `json:"generation"`
	CatalogVersion string            `json:"catalog_version"`
	Sort           string            `json:"sort"`
	TotalFiltered  int               `json:"total_filtered"`
	UnreadCount    int               `json:"unread_count"`
	UnreadStateRev int64             `json:"unread_state_rev,omitempty"`
	CategoryCounts map[string]int    `json:"category_counts"`
	Items          []v2KnowledgeItem `json:"items"`
	NextCursor     string            `json:"next_cursor"`
	HasMore        bool              `json:"has_more"`
	PrevCursor     string            `json:"prev_cursor"`
	HasPrev        bool              `json:"has_prev"`
}

type ProjectMessageSummary struct {
	ProjectID               string              `json:"project_id"`
	DatasetID               string              `json:"dataset_id"`
	AutoCapture             *bool               `json:"auto_capture,omitempty"`
	AutoCaptureVersion      int64               `json:"auto_capture_version,omitempty"`
	AutomaticSave           bool                `json:"automatic_save"`
	Backlog                 *BacklogJob         `json:"backlog"`
	KnowledgeCatalogVersion string              `json:"knowledge_catalog_version"`
	UnreadCount             int                 `json:"unread_count"`
	UnreadStateRev          int64               `json:"unread_state_rev,omitempty"`
	UnreadPreview           []UnreadPreviewItem `json:"unread_preview"`
	UnreadHasMore           bool                `json:"unread_has_more"`
	PendingDocumentsCount   int                 `json:"pending_documents_count"`
	Activity                []activityRow       `json:"activity"`
	Errors                  map[string]string   `json:"errors"`
}

func (r ProjectMessageSummary) unreadPreviewItems(defaultDataset string) []Item {
	ds := r.DatasetID
	if ds == "" {
		ds = defaultDataset
	}
	out := make([]Item, 0, len(r.UnreadPreview))
	for _, row := range r.UnreadPreview {
		if row.DocID == "" {
			continue
		}
		doc := Document{
			ID:              row.DocID,
			Dataset:         ds,
			FirstSourceDate: row.EffectiveDate,
			Created:         row.CreatedAt,
			Metadata:        map[string]any{"node_set": []any{"session_learnings"}},
		}
		title := strings.TrimSpace(row.Title)
		if title == "" {
			title = knowledgePlaceholder(row.EffectiveDate)
		}
		date := row.EffectiveDate
		if date == "" {
			date = documentDate(doc)
		}
		out = append(out, Item{
			ID:                row.DocID,
			Dataset:           ds,
			Kind:              "知识",
			Title:             title,
			Date:              date,
			Document:          &doc,
			ReadingState:      "ready",
			ServerUnread:      true,
			ServerUnreadKnown: true,
		})
	}
	return out
}

func (c ProjectControlClient) projectMessageSummaryV2(ctx context.Context, readingID, projectID string) (ProjectMessageSummary, error) {
	var out ProjectMessageSummary
	path := "/v1/projects/" + url.PathEscape(projectID) + "/message-summary"
	err := c.RequestWithHeaders(ctx, "GET", path, v2IdentityHeaders(readingID), nil, &out, 512*1024)
	return out, err
}
