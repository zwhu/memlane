package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"golang.org/x/sys/unix"
)

const manualDraftFormat = 1
const manualDraftMaxBytes = 16 << 20
const manualDraftMaxEntries = 128
const manualDraftMaxText = 4 << 20
const manualDraftMaxField = 64 << 10

// These files contain editing intent only. Loading them never invokes Apply.
// Every path, receipt and retry belongs to one authoritative library epoch.
type manualDraftLocation struct {
	Page          string  `json:"page"`
	Scope         string  `json:"scope,omitempty"`
	Selected      string  `json:"selected,omitempty"`
	Mode          string  `json:"mode"`
	Kind          string  `json:"kind,omitempty"`
	Scroll        float32 `json:"scroll"`
	Query         string  `json:"query,omitempty"`
	SearchQuery   string  `json:"search_query,omitempty"`
	ReadingSearch string  `json:"reading_search,omitempty"`
	ReadingFilter string  `json:"reading_filter,omitempty"`
}
type manualDraftIntent struct {
	Request    records.Mutation `json:"request"`
	Title      string           `json:"title"`
	Text       string           `json:"text"`
	Conditions string           `json:"conditions,omitempty"`
}
type manualDraftEntry struct {
	Dataset    string              `json:"dataset"`
	Revision   int64               `json:"revision"`
	Title      string              `json:"title"`
	Text       string              `json:"text"`
	Conditions string              `json:"conditions,omitempty"`
	Location   manualDraftLocation `json:"location"`
	Pending    *manualDraftIntent  `json:"pending,omitempty"`
}
type manualDraftEnvelope struct {
	Format   int                         `json:"format"`
	Identity string                      `json:"identity"`
	Entries  map[string]manualDraftEntry `json:"entries"`
	Active   *manualDraftLocation        `json:"active,omitempty"`
}
type manualDraftDisk struct {
	dir, lock *os.File
	name      string
}

