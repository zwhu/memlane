package settings

import "time"

type ProjectQueueAlert struct {
	ProjectID    string
	ProjectName  string
	ProjectIndex int
	ActionCount  int
	ErrorCount   int
}

type ProjectQueueProgress struct {
	ProjectID    string
	ProjectName  string
	ProjectIndex int
	Count        int
}

// Reuse the settings queue reader with bounded concurrent projects. Enumeration
// only reads status; it never saves materials or starts knowledge processing.
func (a *Controller) RefreshSidebarQueueIfDue(now time.Time) {
	if a.windowIsInactive() || a.windowIsHidden() || a.coldStartOverviewPending() {
		return
	}
	a.RefreshSidebarQueueForTray(now)
}

func (a *Controller) RefreshSidebarQueueForTray(now time.Time) {
	if !a.canDispatch() || !a.access.Loaded || a.access.Busy {
		return
	}
	projects := a.access.Directory.Projects
	if len(projects) == 0 {
		return
	}
	active := 0
	for _, p := range projects {
		if q := a.ExistingProjectQueue(p); q != nil && q.Loading {
			active++
		}
	}
	start := a.access.SidebarQueueIndex % len(projects)
	for offset := 0; offset < len(projects) && active < 3; offset++ {
		index := (start + offset) % len(projects)
		p := projects[index]
		q := a.ProjectQueue(p)
		if !q.Loading && (!q.Loaded || !now.Before(q.Next)) {
			a.LoadProjectQueue(q, p)
			if q.Loading {
				active++
			}
			a.access.SidebarQueueIndex = (index + 1) % len(projects)
		}
	}
}

type ProjectQueueSummary struct {
	Alerts        []ProjectQueueAlert
	Progress      []ProjectQueueProgress
	HasErrorAlert bool
}

type trayFlyoutCache struct {
	summarySig          uint64
	summaryValid        bool
	summary             ProjectQueueSummary
	summaryComputations uint64
}

func fnvMixUint64(h, v uint64) uint64 {
	const prime = 1099511628211
	h ^= v & 0xff
	h *= prime
	h ^= (v >> 8) & 0xff
	h *= prime
	h ^= (v >> 16) & 0xff
	h *= prime
	h ^= (v >> 24) & 0xff
	h *= prime
	h ^= (v >> 32) & 0xff
	h *= prime
	h ^= (v >> 40) & 0xff
	h *= prime
	h ^= (v >> 48) & 0xff
	h *= prime
	h ^= (v >> 56) & 0xff
	h *= prime
	return h
}

func fnvMixString(h uint64, s string) uint64 {
	const prime = 1099511628211
	h = fnvMixUint64(h, uint64(len(s)))
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}

func fnvMixBool(h uint64, b bool) uint64 {
	if b {
		return fnvMixUint64(h, 1)
	}
	return fnvMixUint64(h, 0)
}

func (a *Controller) traySummaryFingerprint() uint64 {
	h := uint64(14695981039346656037)
	for _, p := range a.config.Projects {
		h = fnvMixString(h, p.ID)
		h = fnvMixString(h, p.Dataset)
		h = fnvMixString(h, p.Name)
	}
	for _, p := range a.access.Directory.Projects {
		h = fnvMixString(h, p.ID)
		h = fnvMixString(h, p.Dataset)
		h = fnvMixString(h, p.Name)
		h = fnvMixBool(h, p.PolicySyncUnconfirmed())
		q := a.ExistingProjectQueue(p)
		h = fnvMixBool(h, q != nil)
		if q != nil {
			h = fnvMixBool(h, q.Loaded)
			h = fnvMixBool(h, q.LastError != "")
			h = fnvMixBool(h, q.DocumentError != "")
			h = fnvMixUint64(h, uint64(q.InboxFailed))
			h = fnvMixUint64(h, uint64(q.InboxPending))
			h = fnvMixUint64(h, uint64(max(q.PendingDocumentsCount, len(q.Documents))))
		}
	}
	return h
}

// TrayQueueSummary returns the current set of queue alerts and progress rows.
// Its slices belong to the controller and should be treated as read-only.
func (a *Controller) TrayQueueSummary() ProjectQueueSummary {
	sig := a.traySummaryFingerprint()
	if a.trayCache.summaryValid && a.trayCache.summarySig == sig {
		return a.trayCache.summary
	}
	indexByID, indexByDataset := make(map[string]int), make(map[string]int)
	for i, p := range a.config.Projects {
		if _, ok := indexByID[p.ID]; !ok && p.ID != "" {
			indexByID[p.ID] = i
		}
		if _, ok := indexByDataset[p.Dataset]; !ok && p.Dataset != "" {
			indexByDataset[p.Dataset] = i
		}
	}
	var out ProjectQueueSummary
	for _, p := range a.access.Directory.Projects {
		syncUnconfirmed := p.PolicySyncUnconfirmed()
		q := a.ExistingProjectQueue(p)
		errors, progress := 0, 0
		if q != nil {
			if q.InboxFailed > 0 || q.LastError != "" || q.DocumentError != "" {
				errors = max(1, q.InboxFailed)
			}
			if q.Loaded && q.DocumentError == "" {
				progress = max(0, q.InboxPending) + max(q.PendingDocumentsCount, len(q.Documents))
			}
		}
		if !syncUnconfirmed && errors == 0 && progress == 0 {
			continue
		}
		index, ok := indexByID[p.ID]
		if byDataset, found := indexByDataset[p.Dataset]; found && (!ok || byDataset < index) {
			index, ok = byDataset, true
		}
		if !ok {
			index = -1
		}
		name := p.Name
		if name == "" && ok {
			name = a.config.Projects[index].Name
		}
		if name == "" {
			name = p.ID
		}
		if syncUnconfirmed || errors > 0 {
			actions := 0
			if syncUnconfirmed {
				actions = 1
			}
			out.Alerts = append(out.Alerts, ProjectQueueAlert{p.ID, name, index, actions, errors})
			out.HasErrorAlert = out.HasErrorAlert || errors > 0
		}
		if progress > 0 {
			out.Progress = append(out.Progress, ProjectQueueProgress{p.ID, name, index, progress})
		}
	}
	a.trayCache.summarySig, a.trayCache.summaryValid, a.trayCache.summary = sig, true, out
	a.trayCache.summaryComputations++
	return out
}
