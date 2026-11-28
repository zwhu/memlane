package app

import (
	"fmt"
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

type appLayoutState struct{ ProjectListWidth, CommonListWidth, ProfileListWidth float32 }

func readingSplit(c *ui.Context, key string, width *float32, first, second func()) {
	if *width == 0 {
		*width = listPaneW
	}
	ui.Split(c.Key(key), width, first, second).Grow(1).MinHeight(0).FillWidth()
}

// Official List owns selection, keyboard navigation, virtualization and scrolling.
func (a *App) itemList(c *ui.Context, s *pane, common bool) {
	if !s.Loaded && s.Loading && len(s.Items) == 0 {
		ui.Column(c).Grow(1).MinHeight(0).Padding(sp2, sp4).Gap(sp3).Label("正在读取原始记录…").Children(func() {
			for n := 0; n < 5; n++ {
				ui.Column(c).FillWidth().Padding(12, sp2).Gap(8).Children(func() {
					ui.Box(c).FillWidth().Height(11).Radius(radiusSmall).Background(tok.Fill2)
					ui.Box(c).Width(240).Height(11).Radius(radiusSmall).Background(tok.Fill2)
					ui.Box(c).Width(140).Height(9).Radius(3).Background(tok.Fill1)
				})
			}
		})
		return
	}
	if s.Loaded && s.Error == "" && len(s.Items) == 0 {
		message := "此项目暂无" + a.kind
		if common {
			message = "还没有通用记忆"
			if a.commonCategoryFilter() != "" {
				message = "当前分类还没有记忆"
			}
		}
		v4EmptyState(c, "raw-empty", message, "")
		return
	}
	s.ListSelection = -1
	listItem := s.Selected
	if sourceSession(s) && s.SourceReturn == nil {
		listItem = &s.History[len(s.History)-1]
	} else if s.SubagentParent != nil {
		listItem = s.SubagentParent
	}
	for n, item := range s.Items {
		if listItem != nil && listItem.Key() == item.Key() {
			s.ListSelection = n
			break
		}
	}
	s.WidgetList.Key = func(n int) any { return s.Items[n].Key() }
	s.WidgetList.KeepOffset = true
	s.WidgetList.Label = func(n int) string {
		if s.Items[n].Session != nil {
			return itemSessionIdentity(s.Items[n])
		}
		return s.Items[n].Title
	}
	s.WidgetList.Selected = &s.ListSelection
	if s.Selected != nil && s.RevealItem == s.Selected.Key() && s.ListSelection >= 0 {
		s.WidgetList.ScrollTo(s.ListSelection, ui.Center)
		s.RevealItem = ""
	}
	ui.Themed(c, listTheme).Grow(1).MinHeight(0).FillWidth().Background(ui.Transparent).Children(func() {
		ui.List(c.Key("items-"+a.routeKey()), &s.WidgetList, len(s.Items), func(n int) {
			i := s.Items[n]
			itemKey := fmt.Sprintf("ritem-%d-%s", a.motionRuns.rawList, i.Key())
			col := ui.Column(c.Key(itemKey)).FillWidth().MinWidth(0).Padding(12, sp3).Gap(8)
			if !common {
				a.v4StaggerEnter(c, col, "ritem-"+i.Key(), n, a.motionRuns.rawList, false)
			}
			col.Children(func() {
				if i.Session != nil {
					idLabel := itemSessionIdentity(i)
					hasTopic := sessionHasTopic(i)
					// Reserve fixed 2-line title slot (12 * 1.55 * 2 = 37.2px <= 38px) so
					// async hydration or fallback placeholders never shift following rows.
					ui.Column(c.Key("session-title-slot")).FillWidth().Height(38).Shrink(0).Justify(ui.Start).Children(func() {
						switch {
						case hasTopic:
							v4InlineMarkdown(c, strings.TrimSpace(i.Title), fsFootnote, 400, 1.55, tok.Text1).MaxLines(2).Tooltip(idLabel)
						case i.ReadingState == "pending":
							ui.Column(c).FillWidth().Gap(8).Children(func() {
								ui.Box(c).FillWidth().Height(11).Radius(radiusSmall).Background(tok.Fill2)
								ui.Box(c).Width(180).Height(11).Radius(radiusSmall).Background(tok.Fill1)
							})
						default:
							fallback := sessionFallbackTitle(i)
							color := tok.Text2
							if i.Session.IsSubagent && subagentRoleLabel(i.Session) != "" {
								color = tok.Text1
							}
							ui.Text(c, fallback).Font(fontUI).FontSize(fsFootnote).FontWeight(400).LineHeight(1.55).TextColor(color).MaxLines(2).Tooltip(idLabel)
						}
					})
					ui.Row(c).FillWidth().MinWidth(0).Height(18).Shrink(0).AlignItems(ui.Center).Gap(sp2).Children(func() {
						agentLogo(c, recentAgentIcon(i), 16).Tooltip(idLabel)
						if i.Session.IsSubagent {
							v4SubagentChip(c, "子会话", subagentBadgeLabel(i.Session))
						} else {
							v4CategoryChip(c, "会话")
						}
						if len(i.Subagents) > 0 {
							v4SubagentChip(c, fmt.Sprintf("含 %d 子会话", len(i.Subagents)), fmt.Sprintf("关联了 %d 个子会话，点击右侧「子会话」标签查看", len(i.Subagents)))
						}
						summary := sessionDisplaySummary(i)
						if summary != "" {
							homeText(c, summary, homeReadableMeta).SingleLine().Grow(1).MinWidth(0)
						} else if hasTopic {
							homeText(c, idLabel, homeReadableMeta).SingleLine().Grow(1).MinWidth(0)
						} else {
							ui.Spacer(c)
						}
						ui.Text(c, displayDate(i.Date)).Font(fontUI).FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3).SingleLine().Shrink(0)
					})
					if i.ReadingState == "failed" {
						projectLink(c, "标题读取失败 · 重试", func() { a.retryReadingTitle(s, i) })
					}
					return
				}
				ui.Column(c.Key("raw-doc-title-slot")).FillWidth().Height(38).Shrink(0).Justify(ui.Start).Children(func() {
					v4InlineMarkdown(c, i.Title, fsFootnote, 400, 1.55, tok.Text1).MaxLines(2).Tooltip(i.Title)
				})
				kind := memoryKindLabel(i.Kind)
				if common {
					kind = commonCategoryLabel(i.CommonCategory)
				}
				ui.Row(c).FillWidth().MinWidth(0).Height(18).Shrink(0).AlignItems(ui.Center).Gap(sp2).Children(func() {
					if a.itemIsUnread(i) {
						v4UnreadDot(c)
					}
					agentLogo(c, recentAgentIcon(i), 16).Tooltip("来源 Agent：" + sourceAgentName(i))
					v4CategoryChip(c, kind)
					ui.Spacer(c)
					ui.Text(c, displayDate(i.Date)).Font(fontUI).FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3).SingleLine().Shrink(0)
				})
				if i.ReadingState == "failed" {
					projectLink(c, "标题读取失败 · 重试", func() { a.retryReadingTitle(s, i) })
				}
			})
		}).Label("原始记录列表").Grow(1).MinHeight(0).TrackScroll(&s.ListScroll).Padding(sp1, sp2, sp3, sp2).Gap(4).OnChange(func() {
			if s.ListSelection < 0 || s.ListSelection >= len(s.Items) {
				return
			}
			if common {
				a.commonEdit.Open = false
			}
			a.projectUI.InfoOpen = false
			a.selectItem(s, s.Items[s.ListSelection])
		})
	})
}

func appToolbar(c *ui.Context, label string, fn func()) ui.Element {
	return ui.Toolbar(c, fn).Label(label).Height(62).Shrink(0).Padding(0, paneInset).Gap(12)
}
