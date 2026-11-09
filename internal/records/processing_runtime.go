package records

type ProcessingValidationRequest struct {
	OperationID string `json:"operation_id"`
	Revision    string `json:"revision"`
}

// Live progress is observed from the owned process, not durable success. After
// an owner restart, interrupted validation is reported from the ledger instead.
type ProcessingValidationProgress struct {
	Started               bool           `json:"process_started"`
	PID                   int            `json:"pid,omitempty"`
	Terminal              bool           `json:"terminal"`
	Reaped                bool           `json:"process_reaped"`
	GroupGone             bool           `json:"process_group_gone"`
	NativeSchemaRequested bool           `json:"native_schema_requested"`
	AbortSent             bool           `json:"abort_sent"`
	AbortAcknowledged     bool           `json:"abort_acknowledged"`
	Events                map[string]int `json:"events"`
}

type ProcessingValidationStatus struct {
	Validation ProcessingValidation          `json:"validation"`
	Progress   *ProcessingValidationProgress `json:"live_progress,omitempty"`
}
