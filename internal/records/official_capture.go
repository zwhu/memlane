package records

import "encoding/json"

// OfficialCaptureInput is transient and must never be serialized into a
// durable client queue. Only its metadata identity enters the receipt ledger.
type OfficialCaptureInput struct {
	Operation string          `json:"operation"`
	Session   string          `json:"session"`
	Entry     json.RawMessage `json:"entry"`
}

type PiCaptureBatchRequest struct {
	Batch       string                 `json:"batch"`
	Grant       string                 `json:"grant"`
	HostSession string                 `json:"host_session"`
	Request     AgentRequest           `json:"request"`
	Entries     []OfficialCaptureInput `json:"entries"`
}

type PiCaptureOperationReceipt struct {
	Operation string `json:"operation"`
	State     string `json:"state"`
}
type PiCaptureBatchReceipt struct {
	Batch      string                      `json:"batch"`
	Operations []PiCaptureOperationReceipt `json:"operations"`
}

// OfficialCaptureIdentity is a metadata-only delivery observation. It contains
// neither chat/tool text nor credentials. GrantDigest is computed by the host
// from current consent, connection, workspace and official dataset identities.
type OfficialCaptureIdentity struct {
	Operation     string `json:"operation"`
	Library       string `json:"library"`
	GrantDigest   string `json:"grant_digest"`
	Instance      string `json:"instance"`
	Dataset       string `json:"dataset"`
	Session       string `json:"session"`
	Kind          string `json:"kind"`
	PayloadDigest string `json:"payload_digest"`
}

type OfficialCaptureDelivery struct {
	OfficialCaptureIdentity
	State      string `json:"state"`
	OfficialID string `json:"official_id,omitempty"`
}

// PiCaptureConsentReview is a trusted App review, not an Agent tool argument.
// It grants capture of new daily messages only, never historical import/read.
type PiCaptureConsentReview struct {
	Operation   string        `json:"operation"`
	PreviousKey string        `json:"previous_key,omitempty"`
	Binding     CogneeBinding `json:"binding"`
}

type PiCaptureConsent struct {
	Operation         string        `json:"operation"`
	PreviousKey       string        `json:"previous_key,omitempty"`
	Key               string        `json:"key"`
	Library           string        `json:"library"`
	Binding           CogneeBinding `json:"binding"`
	WorkspaceDigest   string        `json:"workspace_digest"`
	WorkspacePhysical string        `json:"workspace_physical"`
	PolicyDigest      string        `json:"policy_digest"`
	Enabled           bool          `json:"enabled"`
}

type PiCaptureConsentDisable struct {
	Connection  string `json:"connection"`
	Project     string `json:"project"`
	ExpectedKey string `json:"expected_key"`
}

type PiCaptureSettingsQuery struct {
	Connection string `json:"connection"`
	Generation int64  `json:"generation"`
}
type PiCaptureSetting struct {
	Binding     CogneeBinding     `json:"binding"`
	PreviousKey string            `json:"previous_key,omitempty"`
	Consent     *PiCaptureConsent `json:"consent,omitempty"`
	Ready       bool              `json:"ready"`
	Saved       int               `json:"saved"`
}
