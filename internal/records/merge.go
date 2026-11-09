package records

// MergeSchema is owned by memlaned, independently of the P3 experiment.
import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
)

const MergeSchema = "memlane-merge-proposal-v1"
const MergeInputLimit = 16 * 1024
const MergeOutputLimit = 48 * 1024

func MergeError(code string) error { return &AccessError{Code: code, Message: code} }

type MergeTarget struct {
	Memory   string `json:"memory"`
	Revision int64  `json:"revision"`
}
type MergeStart struct {
	OperationID string        `json:"operation_id"`
	Project     string        `json:"project"`
	Inputs      []MergeTarget `json:"inputs"`
}
type MergeReview struct {
	Task     string `json:"task"`
	Proposal string `json:"proposal_id"`
	Digest   string `json:"result_digest"`
	Receipt  string `json:"receipt_id"`
}

// Captured source metadata describes what was known at capture time. It does
// not claim that mutable P2 metadata was the provenance of an older revision.
type MergeInput struct {
	Memory       string       `json:"memory"`
	Revision     int64        `json:"revision"`
	Title        string       `json:"title"`
	Text         string       `json:"text"`
	Source       string       `json:"source"`
	SourceDetail *AgentSource `json:"source_detail,omitempty"`
	CapturedAt   string       `json:"captured_at"`
}
type MergeSnapshot struct {
	Schema  string       `json:"schema"`
	Task    string       `json:"task"`
	Attempt int64        `json:"attempt"`
	Project string       `json:"project"`
	Inputs  []MergeInput `json:"inputs"`
	Digest  string       `json:"input_digest"`
}
type MergeSource struct {
	Memory   string `json:"memory"`
	Revision int64  `json:"revision"`
	Quote    string `json:"quote"`
}
type MergeProposal struct {
	Schema  string        `json:"schema"`
	Task    string        `json:"task"`
	Attempt int64         `json:"attempt"`
	Digest  string        `json:"input_digest"`
	ID      string        `json:"proposal_id"`
	Action  string        `json:"action"`
	Title   string        `json:"title"`
	Text    string        `json:"text"`
	Sources []MergeSource `json:"sources"`
}
type MergeReceipt struct {
	ID       string `json:"receipt_id"`
	Proposal string `json:"proposal_id"`
	Digest   string `json:"result_digest"`
	Memory   string `json:"memory"`
	Revision int64  `json:"revision"`
}
type MergeTask struct {
	ID           string          `json:"task"`
	Project      string          `json:"project"`
	State        string          `json:"state"`
	Attempt      int64           `json:"attempt"`
	ReviewState  string          `json:"review_state"`
	Cancelled    bool            `json:"cancel_requested"`
	Error        string          `json:"error_code,omitempty"`
	Snapshot     MergeSnapshot   `json:"snapshot"`
	Proposal     *MergeProposal  `json:"proposal,omitempty"`
	ResultDigest string          `json:"result_digest,omitempty"`
	Receipt      *MergeReceipt   `json:"receipt,omitempty"`
	Execution    json.RawMessage `json:"execution,omitempty"`
}

// List entries contain only task summaries. Frozen inputs are fetched on
// selection, keeping both IPC size and background polling bounded.
type MergeListQuery struct {
	IncludeIndexRecovery bool   `json:"include_index_recovery,omitempty"`
	ActiveOnly           bool   `json:"active_only,omitempty"`
	Memory               string `json:"memory,omitempty"`
	Project              string `json:"project,omitempty"`
	Before               string `json:"before,omitempty"`
	Limit                int    `json:"limit,omitempty"`
}
type MergeSummary struct {
	ID          string `json:"task"`
	Project     string `json:"project"`
	State       string `json:"state"`
	ReviewState string `json:"review_state"`
	Title       string `json:"title"`
	CreatedAt   string `json:"created_at"`
}
type MergePage struct {
	Items []MergeSummary `json:"items"`
	Next  string         `json:"next,omitempty"`
}

func MergeDigest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (s MergeSnapshot) ProposalID() string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("merge-v1:"+s.Task+":"+s.Digest)).String()
}
func (s *MergeSnapshot) Seal() error {
	if s.Schema != MergeSchema || s.Attempt < 1 || len(s.Inputs) != 2 {
		return MergeError("invalid_proposal")
	}
	for _, id := range []string{s.Task, s.Project} {
		if _, err := uuid.Parse(id); err != nil {
			return MergeError("invalid_proposal")
		}
	}
	for i, v := range s.Inputs {
		if _, err := uuid.Parse(v.Memory); err != nil {
			return MergeError("invalid_proposal")
		}
		if v.Revision < 1 || strings.TrimSpace(v.Text) == "" || len(v.Text) > MergeInputLimit || v.CapturedAt == "" || (i == 1 && v.Memory == s.Inputs[0].Memory) {
			return MergeError("invalid_proposal")
		}
	}
	s.Digest = ""
	s.Digest = MergeDigest(*s)
	return nil
}
func (s MergeSnapshot) Decode(raw []byte) (MergeProposal, error) {
	var p MergeProposal
	invalid := MergeError("invalid_proposal")
	if len(raw) > MergeOutputLimit {
		return p, invalid
	}
	var schema jsonschema.Schema
	if json.Unmarshal(s.OutputSchema(), &schema) != nil {
		return p, invalid
	}
	r, err := schema.Resolve(nil)
	var value any
	if err != nil || json.Unmarshal(raw, &value) != nil || r.Validate(value) != nil {
		return p, invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil || d.Decode(new(any)) != io.EOF {
		return p, invalid
	}
	check := s
	if check.Seal() != nil || check.Digest != s.Digest || p.Schema != s.Schema || p.Task != s.Task || p.Attempt != s.Attempt || p.Digest != s.Digest || p.ID != s.ProposalID() || p.Action != "merge" || len(p.Sources) != 2 || strings.TrimSpace(p.Title) == "" || len(p.Title) > 512 || strings.TrimSpace(p.Text) == "" || len(p.Text) > MergeInputLimit {
		return p, invalid
	}
	for i, ref := range p.Sources {
		v := s.Inputs[i]
		if ref.Memory != v.Memory || ref.Revision != v.Revision || strings.TrimSpace(ref.Quote) == "" || !strings.Contains(v.Text, ref.Quote) {
			return p, invalid
		}
	}
	return p, nil
}
func (s MergeSnapshot) OutputSchema() []byte {
	str := func(v string) any { return map[string]any{"type": "string", "const": v} }
	source := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"memory", "revision", "quote"}, "properties": map[string]any{"memory": map[string]any{"type": "string"}, "revision": map[string]any{"type": "integer"}, "quote": map[string]any{"type": "string"}}}
	b, _ := json.Marshal(map[string]any{"type": "object", "additionalProperties": false, "required": []string{"schema", "task", "attempt", "input_digest", "proposal_id", "action", "title", "text", "sources"}, "properties": map[string]any{"schema": str(s.Schema), "task": str(s.Task), "attempt": map[string]any{"type": "integer", "const": s.Attempt}, "input_digest": str(s.Digest), "proposal_id": str(s.ProposalID()), "action": str("merge"), "title": map[string]any{"type": "string"}, "text": map[string]any{"type": "string"}, "sources": map[string]any{"type": "array", "minItems": 2, "maxItems": 2, "items": source}}})
	return b
}
