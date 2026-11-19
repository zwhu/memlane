package simpledesktop

import (
	"time"
)

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
	identity, _ := readingIdentity(Client{Config: a.Client.Config})
	q := a.access.Queues[identity+"/"+p.ID+"/"+p.Dataset]
	if q == nil || !q.Loaded {
		return "", ""
	}
	if q.confirmedNotification() || q.persistentNotification(time.Now()) {
		return "!", "消息保存或处理存在异常，打开项目设置查看原因与所需操作。"
	}
	return "", ""
}
