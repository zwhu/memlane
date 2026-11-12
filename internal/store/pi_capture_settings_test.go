package store

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
	"testing"
)

func TestPiCaptureSettingsObservesWithoutEnablingAndKeepsReopenCAS(t *testing.T) {
	s, _, review, _, policy := piCaptureConsentFixture(t)
	ctx := context.Background()
	q := records.PiCaptureSettingsQuery{Connection: review.Binding.ConnectionID, Generation: review.Binding.Generation}
	rows, err := s.PiCaptureSettings(ctx, q, policy)
	mustMerge(t, err)
	if len(rows) != 1 || !rows[0].Ready || rows[0].Consent != nil || rows[0].PreviousKey != "" || rows[0].Saved != 0 {
		t.Fatal(rows)
	}
	var count int
	mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM pi_capture_consents`).Scan(&count))
	if count != 0 {
		t.Fatal("settings read enabled capture")
	}
	grant, err := s.ReviewPiCaptureConsent(ctx, review, policy)
	mustMerge(t, err)
	rows, err = s.PiCaptureSettings(ctx, q, policy)
	mustMerge(t, err)
	if rows[0].Consent == nil || !rows[0].Consent.Enabled || rows[0].PreviousKey != grant.Key {
		t.Fatal(rows)
	}
	mustMerge(t, s.DisablePiCaptureConsent(ctx, records.PiCaptureConsentDisable{Connection: q.Connection, Project: review.Binding.Project, ExpectedKey: grant.Key}))
	rows, err = s.PiCaptureSettings(ctx, q, policy)
	mustMerge(t, err)
	if rows[0].Consent == nil || rows[0].Consent.Enabled || rows[0].PreviousKey != grant.Key {
		t.Fatal("stopped consent/CAS identity lost", rows)
	}
	q.Generation++
	if _, err = s.PiCaptureSettings(ctx, q, policy); err == nil {
		t.Fatal("changed generation read old settings")
	}
}
