package desktop

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zwhu/memlane/internal/records"
)

func previewExitApp() *App {
	a := New("test", nil)
	a.libraryContext = context.Background()
	a.dispatch = func(fn func()) { fn() }
	a.page = "整理记录"
	a.organization.New = true
	a.organization.PreviewID = "material-preview"
	a.organization.Project = "project"
	a.organization.Kind = "knowledge"
	a.experience.Distill = &ExperienceDistillUI{PreviewID: "experience-preview", TaskID: "task", Source: MemoryItem{ID: "source", Dataset: "project", Revision: 1}}
	return a
}

func TestPreviewExitReusesOnlyVerifiedUnchangedIdleSnapshotsOffline(t *testing.T) {
	a := previewExitApp()
	calls := 0
	mw := &materialPreviewWriter{life: context.Background(), identity: "library", save: func(_ context.Context, r records.OrganizationPreviewWrite) (records.OrganizationPreviewState, error) {
		calls++
		return records.OrganizationPreviewState{Identity: r.Identity, Generation: r.Generation + 1}, nil
	}}
	ew := &experiencePreviewWriter{life: context.Background(), identity: "library", save: func(_ context.Context, r records.ExperiencePreviewWrite) (records.ExperiencePreviewState, error) {
		calls++
		return records.ExperiencePreviewState{Identity: r.Identity, Generation: r.Generation + 1}, nil
	}}
	a.materialPersistence = &materialPersistence{Loaded: true, Writer: mw}
	a.experiencePersistence = &experiencePersistence{Loaded: true, Writer: ew}
	// Establish proof via actual verified commit receipts, never UI.LastDigest.
	if err := mw.commit(records.OrganizationPreviewWrite{Identity: "library", Preview: *a.materialPreviewSnapshot()}); err != nil {
		t.Fatal(err)
	}
	if err := ew.commit(records.ExperiencePreviewWrite{Identity: "library", Preview: *a.experiencePreviewSnapshot()}); err != nil {
		t.Fatal(err)
	}
	mw.save = func(context.Context, records.OrganizationPreviewWrite) (records.OrganizationPreviewState, error) {
		t.Error("unchanged material rewritten while offline")
		return records.OrganizationPreviewState{}, errors.New("service stopped")
	}
	ew.save = func(context.Context, records.ExperiencePreviewWrite) (records.ExperiencePreviewState, error) {
		t.Error("unchanged experience rewritten while offline")
		return records.ExperiencePreviewState{}, errors.New("service stopped")
	}
	called := false
	a.FlushDraftPreviews(func(err error) {
		called = true
		if err != nil {
			t.Error(err)
		}
	})
	if !called || !a.experienceExitPending || calls != 2 || a.organization.PreviewID != "material-preview" || a.experience.Distill.TaskID != "task" {
		t.Fatal("offline exit changed or lost recovered drafts")
	}
}

