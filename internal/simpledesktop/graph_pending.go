package simpledesktop

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/egoist/mygo/ui"
)

type graphPendingState struct {
	Project, Dataset, Identity string
	Revision                   int64
	Request                    uint64
	Busy                       bool
	Error                      string
	Preview                    *toolGraphPreview
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
		var preview toolGraphPreview
		err := client.request(ctx, "GET", "/v1/projects/"+url.PathEscape(p.ID)+"/tool-graph/preview", nil, &preview)
		a.Update(func() {
			s := &a.graphPending
			if request != s.Request || epoch != a.epoch || identity != a.readingIdentity || revision != a.access.Directory.Revision {
				return
			}
			s.Busy = false
			if err != nil {
				s.Error = "待整理工具记录暂无法核对"
				return
			}
			if preview.Sessions < 0 || preview.Steps < 0 {
				s.Error = "待整理工具记录暂无法核对"
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
	if s.Busy {
		label = "正在核对待整理工具记录…"
	}
	if s.Preview != nil && s.Preview.Steps > 0 {
		label = fmt.Sprintf("有 %d 个会话、%d 条工具记录尚未整理为图谱。", s.Preview.Sessions, s.Preview.Steps)
		if toolGraphActive(s.Preview.Job) {
			label += "已有整理任务进行中。"
		}
	}
	if label == "" {
		return
	}
	ui.Row(c).Shrink(0).Padding(0, 32, 12, 32).Gap(12).AlignItems(ui.Center).Children(func() {
		homeText(c, label, homeMeta).Grow(1).MinWidth(0).MaxLines(2)
		if s.Preview != nil && s.Preview.Steps > 0 {
			textButton(c, "查看整理范围", nil).OnClick(func() { a.previewToolGraph(p.ID) })
		}
	})
}
