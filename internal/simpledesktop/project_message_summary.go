package simpledesktop

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

type projectMessageSummaryDocument struct {
	ID        string    `json:"id"`
	DatasetID string    `json:"dataset_id,omitempty"`
	Name      string    `json:"name,omitempty"`
	Label     *string   `json:"label,omitempty"`
	Title     string    `json:"title"`
	Date      string    `json:"date"`
	Kind      string    `json:"kind"`
	CreatedAt string    `json:"created_at,omitempty"`
	Document  *Document `json:"document,omitempty"`
}

type projectMessageSummaryKnowledge struct {
	ID        string `json:"id"`
	CreatedAt string `json:"created_at"`
}

type projectMessageSummaryResponse struct {
	ProjectID                 string                           `json:"project_id"`
	DatasetID                 string                           `json:"dataset_id"`
	AutomaticSave             bool                             `json:"automatic_save"`
	Backlog                   *backlogJob                      `json:"backlog"`
	Checks                    []pendingCheckState              `json:"checks,omitempty"`
	BufferReviews             []bufferReview                   `json:"buffer_reviews,omitempty"`
	ProcessingIssues          []processingIssue                `json:"processing_issues,omitempty"`
	ProcessingNotices         []processingNotice               `json:"processing_notices"`
	ResolvedProcessingNotices []processingNotice               `json:"resolved_processing_notices"`
	PendingDocuments          []projectMessageSummaryDocument  `json:"pending_documents"`
	KnowledgeCatalog          []projectMessageSummaryKnowledge `json:"knowledge_catalog"`
	KnowledgeCatalogVersion   string                           `json:"knowledge_catalog_version,omitempty"`
	KnowledgeCatalogUnchanged bool                             `json:"knowledge_catalog_unchanged,omitempty"`
	Activity                  []activityRow                    `json:"activity"`
	Errors                    map[string]string                `json:"errors"`
}

func (c ProjectControlClient) projectMessageSummary(ctx context.Context, projectID, knownKnowledgeVersion string) (projectMessageSummaryResponse, error) {
	var result projectMessageSummaryResponse
	path := "/v1/projects/" + url.PathEscape(projectID) + "/message-summary"
	if knownKnowledgeVersion != "" {
		path += "?knowledge_version=" + url.QueryEscape(knownKnowledgeVersion)
	}
	err := c.request(ctx, "GET", path, nil, &result)
	return result, err
}

func (r projectMessageSummaryResponse) knowledgeDocuments(defaultDataset string) []Document {
	if r.KnowledgeCatalog == nil {
		return nil
	}
	ds := r.DatasetID
	if ds == "" {
		ds = defaultDataset
	}
	docs := make([]Document, 0, len(r.KnowledgeCatalog))
	for _, row := range r.KnowledgeCatalog {
		if row.ID == "" {
			continue
		}
		docs = append(docs, Document{
			ID:       row.ID,
			Dataset:  ds,
			Created:  row.CreatedAt,
			Metadata: map[string]any{"node_set": []any{"session_learnings"}},
		})
	}
	return docs
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
		rowDS := row.DatasetID
		if rowDS == "" {
			rowDS = ds
		}
		kind := row.Kind
		if kind == "" {
			kind = "文档"
		}
		var docCopy *Document
		if row.Document != nil {
			d := *row.Document
			if d.Dataset == "" {
				d.Dataset = rowDS
			}
			docCopy = &d
		} else if row.ID != "" {
			name := row.Name
			if name == "" && row.Label == nil {
				name = row.Title
			}
			var labelCopy *string
			if row.Label != nil {
				l := *row.Label
				labelCopy = &l
			}
			created := row.CreatedAt
			if created == "" {
				created = row.Date
			}
			d := Document{
				ID:      row.ID,
				Dataset: rowDS,
				Name:    name,
				Label:   labelCopy,
				Created: created,
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
			Dataset:  rowDS,
			Kind:     kind,
			Title:    title,
			Date:     date,
			Document: docCopy,
		})
	}
	return items
}
