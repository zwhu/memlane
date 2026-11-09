package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

// This presentation uses discovery and runtime receipts. It never installs an
// Agent or advances preparation as a consequence of rendering a page.
func (a *App) firstLaunchView(c *ui.Context) {
	s := &a.firstLaunch
	ui.Column(c).Fill().Background(ui.Hex("#ffffff")).Children(func() {
		if a.firstLaunchStep() == "environment" && s.State.Stage == "discovering" {
			ui.Row(c).Shrink(0).Height(design.Toolbar).Padding(10, 24).Children(func() {
				ui.Spacer(c)
				if quietButton(c, "稍后准备").Key("first-launch-skip").OnClick(func() { a.cancelFirstLaunch(true) }).Shortcut(0, ui.KeyEscape) {
					a.cancelFirstLaunch(true)
				}
			})
			ui.Column(c).Grow(1).Justify(ui.Center).AlignItems(ui.Center).Gap(14).Children(func() {
				a.firstLaunchBlocks(c)
				ui.Text(c, s.State.Message).FontSize(14).TextColor(design.Muted)
			})
			return
		}
		ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
			a.firstLaunchSidebar(c)
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Row(c).Height(design.Toolbar).Shrink(0).Padding(0, 28).Gap(10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
					ui.Icon(c, icons["Agent"]).FontSize(15).TextColor(design.Ink)
					ui.Text(c, "首次使用").FontSize(design.Body)
					ui.Spacer(c)
					ui.Text(c, a.firstLaunchStatusLabel()).FontSize(design.Small).TextColor(design.Muted)
				})
				if a.firstLaunchStep() == "processing" {
					a.processingSettings(c)
				} else {
					ui.Scroll(c).Key("first-launch-content").Grow(1).MinHeight(0).Padding(28, 32).Gap(20).Children(func() {
						if a.firstLaunchStep() == "agents" {
							a.firstLaunchAgents(c)
						} else {
							switch s.State.Stage {
							case "results":
								a.firstLaunchResults(c)
							case "confirmation":
								a.firstLaunchConfirmation(c)
							case "ready":
								a.firstLaunchReady(c)
							default:
								a.firstLaunchProgress(c)
							}
						}
						if s.Error != "" {
							firstLaunchNotice(c, "状态保存", s.Error)
						}
					})
					a.firstLaunchFooter(c)
				}
			})
			a.firstLaunchSteps(c)
		})
	})
}

func firstLaunchStageLabel(stage string) string {
	labels := map[string]string{"results": "检查完成", "confirmation": "等待确认", "preparing": "准备中", "verifying": "验证中", "ready": "本机环境就绪", "failed": "尚未完成", "cancelling": "正在取消", "cancelled": "已取消", "interrupted": "准备已中断"}
	return labels[stage]
}

func (a *App) firstLaunchStatusLabel() string {
	switch a.firstLaunchStep() {
	case "agents":
		return "连接 Agent"
	case "processing":
		return a.processingToolbarStatus()
	default:
		return firstLaunchStageLabel(a.firstLaunch.State.Stage)
	}
}

func (a *App) firstLaunchSidebar(c *ui.Context) {
	s := &a.firstLaunch
	ui.Column(c).Width(design.Sidebar).Shrink(0).Background(design.SidebarFill).Padding(44, 24, 24, 24).Gap(8).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons["全部记忆"]).FontSize(22).TextColor(design.Ink)
			ui.Text(c, "Memlane").FontSize(20).Bold()
		})
		ui.Text(c, "让记忆留在工作里").FontSize(design.Small).TextColor(design.Muted).Padding(0, 0, 0, 32)
		ui.Spacer(c)
		ui.Icon(c, icons["Agent"]).FontSize(20).TextColor(design.Muted)
		ui.Text(c, "复用本机已有环境").FontSize(design.Body).Bold().Padding(8, 0, 0, 0)
		ui.Text(c, "兼容组件直接复用。\n准备与接入分别确认。").FontSize(design.Small).TextColor(design.Muted).LineHeight(1.8)
		ui.Checkbox(c.Key("first-launch-reduced"), &s.State.ReducedMotion, "减少动态效果").FontSize(design.Small).OnChange(a.persistFirstLaunch)
	})
}