func manualUUID(value string) bool { v, e := uuid.Parse(value); return e == nil && v.String() == value }
func manualDraftNewKey(key string) bool {
	return strings.HasPrefix(key, "new:") && (key == "new:" || manualUUID(strings.TrimPrefix(key, "new:")))
}
func manualText(value string, limit int) bool { return len(value) <= limit && utf8.ValidString(value) }
func validManualLocation(l manualDraftLocation) bool {
	if l.Page != "项目" && l.Page != "通用知识" && l.Page != "搜索" {
		return false
	}
	if l.Scope != "" && !manualUUID(l.Scope) {
		return false
	}
	if l.Page == "项目" && l.Scope == "" {
		return false
	}
	if l.Page == "通用知识" && l.Scope != "" {
		return false
	}
	if l.Selected != "" && !manualUUID(l.Selected) {
		return false
	}
	if l.Mode != "new" && l.Mode != "edit" && l.Mode != "view" {
		return false
	}
	if l.Kind != "" && l.Kind != "knowledge" && l.Kind != "raw" {
		return false
	}
	if math.IsNaN(float64(l.Scroll)) || math.IsInf(float64(l.Scroll), 0) || l.Scroll < 0 || l.Scroll > 1e8 {
		return false
	}
	for _, v := range []string{l.Query, l.SearchQuery, l.ReadingSearch, l.ReadingFilter} {
		if !manualText(v, 4096) {
			return false
		}
	}
	return true
}
func validateManualEnvelope(e manualDraftEnvelope, identity string) error {
	if e.Format != manualDraftFormat || e.Identity != identity || !manualUUID(identity) || e.Entries == nil || len(e.Entries) > manualDraftMaxEntries {
		return errors.New("manual draft format or epoch invalid")
	}
	for key, d := range e.Entries {
		newEntry := manualDraftNewKey(key)
		if !newEntry && !manualUUID(key) || !manualUUID(d.Dataset) || d.Revision < 0 || newEntry && d.Revision != 0 || !newEntry && d.Revision < 1 {
			return errors.New("manual draft identity or version invalid")
		}
		if !manualText(d.Title, manualDraftMaxField) || !manualText(d.Text, manualDraftMaxText) || !manualText(d.Conditions, manualDraftMaxField) || !validManualLocation(d.Location) {
			return errors.New("manual draft content or location invalid")
		}
		if d.Location.Mode == "new" && !newEntry || d.Location.Mode == "edit" && newEntry {
			return errors.New("manual draft editor mode invalid")
		}
		if !newEntry && d.Location.Mode == "edit" && d.Location.Selected != key {
			return errors.New("manual draft selected identity invalid")
		}
		if newEntry && d.Location.Scope != strings.TrimPrefix(key, "new:") {
			return errors.New("manual new draft owner changed")
		}
		if d.Pending == nil {
			continue
		}
		intent := d.Pending
		r := intent.Request
		if !manualUUID(r.OperationID) || !manualUUID(r.Memory) || !manualUUID(r.Dataset) || r.Dataset != d.Dataset || r.ExpectedRevision != d.Revision || r.RestoreRevision != 0 || r.SourceDetail != nil || r.Origin != "" {
			return errors.New("manual intent identity invalid")
		}
		if newEntry && (r.Kind != "save" || r.Source != "手动新增") || !newEntry && (r.Kind != "edit" || r.Memory != key || r.Dataset != d.Dataset || r.Source != "") {
			return errors.New("manual intent must be its original save or edit")
		}
		if r.Kind != "save" && r.Kind != "edit" {
			return errors.New("manual intent cannot mutate other knowledge state")
		}
		if r.Title != intent.Title || r.Text != intent.Text || !manualText(intent.Title, manualDraftMaxField) || !manualText(intent.Text, manualDraftMaxText) || !manualText(intent.Conditions, manualDraftMaxField) {
			return errors.New("manual intent payload changed")
		}
		if r.Conditions != nil && *r.Conditions != intent.Conditions {
			return errors.New("manual intent conditions changed")
		}
	}
	if e.Active != nil {
		if !validManualLocation(*e.Active) || e.Active.Mode == "view" {
			return errors.New("manual active editor invalid")
		}
		key := e.Active.Selected
		if e.Active.Mode == "new" {
			key = "new:" + e.Active.Scope
		}
		d, ok := e.Entries[key]
		if !ok || d.Location != *e.Active {
			return errors.New("manual active editor not bound to a draft")
		}
	}
	return nil
}
func privateManualFD(file *os.File, directory bool) error {
	var st unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &st); err != nil {
		return errors.New("manual draft file metadata unavailable")
	}
	expected := int(unix.S_IFREG)
	if directory {
		expected = unix.S_IFDIR
	}
	if int(st.Mode)&unix.S_IFMT != expected || st.Uid != uint32(os.Getuid()) || st.Mode&0077 != 0 || !directory && st.Nlink != 1 {
		return errors.New("manual draft file must be private and owned")
	}
	return nil
}
func openManualDraftDisk(root, identity string) (*manualDraftDisk, manualDraftEnvelope, error) {
	empty := manualDraftEnvelope{Format: manualDraftFormat, Identity: identity, Entries: map[string]manualDraftEntry{}}
	if !filepath.IsAbs(root) || !manualUUID(identity) {
		return nil, empty, errors.New("manual draft root or epoch unavailable")
	}
	directory := filepath.Join(root, "client-drafts")
	if err := os.Mkdir(directory, 0700); err != nil && !os.IsExist(err) {
		return nil, empty, errors.New("manual draft directory unavailable")
	}
	fd, err := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, empty, errors.New("manual draft directory must not be a link")
	}
	dir := os.NewFile(uintptr(fd), directory)
	if err = privateManualFD(dir, true); err != nil {
		dir.Close()
		return nil, empty, err
	}
	lockFD, err := unix.Openat(fd, identity+".lock", unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		dir.Close()
		return nil, empty, errors.New("manual draft owner lock unavailable")
	}
	lock := os.NewFile(uintptr(lockFD), identity+".lock")
	if err = privateManualFD(lock, false); err == nil {
		err = unix.Flock(lockFD, unix.LOCK_EX|unix.LOCK_NB)
	}
	if err != nil {
		lock.Close()
		dir.Close()
		return nil, empty, errors.New("manual drafts are owned by another window or unsafe file")
	}
	disk := &manualDraftDisk{dir: dir, lock: lock, name: identity + ".json"}
	result, err := disk.read(identity)
	if err != nil {
		disk.close()
		return nil, empty, err
	}
	return disk, result, nil
}
func (d *manualDraftDisk) close() {
	if d.lock != nil {
		unix.Flock(int(d.lock.Fd()), unix.LOCK_UN)
		d.lock.Close()
	}
	if d.dir != nil {
		d.dir.Close()
	}
}
func (d *manualDraftDisk) read(identity string) (manualDraftEnvelope, error) {
	out := manualDraftEnvelope{Format: manualDraftFormat, Identity: identity, Entries: map[string]manualDraftEntry{}}
	fd, err := unix.Openat(int(d.dir.Fd()), d.name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return out, nil
	}
	if err != nil {
		return out, errors.New("manual draft file cannot be safely opened")
	}
	file := os.NewFile(uintptr(fd), d.name)
	defer file.Close()
	if err = privateManualFD(file, false); err != nil {
		return out, err
	}
	data, err := io.ReadAll(io.LimitReader(file, manualDraftMaxBytes+1))
	if err != nil || len(data) > manualDraftMaxBytes {
		return out, errors.New("manual draft file exceeds safe read bounds")
	}
	if err = manualUniqueJSON(data); err != nil {
		return out, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&out); err != nil {
		return out, errors.New("manual draft file is damaged or unsupported")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return out, errors.New("manual draft file contains extra payload")
	}
	if err = validateManualEnvelope(out, identity); err != nil {
		return out, err
	}
	return out, nil
}
func (d *manualDraftDisk) write(ctx context.Context, data []byte) error {
	if len(data) > manualDraftMaxBytes {
		return errors.New("manual draft file exceeds safe write bounds")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// A substituted link/hardlink is rejected even though rename itself would
	// replace it: a private draft store is never an arbitrary-file editor.
	var st unix.Stat_t
	err := unix.Fstatat(int(d.dir.Fd()), d.name, &st, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil && (st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Getuid()) || st.Mode&0077 != 0 || st.Nlink != 1) {
		return errors.New("manual draft target was replaced")
	}
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return errors.New("manual draft target unavailable")
	}
	temp := "." + d.name + "-" + uuid.NewString() + ".tmp"
	fd, err := unix.Openat(int(d.dir.Fd()), temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return fmt.Errorf("manual draft temporary write unavailable: %w", err)
	}
	file := os.NewFile(uintptr(fd), temp)
	defer unix.Unlinkat(int(d.dir.Fd()), temp, 0)
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return errors.New("manual draft contents not durably written")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = unix.Renameat(int(d.dir.Fd()), temp, int(d.dir.Fd()), d.name); err != nil {
		return errors.New("manual draft atomic publish failed")
	}
	if err = d.dir.Sync(); err != nil {
		return errors.New("manual draft publish durability unknown")
	}
	return nil
}

