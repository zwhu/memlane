package simpledesktop

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var noticeAckMu sync.Mutex

// Only presentation decisions are stored. Acknowledgement never changes a
// message, permission, task or save outcome, and never adopts old archives.
type noticeAcknowledgement struct {
	Fingerprint string    `json:"fingerprint"`
	At          time.Time `json:"acknowledged_at"`
	Deferred    bool      `json:"deferred"`
}

func noticeHash(v any) string {
	b, _ := json.Marshal(v)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func noticeSessionKey(row queuedSession, index int) string {
	if row.Agent.ID == "" || row.Binding.HostSessionKey == "" {
		return fmt.Sprintf("unidentified/%d", index)
	}
	return row.Agent.ID + "/" + row.Binding.HostSessionKey
}

func (a *App) noticeAcknowledgementPath(p AccessProject) (string, error) {
	base := a.Config.ClientStateFile
	if base == "" {
		base = a.Config.ConfigFile
	}
	if base == "" {
		return "", fmt.Errorf("客户端状态位置不可用")
	}
	identity, err := readingIdentity(Client{Config: a.Client.Config})
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(base), "message-acknowledgements-"+noticeHash([]string{identity, p.ID, p.Dataset})+".json"), nil
}

func readNoticeAcknowledgementsAtPathLocked(path string) (map[string]noticeAcknowledgement, string) {
	rows := map[string]noticeAcknowledgement{}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return rows, "提醒处理记录读取失败，原提醒仍保留"
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return rows, ""
	}
	if err != nil || json.Unmarshal(b, &rows) != nil || rows == nil {
		return nil, "提醒处理记录读取失败，原提醒仍保留"
	}
	for key, row := range rows {
		if !validPendingHash(key) || !validPendingHash(row.Fingerprint) || row.At.IsZero() {
			return nil, "提醒处理记录读取失败，原提醒仍保留"
		}
	}
	return rows, ""
}

func updateNoticeAcknowledgementsAtPath(path string, mutate func(map[string]noticeAcknowledgement)) (map[string]noticeAcknowledgement, string, error) {
	noticeAckMu.Lock()
	defer noticeAckMu.Unlock()
	rows, message := readNoticeAcknowledgementsAtPathLocked(path)
	if message != "" {
		return nil, message, nil
	}
	mutate(rows)
	b, err := json.Marshal(rows)
	if err == nil {
		err = writeNoticeAcknowledgements(path, b)
	}
	if err != nil {
		return nil, "", err
	}
	return rows, "", nil
}

func (a *App) readNoticeAcknowledgements(p AccessProject) (map[string]noticeAcknowledgement, string) {
	rows := map[string]noticeAcknowledgement{}
	path, err := a.noticeAcknowledgementPath(p)
	if err != nil { // Fixtures without a configured state path have no decisions.
		return rows, ""
	}
	noticeAckMu.Lock()
	defer noticeAckMu.Unlock()
	return readNoticeAcknowledgementsAtPathLocked(path)
}

// Stable issue identity excludes polling times, task progress and display copy.
// New message identities, a different failure reason or a new task re-alert.
func (q *projectQueueState) noticeFingerprint(key string) string {
	for index, row := range q.Sessions {
		if row.View == nil {
			continue
		}
		base := noticeSessionKey(row, index)
		if !strings.HasPrefix(key, base+"/") {
			continue
		}
		v, s := row.View, row.View.Status
		switch strings.TrimPrefix(key, base+"/") {
		case "unknown", "retry":
			ids := append([]string(nil), s.UnknownEntryIDs...)
			sort.Strings(ids)
			identity := noticeHash(ids)
			if len(ids) == 0 {
				identity = s.Signature
			}
			return noticeHash([]any{key, identity, s.Unknown, s.RetryableUnknown})
		case "pending":
			return noticeHash([]any{key, s.Signature, s.Pending - s.Unknown, s.Blocked})
		case "filtered":
			return noticeHash([]any{key, s.Signature, q.filteredPending(row)})
		case "rejected":
			held := q.heldPending(row)
			return noticeHash([]any{key, s.Signature, held.Rejected, held.RejectedUnknown})
		case "read":
			return noticeHash([]string{key, v.ReadCode, v.ReadError})
		case "receipt":
			return noticeHash([]string{key, v.Message})
		}
	}
	if j := q.Backlog; j != nil {
		switch key {
		case "backlog":
			return noticeHash([]any{key, j.Operation, j.Reason})
		case "processing-failed":
			return noticeHash([]any{key, j.Operation, j.ProcessingFailed})
		case "processing-partial":
			return noticeHash([]any{key, j.Operation, j.ProcessingPartial})
		case "processing-active":
			return noticeHash([]any{key, j.Operation, j.ProcessingActive})
		case "review-failed":
			return noticeHash([]any{key, j.Operation, j.ReviewFailed})
		}
	}
	if key == "policy-sync" {
		return noticeHash([]string{key})
	}
	for _, n := range q.ProcessingNotices {
		if key == "processing/"+n.Operation {
			return noticeHash([]any{key, n.Code, n.Status})
		}
	}
	errors := map[string]string{"knowledge": q.KnowledgeReadError, "backlog-read": q.BacklogError, "activity": q.ActivityError, "documents": q.DocumentError, "processing-read": q.ProcessingReadError}
	return noticeHash([]string{key, errors[key]})
}

