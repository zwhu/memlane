package desktop

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

// Initialization and settings share the RuntimeController and its receipts.
// Discovery is read-only; rendering never creates a second preparation owner.
type EnvironmentUI struct {
	CheckedAt   time.Time
	Items       []EnvironmentItem
	Loading     bool
	Error       string
	Sequence    uint64
	Details     map[string]*bool
	Maintenance bool
}

func (a *App) ensureEnvironment() {
	if a.environment.CheckedAt.IsZero() {
		a.refreshEnvironment()
	}
}

func (a *App) adoptDetectedEnvironment(items []EnvironmentItem) {
	a.environment.Items = append([]EnvironmentItem(nil), items...)
	a.environment.CheckedAt = time.Now()
	a.environment.Error = ""
}

type environmentCard struct {
	Key, Title, Product, Purpose, Icon string
	Items                              []EnvironmentItem
	Bundled                            bool
}

func (a *App) environmentBusy() bool {
	switch a.runtimeStatus.Phase {
	case "checking", "downloading", "installing", "installed", "preparing", "verifying", "checking_availability":
		return true
	}
	return a.firstLaunch.Open && (a.firstLaunch.State.Stage == "cancelling" || a.firstLaunch.State.Stage == "preparing" || a.firstLaunch.State.Stage == "verifying")
}

func (a *App) refreshEnvironment() {
	s, setup := &a.environment, &a.firstLaunch
	if setup.Controller == nil || setup.Context == nil || setup.Dispatch == nil || s.Loading || a.environmentBusy() {
		return
	}
	s.Loading, s.Error = true, ""
	s.Sequence++
	sequence, controller, dispatch := s.Sequence, setup.Controller, setup.Dispatch
	ctx, cancel := context.WithTimeout(setup.Context, 40*time.Second)
	go func() {
		defer cancel()
		items, err := controller.DiscoverEnvironment(ctx)
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		dispatch(func() {
			if sequence != s.Sequence {
				return
			}
			s.Loading = false
			// A preparation started after discovery owns the newer component state.
			if a.environmentBusy() {
				return
			}
			if err != nil {
				s.Error = "环境检查未完成：" + err.Error()
				return
			}
			s.Items = items
			s.CheckedAt = time.Now()
			a.runtimeStatus.Components = nil
		})
	}()
}

func (a *App) environmentItems(setup bool) []EnvironmentItem {
	items := a.environment.Items
	if setup || items == nil {
		items = a.firstLaunch.State.Items
	}
	result := append([]EnvironmentItem(nil), items...)
	for i := range result {
		for _, receipt := range a.runtimeStatus.Components {
			if result[i].Name != receipt.ID {
				continue
			}
			result[i].State = receipt.Phase
			result[i].ExpectedBytes = receipt.ExpectedBytes
			result[i].OccupiedBytes = receipt.OccupiedBytes
			if receipt.Phase == "installed" {
				result[i].Bytes = 0
			}
		}
	}
	return result
}

func (a *App) environmentCards(setup bool) []environmentCard {
	items := a.environmentItems(setup)
	item := func(id string) EnvironmentItem {
		for _, value := range items {
			if value.Name == id {
				return value
			}
		}
		return EnvironmentItem{Name: id, State: "not_verified"}
	}
	engine := environmentCard{Key: "engine", Title: "Cognee 引擎", Purpose: "保存原始记录与知识", Icon: "lancedb"}
	runtime := environmentCard{Key: "database", Title: "原生运行依赖", Purpose: "运行本地记忆服务", Icon: "model", Items: []EnvironmentItem{item("helper / LaunchAgent")}}
	model := environmentCard{Key: "model", Title: "本地向量模型", Purpose: "为知识建立检索索引", Icon: "model"}
	if r := a.firstLaunch.Controller; r != nil {
		runtime.Items = append(runtime.Items, item(r.Config.WorkerID))
		model.Items = append(model.Items, item(r.Config.ModelID))
		if r.Config.CodeExtractorID != "" {
			runtime.Items = append(runtime.Items, item(r.Config.CodeExtractorID))
		}
		if r.Official != nil {
			engine.Items = append(engine.Items, item("cognee-service"), item("cognee-codex-plugin"))
			model.Items = append(model.Items, item("cognee-bge-zh"))
		}
	}
	return []environmentCard{engine, runtime, model}
}

func preparedEnvironmentItem(s string) bool {
	return s == "installed" || s == "verified_reusable" || s == "prepared_verified"
}

func environmentCardState(card environmentCard) string {
	if len(card.Items) == 0 {
		return "尚未验证"
	}
	if card.Bundled {
		if preparedEnvironmentItem(card.Items[0].State) {
			return "随 App 提供"
		}
		if card.Items[0].State == "missing" {
			return "组件缺失"
		}
		return "尚未验证"
	}
	all := true
	for _, item := range card.Items {
		switch item.State {
		case "failed", "validation_failed":
			return "尚未完成"
		case "downloading":
			return "下载中"
		case "installing":
			return "校验安装中"
		}
		all = all && preparedEnvironmentItem(item.State)
	}
	if all {
		return "已校验"
	}
	for _, item := range card.Items {
		if item.State == "not_verified" {
			return "尚未验证"
		}
	}
	return "待准备"
}