func (a *App) firstLaunchSteps(c *ui.Context) {
	active := 0
	switch a.firstLaunchStep() {
	case "agents":
		active = 1
	case "processing":
		active = 2
	}
	ui.Column(c).Width(164).Shrink(0).Background(ui.Hex("#f8fafc")).BorderWidth(0, 0, 0, 1).BorderColor(design.Border).Children(func() {
		ui.Row(c).Height(design.Toolbar).Shrink(0).Padding(0, 20).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
			ui.Text(c, "准备进度").FontSize(design.Small).TextColor(design.Muted)
		})
		ui.Column(c).Padding(100, 12, 0, 12).Gap(12).Children(func() {
			for i, label := range []string{"准备环境", "连接 Agent", "配置记忆处理"} {
				fill, color := ui.Transparent, design.Muted
				if i == active {
					fill, color = design.Selected, design.Ink
				}
				ui.Row(c).Key(fmt.Sprintf("first-launch-step-%d", i)).Height(42).Padding(0, 10).Gap(10).Radius(6).Background(fill).AlignItems(ui.Center).Children(func() {
					mark := fmt.Sprint(i + 1)
					if i < active {
						mark = "✓"
					}
					ui.Column(c).Size(20, 20).Justify(ui.Center).AlignItems(ui.Center).Border(1, color).Radius(10).Children(func() { ui.Text(c, mark).FontSize(11).TextColor(color) })
					ui.Text(c, label).FontSize(design.Small).TextColor(color)
				})
			}
		})
	})
}

func firstLaunchHeading(c *ui.Context, title, lead string) {
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, title).FontSize(22).Bold()
		ui.Text(c, lead).FontSize(design.Body).TextColor(design.Muted).LineHeight(1.6)
	})
}
func firstLaunchSection(c *ui.Context, title, hint string, content func()) {
	ui.Column(c).Gap(10).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(13).Bold()
			ui.Spacer(c)
			ui.Text(c, hint).FontSize(design.Small).TextColor(design.Muted)
		})
		ui.Column(c).Border(1, design.Border).Radius(6).Children(content)
	})
}
func firstLaunchRow(c *ui.Context, key, title, subtitle, state string, icon *ui.SVG) {
	ui.Row(c).Key(key).MinHeight(54).Padding(9, 12).Gap(12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
		ui.Column(c).Size(28, 28).Shrink(0).Justify(ui.Center).AlignItems(ui.Center).Border(1, design.Border).Radius(5).Background(ui.Hex("#fafbfc")).Children(func() { ui.Icon(c, icon).FontSize(16).TextColor(design.Ink) })
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, title).FontSize(design.Body).Bold().SingleLine()
			ui.Text(c, subtitle).FontSize(design.Small).TextColor(design.Muted).LineHeight(1.3)
		})
		ui.Text(c, state).FontSize(design.Small).TextColor(design.Muted).SingleLine().Shrink(0)
	})
}
func firstLaunchNotice(c *ui.Context, title, message string) {
	ui.Column(c).Gap(5).Padding(12, 14).Background(ui.Hex("#f8fafc")).Radius(5).Children(func() {
		ui.Text(c, title).FontSize(design.Body).Bold()
		ui.Text(c, message).FontSize(design.Small).TextColor(design.Muted).LineHeight(1.6).Selectable()
	})
}

