package desktop

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

func (a *App) openOrganizationEntry(id string) {
	s := &a.organization
	if s.Busy || s.Task == nil {
		return
	}
	if s.ReviewItem == "" {
		s.ResultScroll = s.Scroll.Y
	} else {
		a.rememberOrganizationReviewScroll()
	}
	s.ReviewItem = id
	s.Scroll.Y = 0
	if at := s.ReviewScroll[id]; at != nil {
		s.Scroll.Y = at.Y
	}
}
func (a *App) rememberOrganizationReviewScroll() {
	s := &a.organization
	if s.ReviewItem == "" {
		return
	}
	if s.ReviewScroll == nil {
		s.ReviewScroll = map[string]*ui.ScrollState{}
	}
	s.ReviewScroll[s.ReviewItem] = &ui.ScrollState{Y: s.Scroll.Y}
}
func (a *App) showOrganizationResult() {
	a.rememberOrganizationReviewScroll()
	s := &a.organization
	s.ReviewItem = ""
	s.Scroll.Y = s.ResultScroll
}
func (a *App) selectedOrganizationEntry() *records.OrganizationEntry {
	s := &a.organization
	if s.Task != nil {
		for i := range s.Task.Entries {
			if s.Task.Entries[i].ID == s.ReviewItem {
				return &s.Task.Entries[i]
			}
		}
	}
	return nil
}
func (a *App) organizationSourcesCurrent(t records.OrganizationTask, e records.OrganizationEntry) bool {
	if len(e.Item.Sources) == 0 {
		return false
	}
	for _, ref := range e.Item.Sources {
		v, ok := a.organization.Sources[ref.Memory]
		if !ok || a.organization.SourceErrors[ref.Memory] != "" || v.Revision != ref.Revision || v.State != "active" || v.Dataset != t.Project {
			return false
		}
	}
	return true
}
func materialTaskStatus(t records.OrganizationTask) string {
	if t.Cancelled {
		var p struct {
			Gone   bool `json:"process_group_gone"`
			Reaped bool `json:"process_reaped"`
		}
		_ = json.Unmarshal(t.Execution, &p)
		if !p.Gone || !p.Reaped {
			return "取消已记录，正在核对执行者结束状态"
		}
		return "已取消，已提交内容保留"
	}
	if t.State == "partial_failed" {
		return "部分完成，已有内容保留"
	}
	return organizationTaskStatus(t)
}
func organizationExecutor(t records.OrganizationTask) string {
	if p := t.Snapshot.Processing; p != nil {
		return processingExecutorName(p.Revision.Spec)
	}
	return "traex · 历史执行记录"
}
func processingExecutorName(s records.ProcessingSpec) string {
	name := s.Agent
	if s.Mode == "provider" {
		name = s.Provider
	}
	if s.Mode == "magpie" {
		name = "Magpie"
	}
	if name == "" {
		name = "记忆处理"
	}
	return name + " · " + s.Model
}
func (a *App) materialOrganizationDetail(c *ui.Context) {
	s := &a.organization
	entry := a.selectedOrganizationEntry()
	ui.Scroll(c.Key("organization-detail-scroll")).Grow(1).MinHeight(0).TrackScroll(&s.Scroll).Padding(24).Gap(18).Children(func() {
		if s.Error != "" {
			ui.Text(c, s.Error).FontSize(11).Selectable()
			quietButton(c, "重试读取整理记录").Key("organization-refresh").Disabled(s.Busy || s.Loading).OnClick(func() { a.refreshOrganization(false, false) })
		}
		if s.New {
			a.organizationChooser(c)
			return
		}
		if s.Task == nil {
			a.empty(c, "选择一条整理记录", "查看本次范围、执行者、已完成内容和待确认建议。")
			return
		}
		if entry != nil {
			a.materialOrganizationReview(c, *s.Task, *entry)
			return
		}
		a.materialOrganizationResult(c, *s.Task)
	})
	ui.Row(c).Shrink(0).Padding(12, 24).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		if s.New {
			note := "范围与执行者在开始后固定"
			if s.Error != "" {
				note = "原请求和材料保留，可核对后重试。"
			}
			ui.Text(c, note).FontSize(10).TextColor(design.Muted).Grow(1).MinWidth(0)
			label := "开始整理 · 会消耗 Token"
			if s.Start != nil {
				label = "重试原整理请求"
			}
			actionButton(c, label, nil).Key("organization-start").Disabled(s.Busy || s.Project == "" || len(s.Chosen) == 0 || s.Start == nil && (s.ProcessingLoading || !s.ProcessingLoaded || !organizationProcessingAvailable(s.Processing))).OnClick(a.startOrganization)
			return
		}
		if s.Task == nil {
			return
		}
		if entry != nil {
			a.materialOrganizationReviewActions(c, *s.Task, *entry)
			return
		}
		ui.Text(c, "执行完成不等于建议已确认").FontSize(10).TextColor(design.Muted).Grow(1).MinWidth(0)
		t := s.Task
		if t.State == "queued" || t.State == "running" || t.State == "applying" || hasPendingOrganizationReview(*t) {
			quietButton(c, "取消未完成项").Key("organization-cancel").Disabled(s.Busy || t.Cancelled).OnClick(a.cancelOrganization)
		}
		if t.Result != nil && (t.State == "partial_failed" || t.State == "interrupted") {
			quietButton(c, "继续应用已有结果").Key("organization-resume").Disabled(s.Busy).OnClick(a.resumeOrganization)
		}
		if t.State == "failed" || t.State == "cancelled" || t.State == "interrupted" || t.State == "partial_failed" {
			quietButton(c, "重新选择并整理").Key("organization-repeat").Disabled(s.Busy).OnClick(a.beginOrganization)
		}
		quietButton(c, "返回知识").Key("organization-return-knowledge").Disabled(s.Busy).OnClick(func() { a.navigate("项目", t.Project) })
	})
}
func hasPendingOrganizationReview(t records.OrganizationTask) bool {
	for _, e := range t.Entries {
		if e.State == "review_pending" {
			return true
		}
	}
	return false
}
func (a *App) materialOrganizationResult(c *ui.Context, t records.OrganizationTask) {
	s := &a.organization
	ui.Text(c, "整理结果").FontSize(10).TextColor(design.Muted)
	ui.Text(c, materialTaskStatus(t)).Key("organization-task-state").FontSize(design.Title).Bold()
	ui.Text(c, a.scopeLabel(t.Project)+" · "+organizationExecutor(t)).FontSize(11).TextColor(design.Muted)
	ui.Text(c, "自动完成的新增内容、待确认的修改和其他结果分别列出。已有知识不会因查看建议而改变。").FontSize(11).TextColor(design.Muted)
	if t.Error != "" {
		ui.Text(c, experienceDistillError(t.Error)).FontSize(11)
	}
	saved, pending, failed := 0, 0, 0
	for _, e := range t.Entries {
		if e.State == "applied" && e.Receipt != nil && e.Receipt.Memory != "" && (e.Item.Action == "summary" || e.Item.Action == "merge" || e.Item.Action == "global_experience") {
			saved++
		}
		if e.State == "review_pending" {
			pending++
		}
		if e.State == "failed" {
			failed++
		}
	}
	ui.Row(c).Gap(20).Children(func() {
		for _, v := range []struct {
			label string
			n     int
		}{{"已保存知识", saved}, {"待确认建议", pending}, {"失败项", failed}} {
			ui.Column(c).Grow(1).Gap(5).Children(func() {
				ui.Text(c, v.label).FontSize(10).TextColor(design.Muted)
				ui.Text(c, fmt.Sprintf("%d", v.n)).Bold().FontSize(18)
			})
		}
	})
	groups := []struct {
		name  string
		match func(records.OrganizationEntry) bool
	}{
		{"自动完成", func(e records.OrganizationEntry) bool { return !e.Item.RequiresReview() && e.State == "applied" }},
		{"需要你决定", func(e records.OrganizationEntry) bool { return e.State == "review_pending" }},
		{"已确认", func(e records.OrganizationEntry) bool { return e.Item.RequiresReview() && e.State == "applied" }},
		{"已拒绝", func(e records.OrganizationEntry) bool { return e.State == "rejected" }},
		{"未完成与过期", func(e records.OrganizationEntry) bool {
			return e.State != "applied" && e.State != "review_pending" && e.State != "rejected"
		}},
	}
	for _, group := range groups {
		count := 0
		for _, e := range t.Entries {
			if group.match(e) {
				count++
			}
		}
		if count == 0 {
			continue
		}
		ui.Divider(c)
		ui.Text(c, group.name).Bold().FontSize(12)
		for _, e := range t.Entries {
			if !group.match(e) {
				continue
			}
			a.materialOrganizationResultRow(c, t, e)
		}
	}
	if t.Result != nil && len(t.Entries) == 0 {
		ui.Text(c, "本次没有新的可应用内容，已有知识保持不变。").Bold()
	}
	ui.Collapsible(c, "本次使用的材料", &s.DiagnosticOpen, func() {
		for _, input := range t.Snapshot.Inputs {
			ui.Text(c, fmt.Sprintf("%s · v%d", input.Title, input.Revision)).Bold()
			ui.Text(c, input.Text).Selectable().FontSize(11).LineHeight(1.6)
		}
		meta(c, "任务", t.ID)
		meta(c, "执行记录", fmt.Sprintf("%d", t.Attempt))
	})
	if t.Result != nil && t.Result.Explanation != "" {
		ui.Collapsible(c, "生成说明", &s.ExplanationOpen, func() { ui.Text(c, t.Result.Explanation).Selectable().FontSize(11).LineHeight(1.6) })
	}
}
func (a *App) materialOrganizationResultRow(c *ui.Context, t records.OrganizationTask, e records.OrganizationEntry) {
	ui.Column(c).Key("organization-entry-"+e.ID).Gap(8).Padding(8, 0).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
		ui.Row(c).Gap(12).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
				title := e.Item.Title
				if title == "" {
					title = organizationActionLabel(e.Item.Action)
				}
				if e.Item.Action == "add_tags" {
					title = "新增标签：" + strings.Join(e.Item.Tags, "、")
				}
				ui.Text(c, title).Bold().FontSize(12)
				ui.Text(c, organizationActionLabel(e.Item.Action)+" · "+organizationEntryLabel(e.State)).FontSize(10).TextColor(design.Muted)
			})
			if e.Item.RequiresReview() {
				quietButton(c, "查看建议").Key("organization-review-open-" + e.ID).Disabled(a.organization.Busy).OnClick(func() { a.openOrganizationEntry(e.ID) })
			} else if e.Receipt != nil && e.Receipt.Memory != "" {
				quietButton(c, "阅读").Key("organization-result-read-" + e.ID).Disabled(a.organization.Busy).OnClick(func() { a.openOrganizationMemory(e.Receipt.Memory) })
			}
		})
		if e.Receipt != nil {
			if v, ok := a.organization.Results[e.Receipt.Memory]; ok {
				ui.Text(c, a.organizationResultLabel(v)).FontSize(10).TextColor(design.Muted)
			}
		}
		if e.Error != "" {
			ui.Text(c, experienceDistillError(e.Error)).FontSize(11)
		}
	})
}
func (a *App) materialOrganizationReview(c *ui.Context, t records.OrganizationTask, e records.OrganizationEntry) {
	ui.Row(c).Gap(8).Children(func() {
		ui.Text(c, organizationActionLabel(e.Item.Action)).FontSize(10).TextColor(design.Muted).Grow(1)
		quietButton(c, "返回整理结果").Key("organization-review-return").OnClick(a.showOrganizationResult)
	})
	title := e.Item.Title
	if title == "" {
		title = organizationActionLabel(e.Item.Action)
	}
	ui.Text(c, title).FontSize(design.Title).Bold()
	ui.Text(c, "确认前，已有有效知识保持原样。").FontSize(11).TextColor(design.Muted)
	ui.Text(c, organizationEntryLabel(e.State)).Key("organization-review-state").FontSize(11)
	if e.State == "rejected" && e.Receipt != nil && e.Receipt.RejectionReason != "" {
		ui.Text(c, "拒绝理由："+e.Receipt.RejectionReason).Key("organization-rejected-reason").Selectable().FontSize(11)
	}
	if e.State == "review_pending" && !a.organizationSourcesCurrent(t, e) {
		ui.Text(c, "来源已经变化或当前读取未完成，不能提交新的确认；原始依据与建议保留。").Key("organization-review-stale").FontSize(11)
	}
	source := func() {
		ui.Column(c).Gap(12).Padding(16).Border(1, design.Border).Radius(6).Children(func() {
			ui.Text(c, "整理时的来源与范围").FontSize(10).TextColor(design.Muted)
			for _, ref := range e.Item.Sources {
				for _, input := range t.Snapshot.Inputs {
					if input.Memory != ref.Memory {
						continue
					}
					ui.Text(c, fmt.Sprintf("%s · v%d", input.Title, input.Revision)).Bold().FontSize(12)
					ui.Text(c, input.Text).Selectable().LineHeight(1.7)
					ui.Text(c, a.scopeLabel(t.Project)).FontSize(10).TextColor(design.Muted)
					if v, ok := a.organization.Sources[ref.Memory]; ok && (v.Revision != ref.Revision || v.Dataset != t.Project || v.State != "active") {
						ui.Text(c, fmt.Sprintf("当前版本 v%d · %s", v.Revision, a.memoryScopeName(v))).FontSize(10)
						ui.Text(c, v.Text).Selectable().FontSize(11).LineHeight(1.6)
					}
				}
			}
		})
	}
	proposal := func() {
		ui.Column(c).Gap(12).Padding(16).Border(1, design.Border).Radius(6).Children(func() {
			ui.Text(c, "建议结果").FontSize(10).TextColor(design.Muted)
			if e.Item.Action == "global_experience" {
				d := a.experienceReviewDraft(e)
				frozen := a.organization.Busy || d.Request != nil || d.RejectRequest != nil || e.State != "review_pending"
				ui.Text(c, "标题").FontSize(11)
				ui.TextInput(c.Key("experience-review-title-"+e.ID), &d.Value.Title).Height(34).Label("标题").Disabled(frozen)
				ui.Text(c, "通用表述").FontSize(11)
				ui.TextArea(c.Key("experience-review-text-"+e.ID), &d.Value.Text).Height(150).Label("通用表述").Disabled(frozen)
				ui.Text(c, "适用条件").FontSize(11)
				ui.TextArea(c.Key("experience-review-conditions-"+e.ID), &d.Value.Conditions).Height(80).Label("适用条件").Disabled(frozen)
				if e.Receipt != nil && e.Receipt.EditedExperience != nil {
					ui.Text(c, "本次保存的用户确认内容").Bold()
					ui.Text(c, e.Receipt.EditedExperience.Text).Selectable()
					ui.Text(c, e.Receipt.EditedExperience.Conditions).Selectable()
				}
			} else {
				if e.Item.Text != "" {
					ui.Text(c, e.Item.Title).Bold()
					ui.Text(c, e.Item.Text).Selectable().LineHeight(1.7)
				}
				ui.Text(c, e.Item.Reason).FontSize(11).Selectable().LineHeight(1.6)
			}
		})
	}
	width, _ := c.Size()
	if width >= 1200 {
		ui.Row(c).Gap(16).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Children(source)
			ui.Column(c).Grow(1).MinWidth(0).Children(proposal)
		})
	} else {
		source()
		proposal()
	}
	ui.Divider(c)
	ui.Text(c, "具体影响").Bold().FontSize(12)
	switch e.Item.Action {
	case "merge":
		ui.Text(c, "确认后新建合并知识，并归档以上全部来源知识；原项目、出处与历史保留。").FontSize(11)
	case "overwrite":
		ui.Text(c, "确认后替换目标知识正文并产生新版本；旧版本与出处保留。").FontSize(11)
	case "delete":
		ui.Text(c, "确认后逻辑删除目标知识，排除当前检索；历史仍保留，恢复产生新版本。").FontSize(11)
	case "move":
		ui.Text(c, "移入项目："+a.scopeLabel(e.Item.Destination)).Bold()
		ui.Text(c, "确认后正文保留，原项目检索立即排除。移动范围不授予其他项目知识读取权限。").FontSize(11)
	case "global_experience":
		ui.Text(c, "确认后新建独立通用经验；原项目与出处保留。项目明确约定优先，来源关联不授予原项目读取权限。").FontSize(11)
	default:
		ui.Text(c, "确认只记录已核对，不裁决冲突或改变已有知识。").FontSize(11)
	}
	if e.Item.Action == "overwrite" || e.Item.Action == "merge" {
		for _, input := range t.Snapshot.Inputs {
			included := e.Item.Target == input.Memory
			for _, ref := range e.Item.Sources {
				included = included || e.Item.Action == "merge" && ref.Memory == input.Memory
			}
			if included {
				ui.Text(c, input.Title+" · 差异").FontSize(11).Bold()
				ui.Text(c, lineDiff(input.Text, e.Item.Text)).Selectable().FontSize(11).LineHeight(1.6).Padding(10).Background(design.Surface)
			}
		}
	}
	if e.State == "review_pending" {
		draft := a.organizationRejectDraft(e.ID)
		frozen := a.organization.Busy || a.organization.Decisions[e.ID+"false"].Receipt != "" || a.organization.Decisions[e.ID+"true"].Receipt != ""
		if e.Item.Action == "global_experience" {
			d := a.experienceReviewDraft(e)
			frozen = frozen || d.Request != nil || d.RejectRequest != nil
		}
		ui.Text(c, "拒绝理由（可选）").FontSize(11).TextColor(design.Muted)
		ui.TextInput(c.Key("organization-reject-reason-"+e.ID), &draft.Reason).Label("拒绝理由（可选）").Height(34).Disabled(frozen)
		if len(draft.Reason) > 1024 {
			ui.Text(c, "拒绝理由超过保存上限，请缩短后提交。").FontSize(11)
		}
	}
}

