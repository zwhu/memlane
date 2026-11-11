// Package records defines lightweight original/version transport types.
package records

import (
	"errors"
	"github.com/zwhu/memlane/internal/engine"
)

var ErrConflict = errors.New("revision or idempotency conflict")

type Mutation struct {
	Conditions       *string      `json:"experience_conditions,omitempty"`
	OperationID      string       `json:"operation_id"`
	Kind             string       `json:"kind"` // save, edit, delete, restore; all explicit user actions
	Dataset          string       `json:"dataset"`
	Memory           string       `json:"memory"`
	ExpectedRevision int64        `json:"expected_revision"`
	RestoreRevision  int64        `json:"restore_revision,omitempty"`
	Text             string       `json:"text,omitempty"`
	Title            string       `json:"title,omitempty"`
	Source           string       `json:"source,omitempty"`
	Origin           string       `json:"origin,omitempty"` // knowledge, imported, raw
	SourceDetail     *AgentSource `json:"source_detail,omitempty"`
}

type Version struct {
	Usage        *UsageInfo      `json:"usage,omitempty"`
	UsageReceipt *UsageReceipt   `json:"usage_receipt,omitempty"`
	Experience   *ExperienceInfo `json:"experience,omitempty"`
	Tags         []string        `json:"tags,omitempty"`
	Derived      *DerivedInfo    `json:"derived,omitempty"`
	engine.Record
	State        string       `json:"state"`
	Operation    string       `json:"operation"`
	Title        string       `json:"title"`
	CreatedAt    string       `json:"created_at"`
	UpdatedAt    string       `json:"updated_at"`
	Source       string       `json:"source"`
	Original     string       `json:"original"`
	ScopeName    string       `json:"scope_name"`
	ScopeKind    string       `json:"scope_kind"`
	Origin       string       `json:"origin"`
	IndexState   string       `json:"index_state"`
	SourceDetail *AgentSource `json:"source_detail,omitempty"`
}

type Scope struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	Path string `json:"path,omitempty"`
}
type ScopeDraft struct {
	ExpectedRepository string `json:"expected_repository,omitempty"`
	Name               string `json:"name"`
	Path               string `json:"path"`
}
type ListQuery struct {
	ScopeKind      string   `json:"scope_kind,omitempty"`
	UpdatedSinceMS int64    `json:"updated_since_ms,omitempty"`
	Text           string   `json:"text,omitempty"`
	Sort           string   `json:"sort,omitempty"`
	Datasets       []string `json:"datasets,omitempty"`
	Kind           string   `json:"kind,omitempty"` // knowledge or raw
	Source         string   `json:"source,omitempty"`
	Date           string   `json:"date,omitempty"` // activity date, in Timezone
	Timezone       string   `json:"timezone,omitempty"`
	Cursor         string   `json:"cursor,omitempty"`
	Limit          int      `json:"limit,omitempty"`
}
type Page struct {
	Sources          []string  `json:"sources,omitempty"`
	SourcesTruncated bool      `json:"sources_truncated,omitempty"`
	KnowledgeTotal   *int      `json:"knowledge_total,omitempty"`
	RawTotal         *int      `json:"raw_total,omitempty"`
	Items            []Version `json:"items"`
	NextCursor       string    `json:"next_cursor,omitempty"`
	Total            int       `json:"total"`
}
type History struct {
	Experience *ExperienceInfo `json:"experience,omitempty"`
	Revision   int64           `json:"revision"`
	Title      string          `json:"title"`
	Text       string          `json:"text"`
	Operation  string          `json:"operation"`
	CreatedAt  string          `json:"created_at"`
}
type HistoryPage struct {
	Items        []History `json:"items"`
	NextRevision int64     `json:"next_revision,omitempty"`
}
type Activity struct {
	Memory     string `json:"memory"`
	Kind       string `json:"kind"`
	OccurredAt string `json:"occurred_at"`
	State      string `json:"state"`
}
type ActivitySummary struct {
	RawTotal       *int           `json:"raw_total,omitempty"`
	KnowledgeTotal *int           `json:"knowledge_total,omitempty"`
	Days           map[string]int `json:"days"`
	Total          int            `json:"total"`
	Imported       int            `json:"imported"`
	Recent         []Version      `json:"recent"`
}

type SearchHit struct {
	Version   Version          `json:"version"`
	Candidate engine.Candidate `json:"candidate"`
}
