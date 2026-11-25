package app

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

func isRawRecordKind(kind string) bool {
	return kind == "原始记录" || kind == "会话" || kind == "文档"
}

func (a *App) switchProjectKind(kind string) {
	if a.page != "知识" {
		a.navigateKind("知识", a.project, kind)
		return
	}
	if a.kind == kind {
		return
	}
	oldKind := a.kind
	old := a.state()
	a.cancelTitles(old)
	if old.cancel != nil {
		old.cancel()
	}
	if old.readCancel != nil {
		old.readCancel()
	}
	old.Loading = false
	old.Reading = false
	a.epoch++
	a.kind = kind
	a.projectUI.InfoOpen = false
	if kind == "管理" {
		if p, ok := a.current(); ok {
			a.access.OpenProject = p.ID
		}
		return
	}
	n := a.state()
	// Each filter keeps its reading position, but its metadata snapshot can be
	// older than another filter's. Revalidate on entry while retaining content.
	if kind == "知识" {
		if n.Loaded && len(n.Items) > 0 {
			a.motionRuns.projectPage++
			a.motionRuns.projectList++
		} else {
			a.motionRuns.projectPage = 0
			a.motionRuns.projectList = 0
		}
		a.loadKnowledgeFeed(n)
		return
	}
	if isRawRecordKind(kind) {
		if isRawRecordKind(oldKind) {
			// Sub-filter switch within Raw Records keeps tabs/toolbar fixed and
			// seeds from old.RawCatalog so switching never flashes a skeleton.
			if !n.Loaded && len(old.RawCatalog) > 0 {
				seed := make([]Item, 0, len(old.RawCatalog))
				for _, item := range old.RawCatalog {
					if kind == "原始记录" || item.Kind == kind {
						seed = append(seed, item)
					}
				}
				end := min(30, len(seed))
				n.RawCatalog = seed
				n.Items = append([]Item(nil), seed[:end]...)
				n.More = end < len(seed)
				n.Loaded = true
				if n.Selected == nil && len(n.Items) > 0 {
					a.selectReadingItem(n, n.Items[0], false)
				}
			}
			if n.Loaded && len(n.Items) > 0 {
				a.motionRuns.rawList++
			} else {
				a.motionRuns.rawList = 0
			}
		} else {
			if n.Loaded && len(n.Items) > 0 {
				a.motionRuns.rawPage++
				a.motionRuns.rawList++
			} else {
				a.motionRuns.rawPage = 0
				a.motionRuns.rawList = 0
			}
		}
	}
	a.load(n)
}

func projectListStatus(s *pane) string {
	if !s.Loaded {
		if s.Error != "" && !s.Loading {
			return "尚未载入"
		}
		return ""
	}
	return fmt.Sprintf("已载入 %d 条", len(s.Items))
}

func projectLink(c *ui.Context, label string, fn func()) ui.Element {
	return homeLink(c, label, fn).AlignSelf(ui.Start).Justify(ui.Start)
}
