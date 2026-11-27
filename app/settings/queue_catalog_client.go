package settings

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/zwhu/memlane/app/api"
)

type UnreadPreviewItem struct {
	DocID         string `json:"doc_id"`
	Title         string `json:"title"`
	EffectiveDate string `json:"effective_date"`
	CreatedAt     string `json:"created_at"`
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

func unreadCatalogItem(row api.CatalogItem) Item {
	id := row.DocID
	if id == "" {
		id = row.DocumentID
	}
	kind := "知识"
	lowerKind := strings.ToLower(strings.TrimSpace(row.Kind))
	if lowerKind == "raw_doc" || lowerKind == "document" || lowerKind == "raw" || lowerKind == "raw_document" || row.Kind == "文档" {
		kind = "文档"
	}
	var labelPtr *string
	if row.DocLabel != "" {
		l := row.DocLabel
		labelPtr = &l
	}
	doc := Document{
		FirstSourceDate: row.EffectiveDate,
		ID:              id,
		Dataset:         row.DatasetID,
		Name:            row.DocName,
		Label:           labelPtr,
		Created:         row.CreatedAt,
		Updated:         row.UpdatedAt,
		ContentHash:     row.ContentHash,
	}
	date := strings.TrimSpace(row.EffectiveDate)
	if kind == "知识" {
		date = knowledgeCatalogDate(row.EffectiveDate, row.CreatedAt)
	} else if date == "" {
		if row.UpdatedAt != "" {
			date = row.UpdatedAt
		} else {
			date = row.CreatedAt
		}
	}
	title := strings.TrimSpace(row.Title)
	if kind == "知识" {
		title = readableName(title)
		if title == "" {
			title = readableDocumentTitle(doc)
		}
		if title == "" {
			title = knowledgePlaceholder(date)
		}
	} else if title == "" {
		title = readableDocumentTitle(doc)
		if title == "" {
			title = row.DocName
		}
	}
	readingState := ""
	if title != "" {
		readingState = "ready"
	}
	return Item{
		ID:                id,
		Dataset:           row.DatasetID,
		Kind:              kind,
		Title:             title,
		Date:              date,
		Source:            row.SessionID,
		CommonCategory:    row.CommonCategory,
		Document:          &doc,
		ReadingState:      readingState,
		ServerUnread:      row.IsUnread,
		ServerUnreadKnown: true,
	}
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
		date := knowledgeCatalogDate(row.EffectiveDate, row.CreatedAt)
		doc := Document{
			ID:              row.DocID,
			Dataset:         ds,
			FirstSourceDate: row.EffectiveDate,
			Created:         row.CreatedAt,
			Metadata:        map[string]any{"node_set": []any{"session_learnings"}},
		}
		title := readableName(row.Title)
		if title == "" {
			title = knowledgePlaceholder(date)
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

func v2IdentityHeaders(readingID string) map[string]string {
	if readingID == "" {
		return nil
	}
	return map[string]string{"X-Memlane-Reading-Identity": readingID}
}

func isControlNotFound(err error) bool {
	var ce *ControlResponseError
	return errors.As(err, &ce) && ce.Status == http.StatusNotFound
}

func (c ProjectControlClient) projectMessageSummaryV2(ctx context.Context, readingID, projectID string) (ProjectMessageSummary, error) {
	var out ProjectMessageSummary
	path := "/v1/projects/" + url.PathEscape(projectID) + "/message-summary"
	err := c.RequestWithHeaders(ctx, "GET", path, v2IdentityHeaders(readingID), nil, &out, 512*1024)
	return out, err
}
