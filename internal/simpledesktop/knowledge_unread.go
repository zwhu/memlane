package simpledesktop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
)

// Client presentation state only. True means read; false means newly discovered.
// No text, source records, embeddings or Cognee writes are stored here.
type knowledgeReadState struct {
	Version     int             `json:"version"`
	Initialized bool            `json:"initialized"`
	StartedAt   time.Time       `json:"enabled_at"`
	Read        map[string]bool `json:"knowledge_ids"`
}

func (a *App) knowledgeReadPath(p AccessProject) (string, error) {
	base := a.Config.ClientStateFile
	if base == "" {
		base = a.Config.ConfigFile
	}
	if base == "" {
		return "", fmt.Errorf("客户端状态位置不可用")
	}
	identity, err := readingIdentity(Client{Config: a.Client.Config})
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(base), fmt.Sprintf("knowledge-read-%x.json", sha256.Sum256([]byte(identity+"/"+p.ID+"/"+p.Dataset)))), nil
}
func readKnowledgeState(path string) (knowledgeReadState, error) {
	s := knowledgeReadState{Version: 1, Read: map[string]bool{}}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return s, fmt.Errorf("已读状态文件不可用")
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if json.Unmarshal(b, &s) != nil || s.Version != 1 || s.Read == nil {
		return knowledgeReadState{}, fmt.Errorf("已读状态读取失败")
	}
	if s.Initialized && s.StartedAt.IsZero() {
		info, err := os.Stat(path)
		if err != nil {
			return s, err
		}
		s.StartedAt = info.ModTime().UTC()
	}
	return s, nil
}
func writeKnowledgeState(path string, s knowledgeReadState) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".knowledge-read-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	return err
}

type knowledgeReadMarkJob struct {
	key             string
	path            string
	docIDs          []string
	queue           *projectQueueState
	update          func(func())
	control         *ProjectControlClient
	readingIdentity string
	projectID       string
	datasetID       string
}

// knowledgeReadWriter serializes all background atomic writes of
// knowledge-read-*.json and /v2/reading-state/mark-read off the UI thread,
// coalescing rapid mark-read bursts.
type knowledgeReadWriter struct {
	mu       sync.Mutex
	fileMu   sync.Mutex
	cond     *sync.Cond
	pending  map[string]*knowledgeReadMarkJob
	order    []string
	running  bool
	inFlight bool
	writeFn  func(path string, s knowledgeReadState) error
}

func (a *App) ensureKnowledgeWriter() *knowledgeReadWriter {
	if a.knowledgeWriter == nil {
		w := &knowledgeReadWriter{
			pending: map[string]*knowledgeReadMarkJob{},
			writeFn: writeKnowledgeState,
		}
		w.cond = sync.NewCond(&w.mu)
		a.knowledgeWriter = w
	}
	return a.knowledgeWriter
}

func (a *App) enqueueKnowledgeReadMark(path, docID string, q *projectQueueState, control *ProjectControlClient, readingID, projectID, datasetID string) {
	w := a.ensureKnowledgeWriter()
	updateFn := a.Update
	key := path
	if key == "" {
		key = readingID + "/" + projectID + "/" + datasetID
	}
	w.mu.Lock()
	job, exists := w.pending[key]
	if !exists {
		job = &knowledgeReadMarkJob{
			key:             key,
			path:            path,
			queue:           q,
			update:          updateFn,
			control:         control,
			readingIdentity: readingID,
			projectID:       projectID,
			datasetID:       datasetID,
		}
		w.pending[key] = job
		w.order = append(w.order, key)
	} else {
		job.queue = q
		job.update = updateFn
		job.control = control
		job.readingIdentity = readingID
		job.projectID = projectID
		job.datasetID = datasetID
	}
	already := false
	for _, existing := range job.docIDs {
		if existing == docID {
			already = true
			break
		}
	}
	if !already {
		job.docIDs = append(job.docIDs, docID)
	}
	if !w.running {
		w.running = true
		go w.drainLoop()
	}
	w.mu.Unlock()
}

