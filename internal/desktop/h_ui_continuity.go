package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// A comparison is a read-only snapshot, never an edit or implicit CAS rebase.
type ConflictReviewUI struct {
	Owner, Dataset                string
	Epoch                         uint64
	BaseRevision                  int64
	Latest                        MemoryItem
	Open, ReturnFocus, FocusDraft bool
	Scroll                        ui.ScrollState
}

func (a *App) openConflictReview() {
	item := a.current()
	if a.saving || !a.editing || a.detailMode != "edit" || item == nil || a.conflictCurrent == nil || a.conflictCurrent.ID != item.ID || a.conflictCurrent.Dataset != item.Dataset {
		return
	}
	revision := item.Revision
	if draft, ok := a.drafts[item.ID]; ok && draft.Revision > 0 {
		revision = draft.Revision
	}
	a.conflictReview = &ConflictReviewUI{Owner: item.ID, Dataset: item.Dataset, Epoch: a.libraryEpoch, BaseRevision: revision, Latest: *a.conflictCurrent, Open: true}
}
func (a *App) conflictReviewValid(s *ConflictReviewUI) bool {
	item := a.current()
	return s != nil && s.Epoch == a.libraryEpoch && item != nil && a.selectedID == s.Owner && item.ID == s.Owner && item.Dataset == s.Dataset && a.editing && a.detailMode == "edit" && a.conflictCurrent != nil && a.conflictCurrent.ID == s.Latest.ID && a.conflictCurrent.Dataset == s.Latest.Dataset && a.conflictCurrent.Revision == s.Latest.Revision
}
func (a *App) conflictReviewNotice(c *ui.Context) {
	if a.conflictCurrent == nil {
		return
	}
	ui.Column(c).Key("edit-conflict-notice").Shrink(0).Gap(7).Padding(12).Border(1, design.Border).Radius(6).Background(design.Surface).Children(func() {
		ui.Text(c, "这条知识有新版本").Bold().FontSize(11)
		ui.Text(c, fmt.Sprintf("你的草稿和原版本基线保留。请先完整核对最新 v%d；明确继续后才更换保存基线。", a.conflictCurrent.Revision)).FontSize(11).LineHeight(1.6)
		button := quietButton(c, "查看最新正文").Key("edit-conflict-open").AlignSelf(ui.Start).Disabled(a.saving).OnClick(a.openConflictReview)
		if s := a.conflictReview; s != nil && s.ReturnFocus && s.Owner == a.selectedID && s.Epoch == a.libraryEpoch {
			button.Focus()
			if button.Focused() {
				s.ReturnFocus = false
			}
		}
	})
}
func (a *App) conflictReviewPanel(c *ui.Context) bool {
	s := a.conflictReview
	if s == nil || !s.Open {
		return false
	}
	if !a.conflictReviewValid(s) {
		a.conflictReview = nil
		return false
	}
	width, _ := c.Size()
	ui.Column(c).Key("edit-conflict-comparison").Grow(1).MinHeight(0).Children(func() {
		ui.Row(c).Shrink(0).Padding(14, 24).Gap(10).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
			ui.Text(c, "核对最新正文").Bold().FontSize(12)
			ui.Spacer(c)
			quietButton(c, "返回草稿").Key("edit-conflict-return").OnClick(func() { s.Open = false; s.ReturnFocus = true })
		})
		ui.Scroll(c).Grow(1).MinHeight(0).TrackScroll(&s.Scroll).Padding(24).Gap(16).Children(func() {
			ui.Text(c, "只读对照，草稿尚未覆盖任何版本。").FontSize(11).TextColor(design.Muted)
			draft := func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(10).Padding(14).Border(1, design.Border).Radius(6).Children(func() {
					ui.Text(c, fmt.Sprintf("我的草稿 · 原基线 v%d", s.BaseRevision)).Bold().FontSize(12)
					ui.Text(c, a.draftTitle).FontSize(12).Bold()
					ui.Text(c, a.conflictReadableText(c, "conflict-draft-"+s.Owner, a.draft)).Selectable().LineHeight(1.7)
					if a.draftConditions != "" {
						ui.Text(c, "草稿适用条件").Bold().FontSize(11)
						ui.Text(c, a.draftConditions).Selectable().LineHeight(1.6)
					}
				})
			}
			latest := func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(10).Padding(14).Border(1, design.Border).Radius(6).Children(func() {
					ui.Text(c, fmt.Sprintf("最新已保存正文 · v%d", s.Latest.Revision)).Bold().FontSize(12)
					ui.Text(c, s.Latest.Title).FontSize(12).Bold()
					ui.Text(c, a.conflictReadableText(c, fmt.Sprintf("conflict-latest-%s-%d", s.Owner, s.Latest.Revision), s.Latest.Text)).Selectable().LineHeight(1.7)
					if s.Latest.Experience != nil {
						ui.Text(c, "最新适用条件").Bold().FontSize(11)
						ui.Text(c, s.Latest.Experience.Conditions).Selectable().LineHeight(1.6)
					}
					quietButton(c, "复制最新全文").OnClick(func() { c.WriteClipboard(s.Latest.Text) })
				})
			}
			if width >= 1000 {
				ui.Row(c).Gap(14).AlignItems(ui.Start).Children(func() { draft(); latest() })
			} else {
				draft()
				latest()
			}
		})
		ui.Row(c).Shrink(0).Wrap().Padding(14, 24).Gap(10).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
			ui.Text(c, fmt.Sprintf("继续后保存仍须通过 v%d 的并发校验", s.Latest.Revision)).FontSize(10).Grow(1).MinWidth(0)
			actionButton(c, "确认基于此版本继续编辑", nil).Key("edit-conflict-continue").Disabled(a.saving).OnClick(func() {
				if !a.conflictReviewValid(s) {
					return
				}
				a.continueFromLatest()
				s.Open = false
				s.FocusDraft = true
			})
		})
	})
	return true
}
func hasFailedIndexNotice(item MemoryItem) bool {
	return item.IndexState == "failed" && item.Revision > 0 && item.State == "active" && item.Origin != "raw" && (item.Derived == nil || item.Derived.State != "stale")
}
func (a *App) failedIndexNotice(c *ui.Context, item MemoryItem) {
	if !hasFailedIndexNotice(item) {
		return
	}
	ui.Column(c).Key("knowledge-index-failed-notice").Gap(8).Padding(12).Border(1, design.Border).Radius(6).Background(design.Surface).Children(func() {
		ui.Text(c, "正文已保存，索引失败").Bold().FontSize(12)
		ui.Text(c, "可以继续阅读已保存的正文。重试只恢复此版本的检索索引，不重新保存知识。").FontSize(11).LineHeight(1.6)
		if a.retryingIndex {
			ui.Text(c, "正在提交索引重试…").FontSize(11)
		} else if strings.HasPrefix(a.message, "索引重试") || strings.HasPrefix(a.message, "已提交索引重试") {
			ui.Text(c, a.message).FontSize(11)
		}
		quietButton(c, "重试索引").Key("knowledge-index-failed-retry").AlignSelf(ui.Start).Disabled(a.retryingIndex || a.libraryContext == nil).OnClick(a.retryIndex)
	})
}

