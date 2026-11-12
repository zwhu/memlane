package official

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
)

func captureJSONEqual(left, right json.RawMessage) bool {
	decode := func(raw json.RawMessage) ([]byte, error) {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		return json.Marshal(value)
	}
	a, err := decode(left)
	if err != nil {
		return false
	}
	b, err := decode(right)
	return err == nil && bytes.Equal(a, b)
}

// InspectCapture is read-only and owner-scoped in the official adapter. It
// compares the actual cached fields, not merely the stored operation marker.
// nil,nil means UNKNOWN (including eviction/missing rows), never safe to retry.
func (r SessionReader) InspectCapture(ctx context.Context, input CaptureEntry) (*CaptureReceipt, error) {
	identity, expected, err := prepareCapture(input)
	if err != nil {
		return nil, err
	}
	if err = r.verify(ctx); err != nil {
		return nil, err
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
	path := "/api/v1/memlane/datasets/" + r.Dataset + "/capture-receipts/" + url.PathEscape(input.Session) + "/" + identity.Kind + "/" + input.Operation + "?expected_instance=" + r.Instance
	if err = Request(ctx, r.Endpoint, path, "GET", r.Token, "", nil, &response); err != nil {
		return nil, err
	}
	if response.Instance != r.Instance || response.Dataset != r.Dataset || response.Session != input.Session || !canonicalSessionScope(response.User) || response.Operation != input.Operation || response.Kind != identity.Kind {
		return nil, errors.New("official capture lookup scope could not be verified")
	}
	if err = r.verify(ctx); err != nil {
		return nil, err
	}
	if response.State == "unknown" && response.ID == "" && len(response.Entry) == 0 {
		return nil, nil
	}
	if response.State != "saved" || !canonicalSessionScope(response.ID) || !captureJSONEqual(response.Entry, expected) {
		return nil, errors.New("official capture lookup content could not be verified")
	}
	identity.ID = response.ID
	return &identity, nil
}
