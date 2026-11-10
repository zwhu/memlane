package agents

import (
	"context"
	"errors"
	"testing"

	"github.com/zwhu/memlane/internal/records"
)

func TestHistoricalContextRequiresEverySelectedResponseSentAndRechecksReader(t *testing.T) {
	id := "codex_selected"
	digest := "selection-digest"
	revoked := false
	source := records.ProjectRepositorySource{ID: "source", Text: "actual fixture bytes", History: &records.ProjectHistoryOriginal{Agent: "codex", Session: id, Digest: digest}}
	r := newProjectHistoryContext(func(context.Context, string) (records.ProjectRepositorySource, error) {
		if revoked {
			return records.ProjectRepositorySource{}, errors.New("explicit revoke")
		}
		return source, nil
	}, []records.ProjectSessionSource{{ProjectSessionSelection: records.ProjectSessionSelection{ID: id, Digest: digest}}})
	if _, err := r.page(context.Background(), "codex_unselected"); err == nil {
		t.Fatal("unselected session accepted")
	}
	if err := r.requireComplete(); err == nil {
		t.Fatal("unread history released")
	}
	if _, err := r.page(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err := r.requireComplete(); err == nil {
		t.Fatal("read but unsent history released")
	}
	r.markSent(id)
	if err := r.requireComplete(); err != nil {
		t.Fatal(err)
	}
	revoked = true
	if _, err := r.page(context.Background(), id); err == nil {
		t.Fatal("cached source bypassed revocation")
	}
	proof := r.proof()
	if proof.Selected != 1 || proof.Read != 1 || proof.Sent != 1 || proof.Bytes <= 0 {
		t.Fatal(proof)
	}
}
