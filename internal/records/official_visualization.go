package records

import "encoding/json"

// This is the official renderer payload, not the legacy imported-memory graph.
// Bounds describe a loaded subgraph; its nodes are not a knowledge count.
type OfficialVisualizationQuery struct {
	Project  string   `json:"project"`
	Instance string   `json:"instance"`
	Dataset  string   `json:"dataset"`
	MaxNodes int      `json:"max_nodes"`
	Seeds    []string `json:"seeds,omitempty"`
}

type OfficialVisualization struct {
	Instance string          `json:"instance"`
	Dataset  string          `json:"dataset"`
	Scope    string          `json:"scope"`
	MaxNodes int             `json:"max_nodes"`
	Payload  json.RawMessage `json:"payload"`
}
