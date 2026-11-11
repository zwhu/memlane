package records

// Plan policy is explicit user input. Empty fields do not authorize a default
// frequency, material budget, or background model invocation.
type ScheduleSpec struct {
	Processing            *ProcessingSnapshot `json:"processing,omitempty"`
	RetryLimit            int                 `json:"retry_limit,omitempty"`
	RecoveryWindowMinutes int                 `json:"recovery_window_minutes,omitempty"`
	Project               string              `json:"project"`
	Executor              string              `json:"executor"`
	Model                 string              `json:"model,omitempty"`
	Timezone              string              `json:"timezone"`
	Frequency             string              `json:"frequency"`
	LocalTime             string              `json:"local_time,omitempty"`
	Weekday               int                 `json:"weekday"`
	IntervalHours         int                 `json:"interval_hours,omitempty"`
	WindowMinutes         int                 `json:"window_minutes"`
	MaterialLimit         int                 `json:"material_limit"`
	Review                bool                `json:"review"`
	Global                bool                `json:"global_candidates"`
	Destinations          []string            `json:"destinations,omitempty"`
}

type ScheduleDraft struct {
	Processing         *ProcessingTaskAuthorization `json:"processing,omitempty"`
	ID                 string                       `json:"plan"`
	OperationID        string                       `json:"operation_id"`
	ExpectedGeneration int64                        `json:"expected_generation"`
	Enabled            bool                         `json:"enabled"`
	Spec               ScheduleSpec                 `json:"spec"`
}

type Schedule struct {
	ProjectPath  string       `json:"project_path"`
	FrozenScopes []Scope      `json:"authorized_destinations"`
	ID           string       `json:"plan"`
	Generation   int64        `json:"generation"`
	Enabled      bool         `json:"enabled"`
	Spec         ScheduleSpec `json:"spec"`
	AnchorMS     int64        `json:"anchor_ms"`
	NextRunMS    int64        `json:"next_run_ms"`
	CreatedAt    string       `json:"created_at"`
}

type ScheduleRun struct {
	ID         string `json:"run"`
	Plan       string `json:"plan"`
	Generation int64  `json:"generation"`
	DueMS      int64  `json:"due_ms"`
	State      string `json:"state"`
	Reason     string `json:"reason,omitempty"`
	Task       string `json:"task,omitempty"`
	CreatedAt  string `json:"created_at"`
}