func (a *App) firstLaunchItem(name string) EnvironmentItem {
	for _, item := range a.firstLaunch.State.Items {
		if item.Name == name {
			return item
		}
	}
	return EnvironmentItem{Name: name, State: "not_verified", Evidence: "本次检测未取得此项结果"}
}
func (a *App) firstLaunchLocalItems() []EnvironmentItem {
	r := a.firstLaunch.Controller
	if r == nil {
		return nil
	}
	items := []EnvironmentItem{a.firstLaunchItem(r.Config.WorkerID), a.firstLaunchItem(r.Config.ModelID)}
	if r.Official != nil {
		for _, id := range []string{"cognee-service", "cognee-bge-zh", "cognee-codex-plugin"} {
			items = append(items, a.firstLaunchItem(id))
		}
	}
	return items
}
func (a *App) firstLaunchDownloadSummary() string {
	var bytes int64
	for _, item := range a.firstLaunchLocalItems() {
		bytes += item.Bytes
	}
	if bytes > 0 {
		return fmt.Sprintf("本机组件预计下载 %.2f MB", float64(bytes)/1e6)
	}
	return "本机组件按实际校验结果复用"
}

func (a *App) firstLaunchResults(c *ui.Context) {
	firstLaunchHeading(c, "让 Agent 用上记忆", "已检查本机环境。先准备记忆环境，再选择 Agent 并确认本轮使用范围。")
	firstLaunchSection(c, "本地 Agent", "安装、接入与可用分别核对", func() {
		for _, agent := range a.firstLaunch.State.Agents {
			item := a.firstLaunchItem(agent.Name)
			desc := "安装后可在 Agent 页重新检查"
			if agent.Path != "" {
				desc = "现有安装 · " + strings.TrimSpace(agent.Version)
				if agent.Agent == "codex" && agent.Error == "" && agent.CapabilityError == "" {
					desc += " · 项目内确认官方接入"
				} else if agent.CapabilityError != "" {
					desc += " · 此版本接入尚待适配"
				} else {
					desc += " · 接入在 Agent 页独立设置"
				}
			}
			firstLaunchRow(c, "first-launch-agent-"+agent.Agent, agent.Name, desc, environmentStateLabel(item.State), icons["Agent"])
		}
		if len(a.firstLaunch.State.Agents) == 0 {
			firstLaunchRow(c, "first-launch-no-agent", "尚未发现可用 Agent", "可以先准备记忆环境，之后重新检查", "尚未验证", icons["Agent"])
		}
	})
	firstLaunchSection(c, "记忆环境", "兼容组件直接复用", func() {
		for i, item := range a.firstLaunchLocalItems() {
			title := "原文检索 · Rust"
			if i == 1 {
				title = "向量模型"
			}
			switch item.Name {
			case "cognee-service":
				title = "Cognee 服务"
			case "cognee-bge-zh":
				title = "Cognee 本地向量模型"
			case "cognee-codex-plugin":
				title = "官方插件组件"
			}
			firstLaunchRow(c, fmt.Sprintf("first-launch-environment-component-%d", i), title, item.Name, environmentStateLabel(item.State), icons["全部记忆"])
		}
		item := a.firstLaunchItem("记忆服务")
		firstLaunchRow(c, "first-launch-environment-service", "本机记忆服务", "随 App 提供 helper，准备后核对当前服务", environmentStateLabel(item.State), icons["设置"])
		firstLaunchRow(c, "first-launch-environment-official", "官方 Cognee 服务", a.firstLaunchOfficialSummary(), "一起初始化", icons["Agent"])
	})
	a.firstLaunchDetails(c)
}

func (a *App) firstLaunchOfficialSummary() string {
	r := a.firstLaunch.Controller
	if r == nil || r.Official == nil {
		return "尚未配置官方组件来源 · 可在项目中绑定兼容服务"
	}
	return "服务、运行依赖与本地模型统一准备；提炼 Agent 之后单独选择和验证"
}

