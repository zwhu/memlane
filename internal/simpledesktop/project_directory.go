package simpledesktop

// Remap retained reading panes by project identity, never by a reused index.
func (a *App) reconcileProjectReading(oldProjects, oldCommon []Project) {
	for _, old := range append(append([]Project(nil), oldProjects...), oldCommon...) {
		retained := false
		for _, p := range append(append([]Project(nil), a.Config.Projects...), a.Config.Common...) {
			if p.ID == old.ID && p.Dataset == old.Dataset {
				retained = true
				break
			}
		}
		if !retained {
			a.InvalidateReading(old.Dataset)
		}
	}
	indexFor := func(page string, index int) int {
		old, current := oldProjects, a.Config.Projects
		if page == "通用知识" {
			old, current = oldCommon, a.Config.Common
		}
		if index < 0 || index >= len(old) {
			return -1
		}
		for n, p := range current {
			if p.ID == old[index].ID && p.Dataset == old[index].Dataset {
				return n
			}
		}
		return -1
	}
	panes := make(map[string]*pane, len(a.panes))
	for key, state := range a.panes {
		r, ok := parsePaneRoute(key)
		if !ok || !r.IsProjectScoped() {
			panes[key] = state
			continue
		}
		if next := indexFor(r.Page, r.Project); next >= 0 {
			if next != r.Project {
				if state.cancel != nil {
					state.cancel()
					state.Loading = false
				}
				if state.readCancel != nil {
					state.readCancel()
					state.Reading = false
				}
			}
			panes[appRoute{Page: r.Page, Project: next, Kind: r.Kind}.Key()] = state
		} else {
			a.cancelTitles(state)
			if state.cancel != nil {
				state.cancel()
			}
			if state.readCancel != nil {
				state.readCancel()
			}
		}
	}
	a.panes = panes
	if a.currentRoute().IsProjectScoped() {
		if next := indexFor(a.page, a.project); next >= 0 {
			a.project = next
		} else {
			a.page, a.project = "总览", 0
			a.epoch++
			a.graph.clear()
			a.closeUnifiedSearch()
		}
	}
}
