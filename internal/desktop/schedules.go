package desktop

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

type scheduleEditorState struct {
	Draft                                                     records.ScheduleDraft
	ProjectLabel, Frequency, Weekday, Interval, Window, Limit string
	RetryEnabled                                              bool
	RetryCount, RecoveryWindow                                string
	Destinations                                              map[string]bool
	Requested                                                 *records.ScheduleDraft
}

type ScheduleUI struct {
	ConfirmPlan                                               *records.Schedule
	Drafts                                                    map[string]scheduleEditorState
	ConfirmAction                                             string
	HistoryOpen, RulesOpen                                    bool
	Client                                                    serviceipc.ScheduleClient
	Plans                                                     []records.Schedule
	Runs                                                      []records.ScheduleRun
	Selected                                                  string
	Draft                                                     records.ScheduleDraft
	ProjectLabel, Frequency, Weekday, Interval, Window, Limit string
	RetryEnabled                                              bool
	RetryCount, RecoveryWindow                                string
	Destinations                                              map[string]bool
	Requested                                                 *records.ScheduleDraft
	Busy, Loading, Loaded, Editing                            bool
	Error, ReadError, Notice                                  string
	RuntimeError                                              string
	Sequence                                                  int64
	Scroll                                                    ui.ScrollState
}

func (a *App) refreshSchedules() {
	s := &a.schedules
	if a.libraryContext == nil || a.libraryContext.Err() != nil || s.Loading || a.visualFixture {
		return
	}
	s.Loading = true
	s.Sequence++
	seq := s.Sequence
	id := s.Selected
	go func() {
		ctx, stop := context.WithTimeout(a.libraryContext, 5*time.Second)
		defer stop()
		plans, err := s.Client.List(ctx)
		var runs []records.ScheduleRun
		if err == nil && id != "" {
			runs, err = s.Client.Runs(ctx, id)
		}
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			s.Loading = false
			if s.Sequence != seq {
				a.refreshSchedules()
				return
			}
			if err != nil {
				s.ReadError = "计划读取失败，保留已加载内容，请重试。"
				return
			}
			s.Plans = plans
			s.ReadError = ""
			s.Loaded = true
			if s.Selected == id {
				s.Runs = runs
			}
		})
	}()
}

