package simpledesktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

func projectOverlayMotion() ui.ElementTransition {
	return v4ModalTransition()
}

const appFooterHeight float32 = 48

func appFooter(c *ui.Context, label string) ui.Element {
	return ui.Row(c).Label(label).Height(appFooterHeight).Shrink(0).Padding(0, sp4).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).AlignItems(ui.Center)
}

type appLayoutState struct{ ProjectListWidth, CommonListWidth float32 }

func readingSplit(c *ui.Context, key string, width *float32, first, second func()) {
	if *width == 0 {
		*width = listPaneW
	}
	ui.Split(c.Key(key), width, first, second).Grow(1).MinHeight(0).FillWidth()
}

// Official List owns selection, keyboard navigation, virtualization and scrolling.
func (a *App) itemList(c *ui.Context, s *pane, common bool) {
	s.ListSelection = -1
	listItem := s.Selected
	if sourceSession(s) && s.SourceReturn == nil {
		listItem = &s.History[len(s.History)-1]
	}
	for n, item := range s.Items {
		if listItem != nil && listItem.key() == item.key() {
			s.ListSelection = n
			break
		}
	}
	s.WidgetList.Key = func(n int) any { return s.Items[n].key() }
	s.WidgetList.Label = func(n int) string {
		if s.Items[n].Session != nil {
			return sessionIdentity(s.Items[n].ID)
		}
		return s.Items[n].Title
	}
	s.WidgetList.Selected = &s.ListSelection
	if s.Selected != nil && s.RevealItem == s.Selected.key() && s.ListSelection >= 0 {
		s.WidgetList.ScrollTo(s.ListSelection, ui.Center)
		s.RevealItem = ""
	}
	ui.Themed(c, listTheme).Grow(1).MinHeight(0).FillWidth().Background(ui.Transparent).Children(func() {
		ui.List(c.Key("items-"+a.routeKey()), &s.WidgetList, len(s.Items), func(n int) {
			i := s.Items[n]
			ui.Column(c).FillWidth().Padding(10, sp3).Gap(6).MinWidth(0).Children(func() {
				if i.Session != nil {
					idLabel := sessionIdentity(i.ID)
					topic := strings.TrimSpace(i.Title)
					hasTopic := topic != "" && topic != i.ID && topic != "来源会话"
					// Reserve fixed 2-line title slot so async title hydration never
					// shifts following virtualized rows.
					ui.Column(c.Key("session-title-slot")).FillWidth().Height(36).Shrink(0).Justify(ui.Start).Children(func() {
						if hasTopic {
							v4InlineMarkdown(c, topic, fsFootnote, 500, 1.45, tok.Text1).MaxLines(2).Tooltip(idLabel)
						} else {
							ui.Text(c, idLabel).Font("system-ui").FontSize(fsFootnote).FontWeight(500).LineHeight(1.45).TextColor(tok.Text1).MaxLines(2).Tooltip(idLabel)
						}
					})
					ui.Row(c).FillWidth().MinWidth(0).Height(18).Shrink(0).AlignItems(ui.Center).Gap(sp2).Children(func() {
						agentLogo(c, recentAgentIcon(i), 16).Tooltip(idLabel)
						v4CategoryChip(c, "会话")
						if i.SessionSummary != "" {
							homeText(c, i.SessionSummary, homeReadableMeta).SingleLine().Grow(1).MinWidth(0)
						} else if hasTopic {
							homeText(c, idLabel, homeReadableMeta).SingleLine().Grow(1).MinWidth(0)
						} else {
							ui.Spacer(c)
						}
						homeText(c, displayDate(i.Date), homeReadableMeta).FontFeatures("tnum").SingleLine().Shrink(0)
					})
					if i.ReadingState == "failed" {
						projectLink(c, "标题读取失败 · 重试", func() { a.retryReadingTitle(s, i) })
					}
					return
				}
				v4InlineMarkdown(c, i.Title, fsFootnote, 500, 1.45, tok.Text1).MaxLines(2).Tooltip(i.Title)
				kind := memoryKindLabel(i.Kind)
				if common {
					kind = commonCategoryLabel(i.CommonCategory)
				}
				ui.Row(c).FillWidth().MinWidth(0).AlignItems(ui.Center).Gap(sp2).Children(func() {
					if a.itemIsUnread(i) {
						v4UnreadDot(c)
					}
					v4CategoryChip(c, kind)
					ui.Spacer(c)
					homeText(c, displayDate(i.Date), homeReadableMeta).FontFeatures("tnum").SingleLine().Shrink(0)
				})
				if i.ReadingState == "failed" {
					projectLink(c, "标题读取失败 · 重试", func() { a.retryReadingTitle(s, i) })
				}
			})
		}).Grow(1).MinHeight(0).TrackScroll(&s.ListScroll).Padding(sp1, sp2, sp3, sp2).Gap(2).OnChange(func() {
		if s.ListSelection < 0 || s.ListSelection >= len(s.Items) {
			return
		}
		if common {
			a.commonEdit.Open = false
		}
		a.projectUI.InfoOpen = false
		a.selectItem(s, s.Items[s.ListSelection])
	}).Children(func() {
		if s.Loaded && len(s.Items) == 0 && !s.Loading && s.Error == "" {
			message := "此项目暂无" + a.kind
			if common {
				message = "还没有通用记忆"
				if a.commonCategoryFilter() != "" {
					message = "当前分类还没有记忆"
				}
			}
			homeText(c, message, homeBody).Margin(16, listItemInset)
			if common {
				homeText(c, "可新增个人偏好或可复用的经验。", homeMeta).Margin(0, listItemInset)
			}
		}
	})
	})
}

func appToolbar(c *ui.Context, label string, fn func()) ui.Element {
	return ui.Toolbar(c, fn).Label(label).Height(62).Shrink(0).Padding(0, paneInset).Gap(10)
}
