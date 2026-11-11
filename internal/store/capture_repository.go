package store

import (
	"encoding/json"

	"github.com/zwhu/memlane/internal/records"
)

// Generic manually retained submissions keep their existing path. Captured
// conversations additionally bind to their capture-time Git identity; an
// unknown identity can be read but cannot acquire a guessed project.
func checkCapturedRepository(a records.AgentArguments, current deliveryWorkspaceIdentity) error {
	if a.Source == nil || a.Source.Fragment == "" {
		return nil
	}
	var origin struct {
		Agent      string                     `json:"agent"`
		Consent    string                     `json:"collection_consent"`
		Repository *records.CaptureRepository `json:"capture_repository"`
	}
	if json.Unmarshal([]byte(a.Source.Fragment), &origin) != nil || origin.Agent != "codex" || origin.Consent == "" {
		return nil
	}
	r := origin.Repository
	if r == nil || r.State != "known" || r.GitRoot == "" || r.GitCommon == "" {
		return access("capture_repository_unverified", "capture-time repository was unknown; retain without automatic attribution")
	}
	if r.Workspace != current.Workspace || r.WorkspaceDevice != current.WorkspaceDevice || r.WorkspaceInode != current.WorkspaceInode || r.GitRoot != current.GitRoot || r.GitCommon != current.GitCommon || r.RootDevice != current.RootDevice || r.RootInode != current.RootInode || r.CommonDevice != current.CommonDevice || r.CommonInode != current.CommonInode {
		return access("capture_repository_changed", "repository differs from capture time; original retained")
	}
	return nil
}