func (w *knowledgeReadWriter) drainLoop() {
	for {
		w.mu.Lock()
		if len(w.order) == 0 {
			w.running = false
			w.inFlight = false
			w.cond.Broadcast()
			w.mu.Unlock()
			return
		}
		key := w.order[0]
		w.order = w.order[1:]
		job := w.pending[key]
		delete(w.pending, key)
		w.inFlight = true
		writeFn := w.writeFn
		if writeFn == nil {
			writeFn = writeKnowledgeState
		}
		w.mu.Unlock()

		w.executeMarkJob(job, writeFn)

		w.mu.Lock()
		w.inFlight = false
		if len(w.order) == 0 {
			w.cond.Broadcast()
		}
		w.mu.Unlock()
	}
}

func (w *knowledgeReadWriter) executeMarkJob(job *knowledgeReadMarkJob, writeFn func(string, knowledgeReadState) error) {
	if job.control != nil && job.projectID != "" && job.datasetID != "" && job.readingIdentity != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		hasV2 := job.control.hasFeature(ctx, v2FeatureReadingState)
		if hasV2 {
			resp, err := job.control.readingStateMarkReadV2(ctx, v2ReadingStateMarkReadRequest{
				ReadingIdentity: job.readingIdentity,
				ProjectID:       job.projectID,
				DatasetID:       job.datasetID,
				DocIDs:          job.docIDs,
			})
			cancel()
			if err != nil {
				if job.update != nil && job.queue != nil {
					q := job.queue
					job.update(func() {
						q.KnowledgeReadError = "已读状态未保存"
						q.StateVersion++
					})
				}
				return
			}
			if job.update != nil && job.queue != nil {
				q := job.queue
				unreadCount := resp.UnreadCount
				job.update(func() {
					q.ServerUnreadKnown = true
					q.ServerUnreadCount = unreadCount
					if q.KnowledgeReadError == "已读状态未保存" {
						q.KnowledgeReadError = ""
					}
					q.StateVersion++
				})
			}
			return
		}
		cancel()
	}

	if job.path == "" {
		return
	}
	w.fileMu.Lock()
	defer w.fileMu.Unlock()

	s, err := readKnowledgeState(job.path)
	if err != nil {
		// Never overwrite a corrupt or unreadable read-state file with an invented baseline.
		if job.update != nil && job.queue != nil {
			q := job.queue
			job.update(func() {
				q.KnowledgeReadError = "已读状态未保存"
				q.StateVersion++
			})
		}
		return
	}
	changed := false
	for _, id := range job.docIDs {
		if read, ok := s.Read[id]; !ok || !read {
			s.Read[id] = true
			changed = true
		}
	}
	if !changed {
		return
	}
	if err := writeFn(job.path, s); err != nil {
		if job.update != nil && job.queue != nil {
			q := job.queue
			job.update(func() {
				q.KnowledgeReadError = "已读状态未保存"
				q.StateVersion++
			})
		}
		return
	}
	if job.update != nil && job.queue != nil {
		q := job.queue
		job.update(func() {
			if q.KnowledgeReadError == "已读状态未保存" {
				q.KnowledgeReadError = ""
				q.StateVersion++
			}
		})
	}
}

// waitKnowledgeReadFlush blocks until all queued background mark-read writes
// have finished flushing to disk or server.
func (a *App) waitKnowledgeReadFlush() {
	if a.knowledgeWriter == nil {
		return
	}
	w := a.knowledgeWriter
	w.mu.Lock()
	for len(w.order) > 0 || w.inFlight || w.running {
		w.cond.Wait()
	}
	w.mu.Unlock()
}

