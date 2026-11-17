package simpledesktop

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// The SDK owns focus, dismissal, theme and reduced-motion handling. This shared
// transition expresses opening only; it never advances a request or marks success.
func projectOverlayMotion() ui.ElementTransition {
	return ui.ElementTransition{Duration: 160 * time.Millisecond, Ease: ui.EaseOut, Enter: &ui.Motion{Y: 8, Scale: .98}}
}

const appFooterHeight float32 = 48

func appFooter(c *ui.Context, label string) ui.Element {
	return ui.Row(c).Label(label).Height(appFooterHeight).Shrink(0).Padding(0, 16).BorderWidth(1, 0, 0, 0).BorderColor(homeBorder).AlignItems(ui.Center)
}

type appLayoutState struct{ ProjectListWidth, CommonListWidth float32 }

func readingSplit(c *ui.Context, key string, width *float32, first, second func()) {
	if *width == 0 {
		*width = 300
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
	listInset := paneInset
	if !common {
		listInset = pageInset
	}
	ui.List(c.Key("items-"+a.routeKey()), &s.WidgetList, len(s.Items), func(n int) {
		i := s.Items[n]
		ui.Column(c).Padding(12, listItemInset).Gap(8).MinWidth(0).Children(func() {
			label := i.Title
			if i.Session != nil {
				label = sessionIdentity(i.ID)
			}

			ui.Row(c).FillWidth().MinWidth(0).AlignItems(ui.Center).Gap(8).Children(func() {
				display := label
				if i.Session != nil {
					icon := ui.Icon(c, recentAgentIcon(i)).FontSize(16).TextColor(c.Theme().Text).Tooltip(label)
					if n == s.ListSelection {
						icon.TextColor(c.Theme().AccentText)
					}
					if _, key, ok := strings.Cut(label, " · "); ok {
						display = key
					}
				}
				title := ui.Text(c, display).FontSize(12).MaxLines(2).Grow(1).MinWidth(0).Tooltip(label)
				if n == s.ListSelection {
					title.TextColor(c.Theme().AccentText)
				}
			})
			kind := memoryKindLabel(i.Kind)
			if i.Session != nil && i.Title != i.ID && i.Title != "来源会话" {
				text := ui.Text(c, "已保存首条："+i.Title).FontSize(11).MaxLines(2)
				if n == s.ListSelection {
					text.TextColor(c.Theme().AccentText)
				} else {
					text.TextColor(c.Theme().TextMuted)
				}
			}
			if i.Session != nil && i.SessionSummary != "" {
				text := ui.Text(c, i.SessionSummary).FontSize(10)
				if n == s.ListSelection {
					text.TextColor(c.Theme().AccentText)
				} else {
					text.TextColor(c.Theme().TextMuted)
				}
			}
			if common {
				kind = commonCategoryLabel(i.CommonCategory)
			}
			metaLabel := kind + " · " + displayDate(i.Date)
			if a.itemIsUnread(i) {
				metaLabel = "未读 · " + metaLabel
			}
			if i.Session != nil {
				metaLabel = sessionListMeta(i)
			}
			meta := ui.Text(c, metaLabel).FontSize(10).SingleLine()
			if n == s.ListSelection {
				meta.TextColor(c.Theme().AccentText)
			} else {
				meta.TextColor(c.Theme().TextMuted)
			}
			if i.ReadingState == "failed" {
				projectLink(c, "标题读取失败 · 重试", func() { a.retryReadingTitle(s, i) })
			}
		})
	}).Grow(1).MinHeight(0).TrackScroll(&s.ListScroll).Padding(0, listInset-listItemInset).Gap(6).OnChange(func() {
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
}

func appToolbar(c *ui.Context, label string, fn func()) ui.Element {
	return ui.Toolbar(c, fn).Label(label).Height(62).Shrink(0).Padding(0, paneInset).Gap(10)
}