type OrganizationRejectDraft struct{ Reason string }

func (a *App) organizationRejectDraft(id string) *OrganizationRejectDraft {
	s := &a.organization
	if s.RejectReasons == nil {
		s.RejectReasons = map[string]*OrganizationRejectDraft{}
	}
	if s.RejectReasons[id] == nil {
		s.RejectReasons[id] = &OrganizationRejectDraft{}
	}
	return s.RejectReasons[id]
}

func nextOrganizationReview(t records.OrganizationTask, id string) string {
	for i, e := range t.Entries {
		if e.ID != id {
			continue
		}
		for n := 1; n < len(t.Entries); n++ {
			e = t.Entries[(i+n)%len(t.Entries)]
			if e.Item.RequiresReview() {
				return e.ID
			}
		}
	}
	return ""
}
func (a *App) materialOrganizationReviewActions(c *ui.Context, t records.OrganizationTask, e records.OrganizationEntry) {
	s := &a.organization
	pending := 0
	for _, item := range t.Entries {
		if item.State == "review_pending" {
			pending++
		}
	}
	ui.Text(c, fmt.Sprintf("待确认 %d 项", pending)).FontSize(10).TextColor(design.Muted).Grow(1).MinWidth(0)
	confirmedRequest := s.Decisions[e.ID+"true"].Receipt != ""
	valid := true
	if e.Item.Action == "global_experience" {
		d := a.experienceReviewDraft(e)
		confirmedRequest = d.Request != nil
		valid = d.Value.Valid()
	}
	blocked := s.Busy || e.State != "review_pending" || !valid || !confirmedRequest && !a.organizationSourcesCurrent(t, e)
	rejectLabel := "拒绝建议"
	if s.Decisions[e.ID+"false"].Receipt != "" {
		rejectLabel = "重试拒绝回执"
	}
	quietButton(c, rejectLabel).Key("organization-reject-" + e.ID).Disabled(s.Busy || e.State != "review_pending" || confirmedRequest || len(a.organizationRejectDraft(e.ID).Reason) > 1024).OnClick(func() { a.reviewOrganizationEntry(t, e, false) })
	label := "确认这项改动"
	if e.Item.Action == "duplicate" || e.Item.Action == "conflict" {
		label = "记录已核对此项"
	}
	if confirmedRequest {
		label = "重试确认回执"
	}
	actionButton(c, label, nil).Key("organization-confirm-" + e.ID).Disabled(blocked || s.Decisions[e.ID+"false"].Receipt != "").OnClick(func() {
		if e.Item.Action == "global_experience" {
			a.confirmExperienceReview(t, e)
		} else {
			a.reviewOrganizationEntry(t, e, true)
		}
	})
	if next := nextOrganizationReview(t, e.ID); next != "" {
		quietButton(c, "下一项").Key("organization-review-next").Disabled(s.Busy).OnClick(func() {
			a.openOrganizationEntry(next)
		})
	}
}
