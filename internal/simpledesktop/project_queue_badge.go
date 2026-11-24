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
	if a.Update == nil || !a.access.Loaded || a.access.Busy || a.windowInactive || a.windowHidden || a.coldStartOverviewPending() {
		return
	}
	projects := a.access.Directory.Projects
	if len(projects) == 0 {
		return
	}
	active := 0
	for _, p := range projects {
		if q := a.existingProjectQueueState(p); q != nil && q.Loading {
			active++
		}
	}
	start := a.access.SidebarQueueIndex % len(projects)
	for offset := 0; offset < len(projects) && active < 3; offset++ {
		index := (start + offset) % len(projects)
		p := projects[index]
		q := a.projectQueueStateForAsync(p)
		if !q.Loading && (!q.Loaded || now.After(q.Next)) {
			a.loadProjectQueue(q, p)
			if q.Loading {
				active++
			}
			a.access.SidebarQueueIndex = (index + 1) % len(projects)
		}
	}
}

func (a *App) countProjectMessageAlerts(p AccessProject) (actionCount, errorCount int) {
	q := a.existingProjectQueueState(p)
	if q == nil {
		if p.policySyncUnconfirmed() {
			return 0, 1
		}
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
	q := a.existingProjectQueueState(p)
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
	if q.Loaded && q.DocumentError == "" {
		pendingDocs := max(q.PendingDocumentsCount, len(q.Documents))
		if pendingDocs > 0 {
			count += pendingDocs
		}
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

type trayProjectSummary struct {
	Alerts        []trayProjectAlert
	Progress      []trayProjectProgress
	HasErrorAlert bool
}

type trayFlyoutCache struct {
	summarySig          uint64
	summaryValid        bool
	summary             trayProjectSummary
	summaryComputations uint64

	todayGen            uint64
	todayLoaded         bool
	todayKnowledgeCount int
	todayDay            string
	todayCount          int
	todayValid          bool
	todayComputations   uint64
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

func mixQueuedSessionSig(h uint64, row queuedSession) uint64 {
	h = fnvMixString(h, row.Agent.ID)
	h = fnvMixString(h, row.Binding.HostSessionKey)
	if row.View == nil {
		return fnvMixUint64(h, 0)
	}
	v := row.View
	h = fnvMixBool(h, v.Loaded)
	h = fnvMixString(h, v.ReadCode)
	h = fnvMixString(h, v.ReadError)
	h = fnvMixString(h, v.Message)
	h = fnvMixUint64(h, uint64(v.Status.Pending))
	h = fnvMixUint64(h, uint64(v.Status.Unknown))
	h = fnvMixUint64(h, uint64(v.Status.RetryableUnknown))
	h = fnvMixString(h, v.Status.Signature)
	if v.Status.Blocked != nil {
		h = fnvMixString(h, v.Status.Blocked.Reason)
	} else {
		h = fnvMixUint64(h, 0)
	}
	return h
}

// traySummaryFingerprint computes an allocation-free O(P) structural + version
// signature across queue, directory, and monitor state so repeated tray icon
// checks and flyout frames reuse cached alerts/progress without rebuilding
// messageNotices every frame.
func (a *App) traySummaryFingerprint() uint64 {
	var h uint64 = 14695981039346656037
	h = fnvMixUint64(h, pendingBufferObserveSeq.Load())
	h = fnvMixUint64(h, uint64(a.access.Directory.Revision))
	h = fnvMixString(h, a.access.Signature)
	h = fnvMixString(h, a.access.Directory.Defaults.Access)
	h = fnvMixBool(h, a.access.Directory.Defaults.Capture)
	h = fnvMixBool(h, a.access.Directory.Defaults.AutoProcess)
	h = fnvMixUint64(h, uint64(len(a.Config.Projects)))
	for _, cp := range a.Config.Projects {
		h = fnvMixString(h, cp.ID)
		h = fnvMixString(h, cp.Name)
		h = fnvMixString(h, cp.Dataset)
	}
	h = fnvMixUint64(h, a.monitor.Request)
	h = fnvMixString(h, a.monitor.State)
	h = fnvMixUint64(h, uint64(len(a.access.Directory.Projects)))
	identity := a.cachedBaseReadingIdentity()
	for _, p := range a.access.Directory.Projects {
		h = fnvMixString(h, p.ID)
		h = fnvMixString(h, p.Name)
		h = fnvMixString(h, p.Dataset)
		h = fnvMixString(h, p.Status)
		h = fnvMixUint64(h, uint64(p.DesiredRevision))
		h = fnvMixUint64(h, uint64(len(p.Overrides)))
		for _, ag := range a.access.Directory.Agents {
			if ov, ok := p.Overrides[ag.ID]; ok {
				h = fnvMixString(h, ag.ID)
				if ov.Access != nil {
					h = fnvMixString(h, *ov.Access)
				}
				if ov.Capture != nil {
					h = fnvMixBool(h, *ov.Capture)
				}
				if ov.AutoProcess != nil {
					h = fnvMixBool(h, *ov.AutoProcess)
				}
			}
		}
		var q *projectQueueState
		if a.access.Queues != nil {
			q = a.access.Queues[identity+"/"+p.ID+"/"+p.Dataset]
		}
		if q == nil {
			h = fnvMixUint64(h, 0)
			continue
		}
		h = fnvMixUint64(h, q.StateVersion)
		h = fnvMixUint64(h, q.Request)
		h = fnvMixBool(h, q.Loaded)
		h = fnvMixBool(h, q.BacklogLoaded)
		h = fnvMixBool(h, q.AutomaticSave)
		h = fnvMixString(h, q.DocumentError)
		h = fnvMixString(h, q.BacklogError)
		h = fnvMixString(h, q.ProcessingReadError)
		h = fnvMixString(h, q.NoticeAcknowledgementError)
		h = fnvMixString(h, q.ActivityError)
		h = fnvMixString(h, q.KnowledgeReadError)
		h = fnvMixUint64(h, uint64(len(q.Documents)))
		h = fnvMixUint64(h, uint64(len(q.ProcessingNotices)))
		h = fnvMixUint64(h, uint64(len(q.BufferReviews)))
		h = fnvMixUint64(h, uint64(len(q.NoticeAcknowledgements)))
		h = fnvMixUint64(h, uint64(len(q.Checks)))
		h = fnvMixUint64(h, uint64(len(q.NotificationFailures)))
		if q.Backlog != nil {
			h = fnvMixString(h, q.Backlog.Status)
			h = fnvMixString(h, q.Backlog.Reason)
			h = fnvMixString(h, q.Backlog.Operation)
			h = fnvMixUint64(h, uint64(q.Backlog.ProcessingActive))
			h = fnvMixUint64(h, uint64(q.Backlog.ProcessingPartial))
			h = fnvMixUint64(h, uint64(q.Backlog.ProcessingFailed))
			h = fnvMixUint64(h, uint64(q.Backlog.ReviewFailed))
			h = fnvMixUint64(h, uint64(q.Backlog.Remaining))
		} else {
			h = fnvMixUint64(h, 0)
		}
		h = fnvMixUint64(h, uint64(len(q.Sessions)))
		if len(q.Sessions) > 0 {
			h = mixQueuedSessionSig(h, q.Sessions[0])
			if len(q.Sessions) > 1 {
				h = mixQueuedSessionSig(h, q.Sessions[len(q.Sessions)-1])
			}
		}
	}
	return h
}

func (a *App) trayCachedSummary() trayProjectSummary {
	sig := a.traySummaryFingerprint()
	if a.trayCache.summaryValid && a.trayCache.summarySig == sig {
		return a.trayCache.summary
	}
	var out trayProjectSummary
	for _, p := range a.access.Directory.Projects {
		q := a.existingProjectQueueState(p)
		if q == nil {
			if !p.policySyncUnconfirmed() {
				continue
			}
			// Directory policy warnings are available before the queue loads.
			q = &projectQueueState{}
		}
		needs, info := a.messageNotices(q, p)
		actionCount, errorCount := 0, 0
		if q.Loaded || len(needs) > 0 || len(info) > 0 {
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
		}
		progressCount := 0
		if q.Loaded || len(info) > 0 {
			for _, n := range info {
				if n.isActiveProgressNotice() {
					progressCount++
				}
			}
			pendingDocs := max(q.PendingDocumentsCount, len(q.Documents))
			if q.Loaded && q.DocumentError == "" && pendingDocs > 0 {
				progressCount += pendingDocs
			}
		}
		if actionCount+errorCount == 0 && progressCount == 0 {
			continue
		}
		projectIndex, name := a.resolveTrayProjectMeta(p)
		if actionCount+errorCount > 0 {
			if errorCount > 0 {
				out.HasErrorAlert = true
			}
			out.Alerts = append(out.Alerts, trayProjectAlert{
				ProjectID:    p.ID,
				ProjectName:  name,
				ProjectIndex: projectIndex,
				ActionCount:  actionCount,
				ErrorCount:   errorCount,
			})
		}
		if progressCount > 0 {
			out.Progress = append(out.Progress, trayProjectProgress{
				ProjectID:    p.ID,
				ProjectName:  name,
				ProjectIndex: projectIndex,
				Count:        progressCount,
			})
		}
	}
	a.trayCache.summarySig = sig
	a.trayCache.summary = out
	a.trayCache.summaryValid = true
	a.trayCache.summaryComputations++
	return out
}

func (a *App) trayProjectAlerts() []trayProjectAlert {
	return a.trayCachedSummary().Alerts
}

func (a *App) trayProjectProgressItems() []trayProjectProgress {
	return a.trayCachedSummary().Progress
}
