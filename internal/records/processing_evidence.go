package records

// ProcessingCapabilityEvidence contains only runner observations. Configured
// is a local launch policy, not proof of an end-to-end collection test.
type ProcessingCapabilityEvidence struct {
	ValidationID        string `json:"validation"`
	Revision            string `json:"revision"`
	Attempt             int64  `json:"attempt"`
	DependencyDigest    string `json:"dependency_digest"`
	Agent               string `json:"agent"`
	ResponseComplete    bool   `json:"response_complete"`
	StructureValid      bool   `json:"structure_valid"`
	IsolationConfigured bool   `json:"isolation_configured"`
	IsolationPolicy     string `json:"isolation_policy,omitempty"`
	Reaped              bool   `json:"reaped"`
	GroupGone           bool   `json:"group_gone"`
}
