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

var (
	noticeAckMu       sync.Mutex
	noticeAckWriteSeq = map[string]uint64{}
)

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
	noticeAckWriteSeq[path]++
	return rows, "", nil
}

func (a *App) readNoticeAcknowledgements(p AccessProject) (map[string]noticeAcknowledgement, string) {
	path, _ := a.noticeAcknowledgementPath(p)
	rows, message, _ := readNoticeAcknowledgementsWithSeq(path)
	return rows, message
}

func readNoticeAcknowledgementsWithSeq(path string) (map[string]noticeAcknowledgement, string, uint64) {
	if path == "" { // Fixtures without a configured state path have no decisions.
		return map[string]noticeAcknowledgement{}, "", 0
	}
	noticeAckMu.Lock()
	defer noticeAckMu.Unlock()
	rows, message := readNoticeAcknowledgementsAtPathLocked(path)
	return rows, message, noticeAckWriteSeq[path]
}

// The epoch preserves newer in-memory decisions and errors. The path sequence
// also covers successful writes made by the project notification menu.
func (q *projectQueueState) applyNoticeAcknowledgementRead(path string, epoch, seq uint64, rows map[string]noticeAcknowledgement, message string) {
	noticeAckMu.Lock()
	defer noticeAckMu.Unlock()
	if q.noticeAckEpoch == epoch && noticeAckWriteSeq[path] == seq {
		q.NoticeAcknowledgements, q.NoticeAcknowledgementError = rows, message
	}
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
	q.noticeAckEpoch++
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
	q.noticeAckEpoch++
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

func (a *App) unacknowledgedNotificationKeys(q *projectQueueState, p AccessProject) (map[string]bool, bool) {
	alertKeys := map[string]bool{}
	if q == nil {
		if p.policySyncUnconfirmed() {
			alertKeys["policy-sync"] = true
		}
		return alertKeys, false
	}
	needs, info := a.messageNotices(q, p)
	anomalies, _ := partitionInfoNotices(info)
	for _, n := range append(needs, anomalies...) {
		if !q.noticeAcknowledged(n.Key) {
			alertKeys[n.Key] = true
		}
	}
	return alertKeys, q.NoticeAcknowledgementError != ""
}
