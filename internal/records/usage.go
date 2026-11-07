package records

// Usage metadata records service observations, never query text or chat content.
type UsageTarget struct {
	Memory   string `json:"memory"`
	Dataset  string `json:"dataset"`
	Revision int64  `json:"revision"`
}
type UsageObservation struct {
	WorkspaceID  string        `json:"workspace_identity,omitempty"`
	OperationID  string        `json:"operation_id"`
	Digest       string        `json:"digest"`
	Kind         string        `json:"kind"`
	Channel      string        `json:"channel"`
	ConnectionID string        `json:"connection_id,omitempty"`
	Generation   int64         `json:"generation,omitempty"`
	Targets      []UsageTarget `json:"targets"`
	Exposure     string        `json:"exposure,omitempty"`
}
type UsageReceipt struct {
	WorkspaceID string        `json:"workspace_identity,omitempty"`
	ID          string        `json:"id"`
	Kind        string        `json:"kind"`
	Channel     string        `json:"channel"`
	ObservedMS  int64         `json:"observed_ms"`
	Targets     []UsageTarget `json:"targets"`
}
type UsageCounts struct {
	Reads     int64 `json:"reads"`
	Returns   int64 `json:"returns"`
	Adoptions int64 `json:"reported_adoptions"`
	Browses   int64 `json:"browses"`
}
type UsageInfo struct {
	BackgroundCollected bool        `json:"background_collected"`
	AdoptionMeaning     string      `json:"adoption_meaning"`
	StartedMS           int64       `json:"started_ms"`
	WindowDays          int         `json:"window_days"`
	RetentionDays       int         `json:"retention_days"`
	Daily               UsageCounts `json:"daily_agent"`
	App                 UsageCounts `json:"app"`
	Verification        UsageCounts `json:"connection_verification"`
	Background          UsageCounts `json:"background"`
	Important           bool        `json:"important"`
}
type ImportantRequest struct {
	Memory           string `json:"memory"`
	ExpectedRevision int64  `json:"expected_revision"`
	Important        bool   `json:"important"`
}
