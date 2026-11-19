package simpledesktop

import (
	"testing"
	"time"
)

func TestProjectFailuresRequireDurationAndRepeatedSameProblem(t *testing.T) {
	q := &projectQueueState{DocumentError: "unavailable"}
	now := time.Now()
	q.observeNotificationFailures(now)
	q.observeNotificationFailures(now.Add(time.Second))
	q.observeNotificationFailures(now.Add(2 * time.Second))
	if q.persistentNotification(now.Add(time.Second * 30)) {
		t.Fatal("short failure notified")
	}
	if q.persistentNotification(now.Add(2 * time.Minute)) {
		t.Fatal("elapsed time without another failed observation is not persistence")
	}
	q.observeNotificationFailures(now.Add(2 * time.Minute))
	if !q.persistentNotification(now.Add(2 * time.Minute)) {
		t.Fatal("persistent failure suppressed")
	}
	q.DocumentError = ""
	q.observeNotificationFailures(now.Add(3 * time.Minute))
	if q.persistentNotification(now.Add(3 * time.Minute)) {
		t.Fatal("recovery did not clear notification")
	}
	q.Sessions = []queuedSession{{Agent: AccessAgent{ID: "a"}, Binding: ProjectBinding{HostSessionKey: "one"}, View: &pendingBufferView{}}}
	q.observeNotificationFailures(now.Add(4 * time.Minute))
	q.Sessions[0].Binding.HostSessionKey = "two"
	q.observeNotificationFailures(now.Add(5 * time.Minute))
	q.observeNotificationFailures(now.Add(6 * time.Minute))
	if q.persistentNotification(now.Add(6 * time.Minute)) {
		t.Fatal("different failures were merged")
	}
}

func TestConfirmedNotificationDoesNotWaitForFailureWindow(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status PendingMaterials
		job    *backlogJob
		want   bool
	}{
		{name: "unknown original write", status: PendingMaterials{Pending: 1, Unknown: 1}, want: true},
		{name: "blocked unsent messages", status: PendingMaterials{Pending: 1, Blocked: &struct {
			Reason string `json:"reason"`
		}{Reason: "write_denied"}}, want: true},
		{name: "old empty block is not an unresolved message", status: PendingMaterials{Blocked: &struct {
			Reason string `json:"reason"`
		}{Reason: "write_denied"}}},
		{name: "backlog failed", job: &backlogJob{Status: "paused", Reason: "backlog_result_unconfirmed"}, want: true},
		{name: "processing result unknown", job: &backlogJob{Status: "completed", ProcessingFailed: 1}, want: true},
		{name: "user stopped", job: &backlogJob{Status: "stopped", Reason: "user_stopped"}},
		{name: "normal backlog", status: PendingMaterials{Pending: 25}, job: &backlogJob{Status: "running"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &projectQueueState{Backlog: tc.job, Sessions: []queuedSession{{View: &pendingBufferView{Loaded: true, Status: tc.status}}}}
			if q.confirmedNotification() != tc.want {
				t.Fatalf("fresh read: got %v want %v", q.confirmedNotification(), tc.want)
			}
			if len(q.NotificationFailures) != 0 {
				t.Fatal("confirmed notification mutated timing state")
			}
			q.observeNotificationFailures(time.Now())
			if q.persistentNotification(time.Now()) {
				t.Fatal("confirmed failures entered the transient error timer")
			}
		})
	}
}

func TestConfirmedWriteSurvivesReadFailureUntilObservedRecovery(t *testing.T) {
	v := &pendingBufferView{Loaded: true, Status: PendingMaterials{Pending: 20, Unknown: 20}}
	q := &projectQueueState{Sessions: []queuedSession{{View: v}}}
	if !q.confirmedNotification() {
		t.Fatal("existing unknown not shown")
	}
	v.Loaded = false
	v.ReadError = "temporarily offline"
	if !q.confirmedNotification() {
		t.Fatal("failed read cleared a known unknown write")
	}
	v.observe(PendingMaterials{Signature: "confirmed-empty"})
	if q.confirmedNotification() {
		t.Fatal("successful source recovery did not clear notification")
	}
}