func (a *App) currentSchedule() *records.Schedule {
	for i := range a.schedules.Plans {
		if a.schedules.Plans[i].ID == a.schedules.Selected {
			return &a.schedules.Plans[i]
		}
	}
	return nil
}
func (a *App) selectSchedule(id string) {
	s := &a.schedules
	if s.Busy {
		return
	}
	a.rememberScheduleEditor()
	a.navigate("整理计划", "")
	s.ConfirmAction, s.HistoryOpen = "", false
	s.Selected = id
	s.Editing = false
	s.Error = ""
	s.Notice = ""
	s.Requested = nil
	s.Scroll.Y = 0
	s.Sequence++
	a.refreshSchedules()
}
func (a *App) newSchedule() {
	s := &a.schedules
	if s.Busy {
		return
	}
	a.rememberScheduleEditor()
	project := a.scope
	a.navigate("整理计划", "")
	s.Selected = ""
	s.ConfirmAction = ""
	if a.restoreScheduleEditor("new") {
		return
	}
	s.Editing = true
	s.Error = ""
	s.Notice = ""
	s.Requested = nil
	s.Scroll.Y = 0
	s.Draft = records.ScheduleDraft{ID: uuid.NewString(), Spec: records.ScheduleSpec{Executor: "processing"}}
	a.refreshProcessing()
	s.ProjectLabel = ""
	s.Frequency = ""
	s.Weekday = ""
	s.Interval = ""
	s.Window = ""
	s.Limit = ""
	s.RetryEnabled = false
	s.RetryCount = ""
	s.RecoveryWindow = ""
	s.Destinations = map[string]bool{}
	for _, scope := range a.scopes {
		if scope.ID == project && scope.Kind == "project" {
			s.Draft.Spec.Project = scope.ID
			s.ProjectLabel = a.scopeLabel(scope.ID)
		}
	}
	s.Sequence++
}
func (a *App) editSchedule() {
	s := &a.schedules
	plan := a.currentSchedule()
	if plan == nil || s.Busy {
		return
	}
	if a.restoreScheduleEditor(plan.ID) {
		return
	}
	s.ConfirmAction = ""
	s.Editing = true
	s.Error = ""
	s.Notice = ""
	s.Requested = nil
	s.Scroll.Y = 0
	s.Draft = records.ScheduleDraft{ID: plan.ID, ExpectedGeneration: plan.Generation, Enabled: plan.Enabled, Spec: plan.Spec}
	s.ProjectLabel = a.scopeLabel(plan.Spec.Project)
	s.Frequency = map[string]string{"daily": "每日", "weekly": "每周", "interval": "每隔若干小时"}[plan.Spec.Frequency]
	s.Weekday = ""
	if plan.Spec.Weekday >= 0 && plan.Spec.Weekday <= 6 {
		s.Weekday = []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[plan.Spec.Weekday]
	}
	s.Interval = strconv.Itoa(plan.Spec.IntervalHours)
	s.Window = strconv.Itoa(plan.Spec.WindowMinutes)
	s.Limit = strconv.Itoa(plan.Spec.MaterialLimit)
	s.RetryEnabled = plan.Spec.RetryLimit > 0
	s.RetryCount = strconv.Itoa(plan.Spec.RetryLimit)
	s.RecoveryWindow = strconv.Itoa(plan.Spec.RecoveryWindowMinutes)
	s.Destinations = map[string]bool{}
	for _, id := range plan.Spec.Destinations {
		s.Destinations[id] = true
	}
}
func (a *App) saveSchedule() {
	s := &a.schedules
	if !s.Editing || s.Busy {
		return
	}
	d := s.Draft
	if d.Spec.Executor == "processing" && d.Spec.Processing == nil {
		current := a.processing.Configuration.Current
		if !current.Active || current.Revision == nil {
			s.Error = "请先在设置中验证并明确启用记忆处理方式，再保存计划。"
			return
		}
		d.Processing = &records.ProcessingTaskAuthorization{Revision: current.Revision.ID, Generation: current.Generation}
	}
	d.Spec.Project = a.datasetForLabel(s.ProjectLabel)
	d.Spec.Frequency = map[string]string{"每日": "daily", "每周": "weekly", "每隔若干小时": "interval"}[s.Frequency]
	d.Spec.IntervalHours = 0
	d.Spec.Weekday = 0
	if d.Spec.Frequency == "interval" {
		d.Spec.IntervalHours, _ = strconv.Atoi(s.Interval)
		d.Spec.LocalTime = ""
	}
	if d.Spec.Frequency == "weekly" {
		if s.Weekday == "" {
			s.Error = "请选择每周运行日期。"
			return
		}
		for i, label := range []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"} {
			if s.Weekday == label {
				d.Spec.Weekday = i
			}
		}
	}
	d.Spec.WindowMinutes, _ = strconv.Atoi(s.Window)
	d.Spec.MaterialLimit, _ = strconv.Atoi(s.Limit)
	d.Spec.RetryLimit = 0
	d.Spec.RecoveryWindowMinutes = 0
	if s.RetryEnabled {
		d.Spec.RetryLimit, _ = strconv.Atoi(s.RetryCount)
		d.Spec.RecoveryWindowMinutes, _ = strconv.Atoi(s.RecoveryWindow)
		if d.Spec.RetryLimit < 1 || d.Spec.RetryLimit > 2 || d.Spec.RecoveryWindowMinutes < 1 || d.Spec.RecoveryWindowMinutes > 1440 {
			s.Error = "允许自动重试时，请明确填写1–2次额外尝试和1–1440分钟总恢复窗口。"
			return
		}
	}
	d.Spec.Destinations = nil
	if d.Spec.Review {
		for _, scope := range a.scopes {
			if s.Destinations[scope.ID] {
				d.Spec.Destinations = append(d.Spec.Destinations, scope.ID)
			}
		}
	}
	a.putScheduleUI(d)
}
func (a *App) putScheduleUI(d records.ScheduleDraft) {
	s := &a.schedules
	if s.Busy || a.libraryContext == nil {
		return
	}
	d.OperationID = uuid.NewString()
	if s.Requested != nil {
		candidate := d
		candidate.OperationID = s.Requested.OperationID
		if records.MergeDigest(candidate) == records.MergeDigest(*s.Requested) {
			d = *s.Requested
		}
	}
	s.Requested = &d
	wasEditing := s.Editing
	draftKey := s.Selected
	if draftKey == "" {
		draftKey = "new"
	}
	s.Busy = true
	s.Error = ""
	s.Notice = "正在保存计划…"
	go func() {
		ctx, stop := context.WithTimeout(a.libraryContext, 10*time.Second)
		defer stop()
		plan, err := s.Client.Put(ctx, d)
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			s.Busy = false
			if err != nil {
				s.Notice = ""
				s.Scroll.Y = 0
				s.Error = "计划未完成保存，填写内容保留；请核对项目、频率、时区和材料上限后重试：" + err.Error()
				switch err.Error() {
				case "connection_changed", "dependency_changed":
					s.Error = "处理连接已变化，计划尚未保存。填写内容已保留，请在记忆处理中重新选择连接、验证并明确启用，再返回保存。"
				case "configuration_required", "verification_required", "connection_verification_required":
					s.Error = "处理配置尚未就绪，计划尚未保存。填写内容已保留，请先验证并明确启用记忆处理方式。"
				}
				a.refreshSchedules()
				return
			}
			s.Requested = nil
			if wasEditing {
				delete(s.Drafts, draftKey)
			}
			s.ConfirmAction = ""
			s.Selected = plan.ID
			s.Editing = false
			s.Notice = "计划已持久保存；执行记录与知识应用结果分别更新。"
			s.Sequence++
			a.refreshSchedules()
		})
	}()
}
func (a *App) scheduleList(c *ui.Context) {
	s := &a.schedules
	quietButton(c, "查看整理记录").Key("schedule-open-records").AlignSelf(ui.Start).OnClick(func() { a.navigate("整理记录", "") })
	if len(s.Plans) == 0 {
		if s.Loading {
			a.empty(c, "正在读取计划", "读取完成后显示实际计划。")
			return
		}
		if s.ReadError != "" {
			a.empty(c, "计划暂不可用", s.ReadError)
			return
		}
		a.empty(c, "暂无整理计划", "计划由你设置并明确启用，首次不会自动调用模型。")
	}
	for _, plan := range s.Plans {
		label := a.scopeLabel(plan.Spec.Project)
		b := ui.Button(c.Key("schedule-plan-"+plan.ID), "").Label(label).Justify(ui.Start).BorderWidth(0).Padding(12).Children(func() {
			ui.Column(c).Gap(8).Children(func() {
				ui.Text(c, a.scheduleTitle(plan)).Bold().MaxLines(2)
				state := "已停用"
				if plan.Enabled {
					state = "已启用"
				}
				ui.Text(c, label+" · "+scheduleFrequencyLabel(plan.Spec)+" · "+plan.Spec.Timezone).FontSize(design.Small).TextColor(design.Muted).MaxLines(2)
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, state).FontSize(10).TextColor(design.Muted)
					ui.Spacer(c)
					next := "暂无下次执行"
					if plan.Enabled && plan.NextRunMS > 0 {
						next = scheduleLocalTime(plan.NextRunMS, plan.Spec.Timezone)
					}
					ui.Text(c, next).FontSize(10).TextColor(design.Muted).MaxLines(2)
				})
			})
		})
		if s.Selected == plan.ID {
			b.Background(design.ListSelected)
		} else {
			b.Background(ui.Transparent)
		}
		b.Disabled(s.Busy).OnClick(func() { a.selectSchedule(plan.ID) })
	}
}
func scheduleFrequencyLabel(spec records.ScheduleSpec) string {
	switch spec.Frequency {
	case "daily":
		return "每日 " + spec.LocalTime
	case "weekly":
		if spec.Weekday < 0 || spec.Weekday > 6 {
			return "每周日期不可用"
		}
		return []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[spec.Weekday] + " " + spec.LocalTime
	case "interval":
		return fmt.Sprintf("每隔 %d 小时", spec.IntervalHours)
	}
	return "尚未选择频率"
}