func environmentSize(bytes int64) string { return fmt.Sprintf("%.2f MB", float64(bytes)/1e6) }

func environmentNeedsPreparation(cards []environmentCard) bool {
	for _, card := range cards {
		if card.Bundled {
			continue
		}
		for _, item := range card.Items {
			if item.State == "needs_preparation" || item.State == "validation_failed" || item.State == "failed" {
				return true
			}
		}
	}
	return false
}

func (a *App) environmentSizeSummary(cards []environmentCard) string {
	var download, expected, occupied int64
	known, measured := true, true
	for _, card := range cards {
		if card.Bundled {
			continue
		}
		if len(card.Items) == 0 {
			known, measured = false, false
		}
		for _, item := range card.Items {
			download += item.Bytes
			expected += item.ExpectedBytes
			if item.State == "not_verified" || item.ExpectedBytes <= 0 {
				known = false
			}
			if item.OccupiedBytes == nil {
				measured = false
			} else {
				occupied += *item.OccupiedBytes
			}
		}
	}
	downloadText, sizeText := "下载量未知", "组件占用未知"
	if known {
		downloadText = "需下载 " + environmentSize(download)
		sizeText = "预计组件占用 " + environmentSize(expected)
	}
	if measured {
		sizeText = "已安装组件 " + environmentSize(occupied)
	}
	return downloadText + " · " + sizeText
}

func (a *App) environmentSettings(c *ui.Context) {
	ui.Scroll(c).Key("environment-content").TrackScroll(a.settingsScrollState("environment")).Grow(1).MinHeight(0).Padding(28, 36).Gap(20).Children(func() {
		a.environmentContent(c, false)
	})
	a.environmentFooter(c, false)
}

// Use the actual space left by this host; settings has no middle list.
func environmentContentLayout(width float32, setup bool) (available float32, narrow bool) {
	navigation := settingsNavigationWidth(width)
	if setup {
		navigation = 196 + 154
	}
	available = max(0, width-navigation-72)
	return available, available < 3*200+2*14
}

func (a *App) environmentContent(c *ui.Context, setup bool) {
	cards := a.environmentCards(setup)
	width, _ := c.Size()
	available, narrow := environmentContentLayout(width, setup)
	title := "环境与组件"
	if setup {
		title = "准备记忆环境"
	}
	settingsHeading(c, title, "组件按需准备，已有兼容项不重复下载。")
	stage, message := a.runtimeStatus.Phase, a.runtimeStatus.Message
	if setup {
		stage, message = a.firstLaunch.State.Stage, a.firstLaunch.State.Message
	}
	if stage == "ready" && !environmentNeedsPreparation(cards) {
		settingsNotice(c, "记忆环境已就绪 · 复用本机已有组件")
	} else if stage == "ready" {
		firstLaunchNotice(c, "组件需要准备", "部分组件缺失或未通过校验，请准备缺失组件后重新核对。")
	} else if message != "" {
		firstLaunchNotice(c, firstLaunchStageLabel(stage), message)
	}

	if narrow {
		ui.Column(c).Gap(14).Children(func() {
			for _, card := range cards {
				a.environmentCardView(c, card, 0, true)
			}
		})
	} else {
		ui.Row(c).Gap(14).AlignItems(ui.Stretch).Children(func() {
			for _, card := range cards {
				a.environmentCardView(c, card, (available-28)/3, false)
			}
		})
	}
	a.environmentDistribution(c, cards)
	if !setup {
		if a.environment.Loading {
			ui.Text(c, "正在核对环境…").FontSize(design.Small).TextColor(design.Muted)
		}
		if a.environment.Error != "" {
			firstLaunchNotice(c, "检查未完成", a.environment.Error)
		}
	}
}

