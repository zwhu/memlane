package records

// ProjectInspection is a read-only local repository check, not registration or
// Agent authorization. Identity must be rechecked immediately before saving.
type ProjectInspection struct {
	Path     string `json:"path"`
	Root     string `json:"root,omitempty"`
	Name     string `json:"name,omitempty"`
	Identity string `json:"identity,omitempty"`
	State    string `json:"state"`
	Existing *Scope `json:"existing,omitempty"`
}
