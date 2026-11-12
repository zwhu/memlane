package daemon

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"strings"
	"testing"
)

func TestCapturedReadCarriesVerifiedTruncationToTheView(t *testing.T) {
	f := newPiCaptureDeliveryFixture(t, "success")
	f.q.Entries = []records.OfficialCaptureInput{f.trace("bash", "python3 "+strings.Repeat("x", 1400))}
	f.q.Batch = piCaptureFingerprint([]string{f.q.Grant, f.q.HostSession, f.q.Entries[0].Operation})
	ctx := context.Background()
	if _, err := f.s.processPiCaptureBatch(ctx, &f.q, false); err != nil {
		t.Fatal(err)
	}
	b := f.grant.Binding
	q := records.SessionQuery{Captured: true, Project: b.Project, Instance: b.Instance, Dataset: b.Dataset, Limit: 20}
	r, err := f.s.readOfficialSession(ctx, q, false)
	if err != nil {
		t.Fatal(err)
	}
	page := r.(records.SessionPage)
	q.Selected = &page.Sessions[0]
	r, err = f.s.readOfficialSession(ctx, q, true)
	if err != nil {
		t.Fatal(err)
	}
	d := r.(records.SessionDetail)
	if len(d.Traces) != 1 || !d.Traces[0].ParamsTruncated || d.Traces[0].ResultTruncated {
		t.Fatal("truncation metadata lost or invented", d.Traces)
	}
	var params map[string]string
	if json.Unmarshal(d.Traces[0].Params, &params) != nil || len([]rune(params["command"])) > 1000 {
		t.Fatal("stored bounds changed")
	}
}

