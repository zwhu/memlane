package desktop

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

type ExperienceUI struct {
	Distill                 *ExperienceDistillUI
	Copy                    *records.ExperienceCopy
	Source                  MemoryItem
	LatestSource            MemoryItem
	SourceError             string
	CurrentRevision         int64
	ReturnPage, ReturnScope string
	ReturnScroll            float32
	Title, Conditions       string
	Submitted               bool
	Receipt                 *records.OrganizationReceipt
	Busy                    bool
	Error                   string
}

func experienceKindLabel(kind string) string {
	if kind == "copy" {
		return "用户原文复制"
	}
	return "用户确认通用提炼"
}

func (a *App) isProjectKnowledge(v MemoryItem) bool {
	if v.State != "active" || v.Category == "原始记录" || v.Experience != nil || v.Derived != nil && v.Derived.State == "stale" {
		return false
	}
	for _, scope := range a.scopes {
		if scope.ID == v.Dataset && scope.Kind == "project" {
			return true
		}
	}
	return false
}

func (a *App) beginExperienceCopy(v MemoryItem) {
	if !a.isProjectKnowledge(v) || a.experience.Busy {
		return
	}
	a.experience.Copy = &records.ExperienceCopy{OperationID: uuid.NewString(), Project: v.Dataset, Memory: v.ID, Revision: v.Revision}
	a.experience.Source = v
	a.experience.LatestSource, a.experience.SourceError = v, ""
	a.experience.CurrentRevision = v.Revision
	a.experience.ReturnPage, a.experience.ReturnScope = a.page, a.scope
	a.experience.ReturnScroll = a.detailScroll.Y
	a.experience.Title, a.experience.Conditions = v.Title, ""
	a.experience.Submitted = false
	a.experience.Receipt = nil
	a.experience.Error = ""
	a.commonCategory = ""
	a.navigate("通用知识", "")
	a.editing, a.detailMode = false, "view"
	a.detailScroll.Y = 0
	a.refresh(false)
}

func (a *App) experienceCopyPreview(c *ui.Context) {
	s := &a.experience
	if s.Copy == nil {
		return
	}
	width, _ := c.Size()
	source := func() {
		ui.Column(c).Key("experience-copy-source-panel").Grow(1).MinWidth(0).Border(1, design.Border).Radius(7).Padding(16).Gap(12).Children(func() {
			ui.Text(c, fmt.Sprintf("%s · 来源 v%d", a.memoryScopeName(s.Source), s.Copy.Revision)).FontSize(10).TextColor(design.Muted)
			ui.Text(c, s.Source.Title).Bold().FontSize(13)
			ui.Text(c, s.Source.Text).Key("experience-copy-source").Selectable().LineHeight(1.8)
		})
	}
	destination := func() {
		ui.Column(c).Key("experience-copy-destination-panel").Grow(1).MinWidth(0).Border(1, design.Border).Radius(7).Padding(16).Gap(12).Children(func() {
			ui.Text(c, "将新增到：通用经验").FontSize(10).TextColor(design.Muted)
			ui.Text(c, "标题").FontSize(11).TextColor(design.Muted)
			ui.TextInput(c.Key("experience-copy-title"), &s.Title).Label("标题").Height(34).Disabled(s.Busy || s.Submitted)
			ui.Text(c, "原文（保持一致）").FontSize(11).TextColor(design.Muted)
			ui.Column(c).Padding(12).Border(1, design.Border).Radius(5).Children(func() {
				ui.Text(c, s.Source.Text).Key("experience-copy-original").Selectable().LineHeight(1.8)
			})
			ui.Text(c, "适用条件").FontSize(11).TextColor(design.Muted)
			ui.TextArea(c.Key("experience-copy-conditions"), &s.Conditions).Label("适用条件").Placeholder("说明哪些项目或情形适用；项目明确约定优先").Height(72).Disabled(s.Busy || s.Submitted)
		})
	}
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Scroll(c.Key("experience-copy-scroll")).Grow(1).MinHeight(0).TrackScroll(&a.detailScroll).Padding(24).Gap(20).Children(func() {
			ui.Text(c, "新建通用经验").FontSize(10).TextColor(design.Muted)
			ui.Text(c, "原项目内容仍然保留").FontSize(design.Title).Bold()
			ui.Text(c, "复制完整正文为独立通用经验，不调用模型。").FontSize(11).TextColor(design.Muted)
			if s.SourceError != "" {
				ui.Text(c, s.SourceError).FontSize(11)
				quietButton(c, "重试来源读取").Key("experience-copy-source-retry").OnClick(func() { a.refresh(false) })
			} else if s.Copy.Revision != s.CurrentRevision {
				ui.Text(c, "来源版本已经变化，请重新核对来源。已提交请求可以继续核对原回执。").FontSize(11)
			}
			if width >= 1200 {
				ui.Row(c).Gap(16).AlignItems(ui.Start).Children(func() { source(); destination() })
			} else {
				source()
				destination()
			}
			if s.Receipt != nil {
				ui.Text(c, "通用知识副本已保存，重试读取副本。").Bold()
			}
			if s.Error != "" {
				ui.Text(c, s.Error).Selectable()
			}
			ui.Text(c, "只新建通用知识，原项目知识、来源与历史保留。来源关联不自动授予原项目读取权限；项目明确约定优先。").FontSize(11).TextColor(design.Muted)
		})
		ui.Row(c).Shrink(0).Padding(12, 24).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Gap(8).Children(func() {
			ui.Text(c, "保存前再次核对来源版本").FontSize(10).TextColor(design.Muted).Grow(1).MinWidth(0)
			label := "确认保存为通用经验"
			if s.Error != "" || s.Receipt != nil {
				label = "重试复制回执"
			}
			stale := s.Copy.Revision != s.CurrentRevision && !s.Submitted
			if stale {
				quietButton(c, "重新预览").Key("experience-copy-reload").Disabled(s.CurrentRevision == 0).OnClick(a.reloadExperienceCopy)
			}
			actionButton(c, label, nil).Key("experience-copy-confirm").Disabled(s.Busy || stale || strings.TrimSpace(s.Title) == "" || strings.TrimSpace(s.Conditions) == "").OnClick(a.confirmExperienceCopy)
		})
	})
}

