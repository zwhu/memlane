package records

// Preview fields are UI drafts, never effective knowledge or task authority.
// Identity and generation are issued by the service; a restored database gets
// a new identity so an old window cannot write into the replacement library.
type ExperiencePreview struct {
	ID              string                            `json:"id"`
	Project         string                            `json:"project"`
	Source          MergeTarget                       `json:"source"`
	TaskID          string                            `json:"task,omitempty"`
	Start           *OrganizationStart                `json:"start,omitempty"`
	Open            bool                              `json:"open"`
	Page            string                            `json:"page"`
	Scope           string                            `json:"scope,omitempty"`
	Selected        string                            `json:"selected,omitempty"`
	ReturnPage      string                            `json:"return_page"`
	ReturnScope     string                            `json:"return_scope,omitempty"`
	ReturnSelected  string                            `json:"return_selected,omitempty"`
	Scroll          float32                           `json:"scroll"`
	BodyScroll      float32                           `json:"body_scroll"`
	ReturnScroll    float32                           `json:"return_scroll"`
	Candidate       string                            `json:"candidate,omitempty"`
	Focus           string                            `json:"focus,omitempty"`
	ExplanationOpen bool                              `json:"explanation_open,omitempty"`
	Drafts          map[string]ExperiencePreviewDraft `json:"drafts,omitempty"`
}
type ExperiencePreviewDraft struct {
	ProposalDigest string               `json:"proposal_digest"`
	Value          ExperienceReviewEdit `json:"value"`
	Request        *OrganizationReview  `json:"request,omitempty"`
	RejectRequest  *OrganizationReview  `json:"reject_request,omitempty"`
	OriginalOpen   bool                 `json:"original_open,omitempty"`
}
type ExperiencePreviewWrite struct {
	Identity   string            `json:"identity"`
	Generation int64             `json:"generation"`
	Operation  string            `json:"operation"`
	Preview    ExperiencePreview `json:"preview"`
}
type ExperiencePreviewState struct {
	Identity   string             `json:"identity"`
	Generation int64              `json:"generation"`
	Preview    *ExperiencePreview `json:"preview,omitempty"`
	Task       *OrganizationTask  `json:"task,omitempty"`
	Source     *MergeInput        `json:"source,omitempty"`
}
