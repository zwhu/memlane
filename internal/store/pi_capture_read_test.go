package store

import (
	"context"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"strings"
	"testing"
)

func TestCapturedDeliveriesRequireOriginalConsentAndCurrentConnection(t *testing.T) {
	s, _, q, _, policy := piCaptureConsentFixture(t)
	ctx := context.Background()
	if _, err := s.CapturedDeliveries(ctx, q.Binding, policy); err == nil {
		t.Fatal("material binding became reading consent")
	}
	g, err := s.ReviewPiCaptureConsent(ctx, q, policy)
	mustMerge(t, err)
	saved := records.OfficialCaptureIdentity{Operation: strings.Repeat("a", 64), Library: g.Library, GrantDigest: g.Key, Instance: q.Binding.Instance, Dataset: q.Binding.Dataset, Session: "pi-" + strings.Repeat("b", 48), Kind: "qa", PayloadDigest: strings.Repeat("c", 64)}
	_, _, err = s.ClaimOfficialCapture(ctx, saved, false)
	mustMerge(t, err)
	mustMerge(t, s.SealOfficialCapture(ctx, saved, uuid.NewString()))
	for i, kind := range []string{"pending", "filtered", "foreign_grant"} {
		r := saved
		r.Operation = strings.Repeat(string(rune('d'+i)), 64)
		if kind == "foreign_grant" {
			r.GrantDigest = strings.Repeat("f", 64)
		}
		_, _, err = s.ClaimOfficialCapture(ctx, r, kind == "filtered")
		mustMerge(t, err)
		if kind == "foreign_grant" {
			mustMerge(t, s.SealOfficialCapture(ctx, r, uuid.NewString()))
		}
	}
	verify := func() {
		t.Helper()
		rows, e := s.CapturedDeliveries(ctx, q.Binding, policy)
		if e != nil || len(rows) != 1 || rows[0].OfficialCaptureIdentity != saved {
			t.Fatal(rows, e)
		}
	}
	verify()
	mustMerge(t, s.DisablePiCaptureConsent(ctx, records.PiCaptureConsentDisable{Connection: q.Binding.ConnectionID, Project: q.Binding.Project, ExpectedKey: g.Key}))
	verify()
	if err = s.AuthorizeCogneeBinding(ctx, q.Binding); err == nil {
		t.Fatal("captured read enabled historical reading")
	}
	if _, err = s.CapturedDeliveries(ctx, q.Binding, strings.Repeat("1", 64)); err == nil {
		t.Fatal("different policy accepted")
	}
	_, err = s.DB.Exec(`UPDATE connections SET enabled=0 WHERE id=?`, q.Binding.ConnectionID)
	mustMerge(t, err)
	if _, err = s.CapturedDeliveries(ctx, q.Binding, policy); err == nil {
		t.Fatal("revoked connection still read captured content")
	}
}