func scheduleExecutorLabel(spec records.ScheduleSpec) string {
	if spec.Processing != nil {
		return processingSpecLabel(spec.Processing.Revision.Spec)
	}
	if spec.Executor == "processing" {
		return "保存时绑定当前已启用配置"
	}
	if spec.Model != "" {
		return "traex · " + spec.Model
	}
	return "traex 当前模型"
}
func (a *App) scheduleDetail(c *ui.Context) {
	s := &a.schedules
	ui.Scroll(c.Key("schedule-detail-scroll")).Grow(1).MinHeight(0).TrackScroll(&s.Scroll).Padding(28, 24).Gap(18).Children(func() {
		if s.Error != "" {
			ui.Text(c, s.Error).Selectable()
			quietButton(c, "重新读取计划").OnClick(a.refreshSchedules)
			if s.Draft.Spec.Executor == "processing" {
				quietButton(c, "检查记忆处理").OnClick(func() { a.settingsSection = "processing"; a.navigate("设置", ""); a.refreshProcessing() })
			}
		}
		if s.ReadError != "" {
			ui.Text(c, s.ReadError).Selectable()
			quietButton(c, "重试计划读取").OnClick(a.refreshSchedules)
		}
		if s.RuntimeError != "" {
			ui.Text(c, s.RuntimeError).Selectable().TextColor(design.Muted)
		}
		if s.Notice != "" {
			ui.Text(c, s.Notice).FontSize(design.Small).TextColor(design.Muted)
		}
		if s.Editing {
			a.scheduleEditor(c)
			return
		}
		plan := a.currentSchedule()
		if plan == nil {
			a.empty(c, "选择整理计划", "查看下一次运行与执行记录，或新建计划。")
			return
		}
		if s.ConfirmAction != "" {
			if s.ConfirmPlan != nil {
				a.scheduleConfirmation(c, *s.ConfirmPlan)
				if s.ConfirmPlan.ID != plan.ID || s.ConfirmPlan.Generation != plan.Generation {
					ui.Text(c, "计划已更新，请取消并重新核对后确认。").Bold()
				}
			}
			return
		}
		ui.Text(c, "定期整理").FontSize(10).TextColor(design.Muted)
		ui.Text(c, a.scheduleTitle(*plan)).FontSize(design.Title).Bold()
		state := "当前已停用，保存配置不会自动调用模型"
		if plan.Enabled {
			state = "当前已启用"
		}
		ui.Text(c, state).FontSize(design.Small).TextColor(design.Muted)
		ui.Column(c).Padding(18, 0, 0, 0).Children(func() {
			scheduleInfo(c, "项目", a.scopeLabel(plan.Spec.Project))
			scheduleInfo(c, "执行者", scheduleExecutorLabel(plan.Spec))
			scheduleInfo(c, "频率与时区", scheduleFrequencyLabel(plan.Spec)+" · "+plan.Spec.Timezone)
			next := "未启用"
			if plan.Enabled {
				next = "等待下一次运行"
				if plan.NextRunMS > 0 {
					next = scheduleLocalTime(plan.NextRunMS, plan.Spec.Timezone)
				}
			}
			scheduleInfo(c, "下一次执行", next)
			scheduleInfo(c, "材料与补跑窗口", fmt.Sprintf("%d 条 · %d 分钟", plan.Spec.MaterialLimit, plan.Spec.WindowMinutes))
		})
		ui.Column(c).Padding(14, 0).Gap(12).Children(func() {
			ui.Text(c, "最近执行").FontSize(12).Bold()
			if a.runtimeStatus.Phase == "stopped" {
				ui.Text(c, "服务由用户主动停止，计划不会运行；重新打开窗口也不会自动恢复。").FontSize(design.Small).LineHeight(1.7)
			}
			if len(s.Runs) == 0 {
				ui.Text(c, "暂无本计划的执行记录。").FontSize(design.Small).TextColor(design.Muted)
			} else {
				ui.Text(c, fmt.Sprintf("已有 %d 次实际执行记录。", len(s.Runs))).FontSize(design.Small).TextColor(design.Muted)
			}
			label := "查看执行记录"
			if s.HistoryOpen {
				label = "收起执行记录"
			}
			quietButton(c, label).Key("schedule-history-toggle").AlignSelf(ui.Start).Disabled(len(s.Runs) == 0).OnClick(func() { s.HistoryOpen = !s.HistoryOpen })
			if s.HistoryOpen {
				a.scheduleRunHistory(c, *plan)
			}
		})
		ui.Collapsible(c, "执行范围与恢复规则", &s.RulesOpen, func() {
			ui.Text(c, "摘要和标签可按已确认规则新增；修改已有知识及通用提炼必须逐项审核。来源版本与历史保留。").FontSize(design.Small).LineHeight(1.7)
			if plan.Spec.RetryLimit > 0 {
				ui.Text(c, fmt.Sprintf("最多额外%d次尝试 · 总恢复窗口%d分钟；重试可能额外消耗Token。", plan.Spec.RetryLimit, plan.Spec.RecoveryWindowMinutes)).FontSize(design.Small)
			} else {
				ui.Text(c, "未启用自动重试。").FontSize(design.Small)
			}
			ui.Text(c, "错过时最多补最近一次，超过窗口不逐次补跑；已有持久结果只继续应用，不重复调用模型。").FontSize(design.Small).LineHeight(1.7)
		})
	})
	a.scheduleFooter(c)
}