type manualDraftJob struct {
	data      []byte
	digest    string
	automatic bool
	done      func(error)
}
type manualDraftWriter struct {
	mu              sync.Mutex
	queue           []manualDraftJob
	running, closed bool
	confirmed       string
	uncertain       *manualDraftJob
	life            context.Context
	cancel          context.CancelFunc
	save            func(context.Context, []byte) error
	disk            *manualDraftDisk
}

func (w *manualDraftWriter) enqueue(job manualDraftJob) {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		if job.done != nil {
			job.done(errors.New("manual draft epoch retired"))
		}
		return
	}
	if job.automatic && len(w.queue) > 0 && w.queue[len(w.queue)-1].automatic {
		w.queue[len(w.queue)-1] = job
	} else {
		w.queue = append(w.queue, job)
	}
	if !w.running {
		w.running = true
		go w.run()
	}
	w.mu.Unlock()
}
func (w *manualDraftWriter) commit(job manualDraftJob) error {
	err := w.save(w.life, job.data)
	w.mu.Lock()
	defer w.mu.Unlock()
	if err != nil {
		copy := job
		copy.done = nil
		w.uncertain = &copy
		return err
	}
	w.confirmed = job.digest
	w.uncertain = nil
	return nil
}
func (w *manualDraftWriter) run() {
	for {
		w.mu.Lock()
		if w.closed {
			jobs := w.queue
			w.queue = nil
			w.mu.Unlock()
			if w.disk != nil {
				w.disk.close()
			}
			w.mu.Lock()
			w.running = false
			w.mu.Unlock()
			for _, j := range jobs {
				if j.done != nil {
					j.done(errors.New("manual draft epoch retired"))
				}
			}
			return
		}
		if len(w.queue) == 0 {
			w.running = false
			w.mu.Unlock()
			return
		}
		job := w.queue[0]
		w.queue = w.queue[1:]
		prior := w.uncertain
		w.mu.Unlock()
		var err error
		if prior != nil {
			err = w.commit(*prior)
		}
		if err == nil {
			err = w.commit(job)
		}
		if job.done != nil {
			job.done(err)
		}
	}
}
func (w *manualDraftWriter) proof(digest string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return !w.closed && !w.running && len(w.queue) == 0 && w.uncertain == nil && w.confirmed == digest
}
func (w *manualDraftWriter) idle() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return !w.running && len(w.queue) == 0 && w.uncertain == nil
}
func (w *manualDraftWriter) stop() {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true
	if w.cancel != nil {
		w.cancel()
	}
	if !w.running {
		w.running = true
		go w.run()
	}
	w.mu.Unlock()
}

