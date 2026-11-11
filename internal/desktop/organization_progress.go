package desktop

import (
	"encoding/json"
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

// Progress is a read-only view of one attempt, independent of review selection.
type OrganizationProgressUI struct {
	Motion            projectStageMotion
	Root              string
	Epoch             uint64
	OrganizationEpoch string
	Task              records.OrganizationTask
	Results           map[string]MemoryItem
	Scroll            ui.ScrollState
	Loading           bool
	Error             string
	LatestAttempt     int64
}

func (a *App) organizationProgressOpen() bool {
	p := a.organization.Progress
	return a.page == "整理记录" && a.organizationMode && !a.organization.New && p != nil && p.Epoch == a.libraryEpoch && p.Root == a.library.Root && p.OrganizationEpoch == a.organization.Epoch && p.Task.ID == a.organization.Selected
}
func (a *App) openOrganizationProgress() {
	s := &a.organization
	if s.Task == nil || s.New {
		return
	}
	var task records.OrganizationTask
	b, _ := json.Marshal(s.Task)
	if json.Unmarshal(b, &task) != nil {
		return
	}
	results := map[string]MemoryItem{}
	for k, v := range s.Results {
		results[k] = v
	}
	s.Progress = &OrganizationProgressUI{Root: a.library.Root, Epoch: a.libraryEpoch, OrganizationEpoch: s.Epoch, Task: task, Results: results}
}
func (a *App) closeOrganizationProgress() {
	a.organization.Progress = nil
	a.organization.RestoreFocus = true
	a.organization.Focus = "organization-progress-open"
}
func (a *App) organizationProgressHeader(c *ui.Context) {
	if a.organizationProgressOpen() {
		quietButton(c, "查看知识").Key("organization-progress-knowledge").OnClick(func() { a.navigate("项目", a.organization.Progress.Task.Project) })
		return
	}
	button := quietButton(c, "查看进度").Key("organization-progress-open").OnClick(a.openOrganizationProgress)
	if a.organization.RestoreFocus && a.organization.Focus == "organization-progress-open" {
		button.Focus()
		a.organization.RestoreFocus = false
	}
}
func (a *App) refreshOrganizationProgress() {
	if !a.organizationProgressOpen() || a.libraryContext == nil {
		return
	}
	p := a.organization.Progress
	if p.Loading {
		return
	}
	p.Loading = true
	life, client, library, dispatch := a.libraryContext, a.organization.Client, a.library, a.dispatch
	go func() {
		t, err := client.Get(life, p.Task.ID)
		results := map[string]MemoryItem{}
		if err == nil && t.Attempt == p.Task.Attempt {
			for _, e := range t.Entries {
				if e.Receipt != nil && e.Receipt.Memory != "" {
					v, x := library.Get(life, e.Receipt.Memory)
					if x == nil {
						results[v.Memory] = displayVersion(v)
					}
				}
			}
		}
		dispatch(func() {
			if a.organization.Progress != p || !a.organizationProgressOpen() {
				return
			}
			p.Loading = false
			if err != nil {
				p.Error = "暂时无法读取任务进度，原任务与已有结果保留。"
				return
			}
			a.adoptOrganizationProgress(p, p.Root, p.Epoch, t, results)
		})
	}()
}

// Both existing refreshOrganization and explicit read share one attempt/owner gate.
func (a *App) adoptOrganizationProgress(p *OrganizationProgressUI, root string, epoch uint64, t records.OrganizationTask, results map[string]MemoryItem) {
	if p == nil || a.organization.Progress != p || !a.organizationProgressOpen() || p.Root != root || p.Epoch != epoch || t.ID != p.Task.ID {
		return
	}
	p.Error = ""
	if t.Attempt != p.Task.Attempt {
		p.LatestAttempt = t.Attempt
		return
	}
	var snapshot records.OrganizationTask
	b, err := json.Marshal(t)
	if err != nil || json.Unmarshal(b, &snapshot) != nil {
		return
	}
	p.Task = snapshot
	p.Results = map[string]MemoryItem{}
	for k, v := range results {
		p.Results[k] = v
	}
}
func organizationProgressMotionStage(p *OrganizationProgressUI) (string, bool) {
	if p.LatestAttempt != 0 {
		return "interrupted", false
	}
	if p.Task.Cancelled {
		return "cancelling", false
	}
	switch p.Task.State {
	case "queued":
		return "starting", true
	case "running":
		return "organizing", true
	case "applying":
		return "saving", true
	case "partial_failed":
		return "failed", false
	case "failed", "cancelled", "interrupted":
		return p.Task.State, false
	}
	return "ready", false
}

// These labels report only ledger evidence; task completion is not index proof.
func organizationProgressStages(t records.OrganizationTask, results map[string]MemoryItem) []string {
	analysis := "状态待核对"
	if t.State == "running" {
		analysis = "正在处理"
	}
	if t.Result != nil {
		analysis = "已取得整理结果"
	}
	saved, waiting, known, indexed, deleted := 0, 0, 0, 0, 0
	for _, e := range t.Entries {
		if e.State == "review_pending" {
			waiting++
		}
		if e.State == "applied" && e.Receipt != nil && e.Receipt.Memory != "" {
			if e.Item.Action == "delete" {
				deleted++
				continue
			}
			saved++
			if v, ok := results[e.Receipt.Memory]; ok && v.Revision == e.Receipt.Revision && v.State == "active" && v.Origin != "raw" {
				known++
				if v.IndexState == "indexed" {
					indexed++
				}
			}
		}
	}
	saving := fmt.Sprintf("已保存 %d 项 · 待确认 %d 项", saved, waiting)
	if deleted > 0 {
		saving += fmt.Sprintf(" · 已删除 %d 项", deleted)
	}
	index := "索引状态待核对"
	if saved == 0 && deleted > 0 {
		index = "删除项不需要建立索引"
	}
	if saved > 0 {
		index = fmt.Sprintf("当前版本已索引 %d/%d 项", indexed, saved)
		if known < saved {
			index += " · 部分状态待核对"
		}
	}
	return []string{"连接执行者 · 状态待核对", "分析材料 · " + analysis, "保存结果 · " + saving, "索引知识 · " + index}
}
func (a *App) organizationProgressDetail(c *ui.Context) {
	p := a.organization.Progress
	t := p.Task
	ui.Scroll(c.Key("organization-progress-scroll")).Grow(1).MinHeight(0).TrackScroll(&p.Scroll).Padding(24).Gap(16).Children(func() {
		ui.Text(c, "固定任务范围").FontSize(10).TextColor(design.Muted)
		label := a.scopeLabel(t.Project)
		if label == "" {
			label = a.organization.ProjectLabel
		}
		if label == "" {
			label = "本次项目"
		}
		ui.Text(c, label+"的整理").FontSize(design.Title).Bold()
		ui.Text(c, fmt.Sprintf("%s · 尝试 %d · %d 条固定材料", organizationExecutor(t), t.Attempt, len(t.Snapshot.Inputs))).FontSize(11).TextColor(design.Muted)
		ui.Row(c).Gap(10).Children(func() {
			for _, s := range organizationProgressStages(t, p.Results) {
				ui.Column(c).Grow(1).MinWidth(0).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Padding(8, 0).Children(func() { ui.Text(c, s).FontSize(10).TextColor(design.Muted) })
			}
		})
		stage, active := organizationProgressMotionStage(p)
		if active {
			a.drawProjectScene(c, "organization-progress-stage", p.Task.ID, p.Task.Attempt, stage, &p.Motion, 150)
		} else {
			ui.Box(c).Key("organization-progress-stage").Height(150).Draw(func(painter *ui.Painter, r ui.Rect) {
				drawProjectStage(painter, r, projectStageTarget(stage), stage, 0, true)
			})
		}
		ui.Column(c).Padding(14).Gap(8).Background(ui.Hex("#f7f9fc")).BorderWidth(1).BorderColor(design.Border).Children(func() {
			ui.Text(c, materialTaskStatus(t)).Key("organization-progress-state").FontSize(12).Bold()
			ui.Text(c, "进度由任务回执更新，离开页面后仍可回来查看。").FontSize(11).TextColor(design.Muted)
		})
		ui.Text(c, "查看不会重新开始整理，离开页面不会停止任务。").FontSize(11).TextColor(design.Muted)
		if p.LatestAttempt != 0 {
			ui.Text(c, fmt.Sprintf("任务已有尝试 %d；当前仍显示进入时的尝试 %d。返回结果后可查看新尝试。", p.LatestAttempt, t.Attempt)).FontSize(11)
		}
		if p.Error != "" {
			ui.Text(c, p.Error).FontSize(11)
		}
		quietButton(c, "刷新任务进度").Key("organization-progress-refresh").Disabled(p.Loading).OnClick(a.refreshOrganizationProgress)
		ui.Text(c, "本次固定材料").FontSize(12).Bold()
		for _, in := range t.Snapshot.Inputs {
			ui.Row(c).Gap(10).Children(func() {
				ui.Text(c, in.Title).FontSize(11).Grow(1).MinWidth(0)
				ui.Text(c, fmt.Sprintf("来源版本 v%d", in.Revision)).FontSize(10).Shrink(0).TextColor(design.Muted)
			})
		}
		for _, e := range t.Entries {
			if e.Receipt != nil && e.State == "applied" {
				ui.Text(c, fmt.Sprintf("已提交：%s · 回执版本 v%d", e.Item.Title, e.Receipt.Revision)).FontSize(11)
			}
		}
	})
	ui.Row(c).Shrink(0).Padding(12, 24).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		ui.Text(c, "当前项目、材料与审核选择保持不变").FontSize(10).Grow(1).MinWidth(0).TextColor(design.Muted)
		if (t.State == "queued" || t.State == "running" || t.State == "applying") && !t.Cancelled {
			quietButton(c, "停止整理").Key("organization-progress-cancel").Disabled(a.organization.Busy || a.organization.Task == nil || a.organization.Task.ID != t.ID || a.organization.Task.Attempt != t.Attempt || a.organization.Task.Cancelled).OnClick(func() {
				if a.organizationProgressOpen() && a.organization.Task != nil && a.organization.Task.ID == t.ID && a.organization.Task.Attempt == t.Attempt {
					a.cancelOrganization()
				}
			})
		}
		label := "返回任务结果"
		if a.organization.ReviewItem != "" {
			label = "返回建议审核"
		}
		quietButton(c, label).Key("organization-progress-close").OnClick(a.closeOrganizationProgress)
	})
}
