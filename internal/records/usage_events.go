package records

// Each page belongs to one exact knowledge target. Channels are independent;
// the default is daily Agent activity, never connection verification.
type UsageEventsQuery struct {
	Memory           string `json:"memory"`
	Dataset          string `json:"dataset,omitempty"`
	ExpectedRevision int64  `json:"expected_revision,omitempty"`
	Cursor           string `json:"cursor,omitempty"`
	Limit            int    `json:"limit,omitempty"`
	Channel          string `json:"channel,omitempty"`
	Kind             string `json:"kind,omitempty"`
}

type UsageEvent struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Channel      string `json:"channel"`
	ObservedMS   int64  `json:"observed_ms"`
	Memory       string `json:"memory"`
	Revision     int64  `json:"revision"`
	Dataset      string `json:"dataset"`
	ConnectionID string `json:"connection_id,omitempty"`
	Generation   int64  `json:"generation,omitempty"`
	Agent        string `json:"agent,omitempty"`
	Exposure     string `json:"exposure,omitempty"`
}

type UsageEventsPage struct {
	Memory            string       `json:"memory"`
	Dataset           string       `json:"dataset"`
	CurrentRevision   int64        `json:"current_revision"`
	Channel           string       `json:"channel"`
	Kind              string       `json:"kind,omitempty"`
	Events            []UsageEvent `json:"events"`
	HasMore           bool         `json:"has_more"`
	NextCursor        string       `json:"next_cursor,omitempty"`
	CoverageStartedMS int64        `json:"coverage_started_ms"`
	RetentionDays     int          `json:"retention_days"`
	SnapshotMS        int64        `json:"snapshot_ms"`
}
