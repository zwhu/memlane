package records

// OrganizationPreview is a durable UI draft. It never grants task authority
// or changes effective knowledge. Start/decision barriers retain exact requests.
type OrganizationPreview struct {
	ID                 string                            `json:"id"`
	Open               bool                              `json:"open"`
	New                bool                              `json:"new"`
	Project            string                            `json:"project,omitempty"`
	Kind               string                            `json:"kind"`
	Inputs             []MergeTarget                     `json:"inputs,omitempty"`
	Destinations       []string                          `json:"destinations,omitempty"`
	Review             bool                              `json:"review"`
	Global             bool                              `json:"global_candidates"`
	Purpose            string                            `json:"purpose,omitempty"`
	TaskID             string                            `json:"task,omitempty"`
	Candidate          string                            `json:"candidate,omitempty"`
	Start              *OrganizationStart                `json:"start,omitempty"`
	Resume             *OrganizationResume               `json:"resume,omitempty"`
	CancelWanted       bool                              `json:"cancel_wanted,omitempty"`
	Decisions          map[string]OrganizationReview     `json:"decisions,omitempty"`
	RejectReasons      map[string]string                 `json:"reject_reasons,omitempty"`
	ExperienceDrafts   map[string]ExperiencePreviewDraft `json:"experience_drafts,omitempty"`
	OriginFilter       string                            `json:"origin_filter,omitempty"`
	ProjectFilter      string                            `json:"project_filter,omitempty"`
	Today              bool                              `json:"today"`
	FilterOpen         bool                              `json:"filter_open"`
	ListScroll         float32                           `json:"list_scroll"`
	Scroll             float32                           `json:"scroll"`
	ResultScroll       float32                           `json:"result_scroll"`
	ReviewScroll       map[string]float32                `json:"review_scroll,omitempty"`
	ReviewFocus        map[string]string                 `json:"review_focus,omitempty"`
	MoveAuthorizations map[string]string                 `json:"move_authorizations,omitempty"`
	Focus              string                            `json:"focus,omitempty"`
	DiagnosticOpen     bool                              `json:"diagnostic_open"`
	ExplanationOpen    bool                              `json:"explanation_open"`
}

type OrganizationPreviewWrite struct {
	Identity     string              `json:"identity"`
	Generation   int64               `json:"generation"`
	Operation    string              `json:"operation"`
	Preview      OrganizationPreview `json:"preview"`
	AbandonStart bool                `json:"abandon_start,omitempty"`
}

type OrganizationPreviewState struct {
	Identity   string               `json:"identity"`
	Generation int64                `json:"generation"`
	Preview    *OrganizationPreview `json:"preview,omitempty"`
	Task       *OrganizationTask    `json:"task,omitempty"`
	Inputs     []OrganizationInput  `json:"inputs,omitempty"`
}
