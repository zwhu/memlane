package desktop

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

type ExperienceUI struct {
	Copy    *records.ExperienceCopy
	Receipt *records.OrganizationReceipt
	Busy    bool
	Error   string
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
	a.experience.Receipt = nil
	a.experience.Error = ""
	a.detailScroll.Y = 0
}

func (a *App) experienceCopyPreview(c *ui.Context, v MemoryItem) {
	s := &a.experience
	if s.Copy == nil || s.Copy.Memory != v.ID {
		return
	}
	ui.Column(c).Border(1, design.Border).Padding(14).Gap(10).Children(func() {
		ui.Text(c, "原文复制到全局经验").Bold()
		ui.Text(c, fmt.Sprintf("将所选 v%d 正文新增到全局经验；原项目知识与版本来源保留。授权全局的 Agent 可读取该正文，项目明确约定优先。", s.Copy.Revision)).LineHeight(1.7)
		if s.Copy.Revision != v.Revision {
			ui.Text(c, "来源已有新版本；重试只核对原复制请求是否已提交，未提交的旧版本会被拒绝。").Bold()
		}
		if s.Receipt != nil {
			ui.Text(c, "全局副本已保存，重试读取副本。").Bold()
		}
		if s.Error != "" {
			ui.Text(c, s.Error).Selectable()
		}
		ui.Row(c).Gap(8).Children(func() {
			label := "确认原文复制"
			if s.Error != "" || s.Receipt != nil {
				label = "重试复制回执"
			}
			actionButton(c, label, nil).Key("experience-copy-confirm").Disabled(s.Busy).OnClick(a.confirmExperienceCopy)
			quietButton(c, "关闭复制预览").Key("experience-copy-cancel").Disabled(s.Busy).OnClick(func() { s.Copy = nil; s.Receipt = nil; s.Error = "" })
		})
	})
}

func (a *App) confirmExperienceCopy() {
	s := &a.experience
	if s.Busy || s.Copy == nil || a.libraryContext == nil {
		return
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
			if a.page == page && a.scope == scope && a.detailMode == mode && a.selectedID == selected && selected == d.Memory {
				a.openMergeMemory(v.Memory, v.Dataset)
			}
			a.refresh(false)
		})
	}()
}

func (a *App) beginExperienceDistill(v MemoryItem) {
	if !a.isProjectKnowledge(v) || a.organization.Busy || a.libraryContext == nil {
		return
	}
	a.beginOrganization()
	s := &a.organization
	s.Project = v.Dataset
	s.ProjectLabel = a.scopeLabel(v.Dataset)
	s.Chosen = map[string]MemoryItem{v.ID: v}
	s.Order = []string{v.ID}
	s.GlobalMode = true
	s.ReviewMode = true
	s.Purpose = "global"
	a.startOrganization()
}
