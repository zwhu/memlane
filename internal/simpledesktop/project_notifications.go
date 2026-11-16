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
	if q.DocumentError != "" {
		current["documents"] = true
	}
	for _, row := range q.Sessions {
		key := row.Agent.ID + "/" + row.Binding.HostSessionKey
		if !row.View.Loaded || row.View.ReadError != "" {
			current["read/"+key] = true
		} else if q.activePending(row).Unknown > 0 {
			current["write/"+key] = true
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
func (q *projectQueueState) persistentNotification(now time.Time) bool {
	for _, f := range q.NotificationFailures {
		if f.Count >= 3 && !now.Before(f.Last) && f.Last.Sub(f.Since) >= 2*time.Minute {
			return true
		}
	}
	return false
}