// migrateReadingStateV2 runs once per (controlURL, readingIdentity):
// 1. Attempts same-machine fast migration via POST /v2/reading-state/migrate-local.
// 2. For any project whose local file lives outside the server's default directory
//    (e.g., custom ClientStateFile), streams it atomically via 2-phase staging
//    (POST /v2/reading-state/import/begin -> /chunk (<=500) -> /commit).
func (a *App) migrateReadingStateV2(ctx context.Context, control ProjectControlClient, projects []AccessProject) error {
	readingID, err := readingIdentity(Client{Config: a.Client.Config})
	if err != nil || readingID == "" {
		return err
	}
	migKey := control.Config.URL + "|" + readingID
	a.readingMigrateMu.Lock()
	if a.readingMigrated == nil {
		a.readingMigrated = map[string]bool{}
	}
	if a.readingMigrating == nil {
		a.readingMigrating = map[string]bool{}
	}
	if a.readingMigrated[migKey] || a.readingMigrating[migKey] {
		a.readingMigrateMu.Unlock()
		return nil
	}
	a.readingMigrating[migKey] = true
	a.readingMigrateMu.Unlock()

	success := false
	defer func() {
		a.readingMigrateMu.Lock()
		delete(a.readingMigrating, migKey)
		if success {
			a.readingMigrated[migKey] = true
		}
		a.readingMigrateMu.Unlock()
	}()

	refs := make([]v2ReadingStateProjectRef, 0, len(projects))
	byProject := make(map[string]AccessProject, len(projects))
	for _, p := range projects {
		if p.ID == "" || p.Dataset == "" {
			continue
		}
		refs = append(refs, v2ReadingStateProjectRef{ProjectID: p.ID, DatasetID: p.Dataset})
		byProject[p.ID+"/"+p.Dataset] = p
	}
	if len(refs) == 0 {
		success = true
		return nil
	}
	resp, err := control.readingStateMigrateLocalV2(ctx, v2ReadingStateMigrateRequest{
		ReadingIdentity: readingID,
		Projects:        refs,
	})
	if err != nil {
		return err
	}
	migratedStatus := make(map[string]v2ReadingStateMigratedScope, len(resp.Migrated))
	for _, row := range resp.Migrated {
		migratedStatus[row.ProjectID+"/"+row.DatasetID] = row
	}
	for _, p := range projects {
		if p.ID == "" || p.Dataset == "" {
			continue
		}
		row, reported := migratedStatus[p.ID+"/"+p.Dataset]
		status := strings.ToLower(strings.TrimSpace(row.Status))
		needsRemoteImport := !reported || status == "not_found" || status == "missing" || status == "missing_local" || status == "empty" || status == "skipped" || status == "no_local_file"
		if !needsRemoteImport {
			continue
		}
		path, pathErr := a.knowledgeReadPath(p)
		if pathErr != nil {
			continue
		}
		if _, statErr := os.Stat(path); statErr != nil {
			continue
		}
		localState, readErr := readKnowledgeState(path)
		if readErr != nil || !localState.Initialized {
			continue
		}
		if importErr := importReadingStateStagingV2(ctx, control, readingID, p.ID, p.Dataset, localState); importErr != nil {
			return importErr
		}
	}
	success = true
	return nil
}

func importReadingStateStagingV2(ctx context.Context, control ProjectControlClient, readingID, projectID, datasetID string, local knowledgeReadState) error {
	enabledAt := ""
	if !local.StartedAt.IsZero() {
		enabledAt = local.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	beginResp, err := control.readingStateImportBeginV2(ctx, v2ReadingStateImportBeginRequest{
		ReadingIdentity: readingID,
		ProjectID:       projectID,
		DatasetID:       datasetID,
		Initialized:     local.Initialized,
		EnabledAt:       enabledAt,
	})
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(local.Read))
	for id := range local.Read {
		if id != "" {
			keys = append(keys, id)
		}
	}
	sort.Strings(keys)
	const chunkSize = 500
	seq := 0
	for start := 0; start < len(keys); start += chunkSize {
		end := min(start+chunkSize, len(keys))
		chunk := make(map[string]bool, end-start)
		for _, id := range keys[start:end] {
			chunk[id] = local.Read[id]
		}
		if _, err := control.readingStateImportChunkV2(ctx, v2ReadingStateImportChunkRequest{
			ImportID: beginResp.ImportID,
			Seq:      seq,
			Items:    chunk,
		}); err != nil {
			return err
		}
		seq++
	}
	_, err = control.readingStateImportCommitV2(ctx, v2ReadingStateImportCommitRequest{
		ImportID:       beginResp.ImportID,
		ExpectedChunks: seq,
		ExpectedItems:  len(keys),
	})
	return err
}

