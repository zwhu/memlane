package simpledesktop

import (
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
		if m.Item("清空所有通知").Disabled(!a.access.Loaded || q == nil || !q.Loaded).Chosen() {
			if err := a.clearProjectNotifications(q, *target); err != nil {
				c.Toast(err.Error())
			} else {
				c.Toast("已清空「" + project.Name + "」的所有通知")
			}
		}
		m.Separator()
		hidden := a.projectIsHidden(project)
		label := "隐藏项目"
		if hidden {
			label = "取消隐藏"
		}
		if m.Item(label).Chosen() {
			if err := a.setProjectHidden(project, !hidden); err != nil {
				c.Toast(err.Error())
			} else if hidden {
				c.Toast("已恢复显示「" + project.Name + "」")
			} else {
				c.Toast("已隐藏「" + project.Name + "」，可从全部项目中恢复")
			}
		}
	})
}

// Clear only the currently observed presentation state. Retain the knowledge
// baseline and issue fingerprints, so new knowledge and new issues still alert.
// Each file uses the existing atomic writer; a partial save reports a failure.
func (a *App) clearProjectNotifications(q *projectQueueState, p AccessProject) error {
	readPath, err := a.knowledgeReadPath(p)
	if err != nil {
		return fmt.Errorf("通知未清空：客户端状态位置不可用")
	}
	read, err := readKnowledgeState(readPath)
	if err != nil || (!read.Initialized && q.KnowledgeRead.Initialized) {
		return fmt.Errorf("通知未清空：已读状态无法读取")
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
	}
	return nil
}
