package records

// HomeActivity is an observed fact, independent from the current knowledge
// version. Usage return, read and explicit adoption remain distinct.
type HomeActivity struct {
	CurrentDataset   string `json:"current_dataset"`
	CurrentScopeKind string `json:"current_scope_kind"`
	ID               string `json:"id"`
	Kind             string `json:"kind"`
	Channel          string `json:"channel"`
	Agent            string `json:"agent"`
	Memory           string `json:"memory"`
	Dataset          string `json:"dataset"`
	ScopeName        string `json:"scope_name"`
	ScopeKind        string `json:"scope_kind"`
	Revision         int64  `json:"revision"`
	Title            string `json:"title"`
	Origin           string `json:"origin"`
	OccurredMS       int64  `json:"occurred_ms"`
}
type HomeActivityQuery struct {
	Project string `json:"project,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Before  string `json:"before,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}
type HomeActivityPage struct {
	Items []HomeActivity `json:"items"`
	Next  string         `json:"next,omitempty"`
	Total int            `json:"total"`
}

// HomeReview locates a suggestion without applying it. Its own review owner
// rechecks frozen versions and explicit authorization when opened/confirmed.
type HomeReview struct {
	ID        string `json:"id"`
	Task      string `json:"task"`
	Owner     string `json:"owner"`
	Project   string `json:"project"`
	ScopeName string `json:"scope_name"`
	Action    string `json:"action"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
}
type HomeReviewQuery struct {
	Project string `json:"project,omitempty"`
	Action  string `json:"action,omitempty"`
	Before  string `json:"before,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}
type HomeReviewPage struct {
	Items []HomeReview `json:"items"`
	Next  string       `json:"next,omitempty"`
	Total int          `json:"total"`
}
