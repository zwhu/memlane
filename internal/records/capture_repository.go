package records

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Repository identity is captured with the original. Unknown means retained
// without attribution, never a project name or a guessed remote URL.
type CaptureRepository struct {
	State           string `json:"state"`
	Workspace       string `json:"workspace"`
	GitRoot         string `json:"git_root,omitempty"`
	GitCommon       string `json:"git_common,omitempty"`
	WorkspaceDevice uint64 `json:"workspace_device,omitempty"`
	WorkspaceInode  uint64 `json:"workspace_inode,omitempty"`
	RootDevice      uint64 `json:"root_device,omitempty"`
	RootInode       uint64 `json:"root_inode,omitempty"`
	CommonDevice    uint64 `json:"common_device,omitempty"`
	CommonInode     uint64 `json:"common_inode,omitempty"`
}

// Same hash contract as InspectProject.ExpectedRepository. Computing from the
// capture-time snapshot does not authorize a changed repository at that path.
func (r CaptureRepository) RegistrationIdentity() string {
	if r.State != "known" || r.GitRoot == "" || r.GitCommon == "" {
		return ""
	}
	parts := []string{r.GitRoot, r.GitCommon, fmt.Sprintf("%d:%d", r.RootDevice, r.RootInode), fmt.Sprintf("%d:%d", r.CommonDevice, r.CommonInode)}
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:])
}

func (r CaptureRepository) SameRepository(other CaptureRepository) bool {
	return r.State == "known" && other.State == "known" && r.GitCommon != "" && r.GitCommon == other.GitCommon && r.CommonDevice == other.CommonDevice && r.CommonInode == other.CommonInode
}