func (a *App) firstLaunchDetails(c *ui.Context) {
	s := &a.firstLaunch
	opened := s.Details["environment"]
	label := "查看检查详情"
	if opened {
		label = "收起检查详情"
	}
	quietButton(c, label).Key("first-launch-details").OnClick(func() {
		if s.Details == nil {
			s.Details = map[string]bool{}
		}
		s.Details["environment"] = !opened
	})
	if opened {
		ui.Column(c).Gap(12).Padding(14).Background(ui.Hex("#f8fafc")).Radius(5).Children(func() {
			for _, item := range s.State.Items {
				ui.Column(c).Gap(4).Children(func() {
					ui.Text(c, item.Name+" · "+environmentStateLabel(item.State)).FontSize(design.Small).Bold()
					ui.Text(c, item.Evidence).FontSize(design.Small).TextColor(design.Muted).Selectable().LineHeight(1.5)
					if item.Bytes > 0 {
						ui.Text(c, fmt.Sprintf("归档下载 %.2f MB", float64(item.Bytes)/1e6)).FontSize(design.Small).TextColor(design.Muted)
					}
				})
			}
		})
	}
}

func (a *App) firstLaunchConfirmation(c *ui.Context) {
	firstLaunchHeading(c, "确认这次准备的内容", "校验并复用兼容组件，只准备缺失或校验未通过的本机组件。")
	firstLaunchSection(c, "本机组件准备", "确认后开始下载与修改", func() {
		for i, item := range a.firstLaunchLocalItems() {
			title := "准备 Rust 原文检索"
			if i == 1 {
				title = "准备向量模型"
			}
			summary := "下载固定版本，校验签名与文件后安装"
			if item.State == "verified_reusable" || item.State == "prepared_verified" {
				summary = "复核已有文件，兼容时直接复用"
			}
			size := "按实际校验复用"
			if item.Bytes > 0 {
				size = fmt.Sprintf("%.2f MB", float64(item.Bytes)/1e6)
			}
			firstLaunchRow(c, fmt.Sprintf("first-launch-plan-component-%d", i), title, summary, size, icons["全部记忆"])
		}
		firstLaunchRow(c, "first-launch-plan-service", "启动并验证本机服务", "注册此 App 的 helper，核对服务与 Rust 健康回执", "随 App 提供", icons["设置"])
	})
	firstLaunchNotice(c, "准备后选择 Agent", "先选择要使用记忆的 Agent。项目可以跳过；首次任务再确认工作目录与本轮采集范围，有有效记忆后建立或复用项目。")
	firstLaunchNotice(c, "记忆列表从空白开始", "本次准备不会导入历史对话。导入已有记忆是 App 内的独立可选操作。")
	a.firstLaunchDetails(c)
}

