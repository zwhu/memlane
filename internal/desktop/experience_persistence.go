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

type previewJob struct {
	Automatic bool
	value     records.ExperiencePreview
	done      func(error)
}

// One writer owns generation and serializes snapshots. Only adjacent automatic
// writes coalesce; request barriers retain their exact frozen payload.
type experiencePreviewWriter struct {
	mu              sync.Mutex
	queue           []previewJob
	running         bool
	client          serviceipc.OrganizationClient
	life            context.Context
	identity        string
	generation      int64
	confirmedDigest string
	uncertain       *records.ExperiencePreviewWrite
	save            func(context.Context, records.ExperiencePreviewWrite) (records.ExperiencePreviewState, error)
}

func (w *experiencePreviewWriter) enqueue(job previewJob) {
	w.mu.Lock()
	if job.Automatic && len(w.queue) > 0 && w.queue[len(w.queue)-1].Automatic {
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

func (w *experiencePreviewWriter) commit(request records.ExperiencePreviewWrite) error {
	ctx, stop := context.WithTimeout(w.life, 5*time.Second)
	defer stop()
	result, err := w.save(ctx, request)
	w.mu.Lock()
	defer w.mu.Unlock()
	if err != nil {
		w.uncertain = &request
		var rejected *serviceipc.ServiceError
		if errors.As(err, &rejected) && (rejected.Code == "invalid_preview" || rejected.Code == "invalid_request") {
			w.uncertain = nil
		}
		return err
	}
	if result.Identity != w.identity || result.Generation != request.Generation+1 {
		w.uncertain = &request
		return fmt.Errorf("experience preview receipt identity or generation changed")
	}
	w.generation = result.Generation
	w.uncertain = nil
	w.confirmedDigest = records.MergeDigest(request.Preview)
	return nil
}

// Only a verified receipt (or authoritative loaded snapshot) can prove an
// unchanged draft durable. Enqueued payload digests never grant this proof.
func (w *experiencePreviewWriter) durableAndIdle(digest string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.identity != "" && digest != "" && digest == w.confirmedDigest && !w.running && len(w.queue) == 0 && w.uncertain == nil
}
func (w *experiencePreviewWriter) idle() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return !w.running && len(w.queue) == 0 && w.uncertain == nil
}

func (w *experiencePreviewWriter) run() {
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
		if w.uncertain != nil {
			err = w.commit(*w.uncertain)
		}
		if err == nil {
			err = w.commit(records.ExperiencePreviewWrite{Identity: w.identity, Generation: w.generation, Operation: uuid.NewString(), Preview: job.value})
		}
		if job.done != nil {
			job.done(err)
		}
	}
}

type experiencePersistence struct {
	Cancel                   context.CancelFunc
	Loading, Loaded, Touched bool
	Error                    string
	Writer                   *experiencePreviewWriter
	LastDigest               string
}

func (a *App) loadExperiencePreview() {
	s := a.experiencePersistence
	if s == nil || s.Loading || s.Loaded || a.firstLaunch.Detecting || a.firstLaunch.Open || a.libraryContext == nil {
		return
	}
	s.Loading = true
	client, life := a.organization.Client, a.libraryContext
	go func() {
		ctx, stop := context.WithTimeout(life, 5*time.Second)
		defer stop()
		state, err := client.Preview(ctx)
		if life.Err() != nil {
			return
		}
		a.dispatch(func() {
			if a.experiencePersistence != s {
				return
			}
			s.Loading = false
			if err != nil {
				s.Error = "提炼预览恢复未完成，请重试；本地草稿保留。"
				return
			}
			s.Loaded = true
			s.Error = ""
			writerLife, cancelWriter := context.WithCancel(life)
			s.Cancel = cancelWriter
			s.Writer = &experiencePreviewWriter{client: client, life: writerLife, identity: state.Identity, generation: state.Generation, save: client.SavePreview}
			if state.Identity != "" && state.Preview != nil {
				s.Writer.confirmedDigest = records.MergeDigest(state.Preview)
			}
			if state.Preview != nil && a.experience.Distill == nil {
				p := state.Preview
				d := &ExperienceDistillUI{PreviewID: p.ID, Open: p.Open, TaskID: p.TaskID, Task: state.Task, Start: p.Start, CandidateID: p.Candidate, ReturnPage: p.ReturnPage, ReturnScope: p.ReturnScope, ReturnSelected: p.ReturnSelected, ReturnScroll: p.ReturnScroll, BodyScroll: p.BodyScroll, Focus: p.Focus, RestoreFocus: true, ExplanationOpen: p.ExplanationOpen}
				if state.Source != nil {
					input := state.Source
					d.Source = MemoryItem{ID: input.Memory, Dataset: p.Project, Revision: input.Revision, Title: input.Title, Text: input.Text, State: "active", Category: "项目约定"}
				}
				if d.Task == nil {
					d.Error = "原生成请求尚未取得任务回执。可以显式重试同一请求；重开不会自动生成。"
				}
				a.experience.Distill = d
				if a.organization.ExperienceDrafts == nil {
					a.organization.ExperienceDrafts = map[string]*ExperienceReviewDraft{}
				}
				for id, v := range p.Drafts {
					a.organization.ExperienceDrafts[id] = &ExperienceReviewDraft{ProposalDigest: v.ProposalDigest, Value: v.Value, Request: v.Request, RejectRequest: v.RejectRequest, OriginalOpen: v.OriginalOpen}
				}
				if !s.Touched {
					materialTouched := a.materialPersistence != nil && a.materialPersistence.Touched
					a.manualAutomaticNavigation(p.Page, p.Scope)
					if a.materialPersistence != nil {
						a.materialPersistence.Touched = materialTouched
					}
					a.selectedID = p.Selected
					a.detailMode = "view"
					a.editing = false
					a.detailScroll.Y = p.Scroll
				} else {
					d.Open = false
				}
				a.refreshExperienceDistill()
				a.refresh(false)
			}
			a.persistExperiencePreview(nil)
			a.loadMaterialPreview()
		})
	}()
}

