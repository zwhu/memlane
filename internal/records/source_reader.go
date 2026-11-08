package records

// SourceRead is a host-only, scoped read of actual lineage. The owner and
// source revisions are explicit; a model cannot create a citation by passing
// an arbitrary source ID. SourceRecord contains no prose when unavailable.
type SourceRead struct {
	Project       string `json:"project"`
	Owner         string `json:"owner"`
	OwnerRevision int64  `json:"owner_revision"`
	Memory        string `json:"memory"`
	Revision      int64  `json:"revision"`
}

type SourceRecord struct {
	SessionOriginal *Version `json:"session_original,omitempty"`
	OriginalState   string   `json:"original_state,omitempty"`
	Project         string   `json:"project"`
	Memory          string   `json:"memory"`
	Revision        int64    `json:"revision"`
	CurrentRevision int64    `json:"current_revision"`
	State           string   `json:"state"`
	Quote           string   `json:"quote,omitempty"`
	QuoteOffset     int      `json:"quote_offset"`
	Record          Version  `json:"record"`
}
