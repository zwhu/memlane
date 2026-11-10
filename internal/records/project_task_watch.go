package records

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Versions describe a complete observed snapshot, not elapsed animation time.
type ProjectTaskWatch struct {
	Task    string `json:"task"`
	Attempt int64  `json:"attempt"`
	Version string `json:"version"`
}

type ProjectTaskObservation struct {
	Task    ProjectTask `json:"task"`
	Version string      `json:"version"`
}

func ProjectTaskVersion(t ProjectTask) string {
	b, _ := json.Marshal(t)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