func (a *App) syncV2ProjectUnreadCount(projectID, datasetID string, unreadCount int, catalogVersion string) {
	for _, p := range a.access.Directory.Projects {
		if (projectID != "" && p.ID == projectID) || (datasetID != "" && p.Dataset == datasetID) {
			q := a.projectQueueStateFor(p)
			q.ServerUnreadKnown = true
			q.ServerUnreadCount = unreadCount
			if catalogVersion != "" {
				q.KnowledgeCatalogVersion = catalogVersion
			}
			q.StateVersion++
			return
		}
	}
}

func (a *App) clearDatasetServerUnread(datasetID string) {
	if datasetID == "" {
		return
	}
	for _, s := range a.panes {
		for idx := range s.Items {
			if s.Items[idx].Dataset == datasetID && s.Items[idx].ServerUnread {
				s.Items[idx].ServerUnread = false
			}
		}
		if s.Selected != nil && s.Selected.Dataset == datasetID && s.Selected.ServerUnread {
			s.Selected.ServerUnread = false
		}
	}
	for idx := range a.overviewState.Snapshot.Knowledge {
		if a.overviewState.Snapshot.Knowledge[idx].Dataset == datasetID {
			a.overviewState.Snapshot.Knowledge[idx].ServerUnread = false
		}
	}
	for idx := range a.overviewState.DayItems {
		if a.overviewState.DayItems[idx].Dataset == datasetID {
			a.overviewState.DayItems[idx].ServerUnread = false
		}
	}
}

// handleKnowledgeCatalogVersionChanged implements C-P1-07 non-disruptive refresh:
// if the user is at the top of the project's knowledge list and not deep-reading,
// silently refresh page 1; otherwise keep the viewport fixed and show the top banner.
func (a *App) handleKnowledgeCatalogVersionChanged(projectID, datasetID, newVersion string) {
	if a.page != "知识" || a.kind != "知识" {
		return
	}
	cur, ok := a.current()
	if !ok || (cur.ID != projectID && cur.Dataset != datasetID) {
		return
	}
	s := a.state()
	if s == nil || !s.Loaded {
		return
	}
	if s.Knowledge.CatalogVersion != "" && s.Knowledge.CatalogVersion == newVersion {
		return
	}
	detailScrolled := false
	if s.Selected != nil {
		if sc := s.DetailScroll[s.Selected.key()]; sc != nil && sc.Y > 2 {
			detailScrolled = true
		}
	}
	atTop := s.ListScroll.Y <= 0 && !detailScrolled && !s.Knowledge.Detail
	if atTop && !s.Loading && !s.Knowledge.Appending {
		a.loadKnowledgeFeed(s)
		return
	}
	s.Knowledge.NewCatalogAvailable = true
}