func (q *projectQueueState) noticeAcknowledged(key string) bool {
	row, ok := q.NoticeAcknowledgements[noticeHash(key)]
	return q.NoticeAcknowledgementError == "" && ok && row.Fingerprint == q.noticeFingerprint(key)
}

func (a *App) acknowledgeNotice(q *projectQueueState, p AccessProject, n messageNotice, restore bool) {
	path, err := a.noticeAcknowledgementPath(p)
	if err != nil {
		q.NoticeAcknowledgementError = "提醒处理未保存，原提醒仍保留"
		return
	}
	key := noticeHash(n.Key)
	fp := q.noticeFingerprint(n.Key)
	rows, message, writeErr := updateNoticeAcknowledgementsAtPath(path, func(r map[string]noticeAcknowledgement) {
		if restore {
			delete(r, key)
		} else {
			r[key] = noticeAcknowledgement{Fingerprint: fp, At: time.Now().UTC(), Deferred: n.NeedsUser}
		}
	})
	if message != "" {
		q.NoticeAcknowledgementError = message
		return
	}
	if writeErr != nil {
		q.NoticeAcknowledgementError = "提醒处理未保存，原提醒仍保留"
		return
	}
	q.NoticeAcknowledgements, q.NoticeAcknowledgementError = rows, ""
	q.StateVersion++
}

// Remove a decision only after a successful source read proves its issue ended.
// A later recurrence must not inherit acknowledgement of an earlier episode.
func (a *App) clearRecoveredNoticeAcknowledgements(q *projectQueueState, p AccessProject, keys []string) {
	changed := false
	for _, key := range keys {
		if _, ok := q.NoticeAcknowledgements[noticeHash(key)]; ok {
			changed = true
			break
		}
	}
	if !changed {
		return
	}
	path, err := a.noticeAcknowledgementPath(p)
	if err != nil {
		q.NoticeAcknowledgementError = "提醒处理记录未更新，原提醒仍保留"
		return
	}
	rows, message, writeErr := updateNoticeAcknowledgementsAtPath(path, func(r map[string]noticeAcknowledgement) {
		for _, key := range keys {
			delete(r, noticeHash(key))
		}
	})
	if message != "" {
		q.NoticeAcknowledgementError = message
		return
	}
	if writeErr != nil {
		q.NoticeAcknowledgementError = "提醒处理记录未更新，原提醒仍保留"
		return
	}
	q.NoticeAcknowledgements, q.NoticeAcknowledgementError = rows, ""
	q.StateVersion++
}

func writeNoticeAcknowledgements(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".message-acknowledgements-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	return err
}

func (q *projectQueueState) unacknowledgedNotificationKeys(now time.Time) (map[string]bool, bool) {
	alertKeys := map[string]bool{}
	ackErr := q.NoticeAcknowledgementError != ""
	for index, row := range q.Sessions {
		if row.View == nil {
			continue
		}
		key := noticeSessionKey(row, index)
		s := row.View.Status
		if q.heldPending(row).Rejected > 0 && !q.noticeAcknowledged(key+"/rejected") {
			alertKeys[key+"/rejected"] = true
		}
		if s.Unknown-s.RetryableUnknown-q.heldPending(row).RejectedUnknown > 0 && !q.noticeAcknowledged(key+"/unknown") {
			alertKeys[key+"/unknown"] = true
		}
		if s.RetryableUnknown > 0 && !q.noticeAcknowledged(key+"/retry") {
			alertKeys[key+"/retry"] = true
		}
		if s.Pending > s.Unknown && s.Blocked != nil && !q.noticeAcknowledged(key+"/pending") {
			alertKeys[key+"/pending"] = true
		}
	}
	if j := q.Backlog; j != nil {
		if j.ProcessingFailed > 0 && !q.noticeAcknowledged("processing-failed") {
			alertKeys["processing-failed"] = true
		}
		if (j.Status == "paused" || j.Status == "interrupted" || j.Status == "stopped") && j.Reason != "user_stopped" && !q.noticeAcknowledged("backlog") {
			alertKeys["backlog"] = true
		}
	}
	keys := map[string]string{"knowledge-state": "knowledge", "backlog-read": "backlog-read", "activity-read": "activity", "documents": "documents"}
	for key, f := range q.NotificationFailures {
		if f.Count < 3 || now.Before(f.Last) || f.Last.Sub(f.Since) < 2*time.Minute {
			continue
		}
		noticeKey := keys[key]
		if strings.HasPrefix(key, "read/") {
			noticeKey = strings.TrimPrefix(key, "read/") + "/read"
		}
		if noticeKey == "" || !q.noticeAcknowledged(noticeKey) {
			alertKeys[noticeKey] = true
		}
	}
	return alertKeys, ackErr
}
