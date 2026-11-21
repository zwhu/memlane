package simpledesktop

import (
	"context"
	"encoding/json"
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
					if err := a.clearProjectNotifications(q, *target); err != nil {
						c.Toast(err.Error())
					} else {
						c.Toast("已清空「" + project.Name + "」的所有通知")
					}
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
// Uses /v2/reading-state/mark-all-read when supported, otherwise falls back to
// the existing atomic local JSON writer; a partial save reports a failure.
func (a *App) clearProjectNotifications(q *projectQueueState, p AccessProject) error {
	useServerReadingState := false
	var control ProjectControlClient
	if ctrl, ok := a.accessClient(); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if ctrl.hasFeature(ctx, v2FeatureReadingState) {
			useServerReadingState = true
			control = ctrl
		}
		cancel()
	}

	var readPath string
	var read knowledgeReadState
	if !useServerReadingState {
		var err error
		readPath, err = a.knowledgeReadPath(p)
		if err != nil {
			return fmt.Errorf("通知未清空：客户端状态位置不可用")
		}
		read, err = readKnowledgeState(readPath)
		if err != nil || (!read.Initialized && q.KnowledgeRead.Initialized) {
			return fmt.Errorf("通知未清空：已读状态无法读取")
		}
	}
	ackPath, err := a.noticeAcknowledgementPath(p)
	if err != nil {
		return fmt.Errorf("通知未清空：提醒处理记录位置不可用")
	}
	acknowledgements, message := a.readNoticeAcknowledgements(p)
	if message != "" {
		q.NoticeAcknowledgementError = message
		return fmt.Errorf("通知未清空：提醒处理记录无法读取")
	}
	needs, info := a.messageNotices(q, p)
	now := time.Now().UTC()
	for _, n := range append(needs, info...) {
		if n.isProgressNotice() {
			continue
		}
		acknowledgements[noticeHash(n.Key)] = noticeAcknowledgement{
			Fingerprint: q.noticeFingerprint(n.Key), At: now, Deferred: n.NeedsUser,
		}
	}
	b, err := json.Marshal(acknowledgements)
	if err != nil || writeNoticeAcknowledgements(ackPath, b) != nil {
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
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, markErr := control.readingStateMarkAllReadV2(ctx, v2ReadingStateMarkAllReadRequest{
			ReadingIdentity: readingID,
			ProjectID:       p.ID,
			DatasetID:       p.Dataset,
		})
		cancel()
		if markErr != nil {
			q.KnowledgeReadError = "已读状态未保存"
			q.StateVersion++
			return fmt.Errorf("异常提醒已清除，未读记忆状态未保存")
		}
		q.ServerUnreadKnown = true
		q.ServerUnreadCount = 0
		q.UnreadHasMore = false
		q.UnreadKnowledge = nil
		q.KnowledgeReadError = ""
		if q.KnowledgeRead.Read != nil {
			for id := range q.KnowledgeRead.Read {
				q.KnowledgeRead.Read[id] = true
			}
		}
		a.clearDatasetServerUnread(p.Dataset)
		q.StateVersion++
		return nil
	}
	if read.Initialized {
		for id := range read.Read {
			read.Read[id] = true
		}
		if err := writeKnowledgeState(readPath, read); err != nil {
			q.KnowledgeReadError = "已读状态未保存"
			return fmt.Errorf("异常提醒已清除，未读记忆状态未保存")
		}
		q.KnowledgeRead, q.KnowledgeReadError = read, ""
		q.UnreadKnowledge = nil
		q.StateVersion++
	}
	return nil
}
