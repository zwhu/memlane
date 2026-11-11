package records

// Datasets is the caller's explicitly authorized read scope, not a grant.
// Native Library callers own the local library; Agent handlers must intersect
// it with their authenticated connection's grants before calling the store.
type DeletionImpactQuery struct {
	Memory           string   `json:"memory"`
	Dataset          string   `json:"dataset"`
	ExpectedRevision int64    `json:"expected_revision"`
	Datasets         []string `json:"datasets"`
}
type DeletionReference struct {
	Memory         string   `json:"memory"`
	Revision       int64    `json:"revision"`
	Dataset        string   `json:"dataset"`
	Title          string   `json:"title"`
	SourceRevision int64    `json:"source_revision"`
	Relations      []string `json:"relations"`
	DerivedState   string   `json:"derived_state,omitempty"`
}
type DeletionImpact struct {
	Memory            string              `json:"memory"`
	Dataset           string              `json:"dataset"`
	Revision          int64               `json:"revision"`
	CoverageDatasets  []string            `json:"coverage_datasets"`
	DirectMemoryCount int                 `json:"direct_memory_count"`
	ReferenceCount    int                 `json:"reference_count"`
	References        []DeletionReference `json:"references"`
	Truncated         bool                `json:"truncated"`
}
