package desktop

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/egoist/mygo/ui"
)

// History owns an immutable viewing snapshot. It neither changes the selected
// knowledge nor replaces its scroll, drafts or background task identity.
type KnowledgeHistoryUI struct {
	Root          string
	Epoch         uint64
	Open, Loading bool
	ReturnFocus   bool
	Owner         MemoryItem
	Entries       []MemoryHistory
	Selected      int64
	Error         string
	Sequence      int64
	Cancel        context.CancelFunc
	List, Preview ui.ScrollState
}

func (a *App) openKnowledgeHistory() {
	item := a.current()
	if item == nil || a.editing || item.Origin == "raw" {
		return
	}
	a.openKnowledgeHistoryFor(*item)
}
func (a *App) openKnowledgeHistoryFor(item MemoryItem) {
	if a.editing || item.Origin == "raw" {
		return
	}
	a.closeKnowledgeHistory()
	a.loadSequence++ // Retire any page read that began before the dialog.
	a.loading = false
	a.knowledgeHistory = KnowledgeHistoryUI{Root: a.library.Root, Epoch: a.libraryEpoch, Open: true, Owner: item, Selected: item.Revision, Sequence: a.knowledgeHistory.Sequence + 1}
	a.loadKnowledgeHistory()
}

func (a *App) closeKnowledgeHistory() {
	h := &a.knowledgeHistory
	if h.Open {
		h.ReturnFocus = true
	}
	if h.Cancel != nil {
		h.Cancel()
		h.Cancel = nil
	}
	h.Sequence++
	h.Open, h.Loading = false, false
}

func (a *App) loadKnowledgeHistory() {
	h := &a.knowledgeHistory
	if !h.Open || h.Loading {
		return
	}
	if a.libraryContext == nil || a.dispatch == nil {
		// Only unconfigured Native frame fixtures use their supplied snapshots.
		h.Entries = append([]MemoryHistory(nil), a.history...)
		return
	}
	h.Sequence++
	sequence, owner := h.Sequence, h.Owner
	life, library, dispatch, epoch := a.libraryContext, a.library, a.dispatch, a.libraryEpoch
	h.Loading, h.Error = true, ""
	ctx, cancel := context.WithTimeout(life, 15*time.Second)
	h.Cancel = cancel
	go func() {
		defer cancel()
		var entries []MemoryHistory
		before := int64(0)
		var err error
		for {
			page, e := library.History(ctx, owner.ID, before)
			if e != nil {
				err = e
				break
			}
			for _, v := range displayHistory(page.Items) {
				if v.Revision <= owner.Revision {
					entries = append(entries, v)
				}
			}
			if page.NextRevision == 0 {
				break
			}
			if page.NextRevision <= 0 || (before != 0 && page.NextRevision >= before) {
				err = fmt.Errorf("历史分页未前进")
				break
			}
			before = page.NextRevision
		}
		if life.Err() != nil {
			return
		}
		dispatch(func() {
			if a.libraryEpoch != epoch || a.library.Root != library.Root || !h.Open || sequence != h.Sequence || h.Owner.ID != owner.ID || h.Owner.Revision != owner.Revision {
				return
			}
			h.Cancel, h.Loading = nil, false
			if err != nil {
				h.Error = "历史读取未完成：" + err.Error()
				return
			}
			sort.SliceStable(entries, func(i, j int) bool { return entries[i].Revision > entries[j].Revision })
			if len(entries) == 0 || entries[0].Revision != owner.Revision {
				h.Error = "当前版本的历史记录未完整取得，请重试。"
				return
			}
			h.Entries = entries
		})
	}()
}

func historyOperation(v MemoryHistory) string {
	if v.Revision == 1 && v.Operation == "save" {
		return "初始保存"
	}
	return operationLabel(v.Operation)
}

