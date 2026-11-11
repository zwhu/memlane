package desktop

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

type homeActivityDetail struct {
	Event       records.HomeActivity
	Current     *records.Version
	Text, Error string
	Loading     bool
	Sequence    uint64
	Scroll      ui.ScrollState
}

func (a *App) selectHomeActivity(e records.HomeActivity) {
	d := &a.homeActivity.Detail
	d.Sequence++
	seq := d.Sequence
	d.Event = e
	d.Text = ""
	d.Current = nil
	d.Error = ""
	d.Loading = false
	d.Scroll.Y = 0
	if a.libraryContext == nil || a.dispatch == nil {
		return
	}
	d.Loading = true
	owner, library := a.libraryContext, a.library
	go func() {
		ctx, cancel := context.WithTimeout(owner, 5*time.Second)
		defer cancel()
		history, err := library.History(ctx, e.Memory, e.Revision+1)
		var current records.Version
		if err == nil {
			current, err = library.Get(ctx, e.Memory)
		}
		if owner.Err() != nil {
			return
		}
		a.dispatch(func() {
			if d.Sequence != seq || a.library.Root != library.Root {
				return
			}
			d.Loading = false
			if err != nil {
				d.Error = "动态正文读取失败；可以重试。"
				return
			}
			found := false
			for _, version := range history.Items {
				if version.Revision == e.Revision {
					d.Text = version.Text
					found = true
					break
				}
			}
			if !found {
				d.Error = "记录中的版本当前不可读取。"
				return
			}
			d.Current = &current
		})
	}()
}

func (a *App) homeActivityFull(c *ui.Context) {
	if a.homeActivity.Delivery.Full {
		a.agentDeliveryFull(c)
		return
	}
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			width := float32(316)
			w, _ := c.Size()
			if w < 1000 {
				width = 260
			}
			ui.Scroll(c).Key("home-all-activity-list").TrackScroll(&a.homeActivity.Scroll).Width(width).Shrink(0).MinHeight(0).Padding(24, 18).BorderWidth(0, 1, 0, 0).BorderColor(design.Border).Children(func() { a.homeActivityPanel(c) })
			ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
				ui.Row(c).Height(44).Shrink(0).Padding(12, 20).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() { ui.Text(c, "动态详情").Bold() })
				ui.Scroll(c).Key("home-event-detail").TrackScroll(&a.homeActivity.Detail.Scroll).Grow(1).MinHeight(0).Padding(24).Gap(18).Children(func() { a.homeActivityDetailContent(c) })
			})
		})
		a.homePlanFooter(c)
	})
}

func (a *App) homeActivityDetailContent(c *ui.Context) {
	d := &a.homeActivity.Detail
	e := d.Event
	if e.ID == "" {
		ui.Text(c, "选择一条动态，查看实际记录的版本与正文。").FontSize(design.Small).TextColor(design.Muted)
		return
	}
	ui.Text(c, e.Title).FontSize(design.Title).Bold()
	ui.Column(c).Padding(16).Gap(10).Radius(7).Border(1, design.Border).Background(ui.Hex("#f6f9fc")).Children(func() {
		ui.Text(c, homeActivityVerb(e.Kind)).Bold()
		if e.Agent != "" {
			meta(c, "来源", sourceDisplayName(e.Agent))
		}
		meta(c, "记录时间", time.UnixMilli(e.OccurredMS).Local().Format("2006-01-02 15:04:05"))
		meta(c, "记录版本", fmt.Sprintf("v%d", e.Revision))
		if d.Current != nil {
			meta(c, "当前版本", fmt.Sprintf("v%d", d.Current.Revision))
		}
		meta(c, "项目／范围", scopeDisplayName(records.Scope{Kind: e.ScopeKind, Name: e.ScopeName}))
	})
	if d.Loading {
		ui.Text(c, "正在读取记录版本正文…").FontSize(design.Small).TextColor(design.Muted)
	}
	if d.Error != "" {
		ui.Text(c, d.Error).FontSize(design.Small).TextColor(design.Muted)
		quietButton(c, "重试动态正文").OnClick(func() { a.selectHomeActivity(e) })
	}
	if d.Current != nil {
		ui.Text(c, fmt.Sprintf("记录时的正文 · v%d", e.Revision)).Bold()
		// Immutable version text is distinct from the current knowledge. Long text
		// uses the shared complete-reader paging window, never a preview.
		ui.Text(c, a.readableText(c, "home-event-body-"+e.ID, d.Text)).Selectable().LineHeight(1.7)
		quietButton(c, "查看当前知识").AlignSelf(ui.Start).OnClick(func() { a.openHomeActivityCurrent(e) })
	}
}

