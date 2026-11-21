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
