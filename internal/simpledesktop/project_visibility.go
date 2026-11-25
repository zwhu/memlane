package simpledesktop

import (
	"encoding/json"
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/shell"
	"os"
	"path/filepath"
)

type projectVisibilityState struct {
	Version int             `json:"version"`
	Hidden  map[string]bool `json:"hidden_projects"`
	path    string
	loaded  bool
}

// Project/dataset identities survive credential refreshes. This is a local
// navigation preference; it does not alter the directory, permissions or data.
func (a *App) projectVisibilityPath() (string, error) {
	base := a.Config.ClientStateFile
	if base == "" {
		base = a.Config.ConfigFile
	}
	if base == "" {
		return "", fmt.Errorf("项目隐藏设置存储位置不可用")
	}
	return filepath.Join(filepath.Dir(base), "project-visibility.json"), nil
}

func readProjectVisibility(path string) (projectVisibilityState, error) {
	s := projectVisibilityState{Version: 1, Hidden: map[string]bool{}, path: path, loaded: true}
	if path == "" {
		return s, nil
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return s, fmt.Errorf("项目隐藏设置无法读取")
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil || json.Unmarshal(b, &s) != nil || s.Version != 1 || s.Hidden == nil {
		return projectVisibilityState{path: path, loaded: true}, fmt.Errorf("项目隐藏设置无法读取")
	}
	return s, nil
}

func (a *App) loadProjectVisibility() {
	path, _ := a.projectVisibilityPath()
	if !a.projectVisibility.loaded || a.projectVisibility.path != path {
		a.projectVisibility, _ = readProjectVisibility(path)
	}
}

func (a *App) projectIsHidden(p Project) bool {
	return a.projectVisibility.Hidden[p.ID+"/"+p.Dataset]
}

func (a *App) setProjectHidden(p Project, hidden bool) error {
	path, err := a.projectVisibilityPath()
	if err != nil {
		return err
	}
	// Reread at mutation to preserve decisions saved by another window.
	next, err := readProjectVisibility(path)
	if err != nil {
		return err
	}
	key := p.ID + "/" + p.Dataset
	if hidden {
		next.Hidden[key] = true
	} else {
		delete(next.Hidden, key)
	}
	b, err := json.Marshal(next)
	if err != nil || writePrivateJSONFile(path, b) != nil {
		return fmt.Errorf("项目隐藏设置未保存")
	}
	a.projectVisibility = next
	return nil
}

func writePrivateJSONFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".private-json-*")
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
		if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to replace symbolic link: %s", path)
		} else if statErr != nil && !os.IsNotExist(statErr) {
			return statErr
		}
		err = os.Rename(f.Name(), path)
	}
	return err
}

func (a *App) projectNotificationMenu(c *ui.Context, item ui.Element, project Project) {
	item.ContextMenu(func(m *ui.Menu) {
		var target *AccessProject
		for _, p := range a.access.Directory.Projects {
			if p.ID == project.ID && p.Dataset == project.Dataset {
				copy := p
				target = &copy
				break
			}
		}
		var q *projectQueueState
		if target != nil {
			q = a.existingProjectQueueState(*target)
		}
		hidden := a.projectIsHidden(project)
		label := "隐藏项目"
		if hidden {
			label = "取消隐藏"
		}
		v4BuildMenu(m, []v4MenuItem{
			{
				Label:    "清空未读",
				Disabled: !a.access.Loaded || q == nil || !q.Loaded,
				OnSelect: func() {
					if a.Update == nil {
						if err := a.clearProjectNotifications(q, *target); err != nil {
							c.Toast(err.Error())
						} else {
							c.Toast("已清空「" + project.Name + "」的未读记忆")
						}
						return
					}
					a.clearProjectNotificationsAsync(q, *target, func(err error) {
						if err != nil {
							c.Toast(err.Error())
						} else {
							c.Toast("已清空「" + project.Name + "」的未读记忆")
						}
					})
				},
			},
			{Separator: true},
			{
				Label: label,
				OnSelect: func() {
					if err := a.setProjectHidden(project, !hidden); err != nil {
						c.Toast(err.Error())
					} else if hidden {
						c.Toast("已恢复显示「" + project.Name + "」")
					} else {
						c.Toast("已隐藏「" + project.Name + "」，可从全部项目中恢复")
					}
				},
			},
		})
	})
}