func (a *App) focusConflictDraft(field ui.Element) {
	s := a.conflictReview
	if s == nil || !s.FocusDraft {
		return
	}
	item := a.current()
	if s.Epoch != a.libraryEpoch || a.selectedID != s.Owner || item == nil || item.Revision != s.Latest.Revision || !a.editing {
		a.conflictReview = nil
		return
	}
	field.Focus()
	if field.Focused() {
		a.conflictReview = nil
	}
}

// Keep long-document controls inside the narrow comparison card as well as the
// bounded UTF-8 layout window; copying always uses the exact original body.
func (a *App) conflictReadableText(c *ui.Context, key, body string) string {
	if len(body) <= readWindowBytes {
		return body
	}
	if a.textWindows == nil {
		a.textWindows = map[string]*textWindow{}
	}
	w := a.textWindows[key]
	if w == nil || w.Body != body {
		w = newTextWindow(body)
		a.textWindows[key] = w
	}
	pages := len(w.Offsets) - 1
	w.Page = min(max(w.Page, 0), pages-1)
	ui.Text(c, fmt.Sprintf("长文分段显示 · 第 %d / %d 段", w.Page+1, pages)).FontSize(11).TextColor(design.Muted)
	ui.Row(c).Wrap().Gap(8).Children(func() {
		quietButton(c, "上一段").Key("read-" + key + "-previous").Disabled(w.Page == 0).OnClick(func() { w.Page-- })
		quietButton(c, "下一段").Key("read-" + key + "-next").Disabled(w.Page == pages-1).OnClick(func() { w.Page++ })
		quietButton(c, "复制全文").Key("read-" + key + "-copy").OnClick(func() { c.WriteClipboard(body) })
	})
	return body[w.Offsets[w.Page]:w.Offsets[w.Page+1]]
}
