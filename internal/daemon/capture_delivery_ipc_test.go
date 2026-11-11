package daemon_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agentbridge"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/daemon"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"golang.org/x/sys/unix"
)

func TestCaptureDeliveryPrivateIPCAndOfflineRestart(t *testing.T) {
	ctx := context.Background()
	client := t.TempDir()
	root := filepath.Join(client, "data")
	config := daemon.Config{Root: root, ClientRoot: client}
	stop := startService(t, config)
	library := serviceipc.Library{Root: root}
	repo := filepath.Join(t.TempDir(), "repo")
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	project, err := library.RegisterProject(ctx, records.ScopeDraft{Name: "Synthetic capture repository", Path: repo})
	requireService(t, err)
	connection, err := library.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "codex", Name: "Synthetic capture connection", ConnectionOnly: true})
	requireService(t, err)
	program := filepath.Join(t.TempDir(), "synthetic-codex")
	requireService(t, os.WriteFile(program, []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'codex-cli synthetic'; exit 0; fi\nif [ \"$1\" = mcp ]; then echo 'COMMAND --url'; exit 0; fi\nexit 1\n"), 0700))
	profile := filepath.Join(t.TempDir(), "profile")
	requireService(t, os.Mkdir(profile, 0700))
	_, err = agents.Install(ctx, connection, agents.InstallOptions{ComputerLevel: true, ClientRoot: client, ServiceRoot: root, Bridge: program, AgentPath: program, CodexDirectory: profile})
	requireService(t, err)
	requireService(t, agents.SaveProtocolVerification(client, connection.ID, connection.Generation, ""))
	installed, err := agents.SetCodexCaptureConsent(client, connection.ID, connection.Generation, true, true)
	requireService(t, err)
	transcript := func(session string) string {
		t.Helper()
		path := filepath.Join(profile, "sessions", session+".jsonl")
		requireService(t, os.MkdirAll(filepath.Dir(path), 0700))
		raw, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]any{"id": session, "cwd": project.Path, "source": "cli"}})
		requireService(t, os.WriteFile(path, append(raw, '\n'), 0600))
		return path
	}
	retain := func(text string) records.AgentDeliveryStatus {
		t.Helper()
		session := uuid.NewString()
		hook, _ := json.Marshal(map[string]any{"session_id": session, "turn_id": "synthetic-turn", "cwd": project.Path, "hook_event_name": "UserPromptSubmit", "prompt": text, "transcript_path": transcript(session)})
		status, e := agents.CaptureCodexHook(ctx, client, connection.ID, hook, time.Now())
		requireService(t, e)
		if len(status.Entries) != 1 {
			t.Fatal(status)
		}
		return status.Entries[0]
	}
	first := retain("synthetic first original")
	spoolPath := filepath.Join(root, "agent-outbox", "ledger.json")
	beforeSave, err := os.ReadFile(spoolPath)
	requireService(t, err)
	query := records.CaptureDeliveryQuery{EntryID: first.ID, ReviewDigest: first.ReviewDigest}
	if _, err = library.DeliverCapturedRaw(ctx, query); err == nil {
		t.Fatal("saved without explicit repository approval")
	}
	preview, err := library.DeliveryAssignmentPreview(ctx, records.DeliveryAssignmentPreviewQuery{EntryID: first.ID, ReviewDigest: first.ReviewDigest, Project: project.ID})
	requireService(t, err)
	approval := records.CaptureRouteConfirmation{OperationID: uuid.NewString(), LibraryID: preview.LibraryID, ConnectionID: connection.ID, EntryID: first.ID, ReviewDigest: first.ReviewDigest, Project: project.ID}
	if err = serviceipc.Call(ctx, root, serviceipc.Request{Op: "capture_route_confirm", CaptureRouteConfirmation: &approval}, nil); err == nil {
		t.Fatal("route silently approved")
	}
	_, err = library.ConfirmCaptureRoute(ctx, approval)
	requireService(t, err)
	saved, err := library.DeliverCapturedRaw(ctx, query)
	requireService(t, err)
	if !saved.Persisted || saved.AckPending {
		t.Fatal(saved)
	}
	version, err := library.Get(ctx, saved.Memory)
	requireService(t, err)
	if version.Text != preview.Arguments.Text || !strings.Contains(version.Text, "synthetic first original") || version.Origin != "raw" || version.Revision != 1 {
		t.Fatal(version)
	}
	replay, err := library.DeliverCapturedRaw(ctx, query)
	requireService(t, err)
	if replay != saved {
		t.Fatal("duplicate replay", replay, saved)
	}
	stop()
	// Restore only this isolated spool's pre-ack image. SQL remains committed,
	// reproducing a crash after commit and before the local receipt was saved.
	requireService(t, os.WriteFile(spoolPath, beforeSave, 0600))
	offline := retain("synthetic offline original")
	lock, err := unix.Open(filepath.Join(root, "agent-outbox", "lock"), unix.O_RDWR, 0600)
	requireService(t, err)
	requireService(t, unix.Flock(lock, unix.LOCK_EX|unix.LOCK_NB))
	stageSession := uuid.NewString()
	hook, _ := json.Marshal(map[string]any{"session_id": stageSession, "turn_id": "synthetic-stage-recovery", "cwd": project.Path, "hook_event_name": "UserPromptSubmit", "prompt": "SYNTHETIC_STAGED_ONLY_ORIGINAL", "transcript_path": transcript(stageSession)})
	staged, captureErr := agents.CaptureCodexHook(ctx, client, connection.ID, hook, time.Now())
	requireService(t, unix.Flock(lock, unix.LOCK_UN))
	requireService(t, unix.Close(lock))
	if captureErr == nil || staged.Code != "delivery_busy" || !staged.LocalRetained {
		t.Fatal("test did not retain a stage-only event", staged, captureErr)
	}
	stop = startService(t, config)
	defer stop()
	waitService(t, 10*time.Second, func() bool {
		review, e := agentbridge.ReviewDelivery(root, first.ID)
		return e == nil && review.Item.Status.PersistedConfirmed && review.Item.Status.Memory == saved.Memory
	})
	version, err = library.Get(ctx, saved.Memory)
	requireService(t, err)
	if version.Revision != 1 {
		t.Fatal("lost acknowledgement duplicated committed original", version)
	}
	waitService(t, 10*time.Second, func() bool {
		review, e := agentbridge.ReviewDelivery(root, offline.ID)
		return e == nil && review.Item.Status.PersistedConfirmed
	})
	review, err := agentbridge.ReviewDelivery(root, offline.ID)
	requireService(t, err)
	version, err = library.Get(ctx, review.Item.Status.Memory)
	requireService(t, err)
	if version.Text != review.Arguments.Text || !strings.Contains(version.Text, "synthetic offline original") || version.Origin != "raw" || version.Revision != 1 {
		t.Fatal(version)
	}
	var stagedMemory string
	waitService(t, 10*time.Second, func() bool {
		page, e := agentbridge.ListDeliveries(root)
		if e != nil {
			return false
		}
		for _, item := range page.Items {
			review, e := agentbridge.ReviewDelivery(root, item.Status.ID)
			if e == nil && strings.Contains(review.Arguments.Text, "SYNTHETIC_STAGED_ONLY_ORIGINAL") && review.Item.Status.PersistedConfirmed {
				stagedMemory = review.Item.Status.Memory
				return true
			}
		}
		return false
	})
	version, err = library.Get(ctx, stagedMemory)
	requireService(t, err)
	if version.Origin != "raw" || version.Revision != 1 {
		t.Fatal(version)
	}
	connections, err := library.Connections(ctx)
	requireService(t, err)
	if len(connections) != 1 || len(connections[0].AllowedScopes) != 0 {
		t.Fatal("automatic capture changed knowledge authorization", connections)
	}
	stop()
	revoked := retain("synthetic queued original after revocation")
	_, err = agents.SetCodexCaptureConsent(client, connection.ID, connection.Generation, false, true)
	requireService(t, err)
	stop = startService(t, config)
	defer stop()
	if _, err = library.DeliverCapturedRaw(ctx, records.CaptureDeliveryQuery{EntryID: revoked.ID, ReviewDigest: revoked.ReviewDigest}); err == nil {
		t.Fatal("revoked authorization accepted")
	}
	review, err = agentbridge.ReviewDelivery(root, revoked.ID)
	requireService(t, err)
	if review.Item.Status.PersistedConfirmed || !strings.Contains(review.Arguments.Text, "synthetic queued original after revocation") {
		t.Fatal("revoked original lost or falsely saved", review)
	}
	newInstallation, err := agents.SetCodexCaptureConsent(client, connection.ID, connection.Generation, true, true)
	requireService(t, err)
	if newInstallation.CaptureConsent.ID == installed.CaptureConsent.ID {
		t.Fatal("re-authorization reused revoked consent")
	}
	if _, err = library.DeliverCapturedRaw(ctx, records.CaptureDeliveryQuery{EntryID: revoked.ID, ReviewDigest: revoked.ReviewDigest}); err == nil {
		t.Fatal("new authorization silently adopted an old capture")
	}
}
