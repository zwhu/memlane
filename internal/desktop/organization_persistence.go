package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

type materialPreviewJob struct {
	value              records.OrganizationPreview
	automatic, abandon bool
	done               func(error)
}

// One writer owns the library CAS. Different tasks never coalesce, and an
// uncertain write is replayed exactly before the next request barrier.
type materialPreviewWriter struct {
	mu              sync.Mutex
	queue           []materialPreviewJob
	running         bool
	life            context.Context
	identity        string
	generation      int64
	confirmedDigest string
	uncertain       *records.OrganizationPreviewWrite
	save            func(context.Context, records.OrganizationPreviewWrite) (records.OrganizationPreviewState, error)
}

func (w *materialPreviewWriter) enqueue(job materialPreviewJob) {
	w.mu.Lock()
	if job.automatic && len(w.queue) > 0 && w.queue[len(w.queue)-1].automatic && w.queue[len(w.queue)-1].value.ID == job.value.ID {
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

func (w *materialPreviewWriter) commit(request records.OrganizationPreviewWrite) error {
	ctx, stop := context.WithTimeout(w.life, 5*time.Second)
	defer stop()
	result, err := w.save(ctx, request)
	w.mu.Lock()
	defer w.mu.Unlock()
	if err != nil {
		w.uncertain = &request
		var rejected *serviceipc.ServiceError
		if errors.As(err, &rejected) && (rejected.Code == "invalid_preview" || rejected.Code == "invalid_request" || rejected.Code == "scope_denied" || rejected.Code == "preview_start_exists" || rejected.Code == "preview_request_frozen") {
			w.uncertain = nil
		}
		return err
	}
	if result.Identity != w.identity || result.Generation != request.Generation+1 {
		w.uncertain = &request
		return fmt.Errorf("material preview receipt identity or generation changed")
	}
	w.generation = result.Generation
	w.uncertain = nil
	w.confirmedDigest = records.MergeDigest(request.Preview)
	return nil
}

// Only a verified receipt (or authoritative loaded snapshot) can prove an
// unchanged draft durable. Enqueued payload digests never grant this proof.
func (w *materialPreviewWriter) durableAndIdle(digest string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.identity != "" && digest != "" && digest == w.confirmedDigest && !w.running && len(w.queue) == 0 && w.uncertain == nil
}
func (w *materialPreviewWriter) idle() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return !w.running && len(w.queue) == 0 && w.uncertain == nil
}

func (w *materialPreviewWriter) run() {
	for {
		w.mu.Lock()
		if len(w.queue) == 0 {
			w.running = false
			w.mu.Unlock()
			return
		}
		job := w.queue[0]
		w.queue = w.queue[1:]
		w.mu.Unlock()
		var err error
		abandonReplayed := false
		if w.uncertain != nil {
			abandonReplayed = w.uncertain.AbandonStart && job.abandon && w.uncertain.Preview.ID == job.value.ID
			err = w.commit(*w.uncertain)
		}
		if err == nil && !abandonReplayed {
			err = w.commit(records.OrganizationPreviewWrite{Identity: w.identity, Generation: w.generation, Operation: uuid.NewString(), Preview: job.value, AbandonStart: job.abandon})
		}
		if job.done != nil {
			job.done(err)
		}
	}
}

type materialPersistence struct {
	Cancel                   context.CancelFunc
	Loading, Loaded, Touched bool
	Error, LastDigest        string
	Writer                   *materialPreviewWriter
}

func (a *App) loadMaterialPreview() {
	s := a.materialPersistence
	if s == nil || s.Loading || s.Loaded || a.firstLaunch.Detecting || a.firstLaunch.Open || a.libraryContext == nil {
		return
	}
	// Both ledgers may retain drafts; restore their navigation in a stable
	// order after first detection, and do not overwrite newer user navigation.
	if ep := a.experiencePersistence; ep != nil && !ep.Loaded {
		return
	}
	s.Loading = true
	life, client := a.libraryContext, a.organization.Client
	go func() {
		ctx, stop := context.WithTimeout(life, 5*time.Second)
		defer stop()
		state, err := client.MaterialPreview(ctx)
		if life.Err() != nil {
			return
		}
		a.dispatch(func() {
			if a.materialPersistence != s {
				return
			}
			s.Loading = false
			if err != nil {
				s.Error = "整理草稿恢复未完成，请重试；原任务保留。"
				return
			}
			s.Loaded = true
			s.Error = ""
			a.loadManualDrafts(state.Identity)
			writerLife, cancel := context.WithCancel(life)
			s.Cancel = cancel
			s.Writer = &materialPreviewWriter{life: writerLife, identity: state.Identity, generation: state.Generation, save: client.SaveMaterialPreview}
			if state.Identity != "" && state.Preview != nil {
				s.Writer.confirmedDigest = records.MergeDigest(state.Preview)
			}
			if state.Preview != nil && a.organization.PreviewID == "" {
				a.adoptMaterialPreview(state)
				a.restoreMaterialFilters(*state.Preview)
				if state.Preview.Open && !s.Touched && !a.commonDistillOpen() {
					a.organizationMode = true
					a.manualAutomaticNavigation("整理记录", "")
				}
			}
			a.persistMaterialPreview(nil)
		})
	}()
}

func (a *App) adoptMaterialPreview(state records.OrganizationPreviewState) {
	p := state.Preview
	if p == nil {
		return
	}
	s := &a.organization
	s.PreviewID = p.ID
	s.New, s.Selected, s.Task = p.New, p.TaskID, state.Task
	s.Project, s.ProjectLabel, s.Kind = p.Project, a.scopeLabel(p.Project), p.Kind
	s.Start, s.Resume, s.CancelWanted = p.Start, p.Resume, p.CancelWanted
	if state.Task != nil {
		s.Start = nil // The read recovered the original committed Start.
	}
	s.ReviewMode, s.GlobalMode, s.Purpose = p.Review, p.Global, p.Purpose
	s.ReviewItem, s.Scroll.Y, s.ResultScroll = p.Candidate, p.Scroll, p.ResultScroll
	s.Focus, s.RestoreFocus = p.Focus, true
	s.DiagnosticOpen, s.ExplanationOpen = p.DiagnosticOpen, p.ExplanationOpen
	s.MoveAuthorizations = map[string]string{}
	for id, digest := range p.MoveAuthorizations {
		s.MoveAuthorizations[id] = digest
	}
	s.Chosen = map[string]MemoryItem{}
	s.Order = nil
	for _, input := range state.Inputs {
		s.Chosen[input.Memory] = MemoryItem{ID: input.Memory, Dataset: p.Project, Revision: input.Revision, Title: input.Title, Text: input.Text}
		s.Order = append(s.Order, input.Memory)
	}
	s.Destinations = map[string]bool{}
	for _, id := range p.Destinations {
		s.Destinations[id] = true
	}
	if s.Decisions == nil {
		s.Decisions = map[string]records.OrganizationReview{}
	}
	for key, v := range p.Decisions {
		s.Decisions[key] = v
	}
	if s.RejectReasons == nil {
		s.RejectReasons = map[string]*OrganizationRejectDraft{}
	}
	for id, reason := range p.RejectReasons {
		s.RejectReasons[id] = &OrganizationRejectDraft{Reason: reason}
	}
	if s.ExperienceDrafts == nil {
		s.ExperienceDrafts = map[string]*ExperienceReviewDraft{}
	}
	for id, v := range p.ExperienceDrafts {
		s.ExperienceDrafts[id] = &ExperienceReviewDraft{ProposalDigest: v.ProposalDigest, Value: v.Value, Request: v.Request, RejectRequest: v.RejectRequest, OriginalOpen: v.OriginalOpen}
	}
	if s.ReviewScroll == nil {
		s.ReviewScroll = map[string]*ui.ScrollState{}
	}
	for id, y := range p.ReviewScroll {
		s.ReviewScroll[id] = &ui.ScrollState{Y: y}
	}
	if s.ReviewFocus == nil {
		s.ReviewFocus = map[string]string{}
	}
	for id, key := range p.ReviewFocus {
		s.ReviewFocus[id] = key
	}
	if p.Start != nil && state.Task == nil {
		s.Error = "原启动请求尚未取得任务回执。重开只恢复草稿；可核对原请求后明确重试或放弃。"
	}
	if state.Task != nil && s.Resume != nil && state.Task.Attempt > s.Resume.ExpectedAttempt {
		// Keep the frozen resume until an explicit same-operation replay
		// acknowledges its receipt; reading itself grants no new authority.
		s.Notice = "原恢复请求保留，可明确重试同一回执。"
	}
}

func (a *App) materialPreviewSnapshot() *records.OrganizationPreview {
	s := &a.organization
	if !s.New && s.Task == nil {
		return nil
	}
	if s.PreviewID == "" {
		s.PreviewID = uuid.NewString()
	}
	p := records.OrganizationPreview{ID: s.PreviewID, Open: a.page == "整理记录" && a.organizationMode, New: s.New, Project: s.Project, Kind: s.Kind, Review: s.ReviewMode || s.GlobalMode, Global: s.GlobalMode, Purpose: s.Purpose, TaskID: s.Selected, Candidate: s.ReviewItem, Start: s.Start, Resume: s.Resume, CancelWanted: s.CancelWanted, Scroll: s.Scroll.Y, ResultScroll: s.ResultScroll, Focus: s.Focus, DiagnosticOpen: s.DiagnosticOpen, ExplanationOpen: s.ExplanationOpen, Decisions: map[string]records.OrganizationReview{}, RejectReasons: map[string]string{}, ExperienceDrafts: map[string]records.ExperiencePreviewDraft{}, ReviewScroll: map[string]float32{}}
	p.MoveAuthorizations = map[string]string{}
	if p.Kind == "" {
		p.Kind = "knowledge"
	}
	if s.Task != nil && !s.New {
		t := s.Task
		p.Project, p.TaskID = t.Project, t.ID
		p.Review, p.Global, p.Purpose = t.Snapshot.Schema != records.OrganizationSchema, t.Snapshot.Schema == records.OrganizationGlobalSchema, t.Snapshot.Purpose
		for _, input := range t.Snapshot.Inputs {
			p.Inputs = append(p.Inputs, records.MergeTarget{Memory: input.Memory, Revision: input.Revision})
		}
		for _, scope := range t.Snapshot.Destinations {
			if scope.Kind == "project" {
				p.Destinations = append(p.Destinations, scope.ID)
			}
		}
		for _, e := range t.Entries {
			if value := s.MoveAuthorizations[e.ID]; e.Item.Action == "move" && value == t.ID+":"+records.MergeDigest(e.Item) {
				p.MoveAuthorizations[e.ID] = value
			}
			if p.ReviewFocus == nil {
				p.ReviewFocus = map[string]string{}
			}
			if key := s.ReviewFocus[e.ID]; key != "" {
				p.ReviewFocus[e.ID] = key
			}
			for _, suffix := range []string{"true", "false"} {
				key := e.ID + suffix
				if v, ok := s.Decisions[key]; ok {
					p.Decisions[key] = v
				}
			}
			if v := s.RejectReasons[e.ID]; v != nil {
				p.RejectReasons[e.ID] = v.Reason
			}
			if v := s.ExperienceDrafts[e.ID]; v != nil {
				p.ExperienceDrafts[e.ID] = records.ExperiencePreviewDraft{ProposalDigest: v.ProposalDigest, Value: v.Value, Request: v.Request, RejectRequest: v.RejectRequest, OriginalOpen: v.OriginalOpen}
			}
			if v := s.ReviewScroll[e.ID]; v != nil {
				p.ReviewScroll[e.ID] = v.Y
			}
		}
		if s.ReviewItem != "" {
			p.ReviewScroll[s.ReviewItem] = s.Scroll.Y
			p.ReviewFocus[s.ReviewItem] = s.Focus
		}
	} else {
		p.TaskID = ""
		for _, id := range s.Order {
			if v, ok := s.Chosen[id]; ok {
				p.Inputs = append(p.Inputs, records.MergeTarget{Memory: id, Revision: v.Revision})
			}
		}
		for _, scope := range a.scopes {
			if s.Destinations[scope.ID] {
				p.Destinations = append(p.Destinations, scope.ID)
			}
		}
	}
	p.FilterOpen, p.ProjectFilter, p.Today = s.FilterOpen, s.FilterProject, s.FilterTime == "今天"
	if s.FilterOrigin == "手动" {
		p.OriginFilter = "manual"
	} else if s.FilterOrigin == "计划" {
		p.OriginFilter = "schedule"
	}
	if at := a.listScroll["整理记录:"]; at != nil {
		p.ListScroll = at.Y
	}
	raw, _ := json.Marshal(p)
	var frozen records.OrganizationPreview
	_ = json.Unmarshal(raw, &frozen)
	return &frozen
}

func (a *App) restoreMaterialFilters(p records.OrganizationPreview) {
	s := &a.organization
	s.FilterOpen, s.FilterProject, s.FilterTime = p.FilterOpen, p.ProjectFilter, "不限时间"
	s.FilterOrigin = "不限来源"
	if p.OriginFilter == "manual" {
		s.FilterOrigin = "手动"
	} else if p.OriginFilter == "schedule" {
		s.FilterOrigin = "计划"
	}
	if p.Today {
		s.FilterTime = "今天"
	}
	s.FilterProjectLabel = "不限项目"
	if s.FilterProject != "" {
		s.FilterProjectLabel = a.scopeLabel(s.FilterProject)
	}
	s.TasksLoaded = false
	s.Tasks, s.Next, s.ListIdentity = nil, "", ""
	a.listScroll["整理记录:"] = &ui.ScrollState{Y: p.ListScroll}
}

func (a *App) persistMaterialPreview(done func(error)) {
	a.saveMaterialPreview(false, done)
}

func (a *App) materialContextBarrier(change func()) {
	s := &a.organization
	if p := a.materialPersistence; p != nil && !p.Loaded {
		s.Error = "整理草稿尚未恢复，请先重试恢复，再打开或新建任务。"
		a.organizationMode = true
		a.navigate("整理记录", "")
		return
	}
	s.Busy = true
	a.persistMaterialPreview(func(err error) {
		s.Busy = false
		if err != nil {
			s.Error = "当前草稿尚未保存，已保留本任务；请重试后再切换。"
			return
		}
		change()
	})
}

func (a *App) readMaterialStartReceipt() {
	s := &a.organization
	if s.Start == nil || s.Busy || a.libraryContext == nil {
		return
	}
	s.Busy = true
	life, epoch, client, operation := a.libraryContext, s.Epoch, s.Client, s.Start.OperationID
	go func() {
		ctx, stop := context.WithTimeout(life, 5*time.Second)
		defer stop()
		state, err := client.MaterialPreview(ctx)
		if life.Err() != nil {
			return
		}
		a.dispatch(func() {
			if a.organization.Epoch != epoch {
				return
			}
			s.Busy = false
			if err != nil {
				s.Error = "原启动回执读取未完成，原请求保留。"
				return
			}
			if state.Preview != nil && state.Preview.Start == nil && state.Task == nil && state.Preview.ID == s.PreviewID {
				a.adoptMaterialPreview(state)
				s.AbandonWanted = false
				s.Error = ""
				s.Notice = "原请求已放弃，材料草稿保留；没有启动新任务。"
				a.persistMaterialPreview(nil)
				return
			}
			if state.Preview == nil || state.Preview.Start == nil || state.Preview.Start.OperationID != operation {
				s.Error = "原预览回执不匹配，原请求保留。"
				return
			}
			a.adoptMaterialPreview(state)
			a.persistMaterialPreview(nil)
			a.refreshOrganization(false, false)
		})
	}()
}

func (a *App) saveMaterialPreview(abandon bool, done func(error)) {
	s := a.materialPersistence
	if done == nil && (a.organization.Busy || a.organization.AbandonWanted) {
		return
	}
	if a.visualFixture && s == nil {
		if done != nil {
			done(nil)
		}
		return
	}
	p := a.materialPreviewSnapshot()
	if p == nil {
		if done != nil {
			done(nil)
		}
		return
	}
	if s == nil || !s.Loaded || s.Writer == nil {
		if done != nil {
			done(fmt.Errorf("material preview not loaded"))
		}
		return
	}
	digest := records.MergeDigest(p)
	if done == nil && digest == s.LastDigest {
		return
	}
	s.LastDigest = digest
	s.Writer.enqueue(materialPreviewJob{value: *p, automatic: done == nil, abandon: abandon, done: func(err error) {
		a.dispatch(func() {
			if a.materialPersistence != s {
				return
			}
			if err != nil {
				s.Error = "整理草稿尚未保存，当前编辑保留；请重试。"
				if code := maintenanceRemoteStorageCode(err); code != "" {
					s.Error = maintenanceStorageMessage(code)
				}
				s.LastDigest = ""
			} else {
				s.Error = ""
			}
			if done != nil {
				done(err)
			}
		})
	}})
}

func (a *App) abandonMaterialStart() {
	s := &a.organization
	if s.Start == nil || s.Busy {
		return
	}
	original := s.Start
	s.AbandonWanted = true
	s.Start = nil
	s.Busy = true
	a.saveMaterialPreview(true, func(err error) {
		s.Busy = false
		if err != nil {
			s.Start = original
			s.Error = "未能放弃原请求；请核对原任务回执，原请求保留。"
			return
		}
		s.Error = ""
		s.AbandonWanted = false
		s.Notice = "原请求已明确放弃，迟到启动会被拒绝。材料草稿保留。"
		a.persistMaterialPreview(nil)
	})
}

func (a *App) retireMaterialPreview() {
	if p := a.materialPersistence; p != nil && p.Cancel != nil {
		p.Cancel()
	}
	a.materialPersistence = nil
	client := a.organization.Client
	a.organization = OrganizationUI{Client: client, Epoch: uuid.NewString(), Kind: "knowledge", Chosen: map[string]MemoryItem{}, Results: map[string]MemoryItem{}}
}

func (a *App) HasDraftPreview() bool {
	if a.hasManualDrafts() || a.HasExperiencePreview() || a.organization.New || a.organization.Task != nil {
		return true
	}
	if p := a.experiencePersistence; p != nil && (p.Loading || p.Writer != nil && !p.Writer.idle()) {
		return true
	}
	return a.materialPersistence != nil && (a.materialPersistence.Loading || a.materialPersistence.Writer != nil && !a.materialPersistence.Writer.idle())
}
func (a *App) FlushDraftPreviews(done func(error)) {
	a.experienceExitPending = true
	a.flushExperiencePreview(func(err error) {
		if err != nil {
			a.experienceExitPending = false
			done(err)
			return
		}
		a.flushMaterialPreview(func(err error) {
			if err != nil {
				a.experienceExitPending = false
				done(err)
				return
			}
			a.flushManualDrafts(func(err error) {
				if err != nil {
					a.experienceExitPending = false
				}
				done(err)
			})
		})
	})
}

func (a *App) trackMaterialFocus(e ui.Element, key string, disabled bool) {
	s := &a.organization
	if s.RestoreFocus && s.Focus == key && !disabled {
		e.Focus()
		if e.Focused() {
			e.ScrollIntoView()
			s.RestoreFocus = false
		}
	}
	if e.Focused() {
		s.Focus = key
	}
}

// Exit may reuse proof for the exact unchanged, fully acknowledged draft. All
// edits and active/uncertain writers retain the normal write barrier.
func (a *App) flushMaterialPreview(done func(error)) {
	s := a.materialPersistence
	p := a.materialPreviewSnapshot()
	if s != nil && (s.Loading || !s.Loaded || s.Writer == nil) {
		done(fmt.Errorf("material preview durability not loaded"))
		return
	}
	if p == nil {
		if s != nil && !s.Writer.idle() {
			done(fmt.Errorf("material preview writes remain pending"))
			return
		}
		done(nil)
		return
	}
	if s != nil && s.Writer.durableAndIdle(records.MergeDigest(p)) {
		done(nil)
		return
	}
	a.persistMaterialPreview(done)
}
