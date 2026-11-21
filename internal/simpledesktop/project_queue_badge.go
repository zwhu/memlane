package simpledesktop

import (
	"time"
)

type trayProjectAlert struct {
	ProjectID    string
	ProjectName  string
	ProjectIndex int
	ActionCount  int
	ErrorCount   int
}

type trayProjectProgress struct {
	ProjectID    string
	ProjectName  string
	ProjectIndex int
	Count        int
}

// Reuse the settings queue reader with bounded concurrent projects. Enumeration
// only reads status; it never saves materials or starts knowledge processing.
func (a *App) refreshSidebarQueueIfDue(now time.Time) {
	if a.Update == nil || !a.access.Loaded || a.access.Busy || a.windowInactive {
		return
	}
	projects := a.access.Directory.Projects
	if len(projects) == 0 {
		return
	}
	active := 0
	for _, p := range projects {
		if a.projectQueueStateFor(p).Loading {
			active++
		}
	}
	start := a.access.SidebarQueueIndex % len(projects)
	for offset := 0; offset < len(projects) && active < 3; offset++ {
		index := (start + offset) % len(projects)
		p := projects[index]
		q := a.projectQueueStateFor(p)
		if !q.Loading && (!q.Loaded || now.After(q.Next)) {
			a.loadProjectQueue(q, p)
			if q.Loading {
				active++
			}
			a.access.SidebarQueueIndex = (index + 1) % len(projects)
		}
	}
}

func (a *App) sidebarQueueBadge(p Project) (string, string) {
	identity := a.cachedBaseReadingIdentity()
	q := a.access.Queues[identity+"/"+p.ID+"/"+p.Dataset]
	if q == nil || !q.Loaded {
		return "", ""
	}
	if q.unacknowledgedNotification(time.Now()) {
		return "!", "有尚未处理的消息提醒，打开项目设置处理或选择不再提醒。"
	}
	return "", ""
}

func (a *App) countProjectMessageAlerts(p AccessProject) (actionCount, errorCount int) {
	q := a.projectQueueStateFor(p)
	if q == nil {
		return 0, 0
	}
	needs, info := a.messageNotices(q, p)
	if !q.Loaded && len(needs) == 0 && len(info) == 0 {
		return 0, 0
	}
	for _, n := range needs {
		if !q.noticeAcknowledged(n.Key) {
			actionCount++
		}
	}
	for _, n := range info {
		if n.isProgressNotice() {
			continue
		}
		if !q.noticeAcknowledged(n.Key) {
			errorCount++
		}
	}
	return actionCount, errorCount
}

func (a *App) countProjectActiveProgress(p AccessProject) int {
	q := a.projectQueueStateFor(p)
	if q == nil {
		return 0
	}
	_, info := a.messageNotices(q, p)
	if !q.Loaded && len(info) == 0 {
		return 0
	}
	count := 0
	for _, n := range info {
		if n.isActiveProgressNotice() {
			count++
		}
	}
	if q.Loaded && q.DocumentError == "" && len(q.Documents) > 0 {
		count += len(q.Documents)
	}
	return count
}

func (a *App) projectHasActiveMessageAlerts(p AccessProject) bool {
	actionCount, errorCount := a.countProjectMessageAlerts(p)
	return actionCount+errorCount > 0
}

func (a *App) resolveTrayProjectMeta(p AccessProject) (projectIndex int, name string) {
	projectIndex = -1
	for i, cp := range a.Config.Projects {
		if (p.ID != "" && cp.ID == p.ID) || (p.Dataset != "" && cp.Dataset == p.Dataset) {
			projectIndex = i
			break
		}
	}
	name = p.Name
	if name == "" && projectIndex >= 0 && projectIndex < len(a.Config.Projects) {
		name = a.Config.Projects[projectIndex].Name
	}
	if name == "" {
		name = p.ID
	}
	return projectIndex, name
}

func (a *App) trayProjectAlerts() []trayProjectAlert {
	var alerts []trayProjectAlert
	for _, p := range a.access.Directory.Projects {
		actionCount, errorCount := a.countProjectMessageAlerts(p)
		if actionCount+errorCount == 0 {
			continue
		}
		projectIndex, name := a.resolveTrayProjectMeta(p)
		alerts = append(alerts, trayProjectAlert{
			ProjectID:    p.ID,
			ProjectName:  name,
			ProjectIndex: projectIndex,
			ActionCount:  actionCount,
			ErrorCount:   errorCount,
		})
	}
	return alerts
}

func (a *App) trayProjectProgressItems() []trayProjectProgress {
	var items []trayProjectProgress
	for _, p := range a.access.Directory.Projects {
		count := a.countProjectActiveProgress(p)
		if count == 0 {
			continue
		}
		projectIndex, name := a.resolveTrayProjectMeta(p)
		items = append(items, trayProjectProgress{
			ProjectID:    p.ID,
			ProjectName:  name,
			ProjectIndex: projectIndex,
			Count:        count,
		})
	}
	return items
}
