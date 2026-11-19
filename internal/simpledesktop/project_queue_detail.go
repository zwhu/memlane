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
		ui.Column(c).Fill().Padding(32).Gap(16).Children(func() {
			textButton(c, "返回待处理内容", func() { a.closeQueuedDocument(p, q) })
			homeText(c, "内容当前不可用，请返回后重新读取。", homeMeta)
		})
		return
	}
	item := s.Selected
	ui.Column(c).Fill().Background(ui.Hex("#f8fafc")).Children(func() {
		ui.Row(c).FillWidth().Padding(12, 32).Gap(12).AlignItems(ui.Center).Children(func() {
			textButton(c, "返回待处理内容", func() { a.closeQueuedDocument(p, q) })
			ui.Spacer(c)
			refreshAction(c, "刷新处理状态", func() { a.loadProjectQueue(q, p) }).Disabled(q.Loading)
		})
		ui.Scroll(c.Key("queue-detail-"+item.key())).Grow(1).MinHeight(0).Padding(12, 32, 32, 32).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(900).Gap(16).Children(func() {
				ui.Column(c).FillWidth().Padding(18).Gap(10).Background(ui.Hex("#ffffff")).Border(1, homeBorder).Shadow(0, 2, 8, 0, panelShadowInk).Radius(8).Children(func() {
					homeText(c, queuedDocumentTitle(*item), homeHeading).FontSize(20).MaxLines(3)
					homeText(c, p.Name+" · 原始文档 · "+displayDate(item.Date), homeMeta)
					if q.DocumentError != "" {
						homeText(c, q.DocumentError, homeMeta)
					} else {
						pending := containsReadingItem(q.Documents, item.key())
						if pending {
							ui.Row(c).Children(func() { queueBadge(c, "未完成整理", homeLinkInk) })
							homeText(c, "原文已保存，尚无整理完成记录。可在下方核对项目处理情况。", homeMeta).MaxLines(2)
						} else {
							homeText(c, "待整理清单已变化，请返回核对最新结果。", homeMeta)
						}
					}
				})
				ui.Column(c).FillWidth().Padding(18).Gap(10).Background(ui.Hex("#ffffff")).Border(1, homeBorder).Shadow(0, 2, 8, 0, panelShadowInk).Radius(8).Children(func() {
					row := ui.Row(c).AlignItems(ui.Center).Gap(4)
					row.Children(func() {
						homeText(c, "原文", homeHeading).FontSize(14)
						hoverCopyAction(c, row, "复制原文", func() { c.WriteClipboard(item.Body); a.copyFeedback("原文") }).Disabled(!item.BodyLoaded)
					})
					if s.Reading {
						homeText(c, "正在读取原文…", homeMeta)
					}
					if s.Error != "" {
						homeText(c, "原文读取失败，请重试。", homeMeta)
						refreshAction(c, "重试读取", func() { a.selectItem(s, *item) }).Disabled(s.Reading)
					}
					if item.BodyLoaded {
						if question, answer, ok := savedDocumentQA(item.Body); ok {
							homeText(c, "用户", homeMeta)
							markdown.View(c.Key("queue-question-"+item.key()), question, &a.md)
							sourceAgentLabel(c, *item)
							markdown.View(c.Key("queue-answer-"+item.key()), answer, &a.md)
						} else {
							markdown.View(c.Key("queue-body-"+item.key()), item.Body, &a.md)
						}
					}
				})
				ui.Column(c).FillWidth().Padding(18).Gap(10).Background(ui.Hex("#ffffff")).Border(1, homeBorder).Shadow(0, 2, 8, 0, panelShadowInk).Radius(8).Children(func() {
					homeText(c, "项目近期处理", homeHeading).FontSize(14)
					homeText(c, "以下为项目级处理记录，文档完成状态以顶部核对结果为准。", homeMeta)
					if q.ActivityError != "" {
						homeText(c, q.ActivityError, homeMeta)
					} else if len(q.Activity) == 0 {
						homeText(c, "暂未读到项目处理记录。可刷新处理状态继续核对。", homeMeta)
					}
					for _, event := range q.Activity[:min(3, len(q.Activity))] {
						homeText(c, event.Title+" · "+event.State+" · "+event.At.Local().Format("01-02 15:04"), homeBody).FontSize(12)
						if event.State == "失败" {
							homeText(c, eventFailureReason(event), homeMeta)
						}
					}
				})
			})
		})
	})
}