func TestPreviewExitChangedSnapshotDoesNotTrustEnqueuedDigest(t *testing.T) {
	for _, kind := range []string{"material", "experience"} {
		t.Run(kind, func(t *testing.T) {
			a := previewExitApp()
			dispatch := make(chan func(), 4)
			a.dispatch = func(fn func()) { dispatch <- fn }
			if kind == "material" {
				a.experience.Distill = nil
				p := a.materialPreviewSnapshot()
				w := &materialPreviewWriter{life: context.Background(), identity: "library", confirmedDigest: records.MergeDigest(p), save: func(context.Context, records.OrganizationPreviewWrite) (records.OrganizationPreviewState, error) {
					return records.OrganizationPreviewState{}, errors.New("offline")
				}}
				a.materialPersistence = &materialPersistence{Loaded: true, Writer: w}
				a.organization.Purpose = "new unsaved purpose"
				a.materialPersistence.LastDigest = records.MergeDigest(a.materialPreviewSnapshot())
			} else {
				a.organization.New = false
				p := a.experiencePreviewSnapshot()
				w := &experiencePreviewWriter{life: context.Background(), identity: "library", confirmedDigest: records.MergeDigest(p), save: func(context.Context, records.ExperiencePreviewWrite) (records.ExperiencePreviewState, error) {
					return records.ExperiencePreviewState{}, errors.New("offline")
				}}
				a.experiencePersistence = &experiencePersistence{Loaded: true, Writer: w}
				a.experience.Distill.Focus = "new-unsaved-focus"
				a.experiencePersistence.LastDigest = records.MergeDigest(a.experiencePreviewSnapshot())
			}
			result := make(chan error, 1)
			a.FlushDraftPreviews(func(err error) { result <- err })
			select {
			case fn := <-dispatch:
				fn()
			case <-time.After(5 * time.Second):
				t.Fatal("missing barrier callback")
			}
			select {
			case err := <-result:
				if err == nil || a.experienceExitPending {
					t.Fatal("unsaved draft bypassed offline barrier")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("missing exit result")
			}
		})
	}
}

func TestPreviewExitProofRejectsUncertainQueuedAndActiveWriters(t *testing.T) {
	value := records.OrganizationPreview{ID: "preview"}
	digest := records.MergeDigest(value)
	cases := []struct {
		name   string
		mutate func(*materialPreviewWriter)
	}{
		{"unacknowledged", func(w *materialPreviewWriter) { w.confirmedDigest = "" }},
		{"uncertain", func(w *materialPreviewWriter) { w.uncertain = &records.OrganizationPreviewWrite{Preview: value} }},
		{"queued", func(w *materialPreviewWriter) { w.queue = []materialPreviewJob{{value: value}} }},
		{"active", func(w *materialPreviewWriter) { w.running = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := &materialPreviewWriter{identity: "library", confirmedDigest: digest}
			tc.mutate(w)
			if w.durableAndIdle(digest) {
				t.Fatal("invalid proof admitted")
			}
		})
	}
	ev := records.ExperiencePreview{ID: "preview"}
	ed := records.MergeDigest(ev)
	ew := &experiencePreviewWriter{identity: "library", confirmedDigest: ed, running: true}
	if ew.durableAndIdle(ed) {
		t.Fatal("active experience writer admitted")
	}
	ew.running = false
	ew.queue = []previewJob{{value: ev}}
	if ew.durableAndIdle(ed) {
		t.Fatal("queued experience writer admitted")
	}
	ew.queue = nil
	ew.uncertain = &records.ExperiencePreviewWrite{Preview: ev}
	if ew.durableAndIdle(ed) {
		t.Fatal("uncertain experience writer admitted")
	}
}

func TestPreviewExitForeignReceiptKeepsUncertaintyAndCannotProveDurability(t *testing.T) {
	p := records.ExperiencePreview{ID: "preview"}
	w := &experiencePreviewWriter{life: context.Background(), identity: "original", confirmedDigest: records.MergeDigest(p), save: func(_ context.Context, r records.ExperiencePreviewWrite) (records.ExperiencePreviewState, error) {
		return records.ExperiencePreviewState{Identity: "foreign", Generation: r.Generation + 1}, nil
	}}
	if w.commit(records.ExperiencePreviewWrite{Preview: p}) == nil || w.durableAndIdle(records.MergeDigest(p)) || w.uncertain == nil {
		t.Fatal("foreign receipt granted offline exit")
	}
}

func TestPreviewExitPendingInvisibleDraftAndLoadingCannotBypass(t *testing.T) {
	a := New("test", nil)
	a.materialPersistence = &materialPersistence{Loaded: true, Writer: &materialPreviewWriter{running: true}}
	if !a.HasDraftPreview() {
		t.Fatal("pending writer invisible to quit owner")
	}
	called := false
	a.FlushDraftPreviews(func(err error) {
		called = true
		if err == nil {
			t.Error("pending hidden write bypassed")
		}
	})
	if !called || a.experienceExitPending {
		t.Fatal("pending exit did not fail closed")
	}
	a.materialPersistence = nil
	a.experiencePersistence = &experiencePersistence{Loading: true}
	if !a.HasDraftPreview() {
		t.Fatal("loading experience ledger invisible to quit owner")
	}
	a.FlushDraftPreviews(func(err error) {
		if err == nil {
			t.Error("unloaded ledger bypassed")
		}
	})
}

func TestPreviewExitMatchingSnapshotWaitsForQueuedUncertainWrite(t *testing.T) {
	for _, kind := range []string{"material", "experience"} {
		t.Run(kind, func(t *testing.T) {
			a := previewExitApp()
			dispatch := make(chan func(), 3)
			a.dispatch = func(fn func()) { dispatch <- fn }
			entered, release := make(chan struct{}), make(chan struct{})
			operations := make(chan string, 4)
			count := 0
			if kind == "material" {
				a.experience.Distill = nil
				p := *a.materialPreviewSnapshot()
				w := &materialPreviewWriter{life: context.Background(), identity: "library", confirmedDigest: records.MergeDigest(p)}
				w.save = func(_ context.Context, r records.OrganizationPreviewWrite) (records.OrganizationPreviewState, error) {
					count++
					operations <- r.Operation
					if count == 1 {
						close(entered)
						<-release
					}
					return records.OrganizationPreviewState{}, errors.New("receipt unavailable")
				}
				a.materialPersistence = &materialPersistence{Loaded: true, Writer: w}
				w.enqueue(materialPreviewJob{value: p, automatic: true})
			} else {
				a.organization.New = false
				p := *a.experiencePreviewSnapshot()
				w := &experiencePreviewWriter{life: context.Background(), identity: "library", confirmedDigest: records.MergeDigest(p)}
				w.save = func(_ context.Context, r records.ExperiencePreviewWrite) (records.ExperiencePreviewState, error) {
					count++
					operations <- r.Operation
					if count == 1 {
						close(entered)
						<-release
					}
					return records.ExperiencePreviewState{}, errors.New("receipt unavailable")
				}
				a.experiencePersistence = &experiencePersistence{Loaded: true, Writer: w}
				w.enqueue(previewJob{value: p, Automatic: true})
			}
			<-entered
			result := make(chan error, 1)
			a.FlushDraftPreviews(func(err error) { result <- err })
			select {
			case <-result:
				t.Fatal("queued write was bypassed by old proof")
			default:
			}
			close(release)
			select {
			case fn := <-dispatch:
				fn()
			case <-time.After(5 * time.Second):
				t.Fatal("barrier did not resolve")
			}
			if err := <-result; err == nil || a.experienceExitPending {
				t.Fatal("uncertain queued write admitted exit")
			}
			first, second := <-operations, <-operations
			if first == "" || first != second {
				t.Fatal("uncertain write was not replayed exactly")
			}
		})
	}
}

func TestPreviewExitOptimizationDoesNotBypassStartReviewDurabilityBarrier(t *testing.T) {
	for _, kind := range []string{"material", "experience"} {
		t.Run(kind, func(t *testing.T) {
			a := previewExitApp()
			dispatch := make(chan func(), 2)
			a.dispatch = func(fn func()) { dispatch <- fn }
			result := make(chan error, 1)
			if kind == "material" {
				p := a.materialPreviewSnapshot()
				w := &materialPreviewWriter{life: context.Background(), identity: "library", confirmedDigest: records.MergeDigest(p), save: func(context.Context, records.OrganizationPreviewWrite) (records.OrganizationPreviewState, error) {
					return records.OrganizationPreviewState{}, errors.New("offline")
				}}
				a.materialPersistence = &materialPersistence{Loaded: true, Writer: w}
				a.persistMaterialPreview(func(err error) { result <- err })
			} else {
				p := a.experiencePreviewSnapshot()
				w := &experiencePreviewWriter{life: context.Background(), identity: "library", confirmedDigest: records.MergeDigest(p), save: func(context.Context, records.ExperiencePreviewWrite) (records.ExperiencePreviewState, error) {
					return records.ExperiencePreviewState{}, errors.New("offline")
				}}
				a.experiencePersistence = &experiencePersistence{Loaded: true, Writer: w}
				a.persistExperiencePreview(func(err error) { result <- err })
			}
			select {
			case fn := <-dispatch:
				fn()
			case <-time.After(5 * time.Second):
				t.Fatal("request barrier skipped writer")
			}
			if err := <-result; err == nil {
				t.Fatal("request barrier used exit-only offline proof")
			}
		})
	}
}
