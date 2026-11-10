package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

type ExperienceDistillUI struct {
	Open                            bool
	Source, CurrentSource           MemoryItem
	SourceError                     string
	ReturnPage, ReturnScope         string
	ReturnScroll                    float32
	Task                            *records.OrganizationTask
	TaskID, CandidateID             string
	Start                           *records.OrganizationStart
	Saved                           *MemoryItem
	Sequence                        uint64
	Loading, Busy, CancelWanted     bool
	Error                           string
	DebugError                      string
	ExplanationOpen, DiagnosticOpen bool
}

func (a *App) commonDistillOpen() bool {
	s := a.experience.Distill
	return s != nil && s.Open && a.page == "通用知识" && a.detailMode == "view" && !a.editing
}

func (a *App) prepareExperienceDistill(s *ExperienceDistillUI) {
	if s == nil || s.Loading || s.Busy || a.libraryContext == nil {
		return
	}
	s.Loading = true
	s.Error = ""
	s.Sequence++
	sequence := s.Sequence
	library, life := a.library, a.libraryContext
	go func() {
		ctx, stop := context.WithTimeout(life, 5*time.Second)
		defer stop()
		config, err := library.ProcessingConfiguration(ctx)
		var source records.Version
		if err == nil {
			source, err = library.Get(ctx, s.Source.ID)
		}
		if life.Err() != nil {
			return
		}
		a.dispatch(func() {
			if a.experience.Distill != s || sequence != s.Sequence {
				return
			}
			s.Loading = false
			if s.CancelWanted {
				a.closeExperienceDistill()
				return
			}
			if err != nil {
				s.Error = "读取记忆处理配置或来源未完成，请重试。"
				return
			}
			s.CurrentSource = displayVersion(source)
			if source.Memory != s.Source.ID || source.Dataset != s.Source.Dataset || source.Revision != s.Source.Revision || source.State != "active" {
				s.Error = "来源版本已变化，请先重新核对项目知识。"
				return
			}
			current := config.Current
			if !current.Active || current.Revision == nil {
				s.Error = "请先在记忆处理中明确启用一种已验证的处理方式。"
				return
			}
			if current.Revision.Spec.Mode != "agent" || current.Revision.Spec.Agent != "pi" {
				s.Error = "当前处理方式尚未接通此提炼流程，请选择已验证的 pi 配置。"
				return
			}
			s.Start = &records.OrganizationStart{OperationID: uuid.NewString(), Project: source.Dataset, Inputs: []records.MergeTarget{{Memory: source.Memory, Revision: source.Revision}}, Review: true, Global: true, Purpose: "global", Processing: &records.ProcessingTaskAuthorization{Revision: current.Revision.ID, Generation: current.Generation}}
			a.submitExperienceDistill(s)
		})
	}()
}

func (a *App) submitExperienceDistill(s *ExperienceDistillUI) {
	if s == nil || s.Busy || s.Start == nil {
		return
	}
	s.Busy = true
	s.Error = ""
	s.Sequence++
	sequence := s.Sequence
	request := *s.Start
	client, life := a.organization.Client, a.libraryContext
	go func() {
		ctx, stop := context.WithTimeout(life, 10*time.Second)
		defer stop()
		task, err := client.Start(ctx, request)
		if life.Err() != nil {
			return
		}
		a.dispatch(func() {
			if a.experience.Distill != s || sequence != s.Sequence {
				return
			}
			s.Busy = false
			if err != nil {
				s.Error = "生成请求未完成，请核对处理配置；可以按同一请求重试。"
				s.DebugError = err.Error()
				return
			}
			s.Start = nil
			s.TaskID = task.ID
			s.Task = &task
			if s.CancelWanted {
				a.cancelExperienceDistill()
				return
			}
			a.refreshExperienceDistill()
		})
	}()
}

