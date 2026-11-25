package domain

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureReceiptLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	r := CaptureReceipt{CaptureKey: "key", UserID: "user", DatasetID: "dataset", SessionID: "session",
		MessageID: "message", PayloadSHA256: "digest", EntryType: "qa", BaselineCount: new(int)}
	reserve := func(want, receiptStatus, entryID string) {
		t.Helper()
		state, got, err := s.ReserveCaptureReceipt(ctx, r)
		must(t, err)
		if state != want || got.Status != receiptStatus || got.EntryID != entryID || got.BaselineCount == nil || *got.BaselineCount != 0 {
			t.Fatalf("reservation = %q, %+v", state, got)
		}
	}
	reserve("reserved", "pending", "")
	reserve("pending", "pending", "")
	for _, changed := range []CaptureReceipt{
		{CaptureKey: "key", UserID: "user", DatasetID: "dataset", SessionID: "session", MessageID: "message", PayloadSHA256: "different", EntryType: "qa"},
		{CaptureKey: "key", UserID: "user", DatasetID: "different", SessionID: "session", MessageID: "message", PayloadSHA256: "digest", EntryType: "qa"},
	} {
		_, _, err := s.ReserveCaptureReceipt(ctx, changed)
		wantError(t, err, ErrConflict)
	}
	must(t, s.AbortPendingCaptureReceipt(ctx, "key"))
	_, err := s.GetCaptureReceipt(ctx, "key")
	wantError(t, err, ErrNotFound)
	reserve("reserved", "pending", "")
	must(t, s.MarkCaptureReceiptSaved(ctx, "key", "entry"))
	must(t, s.MarkCaptureReceiptSaved(ctx, "key", "entry"))
	wantError(t, s.MarkCaptureReceiptSaved(ctx, "key", "wrong"), ErrConflict)
	must(t, s.AbortPendingCaptureReceipt(ctx, "key"))
	reserve("saved", "saved", "entry")
}

func TestCaptureInboxFailureAndRetry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	receipt := CaptureReceipt{CaptureKey: "receipt", UserID: "u", DatasetID: "dataset", SessionID: "session",
		MessageID: "message", PayloadSHA256: "digest", EntryType: "qa"}
	_, _, err := s.ReserveCaptureReceipt(ctx, receipt)
	must(t, err)
	input := CaptureInboxItem{ID: "inbox", ProjectID: "project", DatasetID: "dataset", AgentID: "agent",
		SessionID: "session", MessageID: "message", CaptureKey: receipt.CaptureKey, Kind: "qa", PayloadJSON: `{"input":"text"}`}
	check := func(item CaptureInboxItem, err error, status string, attempts int, lastError string) {
		t.Helper()
		must(t, err)
		if item.ID != input.ID || item.CaptureKey != receipt.CaptureKey || item.Status != status || item.Attempts != attempts || item.LastError != lastError {
			t.Fatalf("inbox expected %s/%d/%s, got %+v", status, attempts, lastError, item)
		}
	}
	item, err := s.EnqueueInboxItem(ctx, input)
	check(item, err, "pending", 0, "")
	_, err = s.RetryInboxItem(ctx, item.ID)
	wantError(t, err, ErrConflict)
	item, err = s.ClaimNextInboxItem(ctx)
	check(item, err, "processing", 1, "")
	_, err = s.ClaimNextInboxItem(ctx)
	wantError(t, err, ErrNotFound)
	must(t, s.MarkInboxFailed(ctx, input.ID, "transient write failure"))
	item, err = s.GetInboxItem(ctx, input.ID)
	check(item, err, "failed", 1, "transient write failure")
	pending, err := s.GetCaptureReceipt(ctx, receipt.CaptureKey)
	must(t, err)
	if pending.Status != "pending" {
		t.Fatalf("receipt should still be pending, got %+v", pending)
	}
	item, err = s.RetryInboxItem(ctx, input.ID)
	check(item, err, "pending", 1, "")
	item, err = s.ClaimNextInboxItem(ctx)
	check(item, err, "processing", 2, "")
	must(t, s.MarkCaptureReceiptSaved(ctx, receipt.CaptureKey, "saved-document"))
	must(t, s.MarkInboxSucceeded(ctx, item.ID))
	item, err = s.GetInboxItem(ctx, input.ID)
	check(item, err, "succeeded", 2, "")
	_, err = s.RetryInboxItem(ctx, item.ID)
	wantError(t, err, ErrConflict)
}

