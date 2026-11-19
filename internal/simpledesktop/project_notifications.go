package simpledesktop

import "time"

type projectFailure struct {
	Since time.Time
	Last  time.Time
	Count int
}

// Sample once per completed status read, never once per frame. Different
// sessions keep independent failure windows; a successful read clears its window.
func (q *projectQueueState) observeNotificationFailures(now time.Time) {
	current := map[string]bool{}
	if q.KnowledgeReadError != "" {
		current["knowledge-state"] = true
	}
	if q.BacklogError != "" {
		current["backlog-read"] = true
	}
	if q.ActivityError != "" {
		current["activity-read"] = true
	}
	if q.DocumentError != "" {
		current["documents"] = true
	}
	for index, row := range q.Sessions {
		if row.View == nil {
			continue
		}
		key := noticeSessionKey(row, index)
		if !row.View.Loaded || row.View.ReadError != "" {
			current["read/"+key] = true
		}
	}
	if q.NotificationFailures == nil {
		q.NotificationFailures = map[string]projectFailure{}
	}
	for key := range q.NotificationFailures {
		if !current[key] {
			delete(q.NotificationFailures, key)
		}
	}
	for key := range current {
		f := q.NotificationFailures[key]
		if f.Since.IsZero() {
			f.Since = now
		}
		f.Last = now
		f.Count++
		q.NotificationFailures[key] = f
	}
}

// Confirmed task/queue failures come from the durable source, not the lifetime
// of the UI process. Retain a known unknown write even if a later read fails;
// a failed read is not proof that the original write recovered.
func (q *projectQueueState) confirmedNotification() bool {
	for _, row := range q.Sessions {
		if row.View == nil {
			continue
		}
		s := row.View.Status
		if s.Unknown > 0 || (s.Pending > 0 && s.Blocked != nil) {
			return true
		}
	}
	if j := q.Backlog; j != nil {
		if j.ProcessingFailed > 0 {
			return true
		}
		if (j.Status == "paused" || j.Status == "interrupted" || j.Status == "stopped") && j.Reason != "user_stopped" {
			return true
		}
	}
	return false
}
func (q *projectQueueState) persistentNotification(now time.Time) bool {
	for _, f := range q.NotificationFailures {
		if f.Count >= 3 && !now.Before(f.Last) && f.Last.Sub(f.Since) >= 2*time.Minute {
			return true
		}
	}
	return false
}