func (a *App) observeKnowledgeCatalog(p AccessProject, q *projectQueueState, docs []Document) {
	path, err := a.knowledgeReadPath(p)
	if err != nil {
		return
	}
	// Serialize with any in-flight background markKnowledgeRead writes so disk
	// state never loses a pending read transition.
	a.waitKnowledgeReadFlush()
	w := a.ensureKnowledgeWriter()
	w.fileMu.Lock()
	defer w.fileMu.Unlock()

	s, err := readKnowledgeState(path)
	if err != nil {
		q.KnowledgeReadError = "未读状态暂时无法读取"
		q.StateVersion++
		return
	}
	next := map[string]bool{}
	for _, d := range docs {
		if d.Dataset != p.Dataset || d.ID == "" {
			q.KnowledgeReadError = "未读范围尚未确认"
			q.StateVersion++
			return
		}
		if !d.knowledge() {
			continue
		}
		read, exists := s.Read[d.ID]
		if !s.Initialized && !exists {
			read = true
		} // Existing catalog is the first-use baseline.
		if !exists && s.Initialized {
			if created, ok := officialTime(d.Created); ok && !created.After(s.StartedAt) {
				read = true
			}
		}
		next[d.ID] = read
	}
	if !s.Initialized {
		s.StartedAt = time.Now().UTC()
	}
	changed := !s.Initialized || !reflect.DeepEqual(s.Read, next) || q.KnowledgeRead.StartedAt.IsZero()
	s.Read = next
	s.Initialized = true
	if changed {
		if err = writeKnowledgeState(path, s); err != nil {
			q.KnowledgeReadError = "未读状态未保存"
			q.StateVersion++
			return
		}
	}
	q.KnowledgeRead = s
	q.UnreadKnowledge = nil
	for _, d := range docs {
		if d.knowledge() && !s.Read[d.ID] {
			copy := d
			q.UnreadKnowledge = append(q.UnreadKnowledge, Item{ID: d.ID, Dataset: d.Dataset, Kind: "知识", Document: &copy, Date: d.date(), Title: d.title()})
		}
	}
	sort.Slice(q.UnreadKnowledge, func(i, j int) bool {
		return q.UnreadKnowledge[i].Document.Created > q.UnreadKnowledge[j].Document.Created
	})
	q.KnowledgeReadError = ""
	q.StateVersion++
}

func (a *App) markKnowledgeRead(i Item) {
	if i.Kind != "知识" || i.Document == nil || !i.BodyLoaded || !i.Document.knowledge() {
		return
	}
	readingID, _ := readingIdentity(Client{Config: a.Client.Config})
	var controlPtr *ProjectControlClient
	if ctrl, ok := a.accessClient(); ok {
		c := ctrl
		controlPtr = &c
	}
	for _, p := range a.access.Directory.Projects {
		if p.Dataset != i.Dataset {
			continue
		}
		path, pathErr := a.knowledgeReadPath(p)
		if pathErr != nil && controlPtr == nil {
			return
		}
		q := a.projectQueueStateFor(p)
		if q.KnowledgeRead.Read == nil {
			q.KnowledgeRead.Read = map[string]bool{}
		}
		wasRead, exists := q.KnowledgeRead.Read[i.ID]
		wasServerUnread := i.ServerUnreadKnown && i.ServerUnread
		if exists && wasRead && !wasServerUnread && q.KnowledgeReadError == "" {
			return
		}
		// Instant in-memory state update; atomic JSON serialization or
		// /v2/reading-state/mark-read runs serially on the background writer so
		// 10k+ maps never stall the UI thread.
		q.KnowledgeRead.Read[i.ID] = true
		if q.ServerUnreadKnown && q.ServerUnreadCount > 0 && (wasServerUnread || (!exists || !wasRead)) {
			q.ServerUnreadCount--
		}
		if len(q.UnreadKnowledge) > 0 {
			filtered := q.UnreadKnowledge[:0]
			for _, row := range q.UnreadKnowledge {
				if row.ID != i.ID {
					filtered = append(filtered, row)
				}
			}
			q.UnreadKnowledge = filtered
		}
		// Clear ServerUnread flag on in-memory items so unread dots disappear immediately.
		for _, s := range a.panes {
			for idx := range s.Items {
				if s.Items[idx].Dataset == i.Dataset && s.Items[idx].ID == i.ID {
					s.Items[idx].ServerUnread = false
				}
			}
			if s.Selected != nil && s.Selected.Dataset == i.Dataset && s.Selected.ID == i.ID {
				s.Selected.ServerUnread = false
			}
		}
		for idx := range a.overviewState.Snapshot.Knowledge {
			if a.overviewState.Snapshot.Knowledge[idx].Dataset == i.Dataset && a.overviewState.Snapshot.Knowledge[idx].ID == i.ID {
				a.overviewState.Snapshot.Knowledge[idx].ServerUnread = false
			}
		}
		for idx := range a.overviewState.DayItems {
			if a.overviewState.DayItems[idx].Dataset == i.Dataset && a.overviewState.DayItems[idx].ID == i.ID {
				a.overviewState.DayItems[idx].ServerUnread = false
			}
		}
		q.StateVersion++
		a.enqueueKnowledgeReadMark(path, i.ID, q, controlPtr, readingID, p.ID, p.Dataset)
		return
	}
}

