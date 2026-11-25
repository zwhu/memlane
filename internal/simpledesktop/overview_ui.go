package simpledesktop

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/api"
	appoverview "github.com/zwhu/memlane/app/overview"
	"github.com/zwhu/memlane/app/shell"
)

var (
	overviewLoadTimeout     = 90 * time.Second
	overviewActivityTimeout = 60 * time.Second
)

type overviewViewState = appoverview.StateOf[Item]
type overviewFirstUseState = appoverview.FirstUseState
type overviewFirstUseKey = appoverview.FirstUseKey

func (a *App) overviewPageController() *appoverview.ControllerOf[Item] {
	if a.overview.State == nil {
		a.initOverview()
	}
	return &a.overview
}

func (a *App) initOverview() {
	adapter := appShellAdapter{app: a}
	a.overview = appoverview.NewOf(appoverview.DepsOf[Item]{
		Dispatcher: adapter, Router: adapter, Fence: adapter, Theme: adapter,
		FetchDay: a.overviewDayFetcher, FetchActivity: a.overviewActivityFetcher,
		FetchSearchHistory: func() func(context.Context) ([]overviewEvent, error) { return a.Client.overviewSearchHistory },
		Projects:           a.overviewProjectRefs,
		CanDispatch:        func() bool { return a.Update != nil },
		IsSetupOnly:        a.isSetupOnlyRuntime,
		IsWindowHidden:     func() bool { return a.windowHidden },
		ValidateReading:    a.syncReadingIdentity,
		FirstUseFiles:      func() (string, string) { return a.Config.ClientStateFile, a.Config.ConfigFile },
		Diagnostics:        func() appoverview.DiagnosticSource { return overviewDiagnosticSource(a.Config) },
		ErrorMessage:       controlUserError, ReadingDenied: readingUnavailable, InvalidCursor: isControlInvalidCursor,
		BootstrapTimeout:   func() time.Duration { return overviewLoadTimeout },
		ActivityTimeout:    func() time.Duration { return overviewActivityTimeout },
		OnBootstrapSettled: a.onOverviewBootstrapSettled,
		OnBootstrapApplied: func(snapshot appoverview.Hydration[Item]) {
			a.recentScope = ""
			a.applyOverviewV2ProjectSummaries(snapshot.Projects)
		},
		Reload:   func() { a.loadOverview(true) },
		Describe: describeOverviewItem, ItemDate: overviewItemDate, WithdrawItem: clearUnavailableItem,
		OpenItem: a.openOverviewItem, RetryTitle: a.retryRecentTitle, OpenRecalled: a.openRecalledCards,
		ItemIsUnread: a.itemIsUnread, SourceName: a.overviewSourceName, PrepareTitles: a.startRecentTitles,
		RenderAgentIcon: func(c *ui.Context, item Item) { agentLogo(c, recentAgentIcon(item), 20) },
		RenderItemBadges: func(c *ui.Context, item Item) {
			v4RecallChip(c, item)
			if fields := knowledgeBasisFields(item); len(fields) > 0 {
				v4CategoryChip(c, fields[0].Value)
			}
		},
		RenderTopBanners: a.overviewTopBanners,
	}, &a.overviewState, &a.overviewFirstUse)
}

func describeOverviewItem(i Item) appoverview.Item {
	return appoverview.Item{ID: i.ID, Dataset: i.Dataset, Kind: i.Kind, Title: i.Title,
		Date: i.Date, Preview: i.Preview, SourceDocument: i.SourceDocument, ReadingState: i.ReadingState,
		TitlePending: i.ReadingState != "ready" && i.Preview == "" && (i.Document == nil || readableDocumentTitle(*i.Document) == "")}
}

func overviewItemDate(item Item, loc *time.Location) (string, bool) {
	if item.Document != nil {
		return item.Document.knowledgeDay(loc)
	}
	return "", false
}

func (a *App) overviewProjectRefs() []shell.ProjectRef {
	// Keep duplicates here so ActivityProject can reject ambiguous routing.
	projects := overviewProjectRefs(a.Config.Projects)
	for _, p := range a.Config.Common {
		ref := projectRef(p)
		ref.Common = true
		projects = append(projects, ref)
	}
	return projects
}

