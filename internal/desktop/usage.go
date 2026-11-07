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
			ui.Text(c, "未知 · 尚未取得使用记录覆盖起点。").FontSize(design.Small).TextColor(design.Muted)
			return
		}
		u := v.Usage
		ui.Text(c, "采集自 "+time.UnixMilli(u.StartedMS).Local().Format("2006-01-02 15:04")+"；此前未知。近30天统计，详细事件保留90天。").FontSize(design.Small).TextColor(design.Muted)
		meta(c, "日常Agent", fmt.Sprintf("成功读取%d · 检索返回%d · 报告采用%d", u.Daily.Reads, u.Daily.Returns, u.Daily.Adoptions))
		meta(c, "App", fmt.Sprintf("浏览%d · 检索返回%d", u.App.Browses, u.App.Returns))
		meta(c, "连接验证", fmt.Sprintf("读取%d · 返回%d", u.Verification.Reads, u.Verification.Returns))
		meta(c, "后台整理", "次数未采集 · 冻结来源见整理记录")
		ui.Text(c, "返回不代表采用。采用仅表示Agent明确报告；旧Agent未提供回执时采用仍未知。").FontSize(design.Small).TextColor(design.Muted)
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
