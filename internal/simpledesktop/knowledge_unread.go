package simpledesktop

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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
	path   string
	docIDs []string
	queue  *projectQueueState
	update func(func())
}

// knowledgeReadWriter serializes all background atomic writes of
// knowledge-read-*.json off the UI thread and coalesces rapid mark-read bursts.
type knowledgeReadWriter struct {
	mu      sync.Mutex
	fileMu  sync.Mutex
	cond    *sync.Cond
	pending map[string]*knowledgeReadMarkJob
	order   []string
	running bool
	inFlight bool
	writeFn func(path string, s knowledgeReadState) error
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

func (a *App) enqueueKnowledgeReadMark(path, docID string, q *projectQueueState) {
	w := a.ensureKnowledgeWriter()
	updateFn := a.Update
	w.mu.Lock()
	job, exists := w.pending[path]
	if !exists {
		job = &knowledgeReadMarkJob{path: path, queue: q, update: updateFn}
		w.pending[path] = job
		w.order = append(w.order, path)
	} else {
		job.queue = q
		job.update = updateFn
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
		path := w.order[0]
		w.order = w.order[1:]
		job := w.pending[path]
		delete(w.pending, path)
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
// have finished flushing to disk.
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
	for _, p := range a.access.Directory.Projects {
		if p.Dataset != i.Dataset {
			continue
		}
		path, err := a.knowledgeReadPath(p)
		if err != nil {
			return
		}
		q := a.projectQueueStateFor(p)
		if q.KnowledgeRead.Read == nil {
			q.KnowledgeRead.Read = map[string]bool{}
		}
		if read, ok := q.KnowledgeRead.Read[i.ID]; ok && read && q.KnowledgeReadError == "" {
			return
		}
		// Instant in-memory state update; atomic JSON serialization + fsync runs
		// serially on the background writer so 10k+ maps never stall the UI thread.
		q.KnowledgeRead.Read[i.ID] = true
		if len(q.UnreadKnowledge) > 0 {
			filtered := q.UnreadKnowledge[:0]
			for _, row := range q.UnreadKnowledge {
				if row.ID != i.ID {
					filtered = append(filtered, row)
				}
			}
			q.UnreadKnowledge = filtered
		}
		q.StateVersion++
		a.enqueueKnowledgeReadMark(path, i.ID, q)
		return
	}
}
func (q *projectQueueState) unreadCount() int {
	if q.KnowledgeReadError != "" {
		return 0
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
		if q == nil || q.KnowledgeReadError != "" {
			return false
		}
		read, exists := q.KnowledgeRead.Read[i.ID]
		return exists && !read
	}
	return false
}
func (a *App) unreadProjectBadge(c *ui.Context, p Project, index int, active bool) {
	count := a.projectUnread(p)
	if count == 0 {
		return
	}
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
		a.navigate("知识", index)
		s := a.state()
		s.Knowledge.UnreadOnly = true
		s.Knowledge.CategoryFilter = ""
		s.Knowledge.RecallFilter = nil
		a.reorderKnowledge(s)
		if len(s.Items) > 0 {
			a.selectItem(s, s.Items[0])
		}
	})
}
