package official

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/zwhu/memlane/internal/records"
	"regexp"
	"unicode/utf8"
)

// CaptureEntry is transient post-policy input, never a queue/ledger payload.
// The caller owns fresh consent, host provenance and pre-truncation filtering.
type CaptureEntry = records.OfficialCaptureInput

type CaptureReceipt struct {
	Operation string `json:"operation"`
	Digest    string `json:"digest"`
	Session   string `json:"session"`
	Kind      string `json:"kind"`
	ID        string `json:"id"`
}

var captureOperation = regexp.MustCompile(`^[0-9a-f]{64}$`)
var captureSession = regexp.MustCompile(`^pi-[0-9a-f]{48}$`)

type captureMarker struct {
	Origin    string          `json:"origin"`
	Operation string          `json:"operation"`
	Digest    string          `json:"digest"`
	Source    json.RawMessage `json:"source"`
}

func captureString(fields map[string]json.RawMessage, key string, limit int, required bool) (string, error) {
	var value string
	raw, present := fields[key]
	if !present && !required {
		return "", nil
	}
	if json.Unmarshal(raw, &value) != nil || !utf8.ValidString(value) || len(value) > limit || (required && value == "") {
		return "", errors.New("bounded typed capture field required")
	}
	return value, nil
}

// prepareCapture validates only the supported QA/trace surface and embeds a
// stable operation marker in Cognee's own context. No client transcript copy.
func prepareCapture(input CaptureEntry) (CaptureReceipt, json.RawMessage, error) {
	var empty CaptureReceipt
	if !captureOperation.MatchString(input.Operation) || !captureSession.MatchString(input.Session) || len(input.Entry) > 128*1024 || !utf8.Valid(input.Entry) {
		return empty, nil, errors.New("bounded host capture identity required")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(input.Entry, &fields) != nil || fields == nil {
		return empty, nil, errors.New("typed capture object required")
	}
	kind, err := captureString(fields, "type", 5, true)
	if err != nil || (kind != "qa" && kind != "trace") {
		return empty, nil, errors.New("QA or trace capture required")
	}
	allowed := map[string]bool{"type": true}
	contextKey := "context"
	if kind == "qa" {
		for key, limit := range map[string]int{"question": 4000, "answer": 8000, "context": 4096} {
			allowed[key] = true
			if _, err = captureString(fields, key, limit, true); err != nil {
				return empty, nil, err
			}
		}
	} else {
		contextKey = "memory_context"
		for key, limit := range map[string]int{"origin_function": 256, "status": 7, "method_return_value": 8000, "error_message": 500, "memory_context": 4096} {
			allowed[key] = true
			if _, err = captureString(fields, key, limit, key != "error_message" && key != "method_return_value"); err != nil {
				return empty, nil, err
			}
		}
		var status string
		json.Unmarshal(fields["status"], &status)
		if status != "success" && status != "error" {
			return empty, nil, errors.New("actual tool status required")
		}
		allowed["method_params"], allowed["generate_feedback_with_llm"] = true, true
		var feedback bool
		if raw, ok := fields["generate_feedback_with_llm"]; !ok || json.Unmarshal(raw, &feedback) != nil || feedback || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return empty, nil, errors.New("capture cannot generate model feedback")
		}
		params, ok := fields["method_params"]
		var object map[string]json.RawMessage
		if !ok || len(params) > 16000 || json.Unmarshal(params, &object) != nil {
			return empty, nil, errors.New("bounded tool parameter object required")
		}
	}
	for key := range fields {
		if !allowed[key] {
			return empty, nil, errors.New("unsupported capture field")
		}
	}
	var sourceText string
	json.Unmarshal(fields[contextKey], &sourceText)
	var source map[string]json.RawMessage
	if json.Unmarshal([]byte(sourceText), &source) != nil || source == nil {
		return empty, nil, errors.New("host capture provenance required")
	}
	var origin string
	json.Unmarshal(source["origin"], &origin)
	if origin != "memlane_pi_capture_v1" {
		return empty, nil, errors.New("host capture provenance required")
	}
	// Canonical object encoding makes formatting-only transport changes replay
	// consistently; the digest binds the actual authorized payload and session.
	canonical, _ := json.Marshal(fields)
	identity, _ := json.Marshal([]any{input.Operation, input.Session, json.RawMessage(canonical)})
	sum := sha256.Sum256(identity)
	digest := hex.EncodeToString(sum[:])
	marker, _ := json.Marshal(captureMarker{Origin: "memlane_pi_capture_v1", Operation: input.Operation, Digest: digest, Source: json.RawMessage(sourceText)})
	if kind == "trace" && utf8.RuneCount(marker) > 1000 {
		return empty, nil, errors.New("official trace provenance exceeds cache limit")
	}
	fields[contextKey], _ = json.Marshal(string(marker))
	body, err := json.Marshal(fields)
	return CaptureReceipt{Operation: input.Operation, Digest: digest, Session: input.Session, Kind: kind}, body, err
}

