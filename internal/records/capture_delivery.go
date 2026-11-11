package records

import "encoding/json"

type CaptureOrigin struct {
	Agent      string            `json:"agent"`
	Consent    string            `json:"collection_consent"`
	Repository CaptureRepository `json:"capture_repository"`
}

func ReadCaptureOrigin(a AgentArguments) (CaptureOrigin, bool) {
	var out CaptureOrigin
	if a.Origin != "raw" || a.Source == nil || json.Unmarshal([]byte(a.Source.Fragment), &out) != nil || out.Agent != "codex" || out.Consent == "" {
		return out, false
	}
	return out, true
}

// A grant to save raw conversation records, independent of knowledge scopes.
type CaptureRoute struct {
	OperationID  string            `json:"operation_id"`
	LibraryID    string            `json:"library_id"`
	ConnectionID string            `json:"connection_id"`
	ConsentID    string            `json:"consent_id"`
	Repository   CaptureRepository `json:"repository"`
	Project      string            `json:"project"`
	CreatedAt    string            `json:"created_at"`
}

type CaptureRouteConfirmation struct {
	OperationID  string `json:"operation_id"`
	LibraryID    string `json:"library_id"`
	ConnectionID string `json:"connection_id"`
	EntryID      string `json:"entry_id"`
	ReviewDigest string `json:"review_digest"`
	Project      string `json:"project"`
}

type CaptureDeliveryQuery struct {
	EntryID      string `json:"entry_id"`
	ReviewDigest string `json:"review_digest"`
}

type CaptureDeliveryReceipt struct {
	EntryID       string `json:"entry_id"`
	RequestDigest string `json:"request_digest"`
	ReviewDigest  string `json:"review_digest"`
	ConnectionID  string `json:"connection_id"`
	OperationID   string `json:"operation_id"`
	LibraryID     string `json:"library_id"`
	RouteID       string `json:"route_id"`
	Project       string `json:"project"`
	Memory        string `json:"memory"`
	Revision      int64  `json:"revision"`
	Persisted     bool   `json:"persisted"`
	AckPending    bool   `json:"ack_pending,omitempty"`
}
