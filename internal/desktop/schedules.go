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

type ScheduleUI struct {
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
	a.navigate("整理计划", "")
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
	project := a.scope
	a.navigate("整理计划", "")
	s.Selected = ""
	s.Editing = true
	s.Error = ""
	s.Notice = ""
	s.Requested = nil
	s.Scroll.Y = 0
	s.Draft = records.ScheduleDraft{ID: uuid.NewString(), Spec: records.ScheduleSpec{Executor: "traex"}}
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
	s.Editing = true
	s.Error = ""
	s.Notice = ""
	s.Requested = nil
	s.Scroll.Y = 0
	s.Draft = records.ScheduleDraft{ID: plan.ID, ExpectedGeneration: plan.Generation, Enabled: plan.Enabled, Spec: plan.Spec}
	s.ProjectLabel = a.scopeLabel(plan.Spec.Project)
	s.Frequency = map[string]string{"daily": "每日", "weekly": "每周", "interval": "每隔若干小时"}[plan.Spec.Frequency]
	s.Weekday = []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[plan.Spec.Weekday]
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
				s.Error = "计划未完成保存，填写内容保留；请核对项目、频率、时区和材料上限后重试：" + err.Error()
				a.refreshSchedules()
				return
			}
			s.Requested = nil
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
	if len(s.Plans) == 0 {
		a.empty(c, "暂无整理计划", "计划由你设置并明确启用，首次不会自动调用模型。")
	}
	for _, plan := range s.Plans {
		label := a.scopeLabel(plan.Spec.Project)
		b := ui.Button(c.Key("schedule-plan-"+plan.ID), "").Label(label).Justify(ui.Start).BorderWidth(0).Padding(12).Children(func() {
			ui.Column(c).Gap(8).Children(func() {
				ui.Text(c, label).Bold()
				state := "已停用"
				if plan.Enabled {
					state = "已启用"
				}
				ui.Text(c, state+" · "+scheduleFrequencyLabel(plan.Spec)).FontSize(design.Small).TextColor(design.Muted)
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
		return []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[spec.Weekday] + " " + spec.LocalTime
	case "interval":
		return fmt.Sprintf("每隔 %d 小时", spec.IntervalHours)
	}
	return "尚未选择频率"
}
func (a *App) scheduleDetail(c *ui.Context) {
	s := &a.schedules
	ui.Scroll(c.Key("schedule-detail-scroll")).Grow(1).TrackScroll(&s.Scroll).Padding(24).Gap(16).Children(func() {
		if s.Error != "" {
			ui.Text(c, s.Error).Selectable()
			quietButton(c, "重新读取计划").OnClick(a.refreshSchedules)
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
		ui.Text(c, a.scopeLabel(plan.Spec.Project)).FontSize(design.Title).Bold()
		meta(c, "频率", scheduleFrequencyLabel(plan.Spec))
		meta(c, "时区", plan.Spec.Timezone)
		model := plan.Spec.Model
		if model == "" {
			model = "traex 当前模型"
		}
		meta(c, "执行者", "traex")
		meta(c, "模型", model)
		meta(c, "每次材料", fmt.Sprintf("最多 %d 条授权项目材料", plan.Spec.MaterialLimit))
		meta(c, "补跑窗口", fmt.Sprintf("%d 分钟，醒来后最多补一次", plan.Spec.WindowMinutes))
		if plan.Spec.RetryLimit > 0 {
			meta(c, "自动恢复", fmt.Sprintf("最多额外%d次尝试 · 总窗口%d分钟", plan.Spec.RetryLimit, plan.Spec.RecoveryWindowMinutes))
		} else {
			meta(c, "自动重试", "未启用")
		}
		state := "已停用"
		if plan.Enabled {
			state = "已启用"
		}
		meta(c, "计划", state)
		if plan.NextRunMS > 0 {
			loc, _ := time.LoadLocation(plan.Spec.Timezone)
			meta(c, "下一次", time.UnixMilli(plan.NextRunMS).In(loc).Format("2006-01-02 15:04 MST"))
		}
		ui.Text(c, "摘要和标签可按已确认规则新增；修改已有知识及全局提炼必须逐项审核。来源版本与历史保留。").LineHeight(1.7)
		if a.runtimeStatus.Phase == "stopped" {
			ui.Text(c, "记忆服务已主动停止，计划不会执行；需你明确启动服务。").Bold()
		}
		ui.Text(c, fmt.Sprintf("启用后将按所选频率调用执行者模型，可能发生费用；每次最多%d次客户端整理调用，单次最多2分钟。没有持久结果时才可能重跑模型；已有结果只继续应用。修改/停用会取消旧计划尚未完成的运行，已提交结果保留。", plan.Spec.RetryLimit+1)).LineHeight(1.7)
		ui.Row(c).Gap(8).Children(func() {
			quietButton(c, "编辑计划").Key("schedule-edit").Disabled(s.Busy).OnClick(a.editSchedule)
			label := "确认启用计划"
			if plan.Enabled {
				label = "停用计划"
			}
			actionButton(c, label, nil).Key("schedule-toggle").Disabled(s.Busy).OnClick(func() {
				a.putScheduleUI(records.ScheduleDraft{ID: plan.ID, ExpectedGeneration: plan.Generation, Enabled: !plan.Enabled, Spec: plan.Spec})
			})
		})
		ui.Divider(c)
		ui.Text(c, "执行记录").Bold()
		if len(s.Runs) == 0 {
			ui.Text(c, "尚无实际运行记录。").TextColor(design.Muted)
		}
		for _, run := range s.Runs {
			ui.Column(c).Border(1, design.Border).Padding(12).Gap(8).Children(func() {
				loc, _ := time.LoadLocation(plan.Spec.Timezone)
				ui.Text(c, time.UnixMilli(run.DueMS).In(loc).Format("2006-01-02 15:04 MST")+" · "+scheduleRunLabel(run)).Bold()
				if run.Task != "" {
					quietButton(c, "查看本次整理").Key("schedule-open-" + run.ID).OnClick(func() { a.selectOrganization(run.Task) })
				}
			})
		}
	})
}
func scheduleRunLabel(run records.ScheduleRun) string {
	if run.State == "completed" {
		return "执行已完成 · 审核与应用见任务"
	}
	if run.State != "skipped" {
		return organizationStatus(run.State)
	}
	labels := map[string]string{"no_changes": "材料未变化，未重复整理", "no_materials": "没有可整理的材料", "missed_window": "超过补跑窗口，已跳过", "project_busy": "本项目已有进行中的整理，已跳过", "scope_changed": "项目授权位置已变化，计划停用", "input_limit": "材料超出当前整理上限，已跳过"}
	if label := labels[run.Reason]; label != "" {
		return label
	}
	return "本次已跳过"
}
func (a *App) scheduleEditor(c *ui.Context) {
	s := &a.schedules
	spec := &s.Draft.Spec
	ui.Text(c, "设置整理计划").FontSize(design.Title).Bold()
	projects := []string{}
	for _, scope := range a.scopes {
		if scope.Kind == "project" {
			projects = append(projects, a.scopeLabel(scope.ID))
		}
	}
	ui.Select(c.Key("schedule-project"), &s.ProjectLabel, projects).Label("授权项目").Disabled(s.Busy).OnChange(func() { s.Destinations = map[string]bool{} })
	ui.Text(c, "执行者：traex（已验收的本机 CLI）").Bold()
	ui.TextInput(c.Key("schedule-model"), &spec.Model).Label("模型").Placeholder("使用 traex 当前模型").Disabled(s.Busy)
	ui.Select(c.Key("schedule-frequency"), &s.Frequency, []string{"每日", "每周", "每隔若干小时"}).Label("运行频率").Disabled(s.Busy)
	ui.TextInput(c.Key("schedule-timezone"), &spec.Timezone).Label("时区").Placeholder("IANA 时区，例如 Asia/Shanghai").Disabled(s.Busy)
	if s.Frequency == "每隔若干小时" {
		ui.TextInput(c.Key("schedule-interval"), &s.Interval).Label("间隔小时").Placeholder("1–720").Disabled(s.Busy)
	} else {
		ui.TextInput(c.Key("schedule-local-time"), &spec.LocalTime).Label("本地运行时间").Placeholder("HH:MM").Disabled(s.Busy)
	}
	if s.Frequency == "每周" {
		ui.Select(c.Key("schedule-weekday"), &s.Weekday, []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}).Label("每周日期").Disabled(s.Busy)
	}
	ui.TextInput(c.Key("schedule-window"), &s.Window).Label("补跑窗口（分钟）").Placeholder("1–1440").Disabled(s.Busy)
	ui.TextInput(c.Key("schedule-limit"), &s.Limit).Label("每次材料上限").Placeholder("1–16").Disabled(s.Busy)
	ui.Checkbox(c.Key("schedule-retry"), &s.RetryEnabled, "允许有限自动重试，额外模型调用可能收费").Disabled(s.Busy)
	if s.RetryEnabled {
		ui.TextInput(c.Key("schedule-retry-count"), &s.RetryCount).Label("最多额外尝试次数").Placeholder("1–2").Disabled(s.Busy)
		ui.TextInput(c.Key("schedule-recovery-window"), &s.RecoveryWindow).Label("总恢复窗口（分钟，包含首次执行）").Placeholder("1–1440").Disabled(s.Busy)
		ui.Text(c, "仅对明确临时网络错误、超时或意外中断有限恢复；认证、额度、来源过期和无效结果不会自动重跑。已有结果只继续未提交项。保存即确认本页的次数和费用提示，首次仍须单独启用。").LineHeight(1.7)
	}
	ui.Checkbox(c.Key("schedule-review"), &spec.Review, "同时提出修改建议，逐项确认").Disabled(s.Busy).OnChange(func() {
		if !spec.Review {
			spec.Global = false
			s.Destinations = map[string]bool{}
		}
	})
	ui.Checkbox(c.Key("schedule-global"), &spec.Global, "发现全局经验候选，预览确认").Disabled(s.Busy).OnChange(func() {
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
	ui.Row(c).Gap(8).Children(func() {
		quietButton(c, "返回计划").Disabled(s.Busy).OnClick(func() { s.Editing = false })
		actionButton(c, "保存计划", nil).Key("schedule-save").Disabled(s.Busy || a.libraryContext == nil).OnClick(a.saveSchedule)
	})
}