func (a *App) firstLaunchProgress(c *ui.Context) {
	s, status := &a.firstLaunch, a.runtimeStatus
	active := s.State.Stage == "preparing" || s.State.Stage == "verifying"
	title, lead := "正在准备记忆环境", "完成当前组件准备后，核对本机服务与 Rust 的实际可用性。"
	if s.State.Stage == "verifying" {
		title = "正在验证记忆环境"
	}
	if !active {
		title, lead = "准备暂时停在这里", "已校验组件会保留。重新检查后，再确认需要继续的准备。"
	}
	if s.State.Stage == "cancelling" {
		title, lead = "正在取消本轮准备", "正在等待停止回执，完成后可以重新检查或稍后继续。"
	}
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Children(func() { firstLaunchHeading(c, title, lead) })
		if active {
			a.preparationBlocksSized(c, "first-launch-blocks", s.State.Stage, s.State.ReducedMotion, s.ChangedAt, 64, 64)
		}
	})
	if s.State.Attempt > 0 {
		checked := status.Total > 0 || status.Installed
		tasks := []struct {
			name, description string
			done, running     bool
		}{
			{"核对已确认组件", "读取真实签名清单，复核本机组件", checked, active && !checked},
			{"准备引擎与本地模型", "复用或下载、校验 Rust 与 Cognee 依赖", status.Installed, active && checked && !status.Installed},
			{"启动本机记忆服务", "注册当前 App helper，等待服务响应", false, active && status.Installed},
			{"验证服务与 Rust", "当前服务健康回执通过后才能开始使用", false, active && s.State.Stage == "verifying"},
		}
		ui.Column(c).Children(func() {
			for i, task := range tasks {
				state, mark := "等待", fmt.Sprint(i+1)
				if task.done {
					state, mark = "已完成", "✓"
				} else if task.running {
					state, mark = "进行中", "·"
				} else if !active {
					state = "尚未完成"
				}
				ui.Row(c).MinHeight(68).Padding(14, 0).Gap(14).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
					ui.Text(c, mark).Width(20).FontSize(14).TextColor(design.Muted)
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						ui.Text(c, task.name).FontSize(design.Body).Bold()
						ui.Text(c, task.description).FontSize(design.Small).TextColor(design.Muted)
					})
					ui.Text(c, state).FontSize(design.Small).TextColor(design.Muted).SingleLine()
				})
			}
		})
	}
	if active && status.Total > 0 {
		ui.Column(c).Gap(8).Children(func() {
			ui.Row(c).Children(func() {
				ui.Text(c, "当前组件准备进度").FontSize(design.Small).TextColor(design.Muted)
				ui.Spacer(c)
				ui.Text(c, fmt.Sprintf("%.2f / %.2f MB", float64(status.Downloaded)/1e6, float64(status.Total)/1e6)).Key("first-launch-bytes").FontSize(design.Small)
			})
			ui.Column(c).Height(3).Draw(func(p *ui.Painter, r ui.Rect) {
				p.Fill(r, design.Border, 1)
				r.W *= float32(max(0, min(1, float64(status.Downloaded)/float64(status.Total))))
				p.Fill(r, design.Muted, 1)
			})
		})
	}
	firstLaunchNotice(c, firstLaunchStageLabel(s.State.Stage), s.State.Message)
}

func (a *App) firstLaunchReady(c *ui.Context) {
	s := &a.firstLaunch
	ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
		a.preparationBlocksSized(c, "first-launch-blocks", "ready", s.State.ReducedMotion, s.ChangedAt, 64, 64)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			firstLaunchHeading(c, "本机记忆环境已就绪", "本机服务与 Rust 已验证可用。下一步连接 Agent；也可以稍后设置。")
		})
	})
	firstLaunchSection(c, "本机记忆", "来自当前服务健康回执", func() {
		firstLaunchRow(c, "first-launch-ready-local", "记忆服务与原文检索", "本机组件校验与当前服务验证已通过", "实际可用", icons["全部记忆"])
		if a.firstLaunch.Controller != nil && a.firstLaunch.Controller.Official != nil {
			firstLaunchRow(c, "first-launch-ready-official", "Cognee 服务与本地依赖", "已统一准备；提炼能力在首次任务中真实验证", "服务已就绪", icons["Agent"])
		} else {
			firstLaunchRow(c, "first-launch-ready-official", "官方采集服务", "尚未配置组件来源", "尚未验证", icons["Agent"])
		}
	})
	firstLaunchNotice(c, "环境步骤完成", "准备环境不会创建项目、执行编码任务、采集历史聊天或等待记忆结果。")
	a.firstLaunchDetails(c)
}

func (a *App) firstLaunchAgents(c *ui.Context) {
	firstLaunchHeading(c, "连接 Agent", "把 Memlane 配置到这台电脑上的编码 Agent。项目、任务范围和处理记忆的执行者在各自步骤确认。")
	firstLaunchSection(c, "本机 Agent", "连接可稍后完成，不阻塞进入记忆处理配置", func() {
		if a.agents.Loading && len(a.agents.Found) == 0 {
			firstLaunchRow(c, "first-launch-agent-loading", "正在发现 Agent", "只检查已知安装位置、PATH 和用户已选路径", "检查中", icons["Agent"])
		}
		for _, found := range a.agents.Found {
			var connection *records.Connection
			for index := range a.agents.Connections {
				candidate := &a.agents.Connections[index]
				if candidate.Agent == found.Agent && candidate.Enabled {
					connection = candidate
					break
				}
			}
			state, detail := a.agentStatus(found, connection)
			version := found.Version
			if version == "" {
				version = "版本待确认"
			}
			firstLaunchRow(c, "first-launch-setup-agent-"+found.Agent, found.Name, version+" · "+detail, state, icons["Agent"])
		}
		if !a.agents.Loading && len(a.agents.Found) == 0 {
			firstLaunchRow(c, "first-launch-agent-empty", "尚未发现 Agent", "可以稍后从设置连接，不会创建空项目", "未连接", icons["Agent"])
		}
	})
	quietButton(c, "打开现有 Agent 连接页").Key("first-launch-agent-settings").OnClick(a.openFirstLaunchAgentSettings)
	firstLaunchNotice(c, "本轮生产边界", "电脑级批量连接新稿仍待实现；此步骤只展示真实发现与现有连接，不把已安装当成已连接。")
}

