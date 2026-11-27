package app

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/settings"
)

type graphPendingState struct {
	Project, Dataset, Identity string
	Revision                   int64
	Request                    uint64
	Busy                       bool
	Error                      string
	Preview                    *settings.ToolGraphPreview
}

// Read the existing official pending-step projection only on graph entry/refresh.
// This never submits work or changes the user's automatic graph preference.
func (a *App) loadGraphPending(p Project) {
	client, ok := a.accessClient()
	if !ok || a.Update == nil {
		return
	}
	request := a.graphPending.Request + 1
	revision, identity, epoch := a.access.Directory.Revision, a.readingIdentity, a.epoch
	a.graphPending = graphPendingState{Project: p.ID, Dataset: p.Dataset, Identity: identity, Revision: revision, Request: request, Busy: true}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		var preview settings.ToolGraphPreview
		err := client.request(ctx, "GET", "/v1/projects/"+url.PathEscape(p.ID)+"/tool-graph/preview", nil, &preview)
		a.Update(func() {
			s := &a.graphPending
			if request != s.Request || epoch != a.epoch || identity != a.readingIdentity || revision != a.access.Directory.Revision {
				return
			}
			s.Busy = false
			if err != nil {
				s.Error = "待提炼工具记录暂无法核对"
				return
			}
			if preview.Sessions < 0 || preview.Steps < 0 {
				s.Error = "待提炼工具记录暂无法核对"
				return
			}
			s.Preview = &preview
		})
	}()
}

func (a *App) graphPendingHint(c *ui.Context, p Project) {
	s := a.graphPending
	if s.Project != p.ID || s.Dataset != p.Dataset || s.Identity != a.readingIdentity || s.Revision != a.access.Directory.Revision {
		return
	}
	label := s.Error
	dotState := "fail"
	if s.Busy {
		label = "正在核对待提炼工具记录…"
		dotState = "run"
	}
	if s.Preview != nil && s.Preview.Steps > 0 {
		label = fmt.Sprintf("有 %d 个会话、%d 条工具记录尚未提炼为图谱。", s.Preview.Sessions, s.Preview.Steps)
		dotState = "warn"
		if settings.ToolGraphActive(s.Preview.Job) {
			label += "已有提炼任务进行中。"
			dotState = "run"
		}
	}
	if label == "" {
		return
	}
	ui.Row(c).Shrink(0).MinHeight(32).Padding(sp1, sp4).Gap(sp2).AlignItems(ui.Center).
		Background(tok.Fill1).BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
		v4StatusDot(c, dotState)
		ui.Text(c, label).Font(fontUI).FontSize(fsCaption).FontWeight(400).TextColor(tok.Text2).Grow(1).MinWidth(0).MaxLines(2)
		if s.Preview != nil && s.Preview.Steps > 0 {
			v4SmallButton(c, "查看提炼范围", nil, func() { a.previewToolGraph(p.ID) })
		}
	})
}

func (a *App) previewToolGraph(project string) {
	a.syncSettingsTransport()
	a.settingsController.PreviewToolGraph(project)
}