func (a *App) refreshExperienceDistill() {
	s := a.experience.Distill
	if s == nil || s.Loading || s.Busy || s.TaskID == "" || a.libraryContext == nil || a.visualFixture {
		return
	}
	s.Loading = true
	s.Sequence++
	sequence, id := s.Sequence, s.TaskID
	library, client, life := a.library, a.organization.Client, a.libraryContext
	go func() {
		ctx, stop := context.WithTimeout(life, 5*time.Second)
		defer stop()
		task, err := client.Get(ctx, id)
		source, sourceErr := library.Get(ctx, s.Source.ID)
		var saved records.Version
		var savedErr error
		if err == nil {
			for _, e := range task.Entries {
				if e.Receipt != nil && e.Receipt.Memory != "" {
					saved, savedErr = library.Get(ctx, e.Receipt.Memory)
					break
				}
			}
		}
		if life.Err() != nil {
			return
		}
		a.dispatch(func() {
			if a.experience.Distill != s || sequence != s.Sequence || id != s.TaskID {
				return
			}
			s.Loading = false
			if err != nil {
				s.Error = "提炼状态读取未完成，保留上次预览；请重试。"
				return
			}
			s.Error = ""
			if savedErr != nil {
				s.Error = "通用经验已保存，正文回执读取未完成，可以重试读取。"
			}
			s.Task = &task
			if sourceErr != nil {
				s.SourceError = "来源读取未完成，暂不能确认保存。"
			} else {
				s.CurrentSource = displayVersion(source)
				s.SourceError = ""
			}
			if saved.Memory != "" {
				v := displayVersion(saved)
				s.Saved = &v
			}
			if s.CandidateID == "" {
				for _, e := range task.Entries {
					if e.Item.Action == "global_experience" {
						s.CandidateID = e.ID
						break
					}
				}
			}
		})
	}()
}

func distillTaskRunning(task *records.OrganizationTask) bool {
	if task == nil {
		return false
	}
	return task.State == "queued" || task.State == "running" || task.State == "applying"
}

func (a *App) closeExperienceDistill() {
	s := a.experience.Distill
	if s == nil {
		return
	}
	s.Open = false
	a.navigate(s.ReturnPage, s.ReturnScope)
	a.detailScroll.Y = s.ReturnScroll
}

func (a *App) cancelExperienceDistill() {
	s := a.experience.Distill
	if s == nil {
		return
	}
	s.CancelWanted = true
	if s.TaskID == "" {
		if !s.Busy {
			a.closeExperienceDistill()
		}
		return
	}
	if s.Busy {
		return
	}
	s.Busy = true
	s.Sequence++
	sequence, id := s.Sequence, s.TaskID
	client, life := a.organization.Client, a.libraryContext
	go func() {
		ctx, stop := context.WithTimeout(life, 10*time.Second)
		defer stop()
		task, err := client.Cancel(ctx, id)
		if life.Err() != nil {
			return
		}
		a.dispatch(func() {
			if a.experience.Distill != s || sequence != s.Sequence {
				return
			}
			s.Busy = false
			if err != nil {
				s.Error = "取消请求未完成，请重试核对任务。"
				s.CancelWanted = false
				return
			}
			s.Task = &task
			a.refreshExperienceDistill()
		})
	}()
}

func (a *App) confirmDistillExperience(e records.OrganizationEntry, confirm bool) {
	s := a.experience.Distill
	if s == nil || s.Task == nil || s.Busy || e.State != "review_pending" {
		return
	}
	d := a.experienceReviewDraft(e)
	if confirm && (!d.Value.Valid() || d.Request == nil && (s.SourceError != "" || s.CurrentSource.Revision != s.Source.Revision || s.CurrentSource.State != "active" || s.CurrentSource.Dataset != s.Source.Dataset)) {
		return
	}
	var request records.OrganizationReview
	if confirm {
		if d.Request == nil {
			edit := d.Value
			d.Request = &records.OrganizationReview{Task: s.TaskID, Item: e.ID, Digest: records.MergeDigest(e.Item), Receipt: uuid.NewString(), Confirm: true, Edit: &edit}
		}
		request = *d.Request
	} else {
		if d.RejectRequest == nil {
			d.RejectRequest = &records.OrganizationReview{Task: s.TaskID, Item: e.ID, Digest: records.MergeDigest(e.Item), Receipt: uuid.NewString(), Confirm: false}
		}
		request = *d.RejectRequest
	}
	s.Busy = true
	s.Sequence++
	sequence := s.Sequence
	client, life := a.organization.Client, a.libraryContext
	go func() {
		ctx, stop := context.WithTimeout(life, 10*time.Second)
		defer stop()
		task, err := client.Review(ctx, request)
		if life.Err() != nil {
			return
		}
		a.dispatch(func() {
			if a.experience.Distill != s || sequence != s.Sequence {
				return
			}
			s.Busy = false
			if err != nil {
				s.Error = "确认回执未完成，草稿与原候选保留，可以按同一回执重试。"
				s.DebugError = err.Error()
				return
			}
			s.Error = ""
			s.Task = &task
			a.refreshExperienceDistill()
			a.refresh(false)
		})
	}()
}