func scheduleInfo(c *ui.Context, label, value string) {
	ui.Row(c).Padding(14, 0).Gap(12).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(design.Muted).MinWidth(65).Shrink(0)
		ui.Spacer(c)
		ui.Text(c, value).FontSize(11).TextColor(design.Muted).MaxWidth(360).AlignSelf(ui.End)
	})
}
func (a *App) scheduleTitle(plan records.Schedule) string {
	return a.scopeLabel(plan.Spec.Project) + " · 日常整理"
}
func scheduleLocalTime(ms int64, zone string) string {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04 UTC") + "（计划时区不可用）"
	}
	return time.UnixMilli(ms).In(loc).Format("2006-01-02 15:04 MST")
}
func (a *App) scheduleRunHistory(c *ui.Context, plan records.Schedule) {
	for _, run := range a.schedules.Runs {
		ui.Column(c).Border(1, design.Border).Padding(12).Gap(8).Children(func() {
			ui.Text(c, scheduleLocalTime(run.DueMS, plan.Spec.Timezone)).FontSize(11).Bold()
			ui.Text(c, scheduleRunLabel(run)).FontSize(11).LineHeight(1.7)
			if run.Task != "" {
				quietButton(c, "查看本次整理").Key("schedule-open-" + run.ID).AlignSelf(ui.Start).OnClick(func() { a.selectOrganization(run.Task) })
			}
		})
	}
}
func (a *App) scheduleConfirmation(c *ui.Context, plan records.Schedule) {
	disabling := a.schedules.ConfirmAction == "disable"
	title := "确认后才开始计时"
	if disabling {
		title = "停止后续计划执行"
	}
	ui.Text(c, "明确控制后台任务").FontSize(10).TextColor(design.Muted)
	ui.Text(c, title).FontSize(design.Title).Bold()
	ui.Text(c, a.scheduleTitle(plan)).FontSize(design.Small).TextColor(design.Muted)
	ui.Text(c, "范围与费用").FontSize(12).Bold()
	ui.Text(c, fmt.Sprintf("%s · %s · 最多%d条授权项目材料", a.scopeLabel(plan.Spec.Project), scheduleExecutorLabel(plan.Spec), plan.Spec.MaterialLimit)).LineHeight(1.7)
	if disabling {
		ui.Text(c, "未完成的本计划任务将取消。已保存的知识、历史和待确认建议保留；停用不会拒绝历史建议。").LineHeight(1.7)
	} else {
		ui.Text(c, fmt.Sprintf("首次启用只计算之后的下一次运行，不补跑此前时段。按频率使用当前模型与账号额度；允许额外重试%d次，可能额外消耗Token。", plan.Spec.RetryLimit)).LineHeight(1.7)
		if a.runtimeStatus.Phase == "stopped" {
			ui.Text(c, "服务由用户主动停止；启用计划不会启动服务，需明确恢复服务后才能运行。").FontSize(design.Small).TextColor(design.Muted)
		}
	}
}
func (a *App) scheduleFooter(c *ui.Context) {
	s := &a.schedules
	plan := a.currentSchedule()
	if !s.Editing && plan == nil {
		return
	}
	ui.Row(c).Key("schedule-footer").Shrink(0).Padding(14, 24).Gap(10).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		note := "新计划默认停用"
		if plan != nil {
			note = fmt.Sprintf("计划保存版本 %d", plan.Generation)
		}
		if s.ConfirmAction == "enable" {
			note = "保存配置不会自动启用"
		} else if s.ConfirmAction == "disable" {
			note = "停用不代表拒绝历史建议"
		}
		ui.Text(c, note).FontSize(10).TextColor(design.Muted).Grow(1).MinWidth(0)
		if s.Editing {

			actionButton(c, "保存计划配置", nil).Key("schedule-save").Disabled(s.Busy || a.libraryContext == nil).OnClick(a.saveSchedule)
			return
		}
		if s.ConfirmAction != "" {
			quietButton(c, "取消").Key("schedule-confirm-cancel").Disabled(s.Busy).OnClick(func() { s.ConfirmAction = ""; s.ConfirmPlan = nil; s.Scroll.Y = 0 })
			label := "明确启用计划"
			if s.ConfirmAction == "disable" {
				label = "确认停用"
			}
			frozen := s.ConfirmPlan
			stale := frozen == nil || frozen.ID != plan.ID || frozen.Generation != plan.Generation
			actionButton(c, label, nil).Key("schedule-toggle-confirm").Disabled(s.Busy || a.libraryContext == nil || stale).OnClick(func() {
				if frozen == nil {
					return
				}
				a.putScheduleUI(records.ScheduleDraft{ID: frozen.ID, ExpectedGeneration: frozen.Generation, Enabled: s.ConfirmAction == "enable", Spec: frozen.Spec})
			})
			return
		}
		label, mode := "启用计划", "enable"
		if plan.Enabled {
			label, mode = "停用计划", "disable"
		}
		actionButton(c, label, nil).Key("schedule-toggle").Disabled(s.Busy).OnClick(func() { s.ConfirmAction = mode; snapshot := *plan; s.ConfirmPlan = &snapshot; s.Scroll.Y = 0 })
	})
}
func scheduleRunLabel(run records.ScheduleRun) string {
	if run.State == "completed" {
		return "执行已完成 · 审核与应用见任务"
	}
	if run.State != "skipped" {
		return organizationStatus(run.State)
	}
	labels := map[string]string{"processing_changed": "绑定的处理配置已不可用，未调用其他执行者", "no_changes": "材料未变化，未重复整理", "no_materials": "没有可整理的材料", "missed_window": "超过补跑窗口，已跳过", "project_busy": "本项目已有进行中的整理，已跳过", "scope_changed": "项目授权位置已变化，计划停用", "input_limit": "材料超出当前整理上限，已跳过"}
	if label := labels[run.Reason]; label != "" {
		return label
	}
	return "本次已跳过"
}
func (a *App) scheduleEditor(c *ui.Context) {
	s := &a.schedules
	spec := &s.Draft.Spec

	ui.Text(c, "定期整理").FontSize(10).TextColor(design.Muted)
	ui.Text(c, "选择范围与运行时间").FontSize(design.Title).Bold()
	ui.Text(c, "保存配置与启用计划是两个动作。当前模型与账号额度可能产生费用。").FontSize(design.Small).LineHeight(1.7).TextColor(design.Muted)
	if plan := a.currentSchedule(); plan != nil && plan.Generation != s.Draft.ExpectedGeneration {
		ui.Text(c, "计划已有新版本，当前填写内容仍保留；保存会核对原版本。").FontSize(design.Small).LineHeight(1.7)
		quietButton(c, "舍弃草稿，读取当前计划").AlignSelf(ui.Start).Disabled(s.Busy).OnClick(func() { delete(s.Drafts, s.Selected); a.editSchedule() })
	}
	projects := []string{}
	for _, scope := range a.scopes {
		if scope.Kind == "project" {
			projects = append(projects, a.scopeLabel(scope.ID))
		}
	}
	ui.Text(c, "项目").FontSize(11).TextColor(design.Muted)
	ui.Select(c.Key("schedule-project"), &s.ProjectLabel, projects).Label("授权项目").Disabled(s.Busy).OnChange(func() { s.Destinations = map[string]bool{} })
	if spec.Executor == "processing" {
		label := scheduleExecutorLabel(*spec)
		if spec.Processing == nil {
			current := a.processing.Configuration.Current
			label = "尚未启用记忆处理方式"
			if current.Active && current.Revision != nil {
				label = processingSpecLabel(current.Revision.Spec)
			}
		}
		scheduleFormField(c, "执行者", func() { ui.Text(c, label).FontSize(12) })
		ui.Text(c, "保存时固定处理配置；之后切换设置中的当前方式不会替换此计划的执行者。").FontSize(design.Small).TextColor(design.Muted)
	} else {
		ui.Text(c, "执行者：traex").Bold()
		ui.Text(c, "模型").FontSize(11).TextColor(design.Muted)
		ui.TextInput(c.Key("schedule-model"), &spec.Model).Label("模型").Placeholder("使用 traex 当前模型").Disabled(s.Busy)
	}
	scheduleFormField(c, "运行频率", func() {
		ui.Select(c.Key("schedule-frequency"), &s.Frequency, []string{"每日", "每周", "每隔若干小时"}).Label("运行频率").Disabled(s.Busy)
	})
	scheduleFormPair(c, func() {
		if s.Frequency == "每隔若干小时" {
			scheduleFormField(c, "间隔小时（1–720）", func() {
				ui.TextInput(c.Key("schedule-interval"), &s.Interval).Label("间隔小时").Placeholder("1–720").Disabled(s.Busy)
			})
		} else {
			scheduleFormField(c, "本地运行时间", func() {
				ui.TextInput(c.Key("schedule-local-time"), &spec.LocalTime).Label("本地运行时间").Placeholder("HH:MM").Disabled(s.Busy)
			})
		}
	}, func() {
		scheduleFormField(c, "时区", func() {
			ui.TextInput(c.Key("schedule-timezone"), &spec.Timezone).Label("时区").Placeholder("IANA 时区，例如 Asia/Shanghai").Disabled(s.Busy)
		})
	})
	if s.Frequency == "每周" {
		scheduleFormField(c, "每周日期", func() {
			ui.Select(c.Key("schedule-weekday"), &s.Weekday, []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}).Label("每周日期").Disabled(s.Busy)
		})
	}
	scheduleFormPair(c, func() {
		scheduleFormField(c, "每次材料上限（1–16）", func() {
			ui.TextInput(c.Key("schedule-limit"), &s.Limit).Label("每次材料上限").Placeholder("1–16").Disabled(s.Busy)
		})
	}, func() {
		scheduleFormField(c, "错过后的补跑窗口（分钟）", func() {
			ui.TextInput(c.Key("schedule-window"), &s.Window).Label("补跑窗口（分钟）").Placeholder("1–1440").Disabled(s.Busy)
		})
	})

	ui.Checkbox(c.Key("schedule-retry"), &s.RetryEnabled, "允许有限自动重试，额外模型调用可能收费").Disabled(s.Busy)
	if s.RetryEnabled {
		ui.Text(c, "最多额外尝试次数（1–2）").FontSize(11).TextColor(design.Muted)
		ui.TextInput(c.Key("schedule-retry-count"), &s.RetryCount).Label("最多额外尝试次数").Placeholder("1–2").Disabled(s.Busy)
		ui.Text(c, "总恢复窗口（分钟）").FontSize(11).TextColor(design.Muted)
		ui.TextInput(c.Key("schedule-recovery-window"), &s.RecoveryWindow).Label("总恢复窗口（分钟，包含首次执行）").Placeholder("1–1440").Disabled(s.Busy)
		ui.Text(c, "仅对明确临时网络错误、超时或意外中断有限恢复；认证、额度、来源过期和无效结果不会自动重跑。已有结果只继续未提交项。保存即确认本页的次数和费用提示，首次仍须单独启用。").LineHeight(1.7)
	}
	ui.Checkbox(c.Key("schedule-review"), &spec.Review, "同时提出修改建议，逐项确认").Disabled(s.Busy).OnChange(func() {
		if !spec.Review {
			spec.Global = false
			s.Destinations = map[string]bool{}
		}
	})
	ui.Checkbox(c.Key("schedule-global"), &spec.Global, "发现通用经验候选，预览确认").Disabled(s.Busy).OnChange(func() {
		if spec.Global {
			spec.Review = true
		}
	})
	if spec.Review {
		ui.Text(c, "允许建议移入的项目（不会读取其他项目材料）").FontSize(design.Small)
		for _, scope := range a.scopes {
			if scope.Kind == "project" && a.scopeLabel(scope.ID) != s.ProjectLabel {
				allowed := s.Destinations[scope.ID]
				ui.Checkbox(c.Key("schedule-destination-"+scope.ID), &allowed, a.scopeLabel(scope.ID)).Disabled(s.Busy).OnChange(func() { s.Destinations[scope.ID] = allowed })
			}
		}
	}
	message := "新计划保存为停用状态；保存后核对设置，再明确启用。计划使用项目有效材料，摘要不作为新的独立证据。"
	if s.Draft.Enabled {
		message = "保存更新保留启用状态，并取消原计划尚未完成的运行；已提交结果保留。"
	}
	ui.Text(c, message).LineHeight(1.7)

}

