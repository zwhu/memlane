package records

// Diagnostics expose only a compiled gate name and bounded item position.
// The public error remains invalid_proposal; no source/model text is returned.
type OrganizationValidationError struct {
	reason string
	item   int
}

func (e *OrganizationValidationError) Error() string            { return "invalid_proposal" }
func (e *OrganizationValidationError) Unwrap() error            { return MergeError("invalid_proposal") }
func (e *OrganizationValidationError) ValidationReason() string { return e.reason }
func (e *OrganizationValidationError) ValidationItem() int      { return e.item }
