package records

type BackupReceipt struct {
	Format       int    `json:"format"`
	ID           string `json:"id"`
	Vault        string `json:"vault"`
	Schema       int    `json:"schema"`
	CreatedAt    string `json:"created_at"`
	Bytes        int64  `json:"bytes"`
	SHA256       string `json:"sha256"`
	Verification string `json:"verification,omitempty"`
	Error        string `json:"error,omitempty"`
}

// BackupOperation reports the actual publication outcome after cancellation.
// A cancellation request alone is never proof that a snapshot was discarded.
type BackupOperation struct {
	ID      string         `json:"id"`
	State   string         `json:"state"`
	Receipt *BackupReceipt `json:"receipt,omitempty"`
	Error   string         `json:"error,omitempty"`
}
