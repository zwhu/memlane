package simpledesktop

import (
	"errors"
	"net/http"
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

func isControlNotFound(err error) bool {
	var ce *ControlResponseError
	return errors.As(err, &ce) && ce.Status == http.StatusNotFound
}

func (r v2MessageSummaryResponse) pendingItems(defaultDataset string) []Item {
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
