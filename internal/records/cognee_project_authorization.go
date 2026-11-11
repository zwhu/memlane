package records

// Host-confirmed current project only. Service address, dataset, workspace and
// all credentials are resolved by the daemon; no Agent auth is requested.
type CogneeProjectAuthorization struct {
	Operation    string `json:"operation"`
	Project      string `json:"project"`
	ConnectionID string `json:"connection_id"`
	Generation   int64  `json:"generation"`
}
