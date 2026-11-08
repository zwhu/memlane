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