func (a *App) confirmExperienceCopy() {
	s := &a.experience
	if s.Busy || s.Copy == nil || a.libraryContext == nil {
		return
	}
	if !s.Submitted {
		if strings.TrimSpace(s.Title) == "" || strings.TrimSpace(s.Conditions) == "" || len(s.Title) > 512 || len(s.Conditions) > 4096 {
			s.Error = "请填写标题与适用条件，标题最多 512 字节，适用条件最多 4096 字节。"
			return
		}
		if s.CurrentRevision != s.Copy.Revision {
			s.Error = "来源已有新版本，请重新预览。"
			return
		}
		title, conditions := s.Title, s.Conditions
		s.Copy.Title, s.Copy.Conditions = &title, &conditions
		s.Submitted = true
	}
	d := *s.Copy
	prior := s.Receipt
	page, scope, mode, selected := a.page, a.scope, a.detailMode, a.selectedID
	s.Busy = true
	s.Error = ""
	go func() {
		ctx, stop := context.WithTimeout(a.libraryContext, 10*time.Second)
		defer stop()
		var r records.OrganizationReceipt
		var err error
		if prior != nil {
			r = *prior
		} else {
			r, err = a.organization.Client.CopyExperience(ctx, d)
		}
		var v records.Version
		if err == nil {
			v, err = a.library.Get(ctx, r.Memory)
		}
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			s.Busy = false
			if s.Copy == nil || s.Copy.OperationID != d.OperationID {
				return
			}
			if err != nil {
				if r.ID != "" {
					s.Receipt = &r
				}
				s.Error = "复制回执读取未完成，可以按同一请求重试：" + err.Error()
				return
			}
			s.Copy = nil
			s.Receipt = nil
			if a.page == page && a.scope == scope && a.detailMode == mode && a.selectedID == selected {
				a.openMergeMemory(v.Memory, v.Dataset)
			}
			a.refresh(false)
		})
	}()
}

func (a *App) beginExperienceDistill(v MemoryItem) {
	if !a.isProjectKnowledge(v) || a.experience.Busy || a.libraryContext == nil {
		return
	}
	if old := a.experience.Distill; old != nil && old.Source.ID == v.ID && old.Source.Revision == v.Revision {
		pending := old.Start != nil || old.Busy || old.Loading || distillTaskRunning(old.Task)
		if old.Task != nil {
			for _, entry := range old.Task.Entries {
				pending = pending || entry.State == "review_pending"
			}
		}
		if pending {
			a.showExperienceDistill()
			return
		}
	}
	s := &ExperienceDistillUI{Open: true, Source: v, ReturnPage: a.page, ReturnScope: a.scope, ReturnSelected: a.selectedID, ReturnScroll: a.detailScroll.Y}
	a.experience.Copy = nil
	a.experience.Distill = s
	a.commonCategory = ""
	a.navigate("通用知识", "")
	a.editing, a.detailMode = false, "view"
	a.detailScroll.Y = 0
	a.prepareExperienceDistill(s)
}

func (a *App) closeExperienceCopy() {
	s := &a.experience
	if s.Busy || s.Copy == nil {
		return
	}
	page, scope, scroll := s.ReturnPage, s.ReturnScope, s.ReturnScroll
	s.Copy, s.Receipt, s.Error = nil, nil, ""
	a.navigate(page, scope)
	a.detailScroll.Y = scroll
	a.refresh(false)
}

func (a *App) commonCopyOpen() bool {
	return a.experience.Copy != nil && a.page == "通用知识" && a.detailMode == "view" && !a.editing
}

func (a *App) reloadExperienceCopy() {
	s := &a.experience
	if s.Copy == nil || s.Busy || s.CurrentRevision == 0 {
		return
	}
	page, scope, scroll := s.ReturnPage, s.ReturnScope, s.ReturnScroll
	source := s.LatestSource
	a.beginExperienceCopy(source)
	s.ReturnPage, s.ReturnScope, s.ReturnScroll = page, scope, scroll
}
