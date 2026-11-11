package desktop

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func usageSortExplanation(mode string) string {
	switch mode {
	case "常用":
		return "近30天日常Agent成功读取次数；返回与采用另计。"
	case "重要优先":
		return "仅用户标记优先，其次按最近更新。"
	default:
		return "按当前版本在本机的更新时间排序。"
	}
}
func (a *App) recordDisplayedMemory(v MemoryItem) {
	if a.libraryContext == nil || v.Usage == nil || !a.isMemoryPage() || a.editing || a.detailMode == "history" || a.detailMode == "restore" || v.State != "active" || v.Derived != nil && v.Derived.State == "stale" {
		return
	}
	navigation := a.usageNavigation
	key := fmt.Sprint(a.usageNavigation, "/", v.ID, "/", v.Revision)
	if a.usageBrowsed == nil {
		a.usageBrowsed = map[string]bool{}
	}
	if a.usageBrowsed[key] {
		return
	}
	a.usageBrowsed[key] = true
	o := records.UsageObservation{OperationID: key, Digest: usageUIDigest(v.ID, v.Revision), Channel: "app", Kind: "browse", Targets: []records.UsageTarget{{Memory: v.ID, Dataset: v.Dataset, Revision: v.Revision}}}
	go func() {
		ctx, cancel := context.WithTimeout(a.libraryContext, 5*time.Second)
		defer cancel()
		err := serviceipc.Call(ctx, a.library.Root, serviceipc.Request{Op: "usage_browse", UsageObservation: &o}, nil)
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			if a.usageNavigation != navigation || a.selectedID != v.ID || a.current() == nil || a.current().Revision != v.Revision {
				return
			}
			if err != nil {
				a.usageBrowseError = "本次浏览记录未确认，请重新打开详情重试。"
			} else {
				a.usageBrowseError = ""
			}
		})
	}()
}
func usageUIDigest(memory string, revision int64) string {
	// A digest binds the actual displayed version, without storing its content.
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprint(memory, "/", revision))))
}
func (a *App) markImportant(v MemoryItem) {
	if a.importanceBusy || v.Usage == nil {
		return
	}
	a.importanceBusy = true
	navigation := a.usageNavigation
	r := records.ImportantRequest{Memory: v.ID, ExpectedRevision: v.Revision, Important: !v.Usage.Important}
	go func() {
		ctx, cancel := context.WithTimeout(a.libraryContext, 5*time.Second)
		defer cancel()
		err := serviceipc.Call(ctx, a.library.Root, serviceipc.Request{Op: "important", Important: &r, UserConfirmed: true}, nil)
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			a.importanceBusy = false
			if a.usageNavigation != navigation || a.selectedID != v.ID || a.current() == nil || a.current().Revision != v.Revision {
				return
			}
			if err != nil {
				a.usageImportantError = "重要标记未保存，请刷新后重试。"
			} else {
				a.usageImportantError = ""
				a.nextCursor = ""
				a.items = nil
				a.refresh(false)
			}
		})
	}()
}
func (a *App) usageDetail(c *ui.Context, v MemoryItem) {
	ui.Column(c).Key("usage-detail").Gap(8).Children(func() {
		ui.Text(c, "使用记录").Bold()
		if v.Usage == nil {
			ui.Text(c, usageCoverageText(nil)).FontSize(design.Small).TextColor(design.Muted)
			a.usageEventsPanel(c, v)
			return
		}
		u := v.Usage
		ui.Text(c, usageCoverageText(u)).FontSize(design.Small).TextColor(design.Muted)
		ui.Text(c, "日常 Agent · 三种事实分别记录").FontSize(12).Bold()
		ui.Row(c).Gap(12).Children(func() {
			for _, fact := range []struct {
				label string
				count int64
			}{{"检索返回", u.Daily.Returns}, {"成功读取", u.Daily.Reads}, {"报告采用", u.Daily.Adoptions}} {
				ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
					ui.Text(c, fact.label).FontSize(design.Small).TextColor(design.Muted)
					ui.Text(c, usageCountText(u.StartedMS > 0, fact.count)).FontSize(16).Bold()
				})
			}
		})
		meta(c, "App", "浏览"+usageCountText(u.StartedMS > 0, u.App.Browses)+" · 检索返回"+usageCountText(u.StartedMS > 0, u.App.Returns))
		meta(c, "连接验证 · 单独统计", "读取"+usageCountText(u.StartedMS > 0, u.Verification.Reads)+" · 返回"+usageCountText(u.StartedMS > 0, u.Verification.Returns))
		if u.BackgroundCollected {
			meta(c, "后台整理", "读取"+usageCountText(u.StartedMS > 0, u.Background.Reads)+" · 返回"+usageCountText(u.StartedMS > 0, u.Background.Returns))
		} else {
			meta(c, "后台整理", "次数未采集 · 冻结来源见整理记录")
		}
		ui.Text(c, "App 浏览、连接验证不计为日常 Agent 读取。返回不代表采用；报告采用仅表示 Agent 明确报告，不核对实际代码修改；未报告的使用仍未知。").FontSize(design.Small).TextColor(design.Muted)

		a.usageEventsPanel(c, v)
		label := "标记重要"
		if u.Important {
			label = "取消重要标记"
		}
		quietButton(c, label).Key("memory-important").Disabled(a.importanceBusy || v.State != "active").OnClick(func() { a.markImportant(v) })
		for _, message := range []string{a.usageImportantError, a.usageBrowseError} {
			if message != "" {
				ui.Text(c, message).FontSize(design.Small).TextColor(design.Muted)
			}
		}
	})
}

func usageCountText(covered bool, count int64) string {
	if !covered || count < 0 {
		return "未知"
	}
	return fmt.Sprintf("%d 次", count)
}
func usageCoverageText(u *records.UsageInfo) string {
	if u == nil || u.StartedMS <= 0 {
		return "未知 · 尚未取得使用记录覆盖起点，不将未观测显示为零。"
	}
	window := "统计窗口未知"
	retention := "详细事件保留期未知"
	if u.WindowDays > 0 {
		window = fmt.Sprintf("近%d天统计", u.WindowDays)
	}
	if u.RetentionDays > 0 {
		retention = fmt.Sprintf("详细事件保留%d天", u.RetentionDays)
	}
	return "采集自 " + time.UnixMilli(u.StartedMS).Local().Format("2006-01-02 15:04") + "；此前未知。" + window + "，" + retention + "。当前范围内同条记录各版本合计。"
}
