package records

// Explicit manual comparison, never a semantic conflict detector. Datasets is
// the trusted local caller's read scope, not an Agent authorization credential.
type ProjectPreferenceQuery struct {
	LibraryIdentity  string      `json:"library_identity,omitempty"`
	Project          string      `json:"project"`
	ProjectRecord    UsageTarget `json:"project_record"`
	PreferenceRecord UsageTarget `json:"preference_record"`
	Datasets         []string    `json:"datasets"`
}
type ProjectPreferenceComparison struct {
	LibraryIdentity  string  `json:"library_identity"`
	Project          Scope   `json:"project"`
	ProjectRecord    Version `json:"project_record"`
	PreferenceRecord Version `json:"preference_record"`
	// Only return conditions actually persisted with the selected version.
	// Ordinary records usually have none; never infer them from prose.
	ProjectConditions            string                     `json:"project_conditions"`
	PreferenceConditions         string                     `json:"preference_conditions"`
	ProjectConditionsRecorded    bool                       `json:"project_conditions_recorded"`
	PreferenceConditionsRecorded bool                       `json:"preference_conditions_recorded"`
	Confirmation                 *ProjectPreferenceDecision `json:"confirmation,omitempty"`
}
type ProjectPreferenceConfirmation struct {
	OperationID        string                 `json:"operation_id"`
	LibraryIdentity    string                 `json:"library_identity"`
	Query              ProjectPreferenceQuery `json:"query"`
	ProjectQuote       string                 `json:"project_quote"`
	PreferenceQuote    string                 `json:"preference_quote"`
	Conditions         string                 `json:"conditions"`
	Reason             string                 `json:"reason,omitempty"`
	ConfirmProjectRule bool                   `json:"confirm_project_rule"`
	Decision           string                 `json:"decision"`
}
type ProjectPreferenceDecision struct {
	ID               string      `json:"id"`
	LibraryIdentity  string      `json:"library_identity"`
	Project          string      `json:"project"`
	ProjectRecord    UsageTarget `json:"project_record"`
	PreferenceRecord UsageTarget `json:"preference_record"`
	ProjectQuote     string      `json:"project_quote"`
	PreferenceQuote  string      `json:"preference_quote"`
	Conditions       string      `json:"conditions"`
	Reason           string      `json:"reason,omitempty"`
	Source           string      `json:"source"`
	Decision         string      `json:"decision"`
	ConfirmedMS      int64       `json:"confirmed_ms"`
	State            string      `json:"state"`
}

// Discover only explicit current user confirmations for one exact project
// knowledge version. Absence says nothing about semantic conflicts.
type ProjectPreferenceDecisionsQuery struct {
	LibraryIdentity string      `json:"library_identity,omitempty"`
	Project         string      `json:"project"`
	ProjectRecord   UsageTarget `json:"project_record"`
	Datasets        []string    `json:"datasets"`
}
type ProjectPreferenceDecisionsPage struct {
	CoverageDatasets []string                    `json:"coverage_datasets"`
	LibraryIdentity  string                      `json:"library_identity"`
	Project          Scope                       `json:"project"`
	ProjectRecord    UsageTarget                 `json:"project_record"`
	Decisions        []ProjectPreferenceDecision `json:"decisions"`
	Truncated        bool                        `json:"truncated"`
}
