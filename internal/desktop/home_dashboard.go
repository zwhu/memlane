package desktop

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

// homeDashboard follows the current B composition. Counts keep their confirmed
// meaning until the different metrics in the latest candidate are resolved.
func (a *App) homeDashboard(c *ui.Context) {
	windowWidth, _ := c.Size()
	padding := float32(30)
	if windowWidth < 1000 {
		padding = 20
	}
	if a.homeActivity.Review.Full {
		a.homeReviewFull(c)
		return
	}
	if a.homeActivity.Full {
		a.homeActivityFull(c)
		return
	}
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Scroll(c).Key("overview").TrackScroll(&a.homeActivity.OverviewScroll).Grow(1).MinHeight(0).Padding(22, padding, 18, padding).Gap(16).Children(func() {
			ui.Column(c).Gap(4).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "记忆动态").FontSize(design.Heading).Bold()
					ui.Spacer(c)
					a.homeOrganizationButton(c)
				})
				ui.Text(c, "积累的知识，以及最近发生的变化。").FontSize(design.Small).TextColor(design.Muted)
			})
			a.homeTotals(c)
			if a.libraryError != "" {
				ui.Row(c).Gap(10).Children(func() {
					label := "数据暂不可用"
					if !a.overviewUpdated.IsZero() {
						label = "保留上次数据 · 更新于 " + a.overviewUpdated.Format("15:04")
					}
					ui.Text(c, label).FontSize(design.Small).TextColor(design.Muted)
					quietButton(c, "重试读取").OnClick(func() { a.refresh(false) })
				})
			}
			a.heatmap(c)
			if a.homeActivity.Review.Full {
				ui.Column(c).Gap(12).Children(func() { a.homeReviewPanel(c) })
			} else {
				ui.Row(c).AlignItems(ui.Stretch).Gap(24).Children(func() {
					ui.Column(c).Grow(2).MinWidth(0).Gap(0).Children(func() {
						if a.homeDate != "" {
							a.homeRecentKnowledge(c)
						} else {
							a.homeActivityPanel(c)
						}
					})
					ui.Column(c).Grow(1).MinWidth(0).Gap(8).Children(func() { a.homeReviewPanel(c) })
				})
			}
			a.homeProjects(c)
		})
		a.homePlanFooter(c)
	})
}

func (a *App) homeRecentKnowledge(c *ui.Context) {
	ui.Row(c).Wrap().Gap(6).AlignItems(ui.Center).Margin(0, 0, 10, 0).Children(func() {
		label := "最近新增知识"
		if a.homeDate != "" {
			label = a.homeDate + " 新增知识"
		}
		ui.Text(c, label).Bold()
		ui.Spacer(c)
		if a.homeDate != "" {
			quietButton(c, "回到最近新增").OnClick(func() { a.selectHomeDate("") })
		}
		quietButton(c, "查看全部知识").OnClick(func() {
			a.searchSequence++
			a.query, a.searchQuery = "", ""
			a.navigate("搜索", "")
			a.recordKind, a.activityDate = "knowledge", a.homeDate
			a.refresh(false)
		})
	})
	ui.Divider(c)
	recent := a.recent
	if a.visualFixture {
		recent = a.items
	}
	if len(recent) == 0 {
		title, description := "还没有知识", "保存一条知识，或在项目中整理已有记录。"
		switch {
		case a.libraryError != "":
			title, description = "知识暂不可用", "请重试读取，或选择其他日期。"
		case a.loading || (a.homeDate != "" && !a.homeRecentLoaded):
			title, description = "正在读取知识", "等待实际记录。"
		case !a.activityLoaded:
			title, description = "等待本地记忆服务", "知识将在服务连接后显示。"
		case a.homeDate != "":
			title, description = "这一天没有新增知识", "可以选择其他日期，或回到最近新增。"
		case a.overviewRaw != nil && *a.overviewRaw > 0:
			title, description = "已有原始记录，尚未形成知识", "可以从项目查看原始记录；项目整理可随时跳过。"
		case a.overviewKnowledge != nil && *a.overviewKnowledge > 0:
			title, description = "暂无日常新增知识", "已有知识可以从全局搜索或项目查看；导入数量单独统计。"
		}
		ui.Column(c).Padding(16, 0).Gap(8).Children(func() {
			ui.Text(c, title).Bold()
			ui.Text(c, description).FontSize(design.Small).TextColor(design.Muted)
			if a.homeDate == "" && a.libraryError == "" && !a.loading {
				quietButton(c, "新增知识").AlignSelf(ui.Start).OnClick(a.newMemory)
			}
		})
	}
	for i, item := range recent {
		if i >= 4 {
			break
		}
		a.homeKnowledgeRow(c, item)
	}
}