func (a *App) experienceDistillPreview(c *ui.Context) {
	s := a.experience.Distill
	if s == nil {
		return
	}
	var entry *records.OrganizationEntry
	if s.Task != nil {
		for i := range s.Task.Entries {
			e := &s.Task.Entries[i]
			if e.ID == s.CandidateID {
				entry = e
				break
			}
		}
	}
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Scroll(c.Key("experience-distill-body")).Grow(1).MinHeight(0).TrackScroll(&a.detailScroll).Padding(24).Gap(18).Children(func() {
			ui.Text(c, "新建通用经验").FontSize(10).TextColor(design.Muted)
			ui.Text(c, "先看清通用表述与适用条件").FontSize(design.Title).Bold()
			ui.Text(c, "只保存你确认的通用表述，原项目知识按原项目权限保留。重新生成会再次消耗模型额度。").FontSize(11).TextColor(design.Muted)
			if s.Task == nil {
				if s.Loading {
					ui.Text(c, "正在读取记忆处理配置与来源…").Key("experience-distill-state")
				} else if s.Busy {
					ui.Text(c, "正在提交生成请求…").Key("experience-distill-state")
				}
			} else {
				ui.Column(c).Key("experience-distill-status").Padding(16).Gap(8).Radius(7).Border(1, design.Border).Background(design.Surface).Children(func() {
					ui.Text(c, experienceDistillStatus(s.Task, entry)).Key("experience-distill-state").FontSize(12)
					if p := s.Task.Snapshot.Processing; p != nil {
						ui.Text(c, fmt.Sprintf("pi · %s", p.Revision.Spec.Model)).FontSize(10).TextColor(design.Muted)
					}
					if distillTaskRunning(s.Task) {
						quietButton(c, "取消生成").Key("experience-distill-cancel").OnClick(a.cancelExperienceDistill)
					}
				})
				if s.Task.Error != "" {
					ui.Text(c, experienceDistillError(s.Task.Error)).FontSize(11)
				}

			}
			if s.Error != "" {
				ui.Text(c, s.Error).Selectable().FontSize(11)
				quietButton(c, "重试读取提炼状态").Key("experience-distill-refresh").Disabled(s.Busy || s.Loading).OnClick(func() {
					if s.TaskID != "" {
						a.refreshExperienceDistill()
					} else if s.Start != nil {
						a.submitExperienceDistill(s)
					} else {
						a.prepareExperienceDistill(s)
					}
				})
			}
			if s.SourceError != "" {
				ui.Text(c, s.SourceError).FontSize(11)
			} else if s.CurrentSource.ID != "" && (s.CurrentSource.Revision != s.Source.Revision || s.CurrentSource.State != "active" || s.CurrentSource.Dataset != s.Source.Dataset) {
				ui.Text(c, "来源版本已经变化，本次候选不能继续确认。请重新核对并生成。").Key("experience-distill-stale").FontSize(11)
			}

			if s.Task != nil && len(s.Task.Entries) > 1 {
				ui.Row(c).Gap(8).Children(func() {
					for _, e := range s.Task.Entries {
						if e.Item.Action == "global_experience" {
							quietButton(c, e.Item.Title).Key("experience-distill-candidate-" + e.ID).OnClick(func() { s.CandidateID = e.ID })
						}
					}
				})
			}
			sourcePanel := func() {
				ui.Column(c).Border(1, design.Border).Radius(7).Padding(16).Gap(10).Children(func() {
					ui.Text(c, fmt.Sprintf("%s · 来源 v%d", a.memoryScopeName(s.Source), s.Source.Revision)).FontSize(10).TextColor(design.Muted)
					ui.Text(c, s.Source.Title).Bold()
					ui.Text(c, s.Source.Text).Key("experience-distill-source").Selectable().LineHeight(1.8)
				})
			}
			candidatePanel := func() {

				if entry != nil {
					d := a.experienceReviewDraft(*entry)
					if entry.State == "review_pending" || entry.State == "expired" {
						ui.Column(c).Border(1, design.Border).Radius(7).Padding(16).Gap(12).Children(func() {
							ui.Text(c, "将新增到：通用经验").FontSize(10).TextColor(design.Muted)
							frozen := s.Busy || d.Request != nil || entry.State != "review_pending"
							ui.Text(c, "标题").FontSize(11).TextColor(design.Muted)
							ui.TextInput(c.Key("experience-review-title-"+entry.ID), &d.Value.Title).Label("标题").Height(34).Disabled(frozen)
							ui.Text(c, "通用表述").FontSize(11).TextColor(design.Muted)
							ui.TextArea(c.Key("experience-review-text-"+entry.ID), &d.Value.Text).Label("通用表述").Height(160).LineHeight(1.7).Disabled(frozen)
							ui.Text(c, "适用条件").FontSize(11).TextColor(design.Muted)
							ui.TextArea(c.Key("experience-review-conditions-"+entry.ID), &d.Value.Conditions).Label("适用条件").Height(80).LineHeight(1.7).Disabled(frozen)
							ui.Collapsible(c, "模型原候选", &d.OriginalOpen, func() {
								ui.Text(c, entry.Item.Title).Bold()
								ui.Text(c, entry.Item.Text).Selectable().LineHeight(1.7)
								ui.Text(c, entry.Item.Conditions).Selectable().LineHeight(1.7)
							})
						})
					} else {
						ui.Text(c, "此候选："+organizationEntryLabel(entry.State)).FontSize(12)
						if entry.Receipt != nil && entry.Receipt.EditedExperience != nil {
							ui.Text(c, entry.Receipt.EditedExperience.Text).Selectable().LineHeight(1.7)
						}
					}
				}
			}
			width, _ := c.Size()
			if width >= 1200 && entry != nil {
				ui.Row(c).Gap(16).AlignItems(ui.Start).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Children(sourcePanel)
					ui.Column(c).Grow(1).MinWidth(0).Children(candidatePanel)
				})
			} else {
				sourcePanel()
				candidatePanel()
			}

			if s.Task != nil && s.Task.Result != nil && s.Task.Result.Explanation != "" {
				ui.Collapsible(c, "生成说明", &s.ExplanationOpen, func() { ui.Text(c, s.Task.Result.Explanation).Selectable().FontSize(11).LineHeight(1.7) })
			}
			if s.DebugError != "" {
				ui.Collapsible(c, "查看诊断", &s.DiagnosticOpen, func() { ui.Text(c, s.DebugError).Selectable().FontSize(11) })
			}

			if s.Saved != nil {
				ui.Text(c, "通用经验已保存；"+a.availableIndexLabel(s.Saved.IndexState)).Key("experience-distill-saved").FontSize(12)
			}
			if s.Task == nil && s.Error != "" {
				quietButton(c, "前往记忆处理").Key("experience-distill-processing").OnClick(func() { a.navigate("设置", ""); a.settingsSection = "processing"; a.refreshProcessing() })
			}
		})
		ui.Row(c).Shrink(0).Padding(12, 24).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
			ui.Text(c, "保存前再次核对来源版本").FontSize(10).TextColor(design.Muted).Grow(1).MinWidth(0)
			if s.Task == nil && s.Busy {
				quietButton(c, "取消生成").Key("experience-distill-cancel").OnClick(a.cancelExperienceDistill)
			}
			if entry != nil && (entry.State == "review_pending" || entry.State == "expired") {
				d := a.experienceReviewDraft(*entry)
				label := "确认保存为通用经验"
				if d.Request != nil {
					label = "重试确认回执"
				}
				quietButton(c, "拒绝此项").Key("experience-distill-reject").Disabled(s.Busy || entry.State != "review_pending").OnClick(func() { a.confirmDistillExperience(*entry, false) })
				actionButton(c, label, nil).Key("experience-distill-confirm").Disabled(s.Busy || entry.State != "review_pending" || !d.Value.Valid() || d.Request == nil && (s.SourceError != "" || s.CurrentSource.Revision != s.Source.Revision || s.CurrentSource.State != "active" || s.CurrentSource.Dataset != s.Source.Dataset)).OnClick(func() { a.confirmDistillExperience(*entry, true) })
			}
			if s.Task != nil && !distillTaskRunning(s.Task) && s.Saved == nil && (s.Task.State == "failed" || s.Task.State == "interrupted" || s.Task.Cancelled || s.Task.Result != nil && len(s.Task.Entries) == 0 || s.CurrentSource.ID != "" && s.CurrentSource.Revision != s.Source.Revision) {
				quietButton(c, "重新生成").Key("experience-distill-regenerate").Disabled(s.Busy).OnClick(a.repeatExperienceDistill)
			}
			if s.Saved != nil {
				quietButton(c, "查看通用经验").Key("experience-distill-open-saved").OnClick(func() { s.Open = false; a.openMergeMemory(s.Saved.ID, s.Saved.Dataset) })
			}
		})
	})
}