func (a *App) applyMarkAllReadReceipt(q *projectQueueState, dataset string, resp v2ReadingStateMarkAllReadResponse, unreadSnapshot uint64, catalogVersion string) {
	q.KnowledgeReadError = ""
	if resp.StateRev < q.ServerUnreadStateRev || q.ServerUnreadSnapshot != unreadSnapshot || q.KnowledgeCatalogVersion != catalogVersion {
		return
	}
	q.ServerUnreadStateRev = resp.StateRev
	if !resp.unreadCountKnown() {
		return
	}
	q.ServerUnreadKnown = true
	q.ServerUnreadCount = resp.UnreadCount
	if resp.UnreadCount != 0 {
		return
	}
	q.UnreadHasMore = false
	q.UnreadKnowledge = nil
	for id := range q.KnowledgeRead.Read {
		q.KnowledgeRead.Read[id] = true
	}
	a.clearDatasetServerUnread(dataset)
}

func (a *App) clearLocalUnread(path string, initialized bool) (knowledgeReadState, error) {
	a.waitKnowledgeReadFlush()
	w := a.ensureKnowledgeWriter()
	w.fileMu.Lock()
	defer w.fileMu.Unlock()
	read, err := readKnowledgeState(path)
	if err != nil || (!read.Initialized && initialized) {
		return read, fmt.Errorf("已读状态无法读取")
	}
	if !read.Initialized {
		return read, nil
	}
	for id := range read.Read {
		read.Read[id] = true
	}
	return read, writeKnowledgeState(path, read)
}

func (a *App) clearProjectNotifications(q *projectQueueState, p AccessProject) error {
	return a.markProjectUnreadCleared(q, p, nil, nil)
}

func (a *App) clearProjectNotificationsAsync(q *projectQueueState, p AccessProject, completion ...func(error)) {
	var report func(error)
	if len(completion) > 0 {
		report = completion[0]
	}
	if a.Update != nil {
		_ = a.markProjectUnreadCleared(q, p, a.Update, report)
	}
}

func (a *App) markProjectUnreadCleared(q *projectQueueState, p AccessProject, dispatch func(func()), report func(error)) error {
	if q == nil {
		return nil
	}
	client, stateDir := a.Client, a.readingStateDir()
	control, managed := a.accessClient()
	fence := shell.CaptureFence(appShellAdapter{a})
	snapshot, catalog, initialized := q.ServerUnreadSnapshot, q.KnowledgeCatalogVersion, q.KnowledgeRead.Initialized
	writing := func() error {
		path, pathErr := knowledgeReadPath(stateDir, client, p)
		var read knowledgeReadState
		var receipt v2ReadingStateMarkAllReadResponse
		var err error
		if managed {
			var identity string
			identity, err = readingIdentity(Client{Config: client.Config})
			if err == nil && identity != "" {
				receipt, err = a.enqueueKnowledgeMarkAllReadV2WithEpoch(path, stateDir, q, control, identity, p.ID, p.Dataset, fence.AuthEpoch, true)
			} else {
				err = fmt.Errorf("读取身份暂不可用")
			}
		} else if pathErr != nil {
			err = pathErr
		} else {
			read, err = a.clearLocalUnread(path, initialized)
		}
		if err != nil {
			err = fmt.Errorf("未读状态未保存：%w", err)
		}
		apply := func() {
			if !fence.Valid(appShellAdapter{a}) {
				return
			}
			q.StateVersion++
			if report != nil {
				report(err)
			}
			if err != nil {
				return
			}
			if managed {
				a.applyMarkAllReadReceipt(q, p.Dataset, receipt, snapshot, catalog)
			} else if read.Initialized {
				q.KnowledgeRead, q.KnowledgeReadError = read, ""
				q.UnreadKnowledge = nil
			}
		}
		if dispatch != nil {
			dispatch(apply)
		} else {
			apply()
		}
		return err
	}
	if dispatch != nil {
		a.ensureKnowledgeWriter()
		go func() { _ = writing() }()
		return nil
	}
	return writing()
}