func (a *App) experiencePreviewSnapshot() *records.ExperiencePreview {
	s := a.experience.Distill
	if s == nil || s.TaskID == "" && s.Start == nil {
		return nil
	}
	if s.PreviewID == "" {
		s.PreviewID = uuid.NewString()
	}
	if a.commonDistillOpen() {
		s.BodyScroll = a.detailScroll.Y
	}
	p := records.ExperiencePreview{ID: s.PreviewID, Project: s.Source.Dataset, Source: records.MergeTarget{Memory: s.Source.ID, Revision: s.Source.Revision}, TaskID: s.TaskID, Start: s.Start, Open: s.Open, Page: a.page, Scope: a.scope, Selected: a.selectedID, ReturnPage: s.ReturnPage, ReturnScope: s.ReturnScope, ReturnSelected: s.ReturnSelected, ReturnScroll: s.ReturnScroll, Scroll: a.detailScroll.Y, BodyScroll: s.BodyScroll, Candidate: s.CandidateID, Focus: s.Focus, ExplanationOpen: s.ExplanationOpen, Drafts: map[string]records.ExperiencePreviewDraft{}}
	if s.Task != nil {
		for _, e := range s.Task.Entries {
			if d := a.organization.ExperienceDrafts[e.ID]; d != nil {
				p.Drafts[e.ID] = records.ExperiencePreviewDraft{ProposalDigest: d.ProposalDigest, Value: d.Value, Request: d.Request, RejectRequest: d.RejectRequest, OriginalOpen: d.OriginalOpen}
			}
		}
	}
	// The worker owns an immutable copy; edits and pointer retirement stay on UI.
	raw, _ := json.Marshal(p)
	var frozen records.ExperiencePreview
	_ = json.Unmarshal(raw, &frozen)
	return &frozen
}

// done is a durability barrier for explicit Start/Review and normal App exit.
// No IPC or disk access is performed inside View.
func (a *App) persistExperiencePreview(done func(error)) {
	s := a.experiencePersistence
	p := a.experiencePreviewSnapshot()
	if p == nil {
		if done != nil {
			done(nil)
		}
		return
	}
	if s == nil || !s.Loaded || s.Writer == nil {
		if done != nil {
			done(fmt.Errorf("preview not loaded"))
		}
		return
	}
	digest := records.MergeDigest(p)
	if done == nil && digest == s.LastDigest {
		return
	}
	s.LastDigest = digest
	s.Writer.enqueue(previewJob{value: *p, Automatic: done == nil, done: func(err error) {
		a.dispatch(func() {
			if a.experiencePersistence != s {
				return
			}
			if err != nil {
				s.Error = "尚未保存草稿，当前编辑保留；请重试。"
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

func (a *App) FlushExperiencePreview(done func(error)) {
	a.experienceExitPending = true
	a.flushExperiencePreview(func(err error) {
		if err != nil {
			a.experienceExitPending = false
		}
		done(err)
	})
}

func (a *App) CancelExperiencePreviewExit() { a.experienceExitPending = false }

func (a *App) retireExperiencePreview() {
	if p := a.experiencePersistence; p != nil && p.Cancel != nil {
		p.Cancel()
	}
	a.experiencePersistence = nil
	a.experience = ExperienceUI{}
	a.organization.ExperienceDrafts = nil
	a.experienceExitPending = false
}

func (a *App) HasExperiencePreview() bool {
	return a.experience.Distill != nil && (a.experience.Distill.TaskID != "" || a.experience.Distill.Start != nil)
}

func (a *App) trackExperienceFocus(e ui.Element, key string, disabled bool) {
	s := a.experience.Distill
	if s == nil {
		return
	}
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

func (a *App) flushExperiencePreview(done func(error)) {
	s := a.experiencePersistence
	p := a.experiencePreviewSnapshot()
	if s != nil && (s.Loading || !s.Loaded || s.Writer == nil) {
		done(fmt.Errorf("experience preview durability not loaded"))
		return
	}
	if p == nil {
		if s != nil && !s.Writer.idle() {
			done(fmt.Errorf("experience preview writes remain pending"))
			return
		}
		done(nil)
		return
	}
	if s != nil && s.Writer.durableAndIdle(records.MergeDigest(p)) {
		done(nil)
		return
	}
	a.persistExperiencePreview(done)
}
