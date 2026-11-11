//go:build darwin || linux

package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agentbridge"
	"golang.org/x/sys/unix"
)

func TestCaptureRetryAfterActualOutboxLockAndRevocation(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(map[bool]string{false: "recovered", true: "revoked"}[revoke], func(t *testing.T) {
			i, workspace := captureRuntimeFixture(t)
			ctx := context.Background()
			_, err := SetCodexCaptureConsent(i.Options.ClientRoot, i.Connection.ID, 1, true, true)
			if err != nil {
				t.Fatal(err)
			}
			spool := filepath.Join(i.Options.ServiceRoot, "agent-outbox")
			if err = os.Mkdir(spool, 0700); err != nil {
				t.Fatal(err)
			}
			fd, err := unix.Open(filepath.Join(spool, "lock"), unix.O_RDWR|unix.O_CREAT, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer unix.Close(fd)
			if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
				t.Fatal(err)
			}
			session := uuid.NewString()
			hook, _ := json.Marshal(map[string]any{"session_id": session, "turn_id": "retry-turn", "cwd": workspace, "hook_event_name": "UserPromptSubmit", "prompt": "SYNTHETIC_RETRY_ORIGINAL", "transcript_path": capturePromptTranscript(t, i, session, workspace)})
			receipt, err := CaptureCodexHook(ctx, i.Options.ClientRoot, i.Connection.ID, hook, time.Now())
			if err == nil || receipt.Code != "delivery_busy" || !receipt.LocalRetained {
				t.Fatal(receipt, err)
			}
			if err = unix.Flock(fd, unix.LOCK_UN); err != nil {
				t.Fatal(err)
			}
			if revoke {
				if _, err = SetCodexCaptureConsent(i.Options.ClientRoot, i.Connection.ID, 1, false, true); err != nil {
					t.Fatal(err)
				}
			}
			retry := &CodexCaptureRetry{}
			defer retry.Close()
			err = retry.Step(ctx, i.Options.ClientRoot, i.Connection.ID)
			if revoke && err == nil {
				t.Fatal("revoked stage retried")
			}
			if !revoke && err != nil {
				t.Fatal(err)
			}
			page, err := agentbridge.ListDeliveries(i.Options.ServiceRoot)
			if err != nil {
				t.Fatal(err)
			}
			if revoke {
				if len(page.Items) != 0 {
					t.Fatal("revoked stage entered queue", page)
				}
				return
			}
			if len(page.Items) != 1 {
				t.Fatal(page)
			}
			review, err := agentbridge.ReviewDelivery(i.Options.ServiceRoot, page.Items[0].Status.ID)
			if err != nil || !strings.Contains(review.Arguments.Text, "SYNTHETIC_RETRY_ORIGINAL") {
				t.Fatal(review, err)
			}
			for count := 0; count < 3; count++ {
				if err = retry.Step(ctx, i.Options.ClientRoot, i.Connection.ID); err != nil {
					t.Fatal(err)
				}
			}
			page, err = agentbridge.ListDeliveries(i.Options.ServiceRoot)
			if err != nil || len(page.Items) != 1 {
				t.Fatal("retry duplicated original", page, err)
			}
		})
	}
}
