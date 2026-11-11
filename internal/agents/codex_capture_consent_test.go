package agents

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func TestCodexCaptureConsentIsExplicitAndSeparateFromProjectGrants(t *testing.T) {
	root := t.TempDir()
	program := codexCompletionFixtureProgram(t, root, "ok")
	profile := filepath.Join(root, "profile")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	c := records.Connection{ID: uuid.NewString(), Agent: "codex", Enabled: true, Generation: 1, AllowedScopes: []string{}}
	o := InstallOptions{ComputerLevel: true, ClientRoot: filepath.Join(root, "client"), ServiceRoot: filepath.Join(root, "service"), Bridge: program, AgentPath: program, CodexDirectory: profile}
	i, err := Install(context.Background(), c, o)
	if err != nil {
		t.Fatal(err)
	}
	if i.CaptureConsent != nil {
		t.Fatal("discovery/installation granted collection")
	}
	if _, err = SetCodexCaptureConsent(o.ClientRoot, c.ID, 1, true, true); err == nil {
		t.Fatal("unverified connection granted collection")
	}
	if err = SaveProtocolVerification(o.ClientRoot, c.ID, 1, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = SetCodexCaptureConsent(o.ClientRoot, c.ID, 1, true, false); err == nil {
		t.Fatal("missing user confirmation accepted")
	}
	i, err = SetCodexCaptureConsent(o.ClientRoot, c.ID, 1, true, true)
	if err != nil || i.CaptureConsent == nil || !i.CaptureConsent.Enabled || len(i.Connection.AllowedScopes) != 0 || i.Connection.Project != "" {
		t.Fatal("collection granted project knowledge access", i, err)
	}
	first := *i.CaptureConsent
	i, err = SetCodexCaptureConsent(o.ClientRoot, c.ID, 1, true, true)
	if err != nil || *i.CaptureConsent != first {
		t.Fatal("repeated authorization changed boundary", err)
	}
	c.Generation = 2
	c.AllowedScopes = []string{uuid.NewString()}
	i, err = Install(context.Background(), c, o)
	if err != nil || i.CaptureConsent == nil || *i.CaptureConsent != first {
		t.Fatal("project scope update lost independent consent", err)
	}
	if _, err = SetCodexCaptureConsent(o.ClientRoot, c.ID, 1, false, true); err == nil {
		t.Fatal("stale UI mutated consent")
	}
	i, err = SetCodexCaptureConsent(o.ClientRoot, c.ID, 2, false, true)
	if err != nil || i.CaptureConsent.Enabled {
		t.Fatal("revocation failed", err)
	}
	// Re-enabling cannot borrow the old protocol generation.
	if _, err = SetCodexCaptureConsent(o.ClientRoot, c.ID, 2, true, true); err == nil {
		t.Fatal("stale protocol proof authorized collection")
	}
	if err = Uninstall(context.Background(), o.ClientRoot, c.ID); err != nil {
		t.Fatal(err)
	}
	i, err = Install(context.Background(), c, o)
	if err != nil || i.CaptureConsent != nil {
		t.Fatal("disconnect/reconnect revived prior consent", err)
	}
}
