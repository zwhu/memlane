package records

import "time"

// ServiceState is shared by live IPC lifecycle and offline recovery journaling.
type ServiceState struct {
	StopGeneration int64       `json:"stop_generation,omitempty"`
	LastStoppedMS  int64       `json:"last_stopped_ms,omitempty"`
	Stopped        bool        `json:"stopped"`
	Restarts       []time.Time `json:"restarts"`
}
