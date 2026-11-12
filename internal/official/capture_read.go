package official

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/zwhu/memlane/internal/records"
	"net/url"
)

// ReadSavedCapture reads one known operation from the principal's own cache.
// Reconstructing the pre-marker payload lets the metadata-only client verify
// actual content against its committed digest without a transcript mirror.
func (r SessionReader) ReadSavedCapture(ctx context.Context, saved records.OfficialCaptureDelivery) (string, json.RawMessage, error) {
	fail := errors.New("saved official capture content could not be verified")
	if saved.State != "saved" || saved.Instance != r.Instance || saved.Dataset != r.Dataset || !canonicalSessionScope(saved.OfficialID) || !captureOperation.MatchString(saved.Operation) || !captureSession.MatchString(saved.Session) || (saved.Kind != "qa" && saved.Kind != "trace") {
		return "", nil, fail
	}
	if err := r.verify(ctx); err != nil {
		return "", nil, err
	}
	var response struct {
		Instance  string          `json:"instance"`
		Dataset   string          `json:"dataset"`
		Session   string          `json:"session"`
		User      string          `json:"user"`
		Operation string          `json:"operation"`
		Kind      string          `json:"kind"`
		State     string          `json:"state"`
		ID        string          `json:"entry_id"`
		Entry     json.RawMessage `json:"entry"`
	}
	path := "/api/v1/memlane/datasets/" + r.Dataset + "/capture-receipts/" + url.PathEscape(saved.Session) + "/" + saved.Kind + "/" + saved.Operation + "?expected_instance=" + r.Instance
	if err := Request(ctx, r.Endpoint, path, "GET", r.Token, "", nil, &response); err != nil {
		return "", nil, err
	}
	if response.Instance != r.Instance || response.Dataset != r.Dataset || response.Session != saved.Session || !canonicalSessionScope(response.User) || response.Operation != saved.Operation || response.Kind != saved.Kind {
		return "", nil, fail
	}
	if response.State == "unknown" && response.ID == "" && len(response.Entry) == 0 {
		return "", nil, &HTTPError{Status: 404, Path: path}
	}
	if response.State != "saved" || response.ID != saved.OfficialID {
		return "", nil, fail
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(response.Entry, &fields) != nil {
		return "", nil, fail
	}
	key := "context"
	if saved.Kind == "trace" {
		key = "memory_context"
	}
	var text string
	var marker captureMarker
	if json.Unmarshal(fields[key], &text) != nil || json.Unmarshal([]byte(text), &marker) != nil || marker.Origin != "memlane_pi_capture_v1" || marker.Operation != saved.Operation || marker.Digest != saved.PayloadDigest {
		return "", nil, fail
	}
	fields[key], _ = json.Marshal(string(marker.Source))
	original, err := json.Marshal(fields)
	if err != nil {
		return "", nil, fail
	}
	identity, expected, err := prepareCapture(CaptureEntry{Operation: saved.Operation, Session: saved.Session, Entry: original})
	if err != nil || identity.Kind != saved.Kind || identity.Digest != saved.PayloadDigest || !captureJSONEqual(expected, response.Entry) {
		return "", nil, fail
	}
	if err = r.verify(ctx); err != nil {
		return "", nil, err
	}
	return response.User, response.Entry, nil
}
