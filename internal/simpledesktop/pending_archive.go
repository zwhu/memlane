package simpledesktop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo/ui"
)

type pendingArchiveRow struct {
	Agent   string         `json:"agent_id"`
	Host    string         `json:"host_session_key"`
	At      time.Time      `json:"archived_at"`
	Entries map[string]int `json:"entries"`
}

func (a *App) pendingArchivePath(p AccessProject) (string, error) {
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
	sum := sha256.Sum256([]byte(identity + "/" + p.ID + "/" + p.Dataset))
	return filepath.Join(filepath.Dir(base), fmt.Sprintf("pending-anomaly-archive-%x.json", sum)), nil
}
func (a *App) readPendingArchives(p AccessProject) ([]pendingArchiveRow, string) {
	path, err := a.pendingArchivePath(p)
	// An unconfigured fixture has no archive; actual persistence still fails closed.
	if err != nil {
		return nil, ""
	}
	if info, e := os.Lstat(path); e == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, "历史异常读取失败，已保留待处理提醒"
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, ""
	}
	var rows []pendingArchiveRow
	if err != nil || json.Unmarshal(b, &rows) != nil {
		return nil, "历史异常读取失败，已保留待处理提醒"
	}
	seen := map[string]bool{}
	for _, r := range rows {
		key := r.Agent + "/" + r.Host
		if r.Agent == "" || r.Host == "" || r.At.IsZero() || len(r.Entries) == 0 || seen[key] {
			return nil, "历史异常读取失败，已保留待处理提醒"
		}
		seen[key] = true
		for id, n := range r.Entries {
			if !validPendingHash(id) || n < 1 {
				return nil, "历史异常读取失败，已保留待处理提醒"
			}
		}
	}
	return rows, ""
}
func validPendingHash(s string) bool { b, e := hex.DecodeString(s); return e == nil && len(b) == 32 }
func pendingIDCounts(s PendingMaterials) (map[string]int, bool) {
	if s.Unknown < 1 || s.Pending < s.Unknown || len(s.UnknownEntryIDs) != s.Unknown {
		return nil, false
	}
	counts := map[string]int{}
	for _, id := range s.UnknownEntryIDs {
		if !validPendingHash(id) {
			return nil, false
		}
		counts[id]++
	}
	return counts, true
}
func (q *projectQueueState) activePending(row queuedSession) PendingMaterials {
	s := row.View.Status
	counts, ok := pendingIDCounts(s)
	if !ok || q.ArchiveError != "" {
		return s
	}
	for _, r := range q.Archives {
		if r.Agent != row.Agent.ID || r.Host != row.Binding.HostSessionKey {
			continue
		}
		for id, n := range counts {
			hidden := min(n, r.Entries[id])
			s.Pending -= hidden
			s.Unknown -= hidden
		}
		break
	}
	return s
}
func (a *App) archivePending(q *projectQueueState, p AccessProject, row queuedSession) {
	if !row.View.Loaded || row.View.ReadError != "" {
		return
	}
	counts, ok := pendingIDCounts(row.View.Status)
	if !ok {
		q.ArchiveError = "记录标识尚未完整读取，请刷新后再归档"
		return
	}
	path, err := a.pendingArchivePath(p)
	if err != nil {
		q.ArchiveError = "归档未保存，原提醒仍保留"
		return
	}
	rows, message := a.readPendingArchives(p)
	if message != "" {
		q.ArchiveError = message
		return
	}
	found := false
	for n := range rows {
		r := &rows[n]
		if r.Agent == row.Agent.ID && r.Host == row.Binding.HostSessionKey {
			for id, c := range counts {
				r.Entries[id] = max(c, r.Entries[id])
			}
			r.At = time.Now()
			found = true
			break
		}
	}
	if !found {
		rows = append(rows, pendingArchiveRow{Agent: row.Agent.ID, Host: row.Binding.HostSessionKey, At: time.Now(), Entries: counts})
	}
	b, err := json.Marshal(rows)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), 0700)
	}
	var f *os.File
	if err == nil {
		f, err = os.CreateTemp(filepath.Dir(path), ".pending-archive-*")
	}
	if err == nil {
		defer os.Remove(f.Name())
		err = f.Chmod(0600)
		if err == nil {
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
	}
	if err != nil {
		q.ArchiveError = "归档未保存，原提醒仍保留"
		return
	}
	delete(q.NotificationFailures, "write/"+row.Agent.ID+"/"+row.Binding.HostSessionKey)
	q.Archives = rows
	q.ArchiveError = ""
}
func (a *App) pendingArchiveHistory(c *ui.Context, q *projectQueueState) {
	if q.ArchiveError != "" {
		homeText(c, q.ArchiveError, homeMeta)
	}
	if len(q.Archives) == 0 {
		return
	}
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		label := fmt.Sprintf("历史保存异常 · %d 个会话", len(q.Archives))
		projectLink(c, label, func() { q.ArchiveHistoryOpen = !q.ArchiveHistoryOpen })
		if !q.ArchiveHistoryOpen {
			return
		}
		ui.Column(c).FillWidth().Padding(14, 20).Gap(10).Border(1, homeBorder).Radius(8).Children(func() {
			homeText(c, "已归档的记录仍无法确认是否保存；原材料保留，不重复发送。", homeMeta)
			for _, r := range q.Archives {
				count := 0
				for _, n := range r.Entries {
					count += n
				}
				ui.Column(c).FillWidth().Gap(4).Children(func() {
					homeText(c, "会话 "+shortSessionLabel(r.Host), homeBody).FontSize(12).Tooltip(r.Host)
					homeText(c, fmt.Sprintf("%d 条 · 已归档 · 保存结果未知 · %s", count, r.At.Local().Format("01月02日 15:04")), homeMeta)
				})
			}
		})
	})
}