func (a *App) loadOverview(identityChecked ...bool) {
	if a.Update == nil {
		return
	}
	if len(identityChecked) == 0 || !identityChecked[0] {
		a.syncReadingIdentity()
	}
	page := a.overviewPageController()
	control, hasControl := a.accessClient()
	if hasControl && !a.access.Loaded {
		page.RefreshAt = time.Now().Add(overviewRefreshInterval)
		page.State.Loading, page.State.ActivityLoading = true, true
		return
	}
	a.resetDeferredStartupFallback()
	page.FetchBootstrap = func() func(context.Context) (appoverview.Hydration[Item], error) {
		return a.overviewBootstrap(control, hasControl)
	}
	page.Load()
}

func (a *App) overviewBootstrap(control ProjectControlClient, hasControl bool) func(context.Context) (appoverview.Hydration[Item], error) {
	client := a.Client
	projects := overviewProjects(a.Config)
	return func(ctx context.Context) (appoverview.Hydration[Item], error) {
		var result appoverview.Hydration[Item]
		if !hasControl {
			return result, errors.New("控制面服务不可用，请确认服务已启动")
		}
		readingID, _ := readingIdentity(Client{Config: client.Config})
		if err := control.requireReadingContract(ctx); err != nil {
			return result, err
		}
		resp, err := control.overviewBootstrap(ctx, readingID, 365, 20, 50, 50)
		if err != nil {
			return result, err
		}
		result.Snapshot, result.Buckets, result.Events, result.More = buildOverviewFromV2Bootstrap(projects, resp, 50)
		result.Generation, result.TotalKnowledge, result.RecallStartedAt = resp.Generation, resp.TotalKnowledgeCount, resp.RecallStartedAt
		for _, project := range resp.Projects {
			result.Projects = append(result.Projects, api.ProjectSummary{ProjectID: project.ProjectID,
				UnreadCount: project.UnreadCount, UnreadStateRev: project.UnreadStateRev, CatalogVersion: project.CatalogVersion})
		}
		return result, nil
	}
}

func (a *App) overviewDayFetcher() func(context.Context, string, string) (appoverview.DayPage[Item], error) {
	control, ok := a.accessClient()
	client, recallStartedAt := a.Client, a.overviewState.V2RecallStartedAt
	return func(ctx context.Context, day, cursor string) (appoverview.DayPage[Item], error) {
		var result appoverview.DayPage[Item]
		if !ok {
			return result, errors.New("控制面服务不可用，请确认服务已启动")
		}
		readingID, _ := readingIdentity(Client{Config: client.Config})
		resp, err := control.overviewDayV2(ctx, readingID, day, 30, cursor)
		if err != nil {
			return result, err
		}
		for _, row := range resp.Items {
			if row.DocID != "" {
				result.Items = append(result.Items, row.toItem(recallStartedAt))
			}
		}
		result.Total, result.NextCursor, result.HasMore = resp.TotalCount, resp.NextCursor, resp.HasMore
		return result, nil
	}
}

func (a *App) overviewActivityFetcher() func(context.Context) (appoverview.ActivityPage, error) {
	client := a.Client
	control, hasControl := a.accessClient()
	return func(ctx context.Context) (appoverview.ActivityPage, error) {
		const want = 50
		var recalls []RecallEvent
		var recallMore bool
		var recallErr error
		var wg sync.WaitGroup
		if hasControl {
			wg.Add(1)
			go func() {
				defer wg.Done()
				recalls, recallMore, recallErr = control.recallEvents(ctx, want)
			}()
		}
		events, more, err := client.overviewActivity(ctx, want)
		wg.Wait()
		if readingAuthDenied(recallErr) {
			return appoverview.ActivityPage{}, recallErr
		}
		if err != nil {
			return appoverview.ActivityPage{}, err
		}
		var recallError string
		if hasControl && recallErr == nil {
			var mergedMore bool
			events, mergedMore = mergeRecallActivity(events, recalls, overviewProjects(client.Config), want)
			more = more || recallMore || mergedMore
		} else if hasControl {
			recallError = "召回明细暂不可用"
			for i := range events {
				if events[i].Record.Operation == "recall" && events[i].State == "已完成" {
					events[i].Title = "召回请求完成 · 明细未读取"
				}
			}
		}
		return appoverview.ActivityPage{Events: events, More: more, RecallError: recallError}, nil
	}
}

func firstUseKey(cfg Config, now time.Time) overviewFirstUseKey {
	return appoverview.NewFirstUseKey(cfg.ClientStateFile, cfg.ConfigFile, now)
}

