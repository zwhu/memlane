package records

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
)

// OrganizationSchema adds the automatic summary/tag slice independently of
// the already accepted two-source merge contract. Approval is never model data.
const OrganizationSchema = "memlane-organization-v1"
const OrganizationReviewSchema = "memlane-organization-v2"
const OrganizationGlobalSchema = "memlane-organization-v3"
const OrganizationInputLimit = 128 * 1024
const OrganizationOutputLimit = 256 * 1024

type OrganizationStart struct {
	Processing   *ProcessingTaskAuthorization `json:"processing,omitempty"`
	Global       bool                         `json:"global_candidates,omitempty"`
	Purpose      string                       `json:"purpose,omitempty"`
	Review       bool                         `json:"review,omitempty"`
	Destinations []string                     `json:"destinations,omitempty"`
	OperationID  string                       `json:"operation_id"`
	Project      string                       `json:"project"`
	Inputs       []MergeTarget                `json:"inputs"`
}
type OrganizationInput struct {
	MergeInput
	Origin string `json:"origin"`
	// Evidence roots are resolved by the trusted service from persisted lineage.
	// A derived summary does not invent an additional independent source.
	Roots []MergeTarget `json:"evidence_roots"`
}
type OrganizationSnapshot struct {
	Processing   *ProcessingSnapshot `json:"processing,omitempty"`
	Purpose      string              `json:"purpose,omitempty"`
	Destinations []Scope             `json:"destinations,omitempty"`
	Schema       string              `json:"schema"`
	Task         string              `json:"task"`
	Attempt      int64               `json:"attempt"`
	Project      string              `json:"project"`
	Inputs       []OrganizationInput `json:"inputs"`
	Digest       string              `json:"input_digest"`
}
type OrganizationItem struct {
	Conditions  string        `json:"conditions,omitempty"`
	Reason      string        `json:"reason,omitempty"`
	Destination string        `json:"destination,omitempty"`
	Index       int           `json:"index"`
	Action      string        `json:"action"`
	Title       string        `json:"title"`
	Text        string        `json:"text"`
	Target      string        `json:"target"`
	Tags        []string      `json:"tags"`
	Sources     []MergeSource `json:"sources"`
}
type OrganizationResult struct {
	Schema      string             `json:"schema"`
	Task        string             `json:"task"`
	Attempt     int64              `json:"attempt"`
	Digest      string             `json:"input_digest"`
	ID          string             `json:"result_id"`
	Items       []OrganizationItem `json:"items"`
	Explanation string             `json:"explanation"`
}

