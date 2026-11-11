package records

type ProcessingValidationRequest struct {
	OperationID string `json:"operation_id"`
	Revision    string `json:"revision"`
}

// Live progress is observed from the owned process, not durable success. After
// an owner restart, interrupted validation is reported from the ledger instead.
type ProcessingValidationProgress struct {
	HTTP                  *ProcessingHTTPReceipt `json:"http,omitempty"`
	Started               bool                   `json:"process_started"`
	PID                   int                    `json:"pid,omitempty"`
	Terminal              bool                   `json:"terminal"`
	Reaped                bool                   `json:"process_reaped"`
	GroupGone             bool                   `json:"process_group_gone"`
	NativeSchemaRequested bool                   `json:"native_schema_requested"`
	AbortSent             bool                   `json:"abort_sent"`
	AbortAcknowledged     bool                   `json:"abort_acknowledged"`
	Events                map[string]int         `json:"events"`
}

type ProcessingValidationStatus struct {
	Validation ProcessingValidation          `json:"validation"`
	Progress   *ProcessingValidationProgress `json:"live_progress,omitempty"`
}

// HTTP receipts describe local transport and the upstream's reported identity.
// They never assert a process lifecycle or upstream cancellation/billing.
type ProcessingHTTPReceipt struct {
	Protocol              string `json:"protocol"`
	RequestedModel        string `json:"requested_model"`
	ReportedModel         string `json:"reported_model,omitempty"`
	GatewayProvider       string `json:"gateway_provider,omitempty"`
	GatewayModel          string `json:"gateway_model,omitempty"`
	NativeSchemaRequested bool   `json:"native_schema_requested"`
	RequestSent           bool   `json:"request_sent"`
	ResponseComplete      bool   `json:"response_complete"`
	SchemaValid           bool   `json:"schema_valid"`
	LocalCancelled        bool   `json:"local_cancelled"`
	Status                int    `json:"status,omitempty"`
	StartedMS             int64  `json:"started_ms,omitempty"`
	FinishedMS            int64  `json:"finished_ms,omitempty"`
}

type ProcessingCredentialWrite struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Credential       string `json:"credential"`
	Revision         int64  `json:"revision"`
	Provider         string `json:"provider"`
	Endpoint         string `json:"endpoint"`
	Protocol         string `json:"protocol"`
	Secret           []byte `json:"secret"`
}
type ProcessingCredentialReference struct {
	Supplier   *ProcessingSupplier `json:"supplier,omitempty"`
	Credential string              `json:"credential"`
	Revision   int64               `json:"revision"`
}
type ProcessingModel struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Provider string `json:"provider,omitempty"`
}
type ProcessingModelCatalog struct {
	Models   []ProcessingModel `json:"models"`
	HasMore  bool              `json:"has_more"`
	LastID   string            `json:"last_id,omitempty"`
	ReadMS   int64             `json:"read_ms"`
	Endpoint string            `json:"endpoint"`
	Protocol string            `json:"protocol"`
}
type ProcessingModelQuery struct {
	Spec  ProcessingSpec `json:"spec"`
	After string         `json:"after,omitempty"`
}

type ProcessingSupplier struct {
	Provider           string `json:"provider"`
	Endpoint           string `json:"endpoint"`
	Protocol           string `json:"protocol"`
	Credential         string `json:"credential"`
	CredentialRevision int64  `json:"credential_revision"`
	Revision           int64  `json:"revision"`
	UpdatedMS          int64  `json:"updated_ms"`
}