func (a *App) homeReviewFull(c *ui.Context) {
	s := &a.homeActivity.Review
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			width := float32(316)
			w, _ := c.Size()
			if w < 1000 {
				width = 260
			}
			ui.Scroll(c).Key("home-all-reviews-list").TrackScroll(&s.Scroll).Width(width).Shrink(0).MinHeight(0).Padding(24, 18).Gap(12).BorderWidth(0, 1, 0, 0).BorderColor(design.Border).Children(func() { a.homeReviewPanel(c) })
			ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
				if s.Selected == nil {
					a.empty(c, "选择一项待确认建议", "查看真实差异并明确确认或拒绝；已有知识保持不变。")
					return
				}
				item := *s.Selected
				if item.Owner == "merge" && a.merges.Selected == item.Task && !a.organizationMode {
					a.mergeDetail(c)
				} else if item.Owner == "organization" && a.organization.Selected == item.Task && a.organizationMode {
					a.organizationDetail(c)
				} else {
					a.empty(c, "等待切换审核项", "请先处理原审核草稿或请求，再查看本项。")
				}
			})
		})
		a.homePlanFooter(c)
	})
}

func (a *App) selectHomeReview(item records.HomeReview) {
	s := &a.homeActivity.Review
	s.Selected = &item
	s.DecisionStamp = ""
	s.Return, s.ReturnFull = true, true
	a.homeActivity.Return = false
	if item.Owner == "merge" {
		a.materialContextBarrier(func() { a.selectMerge(item.Task) })
	} else {
		a.selectOrganizationAt(item.Task, item.ID)
	}
}

func (a *App) homeActivityListRow(c *ui.Context, e records.HomeActivity) {
	actor := sourceDisplayName(e.Agent)
	label := actor + " " + homeActivityVerb(e.Kind)
	button := ui.Button(c.Key("home-event-"+e.ID), "").Label(e.Title+" · "+label).Justify(ui.Start).Padding(10, 8).BorderWidth(0).Background(ui.Transparent).Radius(5).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
			ui.Text(c, label+" · "+homeTimestamp(time.UnixMilli(e.OccurredMS))).FontSize(10).TextColor(design.Muted).SingleLine().MinWidth(0)
			ui.Text(c, e.Title).FontWeight(600).SingleLine().MinWidth(0)
			ui.Text(c, scopeDisplayName(records.Scope{Kind: e.ScopeKind, Name: e.ScopeName})).FontSize(10).TextColor(design.Muted).SingleLine()
		})
	})
	if a.homeActivity.Detail.Event.ID == e.ID {
		button.Background(design.ListSelected)
	}
	button.OnClick(func() { a.selectHomeActivity(e) })
}

// homeReturnButton is used by the shared toolbar. A specific return restores
// the originating feed/filter/page/selection; clicking sidebar Overview remains
// the ordinary overview route.
func (a *App) homeReturnButton(c *ui.Context) {
	if a.page == "总览" || a.page == "整理记录" && a.homeActivity.Review.Full {
		return
	}
	if a.homeActivity.Return {
		label := "返回最近动态"
		if a.homeActivity.ReturnFull {
			label = "返回全部动态"
		}
		quietButton(c, label).Key("home-activity-return").OnClick(func() {
			full := a.homeActivity.ReturnFull
			a.navigate("总览", "")
			a.homeActivity.Full = full
			a.homeActivity.Review.Full = false
			a.homeActivity.Return = false
		})
	} else if a.homeActivity.Review.Return {
		quietButton(c, "返回待确认").Key("home-review-return").OnClick(func() {
			full := a.homeActivity.Review.ReturnFull
			page := "总览"
			if full {
				page = "整理记录"
			}
			a.navigate(page, "")
			a.homeActivity.Review.Full = full
			a.homeActivity.Full = false
			a.homeActivity.Review.Return = false
		})
	}
}