func TestGenerationProfileParametersAndActivation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p := GenerationProfile{ProfileID: "ollama", Mode: "provider", Provider: "ollama", Model: "model", Endpoint: "http://127.0.0.1:11434"}
	first, err := s.PutGenerationProfile(ctx, p)
	must(t, err)
	state, err := s.GetGenerationState(ctx)
	must(t, err)
	if state.ActiveProfileID != "" || state.ActiveRevision != 0 {
		t.Fatalf("saving activated %+v", state)
	}
	must(t, s.SetActiveGeneration(ctx, first.ProfileID, first.Revision))
	p.Temperature = new(float64)
	second, err := s.PutGenerationProfile(ctx, p)
	must(t, err)
	state, err = s.GetGenerationState(ctx)
	must(t, err)
	if state.ActiveProfileID != first.ProfileID || state.ActiveRevision != first.Revision {
		t.Fatalf("saving revision changed active profile: %+v", state)
	}
	must(t, s.SetActiveGeneration(ctx, second.ProfileID, second.Revision))
	state, err = s.GetGenerationState(ctx)
	must(t, err)
	if state.ActiveProfileID != second.ProfileID || state.ActiveRevision != second.Revision || state.Revision != 2 {
		t.Fatalf("confirmed activation missing: %+v", state)
	}
	assertMigratedTemperature(t, s.DB(), "ollama", first.Revision, nil)
	assertMigratedTemperature(t, s.DB(), "ollama", second.Revision, new(float64))
	for _, saved := range []GenerationProfile{first, second} {
		p, err := s.GetGenerationProfile(ctx, saved.ProfileID, saved.Revision)
		must(t, err)
		data, err := json.Marshal(p)
		must(t, err)
		var fields map[string]json.RawMessage
		must(t, json.Unmarshal(data, &fields))
		value, has := fields["temperature"]
		if _, set := fields["max_output_tokens"]; set {
			t.Fatalf("nil output limit encoded: %s", data)
		}
		if _, set := fields["max_concurrent_requests"]; set {
			t.Fatalf("nil concurrency encoded: %s", data)
		}
		var decoded GenerationProfile
		must(t, json.Unmarshal(data, &decoded))
		if p.MaxOutputTokens != nil || p.MaxConcurrentRequests != nil ||
			(saved.Revision == first.Revision && (has || decoded.Temperature != nil)) ||
			(saved.Revision == second.Revision && (string(value) != "0" || decoded.Temperature == nil || *decoded.Temperature != 0)) {
			t.Fatalf("parameter round trip changed: db=%+v JSON=%s", p, data)
		}
	}
}

func TestGenerationValidationFreshnessAndSecretGuards(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	v := GenerationValidation{OperationID: "old", Digest: "digest", RegisteredRevision: 1, Status: "passed",
		PlainPassed: true, SchemaPassed: true, QualityPassed: true}
	must(t, s.RecordGenerationValidation(ctx, v))
	check := func(want bool) {
		t.Helper()
		got, err := s.IsGenerationDigestValidated(ctx, v.Digest)
		must(t, err)
		if got != want {
			t.Fatalf("digest validated = %v, want %v", got, want)
		}
	}
	check(true)
	v.OperationID, v.RegisteredRevision = "new", 2
	must(t, s.RecordGenerationValidation(ctx, v))
	v.OperationID, v.QualityPassed = "tie-failed", false
	must(t, s.RecordGenerationValidation(ctx, v))
	check(false)
	v.OperationID, v.RegisteredRevision, v.QualityPassed = "latest", 3, true
	must(t, s.RecordGenerationValidation(ctx, v))
	check(true)

	raw := `{"nested":[{"raw_api_key":"secret"}]}`
	v.PayloadJSON = raw
	wantError(t, s.RecordGenerationValidation(ctx, v), ErrForbidden)
	wantError(t, s.UpsertGenerationHandoff(ctx, GenerationHandoff{OperationID: "op", PayloadJSON: raw}), ErrForbidden)
	wantError(t, s.UpsertGenerationHandoff(ctx, GenerationHandoff{OperationID: "op", RuntimePayloadJSON: raw}), ErrForbidden)
	handoffs, err := s.ListGenerationHandoffs(ctx)
	must(t, err)
	if len(handoffs) != 0 {
		t.Fatalf("rejected handoff was stored: %+v", handoffs)
	}
	p := GenerationProfile{ProfileID: "hosted", Mode: "provider", Provider: "openai", Model: "model",
		Endpoint:      (&url.URL{Scheme: "https", Host: "example.test", User: url.UserPassword("api-key", "secret")}).String(),
		CredentialRef: "reference", CredentialVersion: "v1"}
	_, err = s.PutGenerationProfile(ctx, p)
	wantError(t, err, ErrInvalidArgument)
	p.Endpoint = "https://example.test"
	_, err = s.PutGenerationProfile(ctx, p)
	wantError(t, err, ErrForbidden)
	credentials := filepath.Join(filepath.Dir(s.Path()), "generation-credentials")
	must(t, os.Mkdir(credentials, 0o700))
	file := filepath.Join(credentials, "reference-v1")
	must(t, os.WriteFile(file, []byte("synthetic-key"), 0o644))
	_, err = s.PutGenerationProfile(ctx, p)
	wantError(t, err, ErrForbidden)
	must(t, os.Chmod(file, 0o600))
	_, err = s.PutGenerationProfile(ctx, p)
	must(t, err)
	checkFileMode(t, file, 0o600)
}
