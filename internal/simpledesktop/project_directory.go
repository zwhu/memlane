package simpledesktop

import (
	"fmt"
	"strconv"
	"strings"
)

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
	scoped := func(page string) bool {
		return page == "知识" || page == "通用知识" || page == "图谱" || page == "检索结果"
	}
	panes := make(map[string]*pane, len(a.panes))
	for key, state := range a.panes {
		parts := strings.SplitN(key, "/", 3)
		if len(parts) != 3 || !scoped(parts[0]) {
			panes[key] = state
			continue
		}
		index, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		if next := indexFor(parts[0], index); next >= 0 {
			if next != index {
				if state.cancel != nil {
					state.cancel()
					state.Loading = false
				}
				if state.readCancel != nil {
					state.readCancel()
					state.Reading = false
				}
			}
			panes[fmt.Sprintf("%s/%d/%s", parts[0], next, parts[2])] = state
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
	if scoped(a.page) {
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
