package store

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func TestPiCaptureReadReauthorizationPreservesProvenanceAndRequiresFreshReview(t *testing.T) {
	s, _, q, r, policy := piCaptureConsentFixture(t)
	ctx := context.Background()
	g, err := s.ReviewPiCaptureConsent(ctx, q, policy)
	mustMerge(t, err)
	saved := records.OfficialCaptureIdentity{Operation: strings.Repeat("a", 64), Library: g.Library, GrantDigest: g.Key, Instance: q.Binding.Instance, Dataset: q.Binding.Dataset, Session: "pi-" + strings.Repeat("b", 48), Kind: "qa", PayloadDigest: strings.Repeat("c", 64)}
	_, _, err = s.ClaimOfficialCapture(ctx, saved, false)
	mustMerge(t, err)
	mustMerge(t, s.SealOfficialCapture(ctx, saved, uuid.NewString()))
	var original string
	mustMerge(t, s.DB.QueryRow(`SELECT payload FROM pi_capture_consents WHERE key=?`, g.Key).Scan(&original))
	draft := records.ConnectionDraft{ID: r.ConnectionID, Agent: "pi", Name: "synthetic pi", Project: q.Binding.Project, AllowedScopes: []string{q.Binding.Project}}
	for i := 0; i < 2; i++ {
		mustMerge(t, s.Disconnect(ctx, r.ConnectionID))
		fresh, err := s.ConfigureConnection(ctx, draft)
		mustMerge(t, err)
		if _, err = s.CapturedDeliveries(ctx, q.Binding, policy); err == nil {
			t.Fatal("reconnection silently restored reading")
		}
		review := records.CogneeReadReauthorization{Previous: q.Binding, Operation: uuid.NewString(), Generation: fresh.Generation}
		for _, mode := range []string{"binding", "project", "generation", "policy"} {
			bad, p := review, policy
			switch mode {
			case "binding":
				bad.Previous.Operation = uuid.NewString()
			case "project":
				bad.Previous.Project = uuid.NewString()
			case "generation":
				bad.Generation--
			case "policy":
				p = strings.Repeat("b", 64)
			}
			if _, e := s.ReauthorizePiCaptureRead(ctx, bad, p); e == nil {
				t.Fatal("invalid read review accepted", mode)
			}
		}
		grant, err := s.PreparePiCaptureReadReauthorization(ctx, review, policy)
		mustMerge(t, err)
		if _, err = s.CapturedDeliveries(ctx, q.Binding, policy); err == nil {
			t.Fatal("preview granted reading")
		}
		committed, err := s.ReauthorizePiCaptureRead(ctx, review, policy)
		mustMerge(t, err)
		if committed != grant {
			t.Fatal("review identity changed")
		}
		again, err := s.ReauthorizePiCaptureRead(ctx, review, policy)
		mustMerge(t, err)
		if again != grant {
			t.Fatal("same intent retry changed grant")
		}
		conflict := review
		conflict.Operation = uuid.NewString()
		if _, err = s.ReauthorizePiCaptureRead(ctx, conflict, policy); err == nil {
			t.Fatal("another intent replaced current review")
		}
		rows, err := s.CapturedDeliveries(ctx, q.Binding, policy)
		mustMerge(t, err)
		if len(rows) != 1 || rows[0].OfficialCaptureIdentity != saved {
			t.Fatal("original receipt changed", rows)
		}
		currentRequest := r
		currentRequest.Generation = fresh.Generation
		if _, err = s.AuthorizePiCapture(ctx, currentRequest, policy); err == nil {
			t.Fatal("read review enabled future capture")
		}
		if err = s.AuthorizeCogneeBinding(ctx, q.Binding); err == nil {
			t.Fatal("read review enabled historical catalog")
		}
		bindings, err := s.CogneeBindings(ctx, q.Binding.Project)
		mustMerge(t, err)
		if len(bindings) != 1 || bindings[0] != q.Binding {
			t.Fatal("read review rewrote original binding")
		}
		var after string
		mustMerge(t, s.DB.QueryRow(`SELECT payload FROM pi_capture_consents WHERE key=?`, g.Key).Scan(&after))
		if after != original {
			t.Fatal("read review rewrote capture consent")
		}
	}
	var count int
	mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM pi_capture_read_grants`).Scan(&count))
	if count != 2 {
		t.Fatal("review history not retained", count)
	}
	// A new directory at the same pathname is not the original workspace.
	mustMerge(t, os.Rename(q.Binding.Workspace, q.Binding.Workspace+"-retained"))
	mustMerge(t, exec.Command("git", "init", "-q", q.Binding.Workspace).Run())
	if _, err = s.CapturedDeliveries(ctx, q.Binding, policy); err == nil {
		t.Fatal("replacement physical workspace inherited saved records")
	}
}