func (a *App) startOverviewFirstUseDaysLoad(now time.Time) {
	a.overviewPageController().StartFirstUseDaysLoad(now)
}

func (a *App) overviewFirstUseDays(now time.Time) (int, error, bool) {
	return a.overviewPageController().FirstUseDays(now)
}

func (a *App) overviewScope() string  { return a.overviewPageController().Scope() }
func (a *App) withdrawOverviewScope() { a.overviewPageController().WithdrawScope() }
func (a *App) loadOverviewActivity()  { a.startOverviewActivity() }
func (a *App) startOverviewActivity() <-chan struct{} {
	return a.overviewPageController().LoadActivity()
}
func (a *App) loadOverviewDayV2(day string, appendMore bool) {
	a.overviewPageController().LoadDay(day, appendMore)
}
func (a *App) selectOverviewDay(day string) { a.overviewPageController().SelectDay(day) }
func (a *App) overviewDisplayedRows(now time.Time) []Item {
	return a.overviewPageController().DisplayedRows(now)
}
func (a *App) overviewKnowledgeRows(now time.Time) []Item {
	return a.overviewPageController().KnowledgeRows(now)
}
func (a *App) overviewCachedBuckets(now time.Time) map[string]int {
	return a.overviewPageController().CachedBuckets(now)
}

func (a *App) overviewHome(c *ui.Context) { a.overviewPageController().RenderHome(c) }
func (a *App) overviewHeatmap(c *ui.Context, now time.Time) {
	a.overviewPageController().RenderHeatmap(c, now)
}
func (a *App) overviewKnowledge(c *ui.Context, now time.Time) {
	a.overviewPageController().RenderKnowledge(c, now)
}
func (a *App) overviewActivities(c *ui.Context, now time.Time) {
	a.overviewPageController().RenderActivities(c, now)
}
func (a *App) overviewFailureDialog(c *ui.Context) { a.overviewPageController().RenderFailureDialog(c) }
func (a *App) openOverviewFailureDetail(ev overviewEvent, loc *time.Location) {
	a.overviewPageController().OpenFailureDetail(ev, loc)
}
func (a *App) closeOverviewFailureDetail() { a.overviewPageController().CloseFailureDetail() }

func (a *App) overviewActivityProject(event overviewEvent) (int, bool) {
	project, ok := a.overviewPageController().ActivityProject(event)
	if ok {
		for n, candidate := range a.Config.Projects {
			if candidate.ID == project.ID && candidate.Dataset == project.DatasetID {
				return n, true
			}
		}
	}
	return 0, false
}

func (a *App) overviewTopBanners(c *ui.Context) {
	if a.environment.staleRuntimeOwner() || a.canRetryDailyRuntimeStartup() {
		ui.Column(c).FillWidth().Shrink(0).Margin(0, 0, sp3, 0).Children(func() {
			a.staleDaemonBanner(c)
			a.dailyRuntimeStartupError(c)
		})
	}
	a.setupDeferredGenerationBanner(c, true)
}

func (a *App) applyOverviewV2ProjectSummaries(summaries []api.ProjectSummary) {
	if len(summaries) == 0 {
		return
	}
	byID := make(map[string]api.ProjectSummary, len(summaries))
	for _, s := range summaries {
		if s.ProjectID != "" {
			byID[s.ProjectID] = s
		}
	}
	for _, p := range a.access.Directory.Projects {
		summary, ok := byID[p.ID]
		if !ok {
			continue
		}
		q := a.projectQueueStateForAsync(p)
		applyServerUnreadSnapshot(q, summary.UnreadCount, summary.UnreadStateRev, a.knowledgeWriterHasPending(p.ID, p.Dataset))
		if summary.CatalogVersion != "" {
			q.KnowledgeCatalogVersion = summary.CatalogVersion
		}
		q.StateVersion++
	}
}
func overviewBuckets(snapshot overviewSnapshot, location *time.Location) map[string]int {
	return appoverview.Buckets(snapshot.Knowledge, func(item Item) (string, bool) {
		return overviewItemDate(item, location)
	})
}

func (a *App) openOverviewItem(item Item) {
	for idx, p := range a.Config.Projects {
		if p.Dataset == item.Dataset {
			a.navigate("知识", idx)
			a.locateOverviewItem(a.state(), item)
			return
		}
	}
	for idx, p := range a.Config.Common {
		if p.Dataset == item.Dataset {
			a.navigate("通用知识", idx)
			a.commonEdit.Category = ""
			a.locateOverviewItem(a.state(), item)
			return
		}
	}
}