func (a *App) showExperienceDistill() {
	s := a.experience.Distill
	if s == nil {
		return
	}
	s.ReturnPage, s.ReturnScope, s.ReturnScroll = a.page, a.scope, a.detailScroll.Y
	s.Open = true
	a.navigate("通用知识", "")
	a.detailMode = "view"
	a.editing = false
	a.detailScroll.Y = 0
	a.refreshExperienceDistill()
}

func (a *App) repeatExperienceDistill() {
	old := a.experience.Distill
	if old == nil || old.Busy || distillTaskRunning(old.Task) {
		return
	}
	source := old.CurrentSource
	if source.ID == "" {
		source = old.Source
	}
	s := &ExperienceDistillUI{Open: true, Source: source, ReturnPage: old.ReturnPage, ReturnScope: old.ReturnScope, ReturnScroll: old.ReturnScroll}
	a.experience.Distill = s
	a.detailScroll.Y = 0
	a.prepareExperienceDistill(s)
}

func experienceDistillStatus(task *records.OrganizationTask, entry *records.OrganizationEntry) string {
	if task.Cancelled {
		var process struct {
			Reaped bool `json:"process_reaped"`
			Gone   bool `json:"process_group_gone"`
		}
		_ = json.Unmarshal(task.Execution, &process)
		if process.Reaped && process.Gone {
			return "生成已取消，原项目知识保留。"
		}
		return "已请求取消生成，正在核对停止结果。"
	}
	if distillTaskRunning(task) {
		return "正在生成候选；生成完成不会自动保存。"
	}
	if task.Result != nil && len(task.Entries) == 0 {
		return "本次没有可泛化的候选，原项目知识保留。"
	}
	if entry != nil {
		switch entry.State {
		case "review_pending":
			return "候选已生成，尚未保存。"
		case "applied":
			return "通用经验已确认保存。"
		case "rejected":
			return "已拒绝此候选，已有知识保留。"
		case "expired":
			return "来源已变化，候选已过期。"
		}
	}
	return organizationStatus(task.State)
}

func (a *App) cancelAndCloseExperienceDistill() {
	s := a.experience.Distill
	if s == nil {
		return
	}
	if distillTaskRunning(s.Task) || s.Task == nil && (s.Busy || s.Loading) {
		a.cancelExperienceDistill()
	}
	a.closeExperienceDistill()
}

func experienceDistillError(code string) string {
	switch code {
	case "configuration_required":
		return "尚未启用处理方式，请先配置记忆处理。"
	case "verification_required", "connection_verification_required":
		return "处理方式需要重新验证，未开始保存。"
	case "connection_changed", "dependency_changed":
		return "连接或处理配置已变化，请重新验证后再生成。"
	case "unsupported_processing":
		return "当前处理方式尚未支持此提炼流程。"
	case "invalid_proposal":
		return "模型候选的格式或出处未通过核验，未保存。"
	case "agent_timeout":
		return "生成超时，原项目知识保留。"
	case "lease_interrupted":
		return "生成已中断，原项目知识保留。"
	default:
		return "生成未完成，请核对处理配置与账号额度后重试。"
	}
}
