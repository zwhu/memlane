package simpledesktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

func savedDocumentQA(body string) (string, string, bool) {
	if !strings.HasPrefix(strings.TrimSpace(body), "Session ID: ") {
		return "", "", false
	}
	_, rest, ok := strings.Cut(body, "\nQuestion: ")
	if !ok {
		return "", "", false
	}
	question, answer, ok := strings.Cut(rest, "\nAnswer: ")
	return strings.TrimSpace(question), strings.TrimSpace(answer), ok
}

func queuedDocumentTitle(item Item) string {
	if item.Document != nil {
		if title := readableDocumentTitle(*item.Document); title != "" {
			return title
		}
	}
	if question, _, ok := savedDocumentQA(item.Body); ok {
		return firstReadingSentence(question)
	}
	if item.BodyLoaded {
		if title := firstReadingSentence(item.Body); title != "" && !strings.HasPrefix(title, "Session ID:") {
			return title
		}
	}
	if title := readableName(item.Title); title != "" && !strings.HasPrefix(title, "Session ID:") {
		return title
	}
	return "原始文档 · " + displayDate(item.Date)
}

func (a *App) closeQueuedDocument(p AccessProject, q *projectQueueState) {
	if q.Reader != nil && q.Reader.readCancel != nil {
		q.Reader.readCancel()
	}
	q.Reader = nil
	delete(a.panes, "queue:"+p.ID)
}

func (a *App) queuedDocumentDetail(c *ui.Context, p AccessProject, q *projectQueueState) {
	s := q.Reader
	if s == nil {
		return
	}
	if s.Selected == nil {
		ui.Column(c).Fill().Background(tok.Bg).Padding(sp8).Gap(sp4).Children(func() {
			v4OutlineButton(c, "返回待处理内容", nil, func() { a.closeQueuedDocument(p, q) })
			ui.Text(c, "内容当前不可用，请返回后重新读取。").Font("system-ui").FontSize(fsFootnote).TextColor(tok.Text3)
		})
		return
	}
	item := s.Selected
	ui.Column(c).Fill().Background(tok.Bg).Children(func() {
		ui.Row(c).FillWidth().Padding(sp3, sp8).Gap(sp3).AlignItems(ui.Center).
			BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
			v4OutlineButton(c, "返回待处理内容", nil, func() { a.closeQueuedDocument(p, q) })
			ui.Spacer(c)
			v4SmallButton(c, "刷新提炼状态", v4IconRefresh, func() { a.loadProjectQueue(q, p) }).Disabled(q.Loading)
		})
		ui.Scroll(c.Key("queue-detail-"+item.key())).Grow(1).MinHeight(0).Padding(sp6, sp8, sp10, sp8).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(readerMaxW).AlignSelf(ui.Center).Gap(sp6).Children(func() {
				// Header section (cardless, hairline divider).
				ui.Column(c).FillWidth().Gap(sp2).Padding(0, 0, sp4, 0).
					BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
					ui.Text(c, queuedDocumentTitle(*item)).Font("system-ui").FontSize(fsTitle).FontWeight(600).TextColor(tok.Text1).LineHeight(1.35).MaxLines(3)
					ui.Row(c).AlignItems(ui.Center).Gap(sp2).Children(func() {
						ui.Text(c, p.Name+" · 原始文档 · "+displayDate(item.Date)).Font("system-ui").FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3)
						if q.DocumentError == "" && containsReadingItem(q.Documents, item.key()) {
							v4Badge(c, "未完成提炼", "accent")
						}
					})
					if q.DocumentError != "" {
						ui.Text(c, q.DocumentError).Font("system-ui").FontSize(fsFootnote).TextColor(tok.Warning)
					} else if containsReadingItem(q.Documents, item.key()) {
						ui.Text(c, "原文已采集，尚无提炼完成记录。可在下方核对项目提炼情况。").Font("system-ui").FontSize(fsFootnote).TextColor(tok.Text3).MaxLines(2)
					} else {
						ui.Text(c, "待提炼清单已变化，请返回核对最新结果。").Font("system-ui").FontSize(fsFootnote).TextColor(tok.Text3)
					}
				})

				// Body section (cardless V4 reader).
				ui.Column(c).FillWidth().Gap(sp3).Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
						ui.Text(c, "原文").Font("system-ui").FontSize(fsCaption).FontWeight(600).TextColor(tok.Text3).SingleLine()
						ui.Spacer(c)
						v4SmallButton(c, "复制原文", v4IconCopy, func() { c.WriteClipboard(item.Body); a.copyFeedback("原文") }).Disabled(!item.BodyLoaded)
					})
					if s.Reading {
						ui.Text(c, "正在读取原文…").Font("system-ui").FontSize(fsFootnote).TextColor(tok.Text3)
					}
					if s.Error != "" {
						ui.Row(c).AlignItems(ui.Center).Gap(sp2).Children(func() {
							ui.Text(c, "原文读取失败，请重试。").Font("system-ui").FontSize(fsFootnote).TextColor(tok.Warning)
							v4SmallButton(c, "重试读取", v4IconRefresh, func() { a.selectItem(s, *item) }).Disabled(s.Reading)
						})
					}
					if item.BodyLoaded {
						mdOpts := a.v4MarkdownOptions(fsReading)
						if question, answer, ok := savedDocumentQA(item.Body); ok {
							ui.Text(c, "用户").Font("system-ui").FontSize(fsCaption).FontWeight(600).TextColor(tok.Text3)
							markdown.View(c.Key("queue-question-"+item.key()), question, mdOpts)
							sourceAgentLabel(c, *item)
							markdown.View(c.Key("queue-answer-"+item.key()), answer, mdOpts)
						} else {
							markdown.View(c.Key("queue-body-"+item.key()), item.Body, mdOpts)
						}
					}
				})

				// Project recent activity section (hairline top divider + hairline rows).
				ui.Column(c).FillWidth().Gap(0).Padding(sp4, 0, 0, 0).
					BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
					ui.Column(c).FillWidth().Gap(sp1).Padding(0, 0, sp2, 0).Children(func() {
						ui.Text(c, "项目近期提炼").Font("system-ui").FontSize(fsCaption).FontWeight(600).TextColor(tok.Text3)
						ui.Text(c, "以下为项目级提炼记录，文档完成状态以顶部核对结果为准。").Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3)
					})
					if q.ActivityError != "" {
						ui.Text(c, q.ActivityError).Font("system-ui").FontSize(fsFootnote).TextColor(tok.Warning)
					} else if len(q.Activity) == 0 {
						ui.Text(c, "暂未读到项目提炼记录。可刷新提炼状态继续核对。").Font("system-ui").FontSize(fsFootnote).TextColor(tok.Text3)
					}
					for idx, event := range q.Activity[:min(3, len(q.Activity))] {
						dotTone := "ok"
						if event.State == "失败" {
							dotTone = "fail"
						} else if event.State == "进行中" {
							dotTone = "run"
						}
						desc := ""
						if event.State == "失败" {
							desc = eventFailureReason(event)
						}
						v4Row(c, v4RowOptions{
							Key:   "queue-activity-" + item.key() + "-" + string(rune('0'+idx)),
							Title: event.Title + " · " + event.State,
							Desc:  desc,
							Value: event.At.Local().Format("01-02 15:04"),
							Leading: func() {
								v4StatusDot(c, dotTone)
							},
						})
					}
				})
			})
		})
	})
}