func (q *projectQueueState) unreadCount() int {
	if q.KnowledgeReadError != "" {
		return 0
	}
	if q.ServerUnreadKnown {
		return max(0, q.ServerUnreadCount)
	}
	n := 0
	for _, read := range q.KnowledgeRead.Read {
		if !read {
			n++
		}
	}
	return n
}

func (a *App) projectUnread(p Project) int {
	identity := a.cachedBaseReadingIdentity()
	q := a.access.Queues[identity+"/"+p.ID+"/"+p.Dataset]
	if q == nil {
		return 0
	}
	return q.unreadCount()
}

func (a *App) itemIsUnread(i Item) bool {
	if i.Kind != "知识" {
		return false
	}
	for _, p := range a.Config.Projects {
		if p.Dataset != i.Dataset {
			continue
		}
		identity := a.cachedBaseReadingIdentity()
		q := a.access.Queues[identity+"/"+p.ID+"/"+p.Dataset]
		if q != nil && q.KnowledgeReadError != "" {
			return false
		}
		if q != nil {
			if read, exists := q.KnowledgeRead.Read[i.ID]; exists {
				return !read
			}
		}
		if i.ServerUnreadKnown {
			return i.ServerUnread
		}
		return false
	}
	return false
}

func (a *App) openProjectUnreadFeed(index int) {
	a.projectUI.UnreadPopoverOpen = false
	a.projectUI.UnreadPopoverProject = ""
	a.navigate("知识", index)
	s := a.state()
	s.Knowledge.UnreadOnly = true
	s.Knowledge.CategoryFilter = ""
	s.Knowledge.RecallFilter = nil
	s.Knowledge.RecallAgentID = ""
	s.Knowledge.RecallEventID = ""
	if s.Knowledge.V2Mode || s.Loading || !s.Loaded {
		a.loadKnowledgeFeed(s)
		return
	}
	a.reorderKnowledge(s)
	if len(s.Items) > 0 {
		a.selectItem(s, s.Items[0])
	}
}

