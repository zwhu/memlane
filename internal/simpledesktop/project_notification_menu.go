package simpledesktop

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

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
			q = a.projectQueueStateFor(*target)
		}
		hidden := a.projectIsHidden(project)
		label := "隐藏项目"
		if hidden {
			label = "取消隐藏"
		}
		v4BuildMenu(m, []v4MenuItem{
			{
				Label:    "清空所有通知",
				Disabled: !a.access.Loaded || q == nil || !q.Loaded,
				OnSelect: func() {
					if a.Update == nil {
						if err := a.clearProjectNotifications(q, *target); err != nil {
							c.Toast(err.Error())
						} else {
							c.Toast("已清空「" + project.Name + "」的所有通知")
						}
						return
					}
					a.clearProjectNotificationsAsync(q, *target)
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

// Clear only the currently observed presentation state. Retain the knowledge
// baseline and issue fingerprints, so new knowledge and new issues still alert.
// Uses /v2/reading-state/mark-all-read (serialized with mark-read via
// knowledgeWriter) when supported, otherwise falls back to the atomic local
// JSON writer under knowledgeWriter.fileMu; a partial save reports a failure.
func (a *App) clearProjectNotifications(q *projectQueueState, p AccessProject) error {
	useServerReadingState := false
	var control ProjectControlClient
	if ctrl, ok := a.accessClient(); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		hasV2, probeErr := ctrl.probeFeature(ctx, v2FeatureReadingState)
		cancel()
		if probeErr != nil {
			return fmt.Errorf("通知未清空：%s", controlUserError(probeErr))
		}
		if hasV2 {
			useServerReadingState = true
			control = ctrl
		}
	}

	w := a.ensureKnowledgeWriter()
	var readPath string
	var read knowledgeReadState
	if !useServerReadingState {
		a.waitKnowledgeReadFlush()
		var err error
		readPath, err = a.knowledgeReadPath(p)
		if err != nil {
			return fmt.Errorf("通知未清空：客户端状态位置不可用")
		}
		w.fileMu.Lock()
		read, err = readKnowledgeState(readPath)
		w.fileMu.Unlock()
		if err != nil || (!read.Initialized && q.KnowledgeRead.Initialized) {
			return fmt.Errorf("通知未清空：已读状态无法读取")
		}
	}
	ackPath, err := a.noticeAcknowledgementPath(p)
	if err != nil {
		return fmt.Errorf("通知未清空：提醒处理记录位置不可用")
	}
	needs, info := a.messageNotices(q, p)
	now := time.Now().UTC()
	acknowledgements, message, writeErr := updateNoticeAcknowledgementsAtPath(ackPath, func(rows map[string]noticeAcknowledgement) {
		for _, n := range append(needs, info...) {
			if n.isProgressNotice() {
				continue
			}
			rows[noticeHash(n.Key)] = noticeAcknowledgement{
				Fingerprint: q.noticeFingerprint(n.Key), At: now, Deferred: n.NeedsUser,
			}
		}
	})
	if message != "" {
		q.NoticeAcknowledgementError = message
		return fmt.Errorf("通知未清空：提醒处理记录无法读取")
	}
	if writeErr != nil {
		q.NoticeAcknowledgementError = "提醒处理未保存，原提醒仍保留"
		return fmt.Errorf("通知未清空：提醒处理记录未保存")
	}
	q.NoticeAcknowledgements, q.NoticeAcknowledgementError = acknowledgements, ""
	if useServerReadingState {
		readingID, idErr := readingIdentity(Client{Config: a.Client.Config})
		if idErr != nil || readingID == "" {
			q.KnowledgeReadError = "已读状态未保存"
			q.StateVersion++
			return fmt.Errorf("异常提醒已清除，未读记忆状态未保存")
		}
		readPathForKey, _ := a.knowledgeReadPath(p)
		unreadSnapshot, catalogVersion := q.ServerUnreadSnapshot, q.KnowledgeCatalogVersion
		resp, markErr := a.enqueueKnowledgeMarkAllReadV2(readPathForKey, q, control, readingID, p.ID, p.Dataset, true)
		if markErr != nil {
			q.KnowledgeReadError = "已读状态未保存"
			q.StateVersion++
			return fmt.Errorf("异常提醒已清除，未读记忆状态未保存")
		}
		a.applyMarkAllReadReceipt(q, p.Dataset, resp, unreadSnapshot, catalogVersion)
		q.StateVersion++
		return nil
	}
	if read.Initialized {
		w.fileMu.Lock()
		latest, readErr := readKnowledgeState(readPath)
		if readErr == nil && latest.Initialized {
			read = latest
		}
		for id := range read.Read {
			read.Read[id] = true
		}
		writeErr := writeKnowledgeState(readPath, read)
		w.fileMu.Unlock()
		if writeErr != nil {
			q.KnowledgeReadError = "已读状态未保存"
			return fmt.Errorf("异常提醒已清除，未读记忆状态未保存")
		}
		q.KnowledgeRead, q.KnowledgeReadError = read, ""
		q.UnreadKnowledge = nil
		q.StateVersion++
	}
	return nil
}

// clearProjectNotificationsAsync executes clearProjectNotifications network and
// disk I/O off the UI thread using immutable UI-captured snapshots and serialized
// noticeAckMu + knowledgeWriter locking.
func (a *App) clearProjectNotificationsAsync(q *projectQueueState, p AccessProject) {
	if q == nil || a.Update == nil {
		return
	}
	authEpoch := a.authEpoch
	unreadSnapshot, catalogVersion := q.ServerUnreadSnapshot, q.KnowledgeCatalogVersion
	stateDir := a.readingStateDir()
	ctrl, hasCtrl := a.accessClient()
	readingID, _ := readingIdentity(Client{Config: a.Client.Config})
	readPath, readPathErr := a.knowledgeReadPath(p)
	ackPath, ackPathErr := a.noticeAcknowledgementPath(p)
	needs, info := a.messageNotices(q, p)
	qReadInitialized := q.KnowledgeRead.Initialized
	fingerprints := make(map[string]string, len(needs)+len(info))
	noticeMeta := make([]struct {
		Key       string
		NeedsUser bool
		Progress  bool
	}, 0, len(needs)+len(info))
	for _, n := range append(needs, info...) {
		fingerprints[n.Key] = q.noticeFingerprint(n.Key)
		noticeMeta = append(noticeMeta, struct {
			Key       string
			NeedsUser bool
			Progress  bool
		}{Key: n.Key, NeedsUser: n.NeedsUser, Progress: n.isProgressNotice()})
	}
	w := a.ensureKnowledgeWriter()
	go func() {
		useServer := false
		if hasCtrl {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			hasV2, probeErr := ctrl.probeFeature(ctx, v2FeatureReadingState)
			cancel()
			if probeErr != nil {
				a.Update(func() {
					if authEpoch != a.authEpoch {
						return
					}
					q.KnowledgeReadError = controlUserError(probeErr)
					q.StateVersion++
				})
				return
			}
			useServer = hasV2
		}
		var localRead knowledgeReadState
		if !useServer {
			a.waitKnowledgeReadFlush()
			if readPathErr != nil {
				a.Update(func() {
					if authEpoch != a.authEpoch {
						return
					}
					q.KnowledgeReadError = "通知未清空：客户端状态位置不可用"
					q.StateVersion++
				})
				return
			}
			w.fileMu.Lock()
			var err error
			localRead, err = readKnowledgeState(readPath)
			w.fileMu.Unlock()
			if err != nil || (!localRead.Initialized && qReadInitialized) {
				a.Update(func() {
					if authEpoch != a.authEpoch {
						return
					}
					q.KnowledgeReadError = "通知未清空：已读状态无法读取"
					q.StateVersion++
				})
				return
			}
		}
		if ackPathErr != nil {
			a.Update(func() {
				if authEpoch != a.authEpoch {
					return
				}
				q.NoticeAcknowledgementError = "提醒处理记录位置不可用"
				q.StateVersion++
			})
			return
		}
		now := time.Now().UTC()
		acknowledgements, message, writeAckErr := updateNoticeAcknowledgementsAtPath(ackPath, func(rows map[string]noticeAcknowledgement) {
			for _, n := range noticeMeta {
				if n.Progress {
					continue
				}
				rows[noticeHash(n.Key)] = noticeAcknowledgement{
					Fingerprint: fingerprints[n.Key], At: now, Deferred: n.NeedsUser,
				}
			}
		})
		if message != "" {
			a.Update(func() {
				if authEpoch != a.authEpoch {
					return
				}
				q.NoticeAcknowledgementError = message
				q.StateVersion++
			})
			return
		}
		if writeAckErr != nil {
			a.Update(func() {
				if authEpoch != a.authEpoch {
					return
				}
				q.NoticeAcknowledgementError = "提醒处理未保存，原提醒仍保留"
				q.StateVersion++
			})
			return
		}
		if useServer {
			if readingID == "" {
				a.Update(func() {
					if authEpoch != a.authEpoch {
						return
					}
					q.NoticeAcknowledgements, q.NoticeAcknowledgementError = acknowledgements, ""
					q.KnowledgeReadError = "已读状态未保存"
					q.StateVersion++
				})
				return
			}
			resp, markErr := a.enqueueKnowledgeMarkAllReadV2WithEpoch(readPath, stateDir, q, ctrl, readingID, p.ID, p.Dataset, authEpoch, true)
			a.Update(func() {
				if authEpoch != a.authEpoch {
					return
				}
				q.NoticeAcknowledgements, q.NoticeAcknowledgementError = acknowledgements, ""
				if markErr != nil {
					q.KnowledgeReadError = "已读状态未保存"
					q.StateVersion++
					return
				}
				a.applyMarkAllReadReceipt(q, p.Dataset, resp, unreadSnapshot, catalogVersion)
				q.StateVersion++
			})
			return
		}
		var writeErr error
		if localRead.Initialized {
			w.fileMu.Lock()
			if latest, rErr := readKnowledgeState(readPath); rErr == nil && latest.Initialized {
				localRead = latest
			}
			for id := range localRead.Read {
				localRead.Read[id] = true
			}
			writeErr = writeKnowledgeState(readPath, localRead)
			w.fileMu.Unlock()
		}
		a.Update(func() {
			if authEpoch != a.authEpoch {
				return
			}
			q.NoticeAcknowledgements, q.NoticeAcknowledgementError = acknowledgements, ""
			if writeErr != nil {
				q.KnowledgeReadError = "已读状态未保存"
				q.StateVersion++
				return
			}
			if localRead.Initialized {
				q.KnowledgeRead, q.KnowledgeReadError = localRead, ""
				q.UnreadKnowledge = nil
			}
			q.StateVersion++
		})
	}()
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