func (s OrganizationSnapshot) ProposalID() string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("organization-v1:"+s.Task+":"+s.Digest)).String()
}
func (s OrganizationSnapshot) ItemID(index int) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("organization-v1-item:"+s.Task+":"+stringID(index))).String()
}
func stringID(index int) string {
	// Decimal indices avoid lossy rune conversions and ambiguous receipt names.
	b, _ := json.Marshal(index)
	return string(b)
}
func (s *OrganizationSnapshot) Seal() error {
	invalid := MergeError("invalid_proposal")
	if (s.Schema != OrganizationSchema && s.Schema != OrganizationReviewSchema && s.Schema != OrganizationGlobalSchema) || s.Attempt < 1 || len(s.Inputs) < 1 || len(s.Inputs) > 16 {
		return invalid
	}
	for _, id := range []string{s.Task, s.Project} {
		if _, err := uuid.Parse(id); err != nil {
			return invalid
		}
	}
	if len(s.Destinations) > 9 || (s.Schema != OrganizationGlobalSchema && len(s.Destinations) > 8) || (s.Schema == OrganizationSchema && len(s.Destinations) > 0) {
		return invalid
	}
	if s.Purpose != "" && (s.Purpose != "global" || s.Schema != OrganizationGlobalSchema || len(s.Inputs) != 1) {
		return invalid
	}
	if p := s.Processing; p != nil {
		if s.Schema != OrganizationGlobalSchema || s.Purpose != "global" || p.SelectionGeneration < 1 || p.FrozenMS <= 0 || len(p.DependencyDigest) != 64 || p.Revision.Digest != MergeDigest(p.Revision.Spec) || p.Revision.Validation == nil || p.Revision.Validation.State != "succeeded" || p.Revision.Validation.CancelRequested || p.Revision.Validation.DependencyDigest != p.DependencyDigest {
			return invalid
		}
		if _, err := uuid.Parse(p.Revision.ID); err != nil {
			return invalid
		}
	}
	destinations := map[string]bool{}
	globals := 0
	for _, d := range s.Destinations {
		allowed := d.Kind == "project" && d.Path != ""
		if s.Schema == OrganizationGlobalSchema && d.Kind == "experience" {
			allowed = true
			globals++
		}
		if _, err := uuid.Parse(d.ID); err != nil || d.ID == s.Project || !allowed || destinations[d.ID] {
			return invalid
		}
		destinations[d.ID] = true
	}
	if s.Schema == OrganizationGlobalSchema && globals != 1 {
		return invalid
	}
	seen := map[string]bool{}
	size := 0
	for _, v := range s.Inputs {
		if _, err := uuid.Parse(v.Memory); err != nil {
			return invalid
		}
		if seen[v.Memory] || v.Revision < 1 || strings.TrimSpace(v.Text) == "" || len(v.Text) > MergeInputLimit || v.CapturedAt == "" || len(v.Title) > 512 || !utf8.ValidString(v.Text) {
			return invalid
		}
		seen[v.Memory] = true
		if v.Origin != "knowledge" && v.Origin != "raw" && v.Origin != "imported" && v.Origin != "summary" {
			return invalid
		}
		if len(v.Roots) == 0 || len(v.Roots) > 256 {
			return invalid
		}
		roots := map[string]bool{}
		for _, root := range v.Roots {
			if _, err := uuid.Parse(root.Memory); err != nil {
				return invalid
			}
			key := root.Memory + ":" + stringID64(root.Revision)
			if root.Revision < 1 || roots[key] {
				return invalid
			}
			roots[key] = true
		}
		raw, _ := json.Marshal(v)
		size += len(raw)
	}
	if size > OrganizationInputLimit {
		return invalid
	}
	s.Digest = ""
	s.Digest = MergeDigest(*s)
	return nil
}
func stringID64(v int64) string { b, _ := json.Marshal(v); return string(b) }
func (s OrganizationSnapshot) Decode(raw []byte) (OrganizationResult, error) {
	var out OrganizationResult
	invalid := MergeError("invalid_proposal")
	if len(raw) > OrganizationOutputLimit {
		return out, invalid
	}
	var schema jsonschema.Schema
	if json.Unmarshal(s.OutputSchema(), &schema) != nil {
		return out, invalid
	}
	resolved, err := schema.Resolve(nil)
	var value any
	if err != nil || json.Unmarshal(raw, &value) != nil || resolved.Validate(value) != nil {
		return out, invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&out) != nil || decoder.Decode(new(any)) != io.EOF {
		return out, invalid
	}
	check := s
	if check.Seal() != nil || check.Digest != s.Digest || out.Schema != s.Schema || out.Task != s.Task || out.Attempt != s.Attempt || out.Digest != s.Digest || out.ID != s.ProposalID() || len(out.Items) > 32 || len(out.Explanation) > 4096 || (len(out.Items) == 0 && strings.TrimSpace(out.Explanation) == "") {
		return out, invalid
	}
	inputs := map[string]OrganizationInput{}
	for _, v := range s.Inputs {
		inputs[v.Memory] = v
	}
	for i, item := range out.Items {
		if item.Index != i+1 || len(item.Sources) < 1 || len(item.Sources) > 16 {
			return out, invalid
		}
		cited := map[string]bool{}
		for _, ref := range item.Sources {
			v, ok := inputs[ref.Memory]
			if !ok || cited[ref.Memory] || ref.Revision != v.Revision || strings.TrimSpace(ref.Quote) == "" || !strings.Contains(v.Text, ref.Quote) {
				return out, invalid
			}
			cited[ref.Memory] = true
		}
		if s.Schema == OrganizationSchema && (item.Reason != "" || item.Destination != "") {
			return out, invalid
		}
		if s.Schema != OrganizationSchema && (strings.TrimSpace(item.Reason) == "" || len(item.Reason) > 4096) {
			return out, invalid
		}
		if item.Action != "move" && item.Action != "global_experience" && item.Destination != "" {
			return out, invalid
		}
		if item.Action != "global_experience" && item.Conditions != "" {
			return out, invalid
		}
		if s.Purpose == "global" && item.Action != "global_experience" {
			return out, invalid
		}
		switch item.Action {
		case "summary":
			if item.Target != "" || len(item.Tags) != 0 || strings.TrimSpace(item.Title) == "" || len(item.Title) > 512 || strings.TrimSpace(item.Text) == "" || len(item.Text) > MergeInputLimit {
				return out, invalid
			}
		case "add_tags":
			if !cited[item.Target] || item.Text != "" || item.Title != "" || len(item.Tags) < 1 || len(item.Tags) > 8 {
				return out, invalid
			}
			tags := map[string]bool{}
			for _, tag := range item.Tags {
				if strings.TrimSpace(tag) != tag || tag == "" || utf8.RuneCountInString(tag) > 32 || !utf8.ValidString(tag) || strings.ContainsAny(tag, "\n\r\t") || tags[tag] {
					return out, invalid
				}
				tags[tag] = true
			}
		case "merge", "overwrite", "delete", "move", "duplicate", "conflict":
			if s.Schema == OrganizationSchema || len(item.Tags) != 0 {
				return out, invalid
			}
			if item.Action == "merge" || item.Action == "duplicate" || item.Action == "conflict" {
				if len(item.Sources) < 2 || item.Target != "" {
					return out, invalid
				}
			} else if !cited[item.Target] {
				return out, invalid
			}
			if item.Action == "merge" || item.Action == "overwrite" {
				if strings.TrimSpace(item.Title) == "" || len(item.Title) > 512 || strings.TrimSpace(item.Text) == "" || len(item.Text) > MergeInputLimit {
					return out, invalid
				}
			} else if item.Title != "" || item.Text != "" {
				return out, invalid
			}
			if item.Action == "merge" {
				for _, ref := range item.Sources {
					if inputs[ref.Memory].Origin == "raw" {
						return out, invalid
					}
				}
			}
			if item.Target != "" && inputs[item.Target].Origin == "raw" {
				return out, invalid
			}
			if item.Action == "move" {
				allowed := false
				for _, d := range s.Destinations {
					if item.Destination == d.ID && d.Kind == "project" {
						allowed = true
					}
				}
				if !allowed {
					return out, invalid
				}
			}
		case "global_experience":
			if s.Schema != OrganizationGlobalSchema || item.Target != "" || len(item.Tags) != 0 || strings.TrimSpace(item.Conditions) == "" || len(item.Conditions) > 4096 || strings.TrimSpace(item.Title) == "" || len(item.Title) > 512 || strings.TrimSpace(item.Text) == "" || len(item.Text) > MergeInputLimit {
				return out, invalid
			}
			allowed := false
			for _, d := range s.Destinations {
				if d.Kind == "experience" && d.ID == item.Destination {
					allowed = true
				}
			}
			if !allowed {
				return out, invalid
			}
		default:
			return out, invalid
		}
	}
	return out, nil
}
func (s OrganizationSnapshot) OutputSchema() []byte {
	str := func(v string) any { return map[string]any{"type": "string", "const": v} }
	properties := map[string]any{
		"index":  map[string]any{"type": "integer", "minimum": 1, "maximum": 32},
		"action": map[string]any{"type": "string", "enum": []string{"summary", "add_tags"}},
		"title":  map[string]any{"type": "string"}, "text": map[string]any{"type": "string"}, "target": map[string]any{"type": "string"},
		"tags":    map[string]any{"type": "array", "maxItems": 8, "items": map[string]any{"type": "string"}},
		"sources": map[string]any{"type": "array", "minItems": 1, "maxItems": 16, "items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"memory", "revision", "quote"}, "properties": map[string]any{"memory": map[string]any{"type": "string"}, "revision": map[string]any{"type": "integer"}, "quote": map[string]any{"type": "string"}}}},
	}
	required := []string{"index", "action", "title", "text", "target", "tags", "sources"}
	if s.Schema == OrganizationReviewSchema || s.Schema == OrganizationGlobalSchema {
		properties["action"] = map[string]any{"type": "string", "enum": []string{"summary", "add_tags", "merge", "overwrite", "delete", "move", "duplicate", "conflict"}}
		properties["reason"] = map[string]any{"type": "string"}
		properties["destination"] = map[string]any{"type": "string"}
		required = append(required, "reason", "destination")
	}
	if s.Schema == OrganizationGlobalSchema {
		actions := []string{"summary", "add_tags", "merge", "overwrite", "delete", "move", "duplicate", "conflict", "global_experience"}
		if s.Purpose == "global" {
			actions = []string{"global_experience"}
		}
		properties["action"] = map[string]any{"type": "string", "enum": actions}
		properties["conditions"] = map[string]any{"type": "string"}
		required = append(required, "conditions")
	}
	b, _ := json.Marshal(map[string]any{"type": "object", "additionalProperties": false, "required": []string{"schema", "task", "attempt", "input_digest", "result_id", "items", "explanation"}, "properties": map[string]any{
		"schema": str(s.Schema), "task": str(s.Task), "attempt": map[string]any{"type": "integer", "const": s.Attempt}, "input_digest": str(s.Digest), "result_id": str(s.ProposalID()), "explanation": map[string]any{"type": "string"},
		"items": map[string]any{"type": "array", "maxItems": 32, "items": map[string]any{"type": "object", "additionalProperties": false, "required": required, "properties": properties}},
	}})
	return b
}

// Receipts describe committed facts independently of task and index state.
type OrganizationReceipt struct {
	ID               string                `json:"receipt_id"`
	Memory           string                `json:"memory"`
	Revision         int64                 `json:"revision"`
	AddedTags        []string              `json:"added_tags,omitempty"`
	EditedExperience *ExperienceReviewEdit `json:"edited_experience,omitempty"`
}
type OrganizationEntry struct {
	ID      string               `json:"item_id"`
	Item    OrganizationItem     `json:"item"`
	State   string               `json:"state"`
	Error   string               `json:"error_code,omitempty"`
	Receipt *OrganizationReceipt `json:"receipt,omitempty"`
}
type OrganizationTask struct {
	Attempts    []OrganizationAttempt `json:"attempts,omitempty"`
	Recovery    *OrganizationRecovery `json:"recovery,omitempty"`
	AttemptKind string                `json:"attempt_kind,omitempty"`
	ID          string                `json:"task"`
	Project     string                `json:"project"`
	State       string                `json:"state"`
	Attempt     int64                 `json:"attempt"`
	Cancelled   bool                  `json:"cancel_requested"`
	Error       string                `json:"error_code,omitempty"`
	Snapshot    OrganizationSnapshot  `json:"snapshot"`
	Result      *OrganizationResult   `json:"result,omitempty"`
	Entries     []OrganizationEntry   `json:"entries"`
	Execution   json.RawMessage       `json:"execution,omitempty"`
	CreatedAt   string                `json:"created_at"`
}

type OrganizationAttempt struct {
	Attempt   int64           `json:"attempt"`
	Kind      string          `json:"kind"`
	State     string          `json:"state"`
	Execution json.RawMessage `json:"execution,omitempty"`
}

// Recovery is a receipt of the current policy/budget, not an adoption or a
// claim that another model call has already run.
type OrganizationRecovery struct {
	State      string `json:"state"`
	ExtraLimit int    `json:"extra_limit"`
	Used       int    `json:"used"`
	DeadlineMS int64  `json:"deadline_ms"`
	NextMS     int64  `json:"next_ms"`
	Reason     string `json:"reason,omitempty"`
}
type OrganizationResume struct {
	Task            string `json:"task"`
	OperationID     string `json:"operation_id"`
	ExpectedAttempt int64  `json:"expected_attempt"`
}
type DerivedInfo struct {
	Kind  string `json:"kind"`
	State string `json:"state"`
}
type OrganizationSource struct {
	CurrentRevision int64             `json:"current_revision"`
	Changed         bool              `json:"source_changed"`
	Memory          string            `json:"source_memory"`
	Revision        int64             `json:"source_revision"`
	Project         string            `json:"project"`
	Input           OrganizationInput `json:"snapshot"`
	Quote           string            `json:"quote"`
}

type OrganizationSummary struct {
	ID        string `json:"task"`
	Project   string `json:"project"`
	State     string `json:"state"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
}
type OrganizationPage struct {
	Items []OrganizationSummary `json:"items"`
	Next  string                `json:"next,omitempty"`
}

func (i OrganizationItem) RequiresReview() bool {
	return i.Action != "summary" && i.Action != "add_tags"
}

type OrganizationReview struct {
	Task    string                `json:"task"`
	Item    string                `json:"item_id"`
	Digest  string                `json:"item_digest"`
	Receipt string                `json:"receipt_id"`
	Confirm bool                  `json:"confirm"`
	Edit    *ExperienceReviewEdit `json:"edited_experience,omitempty"`
}

// The original model item and its evidence remain immutable. Only the three
// user-authored preview fields can become the separately confirmed knowledge.
type ExperienceReviewEdit struct {
	Title      string `json:"title"`
	Text       string `json:"text"`
	Conditions string `json:"conditions"`
}

func (v ExperienceReviewEdit) Valid() bool {
	return strings.TrimSpace(v.Title) != "" && strings.TrimSpace(v.Text) != "" && strings.TrimSpace(v.Conditions) != "" && len(v.Title) <= 512 && len(v.Text) <= MergeInputLimit && len(v.Conditions) <= 4096 && utf8.ValidString(v.Title) && utf8.ValidString(v.Text) && utf8.ValidString(v.Conditions)
}

// Structured-output providers require all declared v2 keys. Keep their empty
// values in model/result JSON while preserving v1's established wire bytes.
func (r OrganizationResult) MarshalJSON() ([]byte, error) {
	type plain OrganizationResult
	raw, err := json.Marshal(plain(r))
	if err != nil || r.Schema != OrganizationReviewSchema && r.Schema != OrganizationGlobalSchema {
		return raw, err
	}
	var value map[string]any
	if err = json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	if items, ok := value["items"].([]any); ok {
		for i, v := range items {
			item := v.(map[string]any)
			item["reason"] = r.Items[i].Reason
			item["destination"] = r.Items[i].Destination
			if r.Schema == OrganizationGlobalSchema {
				item["conditions"] = r.Items[i].Conditions
			}
		}
	}
	return json.Marshal(value)
}

// Optional preview fields preserve replay of requests from older clients.
type ExperienceCopy struct {
	OperationID string  `json:"operation_id"`
	Project     string  `json:"project"`
	Memory      string  `json:"memory"`
	Revision    int64   `json:"revision"`
	Title       *string `json:"title,omitempty"`
	Conditions  *string `json:"conditions,omitempty"`
}
type ExperienceInfo struct {
	Kind       string `json:"kind"`
	Conditions string `json:"conditions"`
}
