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