func (a *App) unreadProjectBadge(c *ui.Context, p Project, index int, active bool) {
	count := a.projectUnread(p)
	if count == 0 {
		return
	}
	identity := a.cachedBaseReadingIdentity()
	q := a.access.Queues[identity+"/"+p.ID+"/"+p.Dataset]
	usePopover := q != nil && len(q.UnreadKnowledge) > 0 && (count > 10 || q.UnreadHasMore)

	label := formatBadgeCount(count)
	bg, fg := tok.Fill2, tok.Text2
	if active {
		bg, fg = tok.Accent, tok.OnAccent
	}
	btn := ui.ButtonBase(c.Key("unread-badge-"+p.ID)).Role(ui.RoleButton).Cursor(ui.CursorPointer).
		Label("未读记忆："+p.Name).Tooltip(fmt.Sprintf("%d 条未读记忆，点击查看", count)).
		MinWidth(20).Height(18).Padding(0, 6).Radius(9).BorderWidth(0).AlignItems(ui.Center).Justify(ui.Center).Shrink(0)
	hoverBg := tok.Accent
	btn.Background(homeHoverColor(btn, "badge-bg", bg, hoverBg))
	if btn.Hovered() {
		fg = tok.OnAccent
	}
	v4ButtonPress(btn, 0.92)
	btn.Children(func() {
		ui.Text(c, label).Font("system-ui").FontSize(fsCaption).FontWeight(600).FontFeatures("tnum").TextColor(fg).SingleLine()
	}).OnClick(func() {
		if usePopover {
			if a.projectUI.UnreadPopoverOpen && a.projectUI.UnreadPopoverProject == p.ID {
				a.projectUI.UnreadPopoverOpen = false
				a.projectUI.UnreadPopoverProject = ""
			} else {
				a.projectUI.UnreadPopoverProject = p.ID
				a.projectUI.UnreadPopoverOpen = true
			}
			return
		}
		a.openProjectUnreadFeed(index)
	})

	if usePopover && a.projectUI.UnreadPopoverOpen && a.projectUI.UnreadPopoverProject == p.ID {
		open := true
		ui.Popover(c.Key("unread-popover-"+p.ID), btn, &open, func() {
			ui.Column(c).Width(304).Padding(sp2, sp3).Gap(sp2).Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
					ui.Text(c, fmt.Sprintf("未读记忆 · %d 条", count)).Font("system-ui").FontSize(fsFootnote).FontWeight(600).TextColor(tok.Text1).SingleLine()
					ui.Spacer(c)
				})
				previewLimit := min(10, len(q.UnreadKnowledge))
				for idx := 0; idx < previewLimit; idx++ {
					item := q.UnreadKnowledge[idx]
					row := ui.ButtonBase(c.Key("unread-preview-"+item.ID)).Role(ui.RoleButton).Cursor(ui.CursorPointer).
						Label(item.Title).FillWidth().Padding(5, 6).Radius(radiusSmall).BorderWidth(0).AlignItems(ui.Center).Justify(ui.Start).Gap(sp2)
					row.Background(homeHoverColor(row, "unread-prev-bg", ui.Transparent, tok.Hover))
					row.Children(func() {
						v4UnreadDot(c)
						ui.Text(c, item.Title).Font("system-ui").FontSize(fsCaption).TextColor(tok.Text1).SingleLine().Grow(1).MinWidth(0)
						ui.Text(c, displayDate(item.Date)).Font("system-ui").FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3).SingleLine().Shrink(0)
					}).OnClick(func() {
						a.projectUI.UnreadPopoverOpen = false
						a.projectUI.UnreadPopoverProject = ""
						a.openOverviewItem(item)
					})
				}
				if count > 10 || q.UnreadHasMore {
					ui.Row(c).FillWidth().Padding(sp2, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
						viewAllLabel := fmt.Sprintf("查看全部 %d 条未读记忆", count)
						allBtn := ui.ButtonBase(c.Key("unread-view-all-"+p.ID)).Role(ui.RoleButton).Cursor(ui.CursorPointer).
							Label(viewAllLabel).FillWidth().Height(28).Padding(0, sp2).Radius(radius).BorderWidth(0).
							AlignItems(ui.Center).Justify(ui.SpaceBetween)
						allBtn.Background(homeHoverColor(allBtn, "unread-all-bg", ui.Transparent, tok.AccentSofter))
						allBtn.Children(func() {
							ui.Text(c, viewAllLabel).Font("system-ui").FontSize(fsFootnote).FontWeight(500).TextColor(tok.Accent).SingleLine()
							ui.Icon(c, v4IconChev).FontSize(14).TextColor(tok.Accent).Shrink(0)
						}).OnClick(func() {
							a.openProjectUnreadFeed(index)
						})
					})
				}
			})
		})
		if !open {
			a.projectUI.UnreadPopoverOpen = false
			a.projectUI.UnreadPopoverProject = ""
		}
	}
}
