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
