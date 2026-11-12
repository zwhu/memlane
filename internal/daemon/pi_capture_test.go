package daemon

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

func TestPiCaptureDaemonConsentRequiresConfirmationAndSeparatesHistoricalAccess(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "memlane.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := filepath.Join(root, "synthetic-pi-repo")
	if output, err := exec.Command("git", "init", repo).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	project, err := db.RegisterProject(ctx, records.ScopeDraft{Name: "synthetic capture", Path: repo})
	if err != nil {
		t.Fatal(err)
	}
	c, err := db.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "pi", Name: "synthetic pi", Project: project.ID, AllowedScopes: []string{project.ID}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.BindCognee(ctx, records.CogneeBinding{Operation: uuid.NewString(), Instance: uuid.NewString(), Dataset: uuid.NewString(), Project: project.ID, ConnectionID: c.ID, Generation: c.Generation, Workspace: repo, Endpoint: "http://127.0.0.1:49123", MaterialSubmission: true})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: db, config: Config{Root: root}}
	request := records.AgentRequest{Protocol: 1, Tool: "memory_save", Channel: "daily", ConnectionID: c.ID, Generation: c.Generation, Workspace: repo, Arguments: records.AgentArguments{Scope: project.ID}}
	if _, err := s.perform(ctx, Request{Op: "pi_capture_authorize", AgentRequest: request}); err == nil {
		t.Fatal("connected pi implicitly authorized capture")
	}
	review := records.PiCaptureConsentReview{Operation: uuid.NewString(), Binding: b}
	if _, err := s.perform(ctx, Request{Op: "pi_capture_consent_review", PiCaptureConsentReview: &review}); err == nil {
		t.Fatal("unconfirmed capture review accepted")
	}
	var count int
	if err := db.DB.QueryRow(`SELECT count(*) FROM pi_capture_consents`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	result, err := s.perform(ctx, Request{Op: "pi_capture_consent_review", PiCaptureConsentReview: &review, UserConfirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	grant := result.(records.PiCaptureConsent)
	if !grant.Enabled || grant.PolicyDigest != piCapturePolicyDigest {
		t.Fatal("daemon policy contract not bound", grant)
	}
	authorized, err := s.perform(ctx, Request{Op: "pi_capture_authorize", AgentRequest: request})
	if err != nil || authorized.(records.PiCaptureConsent).Key != grant.Key {
		t.Fatal(authorized, err)
	}
	if _, err := s.sessionBinding(ctx, records.SessionQuery{Project: b.Project, Instance: b.Instance, Dataset: b.Dataset}); err == nil {
		t.Fatal("capture consent became historical read permission")
	}
	processing := request
	processing.Channel = "processing"
	if _, err := s.perform(ctx, Request{Op: "pi_capture_authorize", AgentRequest: processing}); err == nil {
		t.Fatal("memory processing could collect itself")
	}
	disable := records.PiCaptureConsentDisable{Connection: c.ID, Project: project.ID, ExpectedKey: grant.Key}
	if _, err := s.perform(ctx, Request{Op: "pi_capture_consent_disable", PiCaptureConsentDisable: &disable}); err == nil {
		t.Fatal("unconfirmed disable accepted")
	}
	if _, err := s.perform(ctx, Request{Op: "pi_capture_consent_disable", PiCaptureConsentDisable: &disable, UserConfirmed: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.perform(ctx, Request{Op: "pi_capture_authorize", AgentRequest: request}); err == nil {
		t.Fatal("disabled pi still authorized")
	}
	for _, table := range []string{"memories", "versions", "operations", "jobs", "official_capture_receipts"} {
		if err := db.DB.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("consent wrote content", table, count, err)
		}
	}
}