func TestCapturedSessionReadsOnlySavedReceiptsAndKeepsHistoryClosed(t *testing.T) {
	f := newPiCaptureDeliveryFixture(t, "success")
	ctx := context.Background()
	receipt, err := f.s.processPiCaptureBatch(ctx, &f.q, false)
	if err != nil || receipt.Operations[0].State != "saved" {
		t.Fatal(receipt, err)
	}
	b := f.grant.Binding
	q := records.SessionQuery{Captured: true, Project: b.Project, Instance: b.Instance, Dataset: b.Dataset, Limit: 20}
	result, err := f.s.readOfficialSession(ctx, q, false)
	if err != nil {
		t.Fatal(err)
	}
	page := result.(records.SessionPage)
	if len(page.Sessions) != 1 || page.Total != 1 || page.HasMore || page.Sessions[0].StartedAt != "" {
		t.Fatal(page)
	}
	q.Selected = &page.Sessions[0]
	result, err = f.s.readOfficialSession(ctx, q, true)
	if err != nil {
		t.Fatal(err)
	}
	detail := result.(records.SessionDetail)
	if detail.QACount != 1 || len(detail.QAs) != 1 || detail.QAs[0].Answer != "synthetic answer" || detail.ToolCount != 0 || detail.QAs[0].Time != "" {
		t.Fatal(detail)
	}
	if _, err = f.s.sessionBinding(ctx, q); err == nil {
		t.Fatal("capture read granted history access")
	}
	historical := q
	historical.Captured = false
	if _, err = f.s.readOfficialSession(ctx, historical, false); err == nil {
		t.Fatal("history flag bypassed")
	}
	foreign := q
	copy := *q.Selected
	copy.Session = "pi-unreceipted-session"
	foreign.Selected = &copy
	before := f.lookups.Load()
	if _, err = f.s.readOfficialSession(ctx, foreign, true); err == nil {
		t.Fatal("unreceipted session accepted")
	}
	if f.lookups.Load() != before {
		t.Fatal("unreceipted session requested upstream")
	}
	copy = *q.Selected
	copy.User = uuid.NewString()
	foreign.Selected = &copy
	if _, err = f.s.readOfficialSession(ctx, foreign, true); err == nil {
		t.Fatal("different owner accepted")
	}
	if err = f.s.disablePiCaptureConsent(ctx, &records.PiCaptureConsentDisable{Connection: b.ConnectionID, Project: b.Project, ExpectedKey: f.grant.Key}, true); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.readOfficialSession(ctx, q, true); err != nil {
		t.Fatal("stopping future capture hid saved content", err)
	}
	if _, err = f.s.store.DB.Exec(`UPDATE connections SET enabled=0 WHERE id=?`, b.ConnectionID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.readOfficialSession(ctx, q, true); err == nil {
		t.Fatal("revoked connection still read captured content")
	}
	if f.posts.Load() != 1 {
		t.Fatal("read path resent capture")
	}
	assertPiCaptureNoMirror(t, f)
}

func TestCapturedSessionLateReadCannotSurviveConnectionRevocation(t *testing.T) {
	f := newPiCaptureDeliveryFixture(t, "revoke_read")
	ctx := context.Background()
	if _, err := f.s.processPiCaptureBatch(ctx, &f.q, false); err != nil {
		t.Fatal(err)
	}
	b := f.grant.Binding
	q := records.SessionQuery{Captured: true, Project: b.Project, Instance: b.Instance, Dataset: b.Dataset, Limit: 20}
	done := make(chan error, 1)
	go func() { _, err := f.s.readOfficialSession(ctx, q, false); done <- err }()
	<-f.started
	f.s.agentAccess.Lock()
	_, err := f.s.store.DB.Exec(`UPDATE connections SET enabled=0 WHERE id=?`, b.ConnectionID)
	f.s.agentAccess.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = <-done; err == nil {
		t.Fatal("late saved capture content survived revoked connection")
	}
	if f.posts.Load() != 1 {
		t.Fatal("late read wrote content")
	}
}

func TestCapturedSessionEmptyReceiptsStillRequireLiveOfficialService(t *testing.T) {
	f := newPiCaptureDeliveryFixture(t, "success")
	b := f.grant.Binding
	q := records.SessionQuery{Captured: true, Project: b.Project, Instance: b.Instance, Dataset: b.Dataset, Limit: 20}
	f.unavailable.Store(true)
	if _, err := f.s.readOfficialSession(context.Background(), q, false); err == nil {
		t.Fatal("unavailable service reported verified empty capture")
	}
	f.unavailable.Store(false)
	result, err := f.s.readOfficialSession(context.Background(), q, false)
	if err != nil || len(result.(records.SessionPage).Sessions) != 0 {
		t.Fatal(result, err)
	}
	if f.posts.Load() != 0 || f.lookups.Load() != 0 {
		t.Fatal("empty receipt list queried unrelated source or wrote content")
	}
}

func TestCapturedReadReauthorizationChecksExactReceiptAndLateRevocation(t *testing.T) {
	for _, mode := range []string{"success", "unavailable", "revoked_in_flight"} {
		t.Run(mode, func(t *testing.T) {
			fixtureMode := "success"
			if mode == "revoked_in_flight" {
				fixtureMode = "revoke_read"
			}
			f := newPiCaptureDeliveryFixture(t, fixtureMode)
			ctx := context.Background()
			if _, err := f.s.processPiCaptureBatch(ctx, &f.q, false); err != nil {
				t.Fatal(err)
			}
			b := f.grant.Binding
			if err := f.s.store.Disconnect(ctx, b.ConnectionID); err != nil {
				t.Fatal(err)
			}
			c, err := f.s.store.ConfigureConnection(ctx, records.ConnectionDraft{ID: b.ConnectionID, Agent: "pi", Name: "synthetic pi", Project: b.Project, AllowedScopes: []string{b.Project}})
			if err != nil {
				t.Fatal(err)
			}
			q := records.CogneeReadReauthorization{Previous: b, Operation: uuid.NewString(), Generation: c.Generation}
			if _, err = f.s.perform(ctx, Request{Op: "cognee_read_reauthorize", CogneeReadReauthorization: &q}); err == nil || f.lookups.Load() != 0 {
				t.Fatal("unconfirmed review contacted service")
			}
			if mode == "unavailable" {
				f.unavailable.Store(true)
			}
			if mode == "success" {
				if _, err = f.s.authorizeCognee(ctx, records.CogneeAuthorization{Binding: b, Credential: "synthetic-capture-token"}); err != nil {
					t.Fatal("credential refresh failed", err)
				}
				if _, err = f.s.store.CapturedDeliveries(ctx, b, piCapturePolicyDigest); err == nil {
					t.Fatal("credential refresh granted reading")
				}
			}
			type reply struct {
				b   records.CogneeBinding
				err error
			}
			done := make(chan reply, 1)
			go func() { out, e := f.s.reauthorizeCogneeRead(ctx, q); done <- reply{out, e} }()
			if mode == "revoked_in_flight" {
				<-f.started
				f.s.agentAccess.Lock()
				err = f.s.store.Disconnect(ctx, b.ConnectionID)
				f.s.agentAccess.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			}
			r := <-done
			var count int
			if err = f.s.store.DB.QueryRow(`SELECT count(*) FROM pi_capture_read_grants`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if mode != "success" {
				if r.err == nil || count != 0 {
					t.Fatal("failed or revoked review committed", r, count)
				}
			} else {
				if r.err != nil || r.b != b || count != 1 || f.lookups.Load() != 2 {
					t.Fatal("exact review receipt missing", r, count, f.lookups.Load())
				}
				query := records.SessionQuery{Captured: true, Project: b.Project, Instance: b.Instance, Dataset: b.Dataset, Limit: 20}
				if _, err = f.s.readOfficialSession(ctx, query, false); err != nil {
					t.Fatal("review did not restore saved reads", err)
				}
				query.Captured = false
				if _, err = f.s.readOfficialSession(ctx, query, false); err == nil {
					t.Fatal("review opened history")
				}
				request := f.q.Request
				request.Generation = c.Generation
				if _, err = f.s.store.AuthorizePiCapture(ctx, request, piCapturePolicyDigest); err == nil {
					t.Fatal("read review opened capture")
				}
			}
			if f.posts.Load() != 1 {
				t.Fatal("read review wrote content")
			}
			assertPiCaptureNoMirror(t, f)
		})
	}
}
