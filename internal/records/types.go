// Package records defines lightweight original/version transport types.
package records

import "github.com/zwhu/memlane/internal/engine"

type Mutation struct {
	OperationID      string `json:"operation_id"`
	Kind             string `json:"kind"` // save, edit, delete, restore; all explicit user actions
	Dataset          string `json:"dataset"`
	Memory           string `json:"memory"`
	ExpectedRevision int64  `json:"expected_revision"`
	RestoreRevision  int64  `json:"restore_revision,omitempty"`
	Text             string `json:"text,omitempty"`
}

type Version struct {
	engine.Record
	State     string `json:"state"`
	Operation string `json:"operation"`
}
