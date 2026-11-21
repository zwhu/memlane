package simpledesktop

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

type projectMessageSummaryDocument struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Date     string    `json:"date"`
	Kind     string    `json:"kind"`
	Document *Document `json:"document,omitempty"`
}

type projectMessageSummaryResponse struct {
	ProjectID         string                          `json:"project_id"`
	DatasetID         string                          `json:"dataset_id"`
	AutomaticSave     bool                            `json:"automatic_save"`
	Backlog           *backlogJob                     `json:"backlog"`
	Checks            []pendingCheckState             `json:"checks,omitempty"`
	BufferReviews     []bufferReview                  `json:"buffer_reviews,omitempty"`
	ProcessingNotices []processingNotice              `json:"processing_notices"`
	PendingDocuments  []projectMessageSummaryDocument `json:"pending_documents"`
	Activity          []activityRow                   `json:"activity"`
	Errors            map[string]string               `json:"errors"`
}

func (c ProjectControlClient) projectMessageSummary(ctx context.Context, projectID string) (projectMessageSummaryResponse, error) {
	var result projectMessageSummaryResponse
	err := c.request(ctx, "GET", "/v1/projects/"+url.PathEscape(projectID)+"/message-summary", nil, &result)
	return result, err
}

func isControlNotFound(err error) bool {
	var ce *ControlResponseError
	return errors.As(err, &ce) && ce.Status == http.StatusNotFound
}

func (r projectMessageSummaryResponse) pendingItems(defaultDataset string) []Item {
	items := make([]Item, 0, len(r.PendingDocuments))
	ds := r.DatasetID
	if ds == "" {
		ds = defaultDataset
	}
	for _, row := range r.PendingDocuments {
		kind := row.Kind
		if kind == "" {
			kind = "文档"
		}
		var docCopy *Document
		if row.Document != nil {
			d := *row.Document
			if d.Dataset == "" {
				d.Dataset = ds
			}
			docCopy = &d
		}
		title := row.Title
		if title == "" && docCopy != nil {
			title = docCopy.title()
		}
		date := row.Date
		if date == "" && docCopy != nil {
			date = docCopy.date()
		}
		items = append(items, Item{
			ID:       row.ID,
			Dataset:  ds,
			Kind:     kind,
			Title:    title,
			Date:     date,
			Document: docCopy,
		})
	}
	return items
}
