package records

type RestoreStats struct {
	Connections   int `json:"connections_retired"`
	Plans         int `json:"plans_disabled"`
	CodeWorktrees int `json:"code_worktrees_disabled"`
	Memories      int `json:"memories_queued"`
}

type RestoreReceipt struct {
	Format    int          `json:"format"`
	ID        string       `json:"id"`
	Backup    string       `json:"backup"`
	Vault     string       `json:"vault"`
	SourceSHA string       `json:"source_sha256"`
	SHA256    string       `json:"prepared_sha256"`
	CreatedAt string       `json:"created_at"`
	Stats     RestoreStats `json:"stats"`
	State     string       `json:"state"`
}