func (a *App) openSettingsFromDeferredBanner() {
	a.access.SettingsSection = "记忆提炼"
	a.access.OpenProject = ""
	a.settingsOrigin = nil
	a.navigate("设置", 0)
}

func (a *App) showSetupDeferredToast() bool {
	return !a.setupDeferredToastDismissed && !a.initialSetup && a.isSetupOnlyRuntime() &&
		a.integrations.Generation.Active == nil && !(a.page == "设置" && a.access.SettingsSection == "记忆提炼")
}

func toastPrimaryButtonTextColor() ui.Color {
	if tok.Dark {
		return tok.Bg
	}
	return tok.OnAccent
}

func (a *App) setupDeferredToast(c *ui.Context) {
	title := "尚未配置记忆提炼"
	desc := "已先进入主界面，新会话暂不自动整理成记忆。完成 CLI 登录或配置模型 API 后即可开启。"
	s := &a.integrations
	if s.GenerationLoaded && s.GenerationError == "" && len(s.Generation.AgentBridges) == 0 && !a.hasDetectedProcessingCLI() {
		title = "尚未安装记忆提炼 CLI"
		desc = "已先进入主界面，对话采集可正常使用；如需自动整理成记忆，请安装本机 CLI 或配置模型 API。"
	}
	dismiss := func() { a.setupDeferredToastDismissed = true }
	ui.Column(c.Key("setup-deferred-toast")).Absolute().Right(sp5).Bottom(sp5).Width(360).
		Padding(sp3, sp4).Gap(sp2).Background(tok.BgElevated).Border(1, tok.Border).
		Radius(radiusMenu).Shadow(0, 12, 32, 0, tok.ShadowLift).Children(func() {
		ui.Row(c).FillWidth().Gap(sp2).AlignItems(ui.Center).Children(func() {
			v4StatusDot(c, "warn")
			homeText(c, title, homePanelHeading).Grow(1).MinWidth(0)
			closeButton := ui.ButtonBase(c).Role(ui.RoleButton).Label("关闭记忆提炼提示").
				Cursor(ui.CursorPointer).Size(24, 24).Radius(radius).BorderWidth(0).
				AlignItems(ui.Center).Justify(ui.Center).Shrink(0)
			closeButton.Background(homeHoverColor(closeButton, "toast-close-bg", ui.Transparent, tok.Hover))
			iconColor := tok.Text2
			if closeButton.Hovered() {
				iconColor = tok.Text1
			}
			closeButton.Children(func() {
				ui.Icon(c, v4IconClose).FontSize(13).TextColor(iconColor)
			}).OnClick(dismiss)
		})
		homeText(c, desc, homeBody).LineHeight(1.5)
		ui.Row(c).FillWidth().Gap(sp2).Justify(ui.End).Children(func() {
			v4SmallButton(c, "知道了", nil, dismiss)
			b := ui.ButtonBase(c).Role(ui.RoleButton).Label("去配置").
				Cursor(ui.CursorPointer).Height(24).Padding(0, sp2).Radius(radius).
				BorderWidth(0).AlignItems(ui.Center).Shrink(0)
			b.Background(homeHoverColor(b, "toast-pbtn-bg", tok.Accent, tok.AccentHover))
			v4ButtonPress(b, 0.96)
			b.Children(func() {
				ui.Text(c, "去配置").Font("system-ui").FontSize(fsCaption).FontWeight(500).
					TextColor(toastPrimaryButtonTextColor()).SingleLine()
			}).OnClick(func() {
				dismiss()
				a.openSettingsFromDeferredBanner()
			})
		})
	})
}

func (a *App) setupDeferredGenerationBanner(c *ui.Context, showAction bool) {
	if a.initialSetup || !a.isSetupOnlyRuntime() {
		return
	}
	ui.Row(c.Key("setup-deferred-generation-banner")).FillWidth().Shrink(0).
		Padding(sp2, sp3).Gap(sp2).AlignItems(ui.Center).
		Background(tok.Fill1).Border(1, tok.Border).Radius(radius).Children(func() {
		homeText(c, "尚未配置记忆提炼模型，新会话暂不自动提炼", homeMeta).Grow(1).MinWidth(0)
		if showAction {
			v4SmallButton(c, "立即配置", nil, a.openSettingsFromDeferredBanner)
		}
	})
}