type manualDraftPersistence struct {
	PersistedNonEmpty                                     bool
	Info                                                  string
	Root, Identity, Error, LastQueued                     string
	Loading, Read, Loaded, ScopesKnown, Touched, Applying bool
	Initial                                               *manualDraftEnvelope
	Writer                                                *manualDraftWriter
	Targets                                               map[string]string
	Labels                                                map[string]string
	Locations                                             map[string]manualDraftLocation
	Intents                                               map[string]manualDraftIntent
}

func (a *App) configureManualDrafts(root string) {
	a.retireManualDrafts()
	if a.guidanceRoot != "" {
		root = a.guidanceRoot
	}
	a.manualDrafts = &manualDraftPersistence{Root: root, Targets: map[string]string{}, Labels: map[string]string{}, Locations: map[string]manualDraftLocation{}, Intents: map[string]manualDraftIntent{}}
}
func (a *App) retireManualDrafts() {
	if p := a.manualDrafts; p != nil {
		if p.Writer != nil {
			p.Writer.stop()
		}
		if p.Applying {
			a.saving = false
		}
	}
	a.manualDrafts = nil
	a.drafts = map[string]Draft{}
	for key, mutation := range a.pendingMutations {
		if mutation.Kind == "save" || mutation.Kind == "edit" {
			delete(a.pendingMutations, key)
		}
	}
	a.draftTitle, a.draft, a.draftScope, a.draftConditions = "", "", "", ""
	a.editing = false
	if a.detailMode == "edit" || a.detailMode == "new" {
		a.detailMode = "view"
	}
	for key, state := range a.routeStates {
		if state.Mode == "new" || state.Mode == "edit" {
			state.Mode = "view"
			a.routeStates[key] = state
		}
	}
}
func (a *App) loadManualDrafts(identity string) {
	p := a.manualDrafts
	if p == nil || p.Loading || p.Read || p.Error != "" || a.libraryContext == nil {
		return
	}
	if !manualUUID(identity) {
		p.Error = "手动草稿的当前库身份尚未核验。"
		return
	}
	p.Identity = identity
	p.Loading = true
	life, dispatch := a.libraryContext, a.dispatch
	go func() {
		disk, envelope, err := openManualDraftDisk(p.Root, identity)
		if life.Err() != nil {
			if disk != nil {
				disk.close()
			}
			return
		}
		var lease sync.Mutex
		adopted, closed := false, false
		settled := make(chan struct{})
		go func() {
			select {
			case <-life.Done():
				lease.Lock()
				if !adopted && !closed && disk != nil {
					disk.close()
					closed = true
				}
				lease.Unlock()
			case <-settled:
			}
		}()
		dispatch(func() {
			lease.Lock()
			if a.manualDrafts != p || life.Err() != nil || closed {
				if !closed && disk != nil {
					disk.close()
					closed = true
				}
				lease.Unlock()
				close(settled)
				return
			}
			adopted = true
			lease.Unlock()
			close(settled)
			p.Loading = false
			if err != nil {
				p.Error = "手动草稿恢复未完成，原文件保留；请检查后重试。"
				return
			}
			writerLife, cancel := context.WithCancel(life)
			w := &manualDraftWriter{life: writerLife, cancel: cancel, disk: disk, save: disk.write, confirmed: records.MergeDigest(envelope)}
			p.Writer = w
			p.Read = true
			p.Initial = &envelope
			p.PersistedNonEmpty = len(envelope.Entries) > 0
			go func() { <-writerLife.Done(); w.stop() }()
			a.adoptManualDrafts()
		})
	}()
}
func (a *App) manualScopesLoaded() {
	if p := a.manualDrafts; p != nil {
		p.ScopesKnown = true
	}
}
func (a *App) adoptManualDrafts() {
	p := a.manualDrafts
	if p == nil || !p.Read || p.Loaded || p.Initial == nil {
		return
	}
	envelope := p.Initial
	if len(envelope.Entries) > 0 && !p.ScopesKnown {
		return
	}
	for key, entry := range envelope.Entries {
		if _, newer := a.drafts[key]; newer {
			continue
		}
		label := a.scopeLabel(entry.Dataset)
		if label == "" {
			label = "原范围不可用 · " + entry.Dataset[:8]
		}
		a.drafts[key] = Draft{Title: entry.Title, Text: entry.Text, Scope: label, Conditions: entry.Conditions, Revision: entry.Revision}
		p.Targets[key], p.Labels[key], p.Locations[key] = entry.Dataset, label, entry.Location
		if entry.Pending != nil {
			a.pendingMutations[key] = entry.Pending.Request
			p.Intents[key] = *entry.Pending
		}
		loc := entry.Location
		route := loc.Page + ":" + loc.Scope
		if _, exists := a.routeStates[route]; !exists {
			a.routeStates[route] = PaneState{ID: loc.Selected, Mode: loc.Mode, Kind: loc.Kind, BodyScroll: loc.Scroll}
		}
	}
	p.Loaded = true
	p.Initial = nil
	p.Error = ""
	p.Info = ""
	if envelope.Active != nil && !p.Touched {
		a.loadSequence++ // Retire every read that began before draft restoration.
		a.loading = false
		loc := *envelope.Active
		if loc.Scope == "" || a.scopeLabel(loc.Scope) != "" {
			a.navigate(loc.Page, loc.Scope)
			a.restoreManualLocation(loc)
			a.refresh(false)
		}
	}
}
func (a *App) restoreManualLocation(loc manualDraftLocation) {
	key := loc.Selected
	if loc.Mode == "new" {
		key = "new:" + loc.Scope
	}
	draft, ok := a.drafts[key]
	if !ok {
		return
	}
	a.page, a.scope = loc.Page, loc.Scope
	a.memoryRoute = loc.Page + ":" + loc.Scope
	a.selectedID, a.selectedItem = loc.Selected, nil
	a.detailMode, a.editing, a.recordKind = loc.Mode, loc.Mode == "edit" || loc.Mode == "new", loc.Kind
	a.draftTitle, a.draft, a.draftScope, a.draftConditions = draft.Title, draft.Text, draft.Scope, draft.Conditions
	a.detailScroll.Y = loc.Scroll
	a.query, a.searchQuery, a.readingSearch, a.readingFilter = loc.Query, loc.SearchQuery, loc.ReadingSearch, loc.ReadingFilter
}
func (a *App) manualLocation() manualDraftLocation {
	page := a.page
	if page == "全部记忆" {
		page = "搜索"
	}
	return manualDraftLocation{Page: page, Scope: a.scope, Selected: a.selectedID, Mode: a.detailMode, Kind: a.recordKind, Scroll: a.detailScroll.Y, Query: a.query, SearchQuery: a.searchQuery, ReadingSearch: a.readingSearch, ReadingFilter: a.readingFilter}
}
func (a *App) beginManualDraftTarget(key, dataset string) {
	p := a.manualDrafts
	if p == nil {
		return
	}
	if p.Targets[key] == "" && manualUUID(dataset) {
		p.Targets[key] = dataset
		p.Labels[key] = a.draftScope
	}
	a.noteManualDraft(key)
}
func (a *App) noteManualDraft(key string) {
	p := a.manualDrafts
	if p == nil {
		return
	}
	p.Touched = true
	p.Locations[key] = a.manualLocation()
}
func (a *App) manualDraftScopeChanged() {
	p := a.manualDrafts
	if p == nil {
		return
	}
	if !a.editing || a.detailMode != "new" {
		return
	}
	key := a.newDraftKey()
	dataset := a.datasetForLabel(a.draftScope)
	if !manualUUID(dataset) {
		p.Error = "所选范围不能唯一对应当前库，请重新选择；草稿保留。"
		return
	}
	if request, exists := a.pendingMutations[key]; exists && request.Dataset != dataset {
		label := a.scopeLabel(p.Targets[key])
		if label == "" {
			label = "原范围不可用 · " + p.Targets[key][:min(8, len(p.Targets[key]))]
		}
		a.draftScope = label
		p.Info = "原保存请求尚待核对，已保留原范围与草稿；请先核对原请求再更改范围。"
		return
	}
	p.Targets[key], p.Labels[key] = dataset, a.draftScope
	p.Info = ""
	a.rememberDraft()
}
func (a *App) refreshManualDraftLabels() {
	p := a.manualDrafts
	if p == nil || !p.ScopesKnown {
		return
	}
	for key, target := range p.Targets {
		d, ok := a.drafts[key]
		if !ok {
			continue
		}
		label := a.scopeLabel(target)
		if label == "" {
			label = "原范围不可用 · " + target[:min(8, len(target))]
		}
		old := p.Labels[key]
		if d.Scope == old {
			d.Scope = label
			a.drafts[key] = d
		}
		active := a.selectedID
		if a.detailMode == "new" {
			active = a.newDraftKey()
		}
		if a.editing && active == key && a.draftScope == old {
			a.draftScope = label
		}
		p.Labels[key] = label
	}
}
func (a *App) manualEnvelope() (manualDraftEnvelope, error) {
	p := a.manualDrafts
	if p == nil || p.Identity == "" {
		return manualDraftEnvelope{}, errors.New("manual draft epoch not loaded")
	}
	a.refreshManualDraftLabels()
	a.rememberDraft()
	envelope := manualDraftEnvelope{Format: manualDraftFormat, Identity: p.Identity, Entries: map[string]manualDraftEntry{}}
	keys := []string{}
	for key := range a.drafts {
		keys = append(keys, key)
	}
	for key, pending := range a.pendingMutations {
		if pending.Kind == "save" || pending.Kind == "edit" {
			if _, ok := a.drafts[key]; !ok {
				return envelope, errors.New("manual request has no draft owner")
			}
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		draft := a.drafts[key]
		dataset := p.Targets[key]
		if !manualUUID(dataset) {
			return envelope, errors.New("manual draft destination is not verified")
		}
		p.Targets[key], p.Labels[key] = dataset, draft.Scope
		loc, exists := p.Locations[key]
		if !exists {
			loc = manualDraftLocation{Page: "通用知识", Mode: "edit", Selected: key, Kind: "knowledge"}
			for _, scope := range a.scopes {
				if scope.ID == dataset && scope.Kind == "project" {
					loc.Page, loc.Scope = "项目", dataset
				}
			}
			if manualDraftNewKey(key) {
				loc.Scope = strings.TrimPrefix(key, "new:")
				loc.Mode = "new"
				loc.Selected = ""
				if loc.Scope != "" {
					loc.Page = "项目"
				}
			}
		}
		entry := manualDraftEntry{Dataset: dataset, Revision: draft.Revision, Title: draft.Title, Text: draft.Text, Conditions: draft.Conditions, Location: loc}
		if mutation, ok := a.pendingMutations[key]; ok {
			intent := manualDraftIntent{Request: mutation, Title: mutation.Title, Text: mutation.Text}
			if mutation.Conditions != nil {
				intent.Conditions = *mutation.Conditions
			}
			entry.Pending = &intent
			p.Intents[key] = intent
		} else {
			delete(p.Intents, key)
		}
		envelope.Entries[key] = entry
	}
	if a.editing && (a.detailMode == "new" || a.detailMode == "edit") {
		loc := a.manualLocation()
		key := a.selectedID
		if loc.Mode == "new" {
			key = a.newDraftKey()
		}
		if entry, ok := envelope.Entries[key]; ok {
			entry.Location = loc
			envelope.Entries[key] = entry
			envelope.Active = &loc
		}
	}
	if err := validateManualEnvelope(envelope, p.Identity); err != nil {
		return envelope, err
	}
	return envelope, nil
}
func (a *App) syncManualDrafts() {
	p := a.manualDrafts
	if p == nil || a.visualFixture {
		return
	}
	a.adoptManualDrafts()
	if !p.Loaded || p.Error != "" || p.Applying {
		return
	}
	a.persistManualDrafts(nil)
}
func (a *App) persistManualDrafts(done func(error)) {
	p := a.manualDrafts
	if p == nil {
		if done != nil {
			if a.hasManualDrafts() {
				done(errors.New("manual draft persistence unavailable"))
			} else {
				done(nil)
			}
		}
		return
	}
	if !p.Loaded || p.Writer == nil {
		if done != nil {
			done(errors.New("manual draft recovery not verified"))
		}
		return
	}
	envelope, err := a.manualEnvelope()
	if err != nil {
		p.Error = "手动草稿范围或内容尚未完成本地校验；编辑保留。"
		if done != nil {
			done(err)
		}
		return
	}
	data, err := json.Marshal(envelope)
	if err != nil || len(data) > manualDraftMaxBytes {
		p.Error = "手动草稿超出本地保存范围，编辑仍保留。"
		if done != nil {
			done(errors.New("manual draft payload too large"))
		}
		return
	}
	digest := records.MergeDigest(envelope)
	if p.Writer.proof(digest) {
		p.PersistedNonEmpty = len(envelope.Entries) > 0
		p.Error = ""
		if done != nil {
			done(nil)
		}
		return
	}
	if done == nil && (p.Error != "" || p.LastQueued == digest) {
		return
	}
	p.LastQueued = digest
	dispatch := a.dispatch
	p.Writer.enqueue(manualDraftJob{data: data, digest: digest, automatic: done == nil, done: func(err error) {
		dispatch(func() {
			if a.manualDrafts != p {
				if done != nil {
					done(errors.New("manual draft epoch retired"))
				}
				return
			}
			if err != nil {
				p.Error = "手动草稿尚未确认本地持久保存；编辑保留，请重试。"
				p.LastQueued = ""
			} else {
				p.Error = ""
				p.PersistedNonEmpty = len(envelope.Entries) > 0
			}
			if done != nil {
				done(err)
			}
		})
	}})
}
func (a *App) hasManualDrafts() bool {
	for _, pending := range a.pendingMutations {
		if pending.Kind == "save" || pending.Kind == "edit" {
			return true
		}
	}
	p := a.manualDrafts
	if len(a.drafts) > 0 || a.editing && (a.detailMode == "new" || a.detailMode == "edit") {
		return true
	}
	return p != nil && (p.PersistedNonEmpty || p.Loading || p.Read && (p.Error != "" || !p.Loaded) || p.Writer != nil && !p.Writer.idle())
}
func (a *App) flushManualDrafts(done func(error)) {
	if a.saving {
		done(errors.New("knowledge save still owns its request"))
		return
	}
	if !a.hasManualDrafts() {
		done(nil)
		return
	}
	a.persistManualDrafts(done)
}
func (a *App) persistManualMutation(key string, mutation records.Mutation) {
	p := a.manualDrafts
	if p == nil || !p.Loaded || p.Writer == nil {
		a.message = "手动草稿尚未完成当前库的本地持久恢复，请稍后重试保存。"
		return
	}
	if mutation.Kind != "save" && mutation.Kind != "edit" {
		a.message = "手动草稿不能执行其他知识操作。"
		return
	}
	if mutation.Dataset != p.Targets[key] {
		if prior, ok := p.Intents[key]; ok {
			a.pendingMutations[key] = prior.Request
		} else {
			delete(a.pendingMutations, key)
		}
		a.refreshManualDraftLabels()
		a.draftScope = p.Labels[key]
		a.message = "原草稿范围与当前选择不一致，未写入知识；请重新核对。"
		return
	}
	if mutation.Kind == "edit" {
		current := a.current()
		if current == nil || current.ID != mutation.Memory || current.Dataset != mutation.Dataset {
			if prior, ok := p.Intents[key]; ok {
				a.pendingMutations[key] = prior.Request
			} else {
				delete(a.pendingMutations, key)
			}
			a.message = "编辑目标尚未读取核验，草稿与原保存请求保留。"
			return
		}
	}
	a.saving = true
	p.Applying = true
	a.persistManualDrafts(func(err error) {
		if a.manualDrafts != p {
			return
		}
		a.saving = false
		p.Applying = false
		if err != nil {
			a.message = "保存前草稿与请求尚未持久保存，未写入知识；请重试。"
			if a.noteLocalMaintenanceStorageError("manual", key, mutation.OperationID, err) {
				a.message = "保存前草稿写入未完成，原请求尚未发送；请查看存储提示。"
			}
			return
		}
		// The exact request and its operation ID are durable before any IPC Apply.
		a.applyMutation(key, mutation)
	})
}
func (a *App) manualDraftNotice(c *ui.Context) {
	p := a.manualDrafts
	if p == nil {
		return
	}
	if p.Error == "" && p.Identity == "" && a.runtimeStatus.Phase == "stopped" {
		ui.Row(c).Key("manual-draft-await-identity").Padding(8, 24).Gap(10).Children(func() {
			ui.Text(c, "服务已停止；手动草稿文件保留，明确启动服务后核验当前库并恢复。").FontSize(11).Grow(1).MinWidth(0)
			quietButton(c, "启动服务并核验草稿").Disabled(a.resumeRuntime == nil).OnClick(func() {
				if a.resumeRuntime != nil {
					a.resumeRuntime()
				}
			})
		})
		return
	}
	if p.Error == "" && p.Info != "" {
		ui.Row(c).Padding(8, 24).Children(func() { ui.Text(c, p.Info).FontSize(11).TextColor(design.Muted) })
		return
	}
	if p.Error == "" {
		return
	}
	ui.Row(c).Key("manual-draft-notice").Padding(8, 24).Gap(10).Children(func() {
		ui.Text(c, p.Error).FontSize(11).Grow(1).MinWidth(0)
		quietButton(c, "重试草稿保存与恢复").Key("manual-draft-retry").Disabled(p.Loading || p.Applying).OnClick(func() {
			if !p.Read {
				p.Error = ""
				a.loadManualDrafts(p.Identity)
			} else {
				a.persistManualDrafts(func(error) {})
			}
		})
	})
}

func (a *App) manualAutomaticNavigation(page, scope string) {
	p := a.manualDrafts
	touched := false
	if p != nil {
		touched = p.Touched
	}
	a.navigate(page, scope)
	if p != nil && a.manualDrafts == p {
		p.Touched = touched
	}
}

// This runs only after the user's existing "continue from latest" action.
// Reading a newer version alone never changes a draft's target or baseline.
func (a *App) confirmManualLatest(key, dataset string) {
	p := a.manualDrafts
	if p == nil || !manualUUID(key) || !manualUUID(dataset) {
		return
	}
	p.Targets[key] = dataset
	p.Labels[key] = a.draftScope
	delete(p.Intents, key)
	a.noteManualDraft(key)
}

func manualUniqueJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var value func(int) error
	value = func(depth int) error {
		if depth > 24 {
			return errors.New("manual draft JSON nesting exceeds bounds")
		}
		token, err := decoder.Token()
		if err != nil {
			return errors.New("manual draft JSON invalid")
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return errors.New("manual draft JSON has duplicate keys")
				}
				seen[name] = true
				if err = value(depth + 1); err != nil {
					return err
				}
			}
			close, err := decoder.Token()
			if err != nil || close != json.Delim('}') {
				return errors.New("manual draft JSON invalid")
			}
		case '[':
			for decoder.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
			close, err := decoder.Token()
			if err != nil || close != json.Delim(']') {
				return errors.New("manual draft JSON invalid")
			}
		default:
			return errors.New("manual draft JSON invalid")
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("manual draft JSON has trailing payload")
	}
	return nil
}
