package records

// ProcessingSpec stores capability-affecting configuration, never a credential
// value. Credential revisions and Agent generations are immutable references.
type ProcessingSpec struct {
	Mode                 string `json:"mode"`
	Provider             string `json:"provider,omitempty"`
	Model                string `json:"model"`
	Protocol             string `json:"protocol,omitempty"`
	Endpoint             string `json:"endpoint,omitempty"`
	Credential           string `json:"credential,omitempty"`
	CredentialRevision   int64  `json:"credential_revision,omitempty"`
	Agent                string `json:"agent,omitempty"`
	Connection           string `json:"connection,omitempty"`
	ConnectionGeneration int64  `json:"connection_generation,omitempty"`
}

type ProcessingRevision struct {
	ID         string                `json:"revision"`
	Spec       ProcessingSpec        `json:"spec"`
	Digest     string                `json:"config_digest"`
	CreatedMS  int64                 `json:"created_ms"`
	Validation *ProcessingValidation `json:"validation,omitempty"`
}

type ProcessingSave struct {
	OperationID   string         `json:"operation_id"`
	ExpectedSaved string         `json:"expected_saved_revision"`
	Spec          ProcessingSpec `json:"spec"`
}

type ProcessingSelection struct {
	Generation int64               `json:"generation"`
	Active     bool                `json:"active"`
	Revision   *ProcessingRevision `json:"current,omitempty"`
	ChangedMS  int64               `json:"changed_ms"`
}

// ProcessingReadiness is a read-only runtime observation. It never changes a
// saved validation receipt, current selection or permission to start a task.
type ProcessingReadiness struct {
	State     string `json:"state"`
	ErrorCode string `json:"error_code,omitempty"`
}

type ProcessingConfiguration struct {
	Runtime map[string]ProcessingReadiness `json:"runtime,omitempty"`
	Saved   map[string]ProcessingRevision  `json:"saved"`
	Current ProcessingSelection            `json:"current"`
}

type ProcessingValidationDiagnostics struct {
	Stage        string   `json:"stage"`
	ResolveCode  string   `json:"resolve_code,omitempty"`
	ChangedNames []string `json:"changed_names,omitempty"`
}

type ProcessingValidation struct {
	Evidence         *ProcessingCapabilityEvidence    `json:"evidence,omitempty"`
	HTTP             *ProcessingHTTPReceipt           `json:"http,omitempty"`
	Diagnostics      *ProcessingValidationDiagnostics `json:"diagnostics,omitempty"`
	ID               string                           `json:"validation"`
	Revision         string                           `json:"revision"`
	Attempt          int64                            `json:"attempt"`
	DependencyDigest string                           `json:"dependency_digest"`
	State            string                           `json:"state"`
	CancelRequested  bool                             `json:"cancel_requested"`
	ReceiptDigest    string                           `json:"receipt_digest,omitempty"`
	ErrorCode        string                           `json:"error_code,omitempty"`
	StartedMS        int64                            `json:"started_ms"`
	FinishedMS       int64                            `json:"finished_ms,omitempty"`
}

type ProcessingActivation struct {
	OperationID        string `json:"operation_id"`
	Revision           string `json:"revision"`
	ExpectedGeneration int64  `json:"expected_generation"`
}

type ProcessingActivationReceipt struct {
	Revision   string `json:"revision"`
	Generation int64  `json:"applied_generation"`
	ChangedMS  int64  `json:"changed_ms"`
}

// ProcessingSnapshot belongs to the enclosing task transaction. Reading the
// selection alone does not authorize or launch an execution.
type ProcessingSnapshot struct {
	SelectionGeneration int64              `json:"selection_generation"`
	Revision            ProcessingRevision `json:"revision"`
	DependencyDigest    string             `json:"dependency_digest"`
	FrozenMS            int64              `json:"frozen_ms"`
}

// A user submits only expected ledger identity. Runtime dependency material and
// the actual frozen configuration are resolved by the service owner.
type ProcessingTaskAuthorization struct {
	Revision   string `json:"revision"`
	Generation int64  `json:"selection_generation"`
}