func (a *App) rememberScheduleEditor() {
	s := &a.schedules
	if !s.Editing {
		return
	}
	if s.Drafts == nil {
		s.Drafts = map[string]scheduleEditorState{}
	}
	key := s.Selected
	if key == "" {
		key = "new"
	}
	destinations := map[string]bool{}
	for id, allowed := range s.Destinations {
		destinations[id] = allowed
	}
	s.Drafts[key] = scheduleEditorState{Draft: s.Draft, ProjectLabel: s.ProjectLabel, Frequency: s.Frequency, Weekday: s.Weekday, Interval: s.Interval, Window: s.Window, Limit: s.Limit, RetryEnabled: s.RetryEnabled, RetryCount: s.RetryCount, RecoveryWindow: s.RecoveryWindow, Destinations: destinations, Requested: s.Requested}
}
func (a *App) restoreScheduleEditor(key string) bool {
	s := &a.schedules
	d, ok := s.Drafts[key]
	if !ok {
		return false
	}
	s.Draft, s.ProjectLabel, s.Frequency, s.Weekday, s.Interval, s.Window, s.Limit = d.Draft, d.ProjectLabel, d.Frequency, d.Weekday, d.Interval, d.Window, d.Limit
	s.RetryEnabled, s.RetryCount, s.RecoveryWindow = d.RetryEnabled, d.RetryCount, d.RecoveryWindow
	s.Destinations = map[string]bool{}
	for id, allowed := range d.Destinations {
		s.Destinations[id] = allowed
	}
	s.Requested = d.Requested
	s.Editing, s.ConfirmAction, s.Error, s.Notice = true, "", "", "已恢复未保存的计划草稿。"
	s.Scroll.Y = 0
	return true
}