func (a *App) homeKnowledgeRow(c *ui.Context, item MemoryItem) {
	ui.Button(c.Key("home-knowledge-"+item.ID), "").Label(item.Title).Justify(ui.Start).Padding(9, 4).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Background(ui.Transparent).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons["全部记忆"]).FontSize(12).TextColor(design.Muted)
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Text(c, item.Title).FontWeight(600).SingleLine().MinWidth(0)
				ui.Text(c, item.Source+" · "+a.memoryScopeName(item)+fmt.Sprintf(" · v%d", item.Revision)).FontSize(10).TextColor(design.Muted).SingleLine().MinWidth(0)
			})
			ui.Text(c, homeTimestamp(item.CreatedAt)).FontSize(10).TextColor(design.Muted).Shrink(0)
		})
	}).OnClick(func() { a.openHomeKnowledge(item) })
}

func (a *App) openHomeKnowledge(item MemoryItem) {
	// Navigate first so a previous project's draft and reading context are stored;
	// selection is then bound to the actual dataset and stable knowledge identity.
	page, scope := "项目", item.Dataset
	if item.Category == "个人偏好" || item.Category == "通用经验" {
		page, scope = "通用知识", ""
	}
	a.navigate(page, scope)
	// An explicit record target takes precedence over the project's restored
	// entity/graph view. Keep ordinary project navigation restoration intact.
	if page == "项目" {
		a.browsing.View = "memories"
		a.browsing.Knowledge, a.browsing.Explorer = nil, nil
		a.browsing.Relation = ""
	}
	a.recordKind, a.activityDate = "knowledge", ""
	a.selectedID, a.selectedItem = item.ID, nil
	a.detailMode, a.editing = "view", false
	a.detailScroll.Y = 0
	a.message = ""
	a.conflictCurrent = nil
	if a.onSelect != nil {
		a.onSelect(item.ID)
	}
}

func homeTimestamp(t time.Time) string {
	if t.IsZero() {
		return "时间未知"
	}
	now := time.Now()
	if t.Local().Format("2006-01-02") == now.Format("2006-01-02") {
		return "今天 " + t.Local().Format("15:04")
	}
	if t.Year() == now.Year() {
		return t.Local().Format("01-02 15:04")
	}
	return t.Local().Format("2006-01-02")
}

func (a *App) homeProjects(c *ui.Context) {
	ui.Column(c).Key("home-projects").Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "项目中的知识").Bold()
			ui.Spacer(c)
			quietButton(c, "添加项目").OnClick(a.beginProjectCreation)
		})
		if len(a.projects) == 0 {
			ui.Text(c, "添加本地项目，分别管理知识与原始记录。").FontSize(design.Small).TextColor(design.Muted)
			return
		}
		for start := 0; start < len(a.projects); start += 2 {
			ui.Row(c).Gap(12).AlignItems(ui.Stretch).Children(func() {
				for i := start; i < min(start+2, len(a.projects)); i++ {
					p := a.projects[i]
					a.ensureProjectOverview(p.ID)
					overview := a.projectOverview(p.ID)
					count := "知识数量待读取"
					if overview.Ready {
						count = fmt.Sprintf("%d 条知识", overview.Knowledge)
					} else if overview.Error != "" {
						count = "知识数量暂不可用"
					}
					ui.Button(c.Key("home-project-"+p.ID), "").Label("项目中的知识："+p.Name).Grow(1).MinWidth(0).Justify(ui.Start).Padding(10, 14).Border(1, design.Border).Radius(7).Background(ui.Transparent).Children(func() {
						ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
							ui.Icon(c, icons["项目"]).FontSize(13).TextColor(design.Muted)
							ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
								ui.Text(c, p.Name).FontWeight(600).SingleLine().MinWidth(0)
								ui.Text(c, count).FontSize(10).TextColor(design.Muted)
							})
							ui.Text(c, "›").TextColor(design.Muted)
						})
					}).OnClick(func() { a.navigate("项目", p.ID) })
				}
				if start+1 >= len(a.projects) {
					ui.Box(c).Grow(1).MinWidth(0)
				}
			})
		}
	})
}
