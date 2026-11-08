package records

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const IDEVerificationSource = "Memlane IDE connection verification; synthetic only"

type IDEVerification struct {
	ConnectionID string `json:"connection_id"`
	Generation   int64  `json:"generation"`
	Workspace    string `json:"workspace"`
	Nonce        string `json:"nonce"`
	CreatedMS    int64  `json:"created_ms"`
}

type IDEVerificationReceipt struct {
	Complete bool              `json:"complete"`
	State    string            `json:"state"`
	Memory   string            `json:"memory"`
	Revision int64             `json:"revision,omitempty"`
	Evidence map[string]string `json:"evidence,omitempty"`
}

func (v IDEVerification) OperationID() string {
	return "ide-check-" + fmt.Sprint(v.Generation) + "-" + v.Nonce
}
func (v IDEVerification) Marker() string {
	return "memlaneIDEConnectionCheck:" + v.ConnectionID + ":" + fmt.Sprint(v.Generation) + ":" + v.Nonce
}
func (v IDEVerification) Text() string {
	return "Memlane synthetic IDE connection verification " + v.Marker()
}
func (v IDEVerification) MemoryID() string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(v.ConnectionID+"\x00"+v.OperationID())).String()
}

// Only the exact synthetic record for this connection generation is excluded
// from daily statistics. Mixed search results retain their own channels.
func IDEVerificationRecord(c Connection, v Version) bool {
	if c.Agent != "trae" || v.Origin != "raw" || v.Source != "trae" || v.SourceDetail == nil || v.SourceDetail.Fragment != IDEVerificationSource || len(v.SourceDetail.References) != 0 || v.Revision != 1 {
		return false
	}
	prefix := "Memlane synthetic IDE connection verification memlaneIDEConnectionCheck:" + c.ID + ":" + fmt.Sprint(c.Generation) + ":"
	if !strings.HasPrefix(v.Text, prefix) {
		return false
	}
	nonce := strings.TrimPrefix(v.Text, prefix)
	if id, err := uuid.Parse(nonce); err != nil || id.String() != nonce {
		return false
	}
	check := IDEVerification{ConnectionID: c.ID, Generation: c.Generation, Nonce: nonce}
	return v.Memory == check.MemoryID() && v.Text == check.Text()
}
