package records

// Experimental P3 contract. This is deliberately absent from IPC/MCP and from
// automatic migrations. Limits and action vocabulary are experiment choices.
import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
)

const P3ExperimentSchema = "memlane-p3-experiment-v1"

var ErrP3Invalid = errors.New("p3_experiment_invalid_output")
var ErrP3Cancelled = errors.New("p3_experiment_cancelled_or_old_attempt")
var ErrP3Expired = errors.New("p3_experiment_proposal_expired")

type P3Snapshot struct {
	Schema  string    `json:"schema"`
	Task    string    `json:"task"`
	Attempt int64     `json:"attempt"`
	Dataset string    `json:"dataset"`
	Inputs  []Version `json:"inputs"`
	Digest  string    `json:"input_digest"`
}

type P3Source struct {
	Memory   string `json:"memory"`
	Revision int64  `json:"revision"`
	Quote    string `json:"quote"`
}

type P3Proposal struct {
	Schema  string     `json:"schema"`
	Task    string     `json:"task"`
	Attempt int64      `json:"attempt"`
	Digest  string     `json:"input_digest"`
	ID      string     `json:"proposal_id"`
	Action  string     `json:"action"`
	Title   string     `json:"title"`
	Text    string     `json:"text"`
	Sources []P3Source `json:"sources"`
}

func (s P3Snapshot) ProposalID() string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(s.Task+":"+s.Digest)).String()
}

func (s *P3Snapshot) Seal() error {
	if s.Schema != P3ExperimentSchema || s.Attempt < 1 || len(s.Inputs) != 2 {
		return ErrP3Invalid
	}
	if _, err := uuid.Parse(s.Task); err != nil {
		return ErrP3Invalid
	}
	if _, err := uuid.Parse(s.Dataset); err != nil {
		return ErrP3Invalid
	}
	seen := map[string]bool{}
	for _, v := range s.Inputs {
		if _, err := uuid.Parse(v.Memory); err != nil {
			return ErrP3Invalid
		}
		if seen[v.Memory] || v.Dataset != s.Dataset || v.Revision < 1 || v.State != "active" || strings.TrimSpace(v.Text) == "" || len(v.Text) > 16384 {
			return ErrP3Invalid
		}
		seen[v.Memory] = true
	}
	s.Digest = ""
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	h := sha256.Sum256(b)
	s.Digest = hex.EncodeToString(h[:])
	return nil
}

// Decode also checks grounding and host bindings; JSON Schema alone cannot do
// this. Source text is data, never a grant of tools or permission to apply.
func (s P3Snapshot) Decode(data []byte) (P3Proposal, error) {
	var p P3Proposal
	if len(data) > 32768 {
		return p, ErrP3Invalid
	}
	var schema jsonschema.Schema
	if json.Unmarshal(s.OutputSchema(), &schema) != nil {
		return p, ErrP3Invalid
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return p, ErrP3Invalid
	}
	var value any
	if json.Unmarshal(data, &value) != nil || resolved.Validate(value) != nil {
		return p, ErrP3Invalid
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return p, ErrP3Invalid
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return p, ErrP3Invalid
	}
	check := s
	if check.Seal() != nil || check.Digest != s.Digest || p.Schema != s.Schema || p.Task != s.Task || p.Attempt != s.Attempt || p.Digest != s.Digest || p.ID != s.ProposalID() || p.Action != "propose_merge" || len(p.Sources) != 2 || len(p.Title) > 512 || strings.TrimSpace(p.Title) == "" || strings.TrimSpace(p.Text) == "" || len(p.Text) > 16384 {
		return p, ErrP3Invalid
	}
	for i, ref := range p.Sources {
		v := s.Inputs[i]
		if ref.Memory != v.Memory || ref.Revision != v.Revision || strings.TrimSpace(ref.Quote) == "" || !strings.Contains(v.Text, ref.Quote) {
			return p, ErrP3Invalid
		}
	}
	return p, nil
}

// OutputSchema binds a single merge suggestion to the host-owned snapshot.
func (s P3Snapshot) OutputSchema() []byte {
	str := func(value string) map[string]any { return map[string]any{"type": "string", "const": value} }
	source := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"memory", "revision", "quote"}, "properties": map[string]any{"memory": map[string]any{"type": "string"}, "revision": map[string]any{"type": "integer"}, "quote": map[string]any{"type": "string"}}}
	v := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"schema", "task", "attempt", "input_digest", "proposal_id", "action", "title", "text", "sources"}, "properties": map[string]any{
		"schema": str(s.Schema), "task": str(s.Task), "attempt": map[string]any{"type": "integer", "const": s.Attempt}, "input_digest": str(s.Digest), "proposal_id": str(s.ProposalID()), "action": str("propose_merge"), "title": map[string]any{"type": "string"}, "text": map[string]any{"type": "string"}, "sources": map[string]any{"type": "array", "minItems": 2, "maxItems": 2, "items": source},
	}}
	b, _ := json.Marshal(v)
	return b
}