func (a *App) firstLaunchFooter(c *ui.Context) {
	s := &a.firstLaunch
	ui.Row(c).Key("first-launch-footer").MinHeight(74).Shrink(0).Padding(16, 28).Gap(14).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			text, hint := a.firstLaunchDownloadSummary(), "下一步确认准备，当前还未修改环境。"
			if a.firstLaunchStep() == "agents" {
				text, hint = "Agent 可以稍后连接", "继续后选择并验证一种记忆处理方式。"
			}
			switch s.State.Stage {
			case "confirmation":
				hint = "本次仅准备本机环境；Agent 接入在项目中确认。"
			case "preparing", "verifying":
				text, hint = "请保持 App 打开", "完成真实验证后进入下一步。"
			case "ready":
				text, hint = "本机环境可用", "选择 Agent，继续确认首次使用。"
			case "failed", "cancelled", "interrupted", "cancelling":
				text, hint = firstLaunchStageLabel(s.State.Stage), "当前仍不能开始使用。"
			}
			ui.Text(c, text).FontSize(design.Small).Bold()
			ui.Text(c, hint).FontSize(design.Small).TextColor(design.Muted)
		})
		if a.firstLaunchStep() == "agents" {
			quietButton(c, "稍后设置").Key("first-launch-skip").OnClick(func() { a.cancelFirstLaunch(true) })
		} else if s.State.Stage == "confirmation" {
			quietButton(c, "返回").Key("first-launch-back").OnClick(func() { s.State.Stage = "results"; a.persistFirstLaunch() })
		} else if s.State.Stage != "ready" {
			quietButton(c, "稍后准备").Key("first-launch-skip").OnClick(func() { a.cancelFirstLaunch(true) })
		}
		if a.firstLaunchStep() == "agents" {
			firstLaunchPrimary(c, "继续配置记忆处理").Key("first-launch-processing").OnClick(a.advanceFirstLaunchToProcessing)
			return
		}
		switch s.State.Stage {
		case "results":
			firstLaunchPrimary(c, "继续").Key("first-launch-confirmation").OnClick(func() { s.State.Stage = "confirmation"; a.persistFirstLaunch() })
		case "confirmation":
			firstLaunchPrimary(c, "确认并准备本机环境").Key("first-launch-prepare").OnClick(a.prepareFirstLaunch)
		case "preparing", "verifying":
			firstLaunchPrimary(c, "取消准备").Key("first-launch-cancel").OnClick(func() { a.cancelFirstLaunch(false) })
		case "ready":
			firstLaunchPrimary(c, "继续连接 Agent").Key("first-launch-use").OnClick(a.advanceFirstLaunchToAgents)
		case "failed", "cancelled", "interrupted":
			firstLaunchPrimary(c, "重新检查").Key("first-launch-retry").OnClick(a.discoverFirstLaunch)
		}
	})
}

func firstLaunchPrimary(c *ui.Context, label string) ui.Element {
	return ui.Button(c, label).Height(34).Padding(7, 16).FontSize(design.Body).Background(design.Ink).TextColor(ui.Hex("#ffffff")).BorderWidth(0).Radius(5).Shrink(0)
}
