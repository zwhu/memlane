package records

// Original records are read in bounded native text windows. This limit is
// independent of the smaller AI summary/input quote budget.
const CogneeRawLimit = 256 * 1024

// CogneeImport is a trusted-host import of an actual official API result. It
// is not a daily Agent tool and cannot update existing knowledge. The host
// binds the approved workspace/connection; external records never choose them.
type CogneeImport struct {
	ConnectionID string          `json:"connection_id"`
	Generation   int64           `json:"generation"`
	Workspace    string          `json:"workspace"`
	Project      string          `json:"project"`
	ExternalID   string          `json:"external_id"`
	Origin       string          `json:"origin"`
	Title        string          `json:"title"`
	Text         string          `json:"text"`
	Fragment     string          `json:"fragment"`
	Material     *CogneeOriginal `json:"material,omitempty"`
	Native       *CodexOriginal  `json:"native,omitempty"`
	Sources      []MergeSource   `json:"sources,omitempty"`
}

// The complete user/assistant and tool stream is separate from Cognee's
// truncated SDK trace DTO. Only the trusted scoped log reader stamps this.
type CodexOriginal struct {
	Instance   string `json:"instance"`
	Dataset    string `json:"dataset"`
	Session    string `json:"session"`
	Workspace  string `json:"workspace"`
	CLIVersion string `json:"cli_version"`
	CapturedAt string `json:"captured_at"`
	Digest     string `json:"digest"`
}

// Official metadata is stamped only by trusted import, separately from
// free-form Agent citations. Trace sequence is real SDK order; no time is
// invented for upstream trace records that do not carry timestamps.
type CogneeOriginal struct {
	Instance      string `json:"instance"`
	Session       string `json:"session"`
	Kind          string `json:"kind"`
	Sequence      int    `json:"sequence"`
	CapturedAt    string `json:"captured_at,omitempty"`
	OfficialState string `json:"official_state"`
}
