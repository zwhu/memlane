package app

import (
	"time"

	"github.com/zwhu/memlane/app/reading"
	"github.com/zwhu/memlane/app/shell"
)

func (a *App) cachedBaseReadingIdentity() string {
	if a.baseReadingIdentity != "" &&
		a.baseIdentityURL == a.Client.Config.ServiceURL &&
		a.baseIdentityTokenFile == a.Client.Config.TokenFile {
		return a.baseReadingIdentity
	}
	_, base, err := readingIdentityPair(a.Client)
	if err != nil {
		return ""
	}
	a.updateBaseReadingIdentity(base, a.Client.Config.ServiceURL, a.Client.Config.TokenFile)
	return base
}

// Poll token changes on the existing view cadence without statting token files in View.
func (a *App) pollViewReadingIdentity(now time.Time) {
	if a.Update == nil || a.Client.Config.TokenFile == "" || now.Before(a.nextViewIdentityCheck) {
		return
	}
	a.nextViewIdentityCheck = now.Add(2 * time.Second)
	client, update := a.Client, a.Update
	adapter := appShellAdapter{app: a}
	fence, activation := shell.CaptureFence(adapter), a.windowActivation
	go func() {
		full, base, err := readingIdentityPair(client)
		if err != nil {
			full = "unavailable"
		}
		if full == fence.ReadingIdentity {
			return
		}
		update(func() {
			if !a.windowHidden && activation == a.windowActivation && fence.Valid(adapter) {
				a.applyReadingIdentity(full, base, err)
			}
		})
	}()
}

func (a *App) syncReadingIdentity() bool {
	return a.applyReadingIdentity(readingIdentityPair(a.Client))
}

func (a *App) applyReadingIdentity(identity, base string, err error) bool {
	if err != nil {
		identity = "unavailable"
		base = ""
	}
	a.updateBaseReadingIdentity(base, a.Client.Config.ServiceURL, a.Client.Config.TokenFile)
	if identity != a.readingIdentity {
		if a.commonEdit.Existing != nil {
			a.withdrawCommonEditor("")
			a.commonEdit.Message = "连接或权限配置已变化，先前正文已撤下；请重新读取，原操作回执保留。"
		}
		a.readingIdentity = identity
		a.epoch++
		for _, s := range a.panes {
			a.cancelTitles(s)
			if s.cancel != nil {
				s.cancel()
			}
			s.Loading = false
			s.Loaded = false
		}
		a.InvalidateReading("")
		return false
	}
	return err == nil
}

// Keep presentation metadata for an explicitly unchanged content revision while
// title hydration runs. Never carry raw bodies or unknown document revisions.
func preserveResolvedReadingTitles(rows, previous []Item) {
	old := make(map[string]Item, len(previous))
	for _, item := range previous {
		if item.ReadingState == "ready" {
			old[item.Key()] = item
		}
	}
	for n, row := range rows {
		before, ok := old[row.Key()]
		if !ok {
			continue
		}
		same := false
		if row.Document != nil && before.Document != nil && (row.Document.ContentHash != "" || row.Document.Updated != "") {
			same = keyForReading("", row, "") == keyForReading("", before, "") && readableDocumentTitle(*row.Document) == ""
		} else if row.Session != nil && before.Session != nil {
			same = row.Session.Updated != "" && row.Session.Updated == before.Session.Updated
		}
		if same {
			rows[n].Title, rows[n].SessionSummary = before.Title, before.SessionSummary
			rows[n].ReadingState = before.ReadingState
		}
	}
}

func (a *App) withdrawChangedReadingBody(s *pane, rows []Item) {
	selected := s.Selected
	if selected == nil || selected.Document == nil {
		return
	}
	for _, row := range rows {
		if row.Document == nil || row.Key() != selected.Key() {
			continue
		}
		old, next := selected.Document, row.Document
		if reading.BodyVersionChanged(old.ContentHash, next.ContentHash, old.Updated, next.Updated) {
			a.unavailableReadingItem(*selected)
			a.selectReadingItem(s, row, false)
		}
		return
	}
}