func (a *App) scheduleHeaderLabel() string {
	s := &a.schedules
	if s.Editing {
		if s.Draft.ExpectedGeneration > 0 {
			return "编辑计划"
		}
		return "新建计划"
	}
	if s.ConfirmAction == "enable" {
		return "启用计划"
	}
	if s.ConfirmAction == "disable" {
		return "停用计划"
	}
	return "计划详情"
}
func (a *App) scheduleHeaderActions(c *ui.Context) {
	s := &a.schedules
	if s.Editing {
		quietButton(c, "保留草稿返回").Key("schedule-editor-return").Disabled(s.Busy).OnClick(func() {
			a.rememberScheduleEditor()
			s.Editing = false
			s.Notice = "计划草稿已保留，可返回继续编辑。"
		})
	} else if s.ConfirmAction != "" {
		quietButton(c, "取消").Key("schedule-header-confirm-cancel").Disabled(s.Busy).OnClick(func() { s.ConfirmAction = ""; s.ConfirmPlan = nil; s.Scroll.Y = 0 })
	} else if a.currentSchedule() != nil {
		quietButton(c, "编辑").Key("schedule-edit").Disabled(s.Busy).OnClick(a.editSchedule)
	}
}

func scheduleFormField(c *ui.Context, label string, body func()) {
	ui.Column(c).Grow(1).MinWidth(0).Gap(8).Children(func() { ui.Text(c, label).FontSize(11).TextColor(design.Muted); body() })
}
func scheduleFormPair(c *ui.Context, left, right func()) {
	width, _ := c.Size()
	if width >= 1100 {
		ui.Row(c).Gap(16).AlignItems(ui.Stretch).Children(func() { left(); right() })
	} else {
		ui.Column(c).Gap(18).Children(func() { left(); right() })
	}
}