// CaptureIdentity contains metadata only and is safe for the durable operation
// ledger. The body stays in the caller's bounded in-memory pending batch.
func CaptureIdentity(input CaptureEntry) (CaptureReceipt, error) {
	identity, _, err := prepareCapture(input)
	return identity, err
}

// Preflight is read-only. A broker can reject an unavailable environment
// before claiming dispatch; it does not guarantee the later POST's outcome.
func (r SessionReader) VerifyCaptureService(ctx context.Context) error {
	if !canonicalSessionScope(r.Instance) || !canonicalSessionScope(r.Dataset) || r.Token == "" {
		return errors.New("authorized capture service required")
	}
	var identity struct {
		Instance     string   `json:"instance"`
		Version      string   `json:"cognee_version"`
		Capabilities []string `json:"capabilities"`
	}
	if err := Request(ctx, r.Endpoint, "/api/v1/memlane/identity", "GET", r.Token, "", nil, &identity); err != nil {
		return err
	}
	compatible := false
	for _, capability := range identity.Capabilities {
		compatible = compatible || capability == "capture_receipt_v1"
	}
	if identity.Instance != r.Instance || identity.Version != "1.6.7" || !compatible {
		return errors.New("capture receipt component required")
	}
	return nil
}

// WriteCaptureOnce makes exactly one typed-entry POST. A caller MUST journal
// dispatch before calling and MUST NOT retry an uncertain result. Identity
// failure after POST also leaves the outcome unknown, rather than re-sending.
func (r SessionReader) WriteCaptureOnce(ctx context.Context, input CaptureEntry) (CaptureReceipt, error) {
	identity, entry, err := prepareCapture(input)
	if err != nil {
		return CaptureReceipt{}, err
	}
	if err = r.verify(ctx); err != nil {
		return CaptureReceipt{}, err
	}
	body, _ := json.Marshal(map[string]any{"dataset_id": r.Dataset, "session_id": input.Session, "self_improvement": false, "entry": entry})
	var result struct {
		Status   string   `json:"status"`
		Dataset  string   `json:"dataset_id"`
		Sessions []string `json:"session_ids"`
		Kind     string   `json:"entry_type"`
		ID       string   `json:"entry_id"`
		Error    string   `json:"error"`
	}
	if err = Request(ctx, r.Endpoint, "/api/v1/remember/entry", "POST", r.Token, "application/json", body, &result); err != nil {
		return CaptureReceipt{}, err
	}
	if result.Status != "session_stored" || result.Dataset != r.Dataset || len(result.Sessions) != 1 || result.Sessions[0] != input.Session || result.Kind != identity.Kind || !canonicalSessionScope(result.ID) || result.Error != "" {
		return CaptureReceipt{}, errors.New("official capture receipt could not be verified")
	}
	if err = r.verify(ctx); err != nil {
		return CaptureReceipt{}, err
	}
	identity.ID = result.ID
	return identity, nil
}