func (a *App) environmentCardView(c *ui.Context, card environmentCard, width float32, narrow bool) {
	state := environmentCardState(card)
	container := ui.Column(c).Key("environment-card-"+card.Key).MinHeight(150).Padding(18).Gap(9).Border(1, ui.Hex("#dfe5eb")).Radius(8)
	if narrow {
		container.FillWidth().MinHeight(110)
	} else {
		container.Width(width)
	}
	container.Children(func() {
		ui.Text(c, card.Title).FontSize(13).Bold()
		ui.Text(c, card.Purpose).FontSize(design.Small).TextColor(design.Muted).LineHeight(1.5)
		if !narrow {
			ui.Spacer(c)
		}
		ui.Text(c, state).Key("environment-state-" + card.Key).FontSize(design.Small).TextColor(design.Muted)
		if card.Bundled {
			ui.Text(c, "无需下载").FontSize(10).TextColor(design.Muted)
			return
		}
		details := func() {
			for _, item := range card.Items {
				label := "原文检索引擎"
				if r := a.firstLaunch.Controller; r != nil {
					if item.Name == r.Config.ModelID {
						label = "E5 原文检索模型"
					}
					if item.Name == r.Config.CodeExtractorID {
						label = "代码图谱依赖"
					}
				}
				switch item.Name {
				case "cognee-service":
					label = "Cognee 服务与依赖"
				case "cognee-codex-plugin":
					label = "官方插件组件"
				case "cognee-bge-zh":
					label = "BGE 官方采集模型"
				}
				phase := environmentStateLabel(item.State)
				switch item.State {
				case "installed", "verified_reusable", "prepared_verified":
					phase = "文件已校验"
				case "waiting":
					phase = "等待准备"
				case "downloading":
					phase = "下载中"
				case "installing":
					phase = "校验安装中"
				case "failed":
					phase = "尚未完成"
				}
				if item.Bytes > 0 && item.State == "needs_preparation" {
					phase = "需下载 " + environmentSize(item.Bytes)
				}
				ui.Text(c, label+" · "+phase).FontSize(10).TextColor(design.Muted)
				for _, receipt := range a.runtimeStatus.Components {
					if receipt.ID != item.Name || receipt.Total <= 0 {
						continue
					}
					if receipt.Phase == "failed" && receipt.Downloaded > 0 {
						ui.Text(c, "已保留下载 "+environmentSize(receipt.Downloaded)).FontSize(10).TextColor(design.Muted)
						continue
					}
					if receipt.Phase != "downloading" {
						continue
					}
					ui.Text(c, environmentSize(receipt.Downloaded)+" / "+environmentSize(receipt.Total)).Key("environment-bytes-" + item.Name).FontSize(10).TextColor(design.Muted)
					ui.Progress(c.Key("environment-progress-"+item.Name), max(0, min(1, float64(receipt.Downloaded)/float64(receipt.Total)))).Height(3)
				}
			}
		}
		if state == "已校验" {
			if a.environment.Details == nil {
				a.environment.Details = map[string]*bool{}
			}
			expanded := a.environment.Details[card.Key]
			if expanded == nil {
				expanded = new(bool)
				a.environment.Details[card.Key] = expanded
			}
			ui.Collapsible(c, "组件明细", expanded, details)
		} else {
			details()
		}

	})
}

func (a *App) environmentFooter(c *ui.Context, setup bool) {
	ui.Row(c).Key("environment-footer").MinHeight(64).Shrink(0).Padding(14, 28).Gap(14).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		busy := a.environmentBusy()
		if setup {
			ui.Spacer(c)
			if busy {
				firstLaunchPrimary(c, "取消准备").Key("first-launch-cancel").Disabled(a.firstLaunch.State.Stage == "cancelling").OnClick(func() { a.cancelFirstLaunch(false) })
			} else if a.firstLaunch.State.Stage == "ready" {
				firstLaunchPrimary(c, "继续连接 Agent").Key("first-launch-use").OnClick(a.advanceFirstLaunchToAgents)
			} else {
				quietButton(c, "稍后设置").Key("first-launch-skip").OnClick(func() { a.cancelFirstLaunch(true) })
				if a.firstLaunch.State.Stage == "failed" || a.firstLaunch.State.Stage == "interrupted" {
					quietButton(c, "重新检查").Key("first-launch-retry").OnClick(a.discoverFirstLaunch)
				}
				label := "安装并继续"
				if a.firstLaunch.State.Stage == "cancelled" || a.firstLaunch.State.Stage == "interrupted" {
					label = "继续准备"
				}
				if a.firstLaunch.State.Stage == "failed" {
					label = "重试准备"
				}
				firstLaunchPrimary(c, label).Key("first-launch-prepare").Disabled(a.firstLaunch.Controller == nil || a.prepareRuntime == nil).OnClick(a.prepareFirstLaunch)
			}
			return
		}
		locked := a.backups.RestoreLocked || a.backups.Busy
		if busy {
			firstLaunchPrimary(c, "取消准备").Disabled(a.cancelRuntime == nil || locked).OnClick(a.cancelRuntime)
		} else {
			quietButton(c, "重新检查").Disabled(a.environment.Loading || locked || a.firstLaunch.Controller == nil).OnClick(a.refreshEnvironment)
			if a.runtimeStatus.Phase == "ready" {
				if environmentNeedsPreparation(a.environmentCards(false)) {
					firstLaunchPrimary(c, "准备缺失组件").Disabled(a.prepareRuntime == nil || locked || a.environment.Loading).OnClick(a.prepareRuntime)
				}
			} else if a.runtimeStatus.Phase == "stopped" {
				firstLaunchPrimary(c, "启动服务").Disabled(a.resumeRuntime == nil || locked).OnClick(a.resumeRuntime)
			} else {
				firstLaunchPrimary(c, "准备 / 重试").Disabled(a.prepareRuntime == nil || locked || a.environment.Loading).OnClick(a.prepareRuntime)
			}
		}
	})
}