func (a *App) knowledgeHistoryView(c *ui.Context) {
	h := &a.knowledgeHistory
	if !h.Open {
		return
	}
	w, height := c.Size()
	dialog := ui.DialogBase(c, &h.Open, func(back, panel ui.Element) {
		back.Background(ui.RGBA(38, 56, 76, .21))
		panel.Column().Key("knowledge-history-dialog").Label("知识历史").Width(min(820, w-48)).Height(min(524, height-48)).Radius(18).Clip().Border(1, ui.Hex("#dce5ef")).Background(ui.Hex("#ffffff")).Shadow(0, 25, 90, 0, ui.RGBA(38, 59, 87, .18))
		ui.Row(c).Shrink(0).Padding(24, 27, 20, 27).Gap(20).AlignItems(ui.Start).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(8).Children(func() {
				ui.Text(c, a.memoryScopeName(h.Owner)).FontSize(10).TextColor(design.Muted)
				ui.Text(c, "知识历史").FontSize(22).Bold()
				ui.Text(c, h.Owner.Title).FontSize(12).MaxLines(2).TextColor(design.Muted)
			})
			quietButton(c, "×").Label("关闭知识历史").Key("knowledge-history-close").Size(28, 28).Radius(14).OnClick(a.closeKnowledgeHistory)
		})
		ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			ui.Scroll(c).Key("knowledge-history-versions").Width(190).Shrink(0).TrackScroll(&h.List).Padding(17, 11).Background(ui.Hex("#f9fbfe")).BorderWidth(0, 1, 0, 0).BorderColor(design.Border).Gap(5).Children(func() {
				ui.Row(c).Padding(0, 10, 13, 10).Children(func() {
					ui.Text(c, "版本记录").FontSize(10).TextColor(design.Muted)
					ui.Spacer(c)
					if !h.Loading && h.Error == "" {
						ui.Text(c, fmt.Sprint(len(h.Entries))).FontSize(10).TextColor(design.Muted)
					}
				})
				for _, v := range h.Entries {
					button := quietButton(c, "").Label(fmt.Sprintf("v%d", v.Revision)).Key(fmt.Sprintf("knowledge-history-version-%d", v.Revision)).FillWidth().Padding(12, 11).Justify(ui.Start).Radius(8)
					if v.Revision == h.Selected {
						button.Background(ui.Hex("#edf3fb")).Border(1, ui.Hex("#dbe6f3"))
					}
					button.Children(func() {
						ui.Column(c).MinWidth(0).Gap(7).Children(func() {
							ui.Text(c, fmt.Sprintf("v%d", v.Revision)).FontSize(12).Bold()
							if v.Revision == h.Owner.Revision {
								ui.Text(c, "当前").FontSize(9).TextColor(design.Muted)
							}
							ui.Text(c, historyOperation(v)).FontSize(10).TextColor(design.Muted)
							ui.Text(c, v.Title).FontSize(10).SingleLine().MinWidth(0).TextColor(design.Muted)
						})
					}).OnClick(func() { h.Selected = v.Revision; h.Preview.Y = 0 })
				}
			})
			ui.Scroll(c).Key("knowledge-history-preview").Grow(1).MinWidth(0).TrackScroll(&h.Preview).Padding(24, 27).Gap(16).Children(func() {
				if h.Loading {
					ui.Text(c, "正在读取版本记录…").FontSize(12).TextColor(design.Muted)
					return
				}
				if h.Error != "" {
					ui.Text(c, h.Error).FontSize(12).TextColor(design.Muted)
					quietButton(c, "重试历史读取").OnClick(a.loadKnowledgeHistory)
					return
				}
				for _, v := range h.Entries {
					if v.Revision != h.Selected {
						continue
					}
					ui.Row(c).Gap(10).Children(func() {
						ui.Text(c, fmt.Sprintf("v%d · %s", v.Revision, historyOperation(v))).FontSize(10).TextColor(design.Muted)
						ui.Spacer(c)
						state := "历史版本"
						if v.Revision == h.Owner.Revision {
							state = "当前版本"
						}
						ui.Text(c, state).FontSize(9).Padding(3, 7).Radius(5).Background(design.Surface).TextColor(design.Muted)
					})
					ui.Text(c, v.Title).FontSize(18).Bold()
					ui.Text(c, a.readableText(c, fmt.Sprintf("knowledge-history-%s-%d", h.Owner.ID, v.Revision), v.Text)).Selectable().FontSize(12).LineHeight(2)
					if v.Conditions != "" {
						meta(c, "适用条件", v.Conditions)
					}
					ui.Column(c).Padding(15, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Gap(6).Children(func() {
						if !v.CreatedAt.IsZero() {
							ui.Text(c, v.CreatedAt.Format("2006-01-02 15:04")).FontSize(10).TextColor(design.Muted)
						}
						ui.Text(c, "记录来源："+h.Owner.Source).FontSize(10).TextColor(design.Muted)
					})
				}
			})
		})
		ui.Row(c).Shrink(0).Padding(13, 26).Gap(15).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Background(ui.Hex("#fcfdfe")).Children(func() {
			ui.Text(c, "查看旧版本时，当前知识保留。").FontSize(10).TextColor(design.Muted)
			ui.Spacer(c)
			quietButton(c, "预览恢复此版本").Key("knowledge-history-restore-preview").Disabled(h.Loading || h.Error != "" || h.Selected < 1 || len(h.Entries) == 0).OnClick(a.previewKnowledgeHistoryRestore)
			quietButton(c, "关闭").Key("knowledge-history-dismiss").Border(1, design.Border).Padding(6, 20).Radius(6).OnClick(a.closeKnowledgeHistory)
		})
	})
	if dialog.Dismissed() {
		// DialogBase has already cleared Open on Escape or backdrop dismissal.
		h.ReturnFocus = true
		a.closeKnowledgeHistory()
	}
}

func (a *App) previewKnowledgeHistoryRestore() {
	h := &a.knowledgeHistory
	if !h.Open || h.Loading || h.Error != "" || h.Root != a.library.Root || h.Epoch != a.libraryEpoch || h.Owner.Origin == "raw" || a.saving {
		return
	}
	found := false
	for _, entry := range h.Entries {
		found = found || entry.Revision == h.Selected
	}
	if !found {
		return
	}
	owner, selected := h.Owner, h.Selected
	if current := a.current(); a.isMemoryPage() && current != nil && current.ID == owner.ID && (current.Revision != owner.Revision || current.Dataset != owner.Dataset) {
		h.Error = "当前知识的版本或范围已变化，请返回当前知识后重新打开历史。"
		return
	}
	entries := append([]MemoryHistory(nil), h.Entries...)
	if !a.isMemoryPage() || a.selectedID != owner.ID || a.current() == nil || a.current().Dataset != owner.Dataset {
		a.openMergeMemory(owner.ID, owner.Dataset, owner.Origin)
		a.selectedItem = &owner
		a.loadSequence++ // The preview owns the frozen version, not a late page read.
		a.loading = false
	}
	a.history, a.historyRevision, a.restoreExpected = entries, selected, owner.Revision
	a.detailMode, a.detailScroll.Y = "restore", 0
	a.closeKnowledgeHistory()
}

func (a *App) returnToKnowledgeHistory() {
	h := &a.knowledgeHistory
	a.detailMode = "view"
	if h.Owner.ID == a.selectedID && h.Root == a.library.Root && h.Epoch == a.libraryEpoch && len(h.Entries) > 0 {
		h.Open, h.Selected = true, a.historyRevision
		return
	}
	a.openKnowledgeHistory()
}
