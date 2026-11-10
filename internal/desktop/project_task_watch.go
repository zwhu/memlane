package desktop

import (
	"context"
	"time"

	"github.com/zwhu/memlane/internal/records"
)

func (s *ProjectOrganizerUI) stopWatch() {
	if s.WatchCancel != nil {
		s.WatchCancel()
		s.WatchCancel = nil
	}
	s.WatchSequence++
}

func (a *App) ensureProjectTaskWatch() {
	s := &a.projectOrganizer
	if a.windowHidden || !s.Open || s.WatchCancel != nil || s.Task == nil || !(projectTaskRunning(s.Task) || projectIndexRecoveryRunning(s.Task)) || s.Task.AccessState != "available" || a.libraryContext == nil || a.dispatch == nil {
		return
	}
	ctx, cancel := context.WithCancel(a.libraryContext)
	s.WatchCancel = cancel
	s.WatchSequence++
	sequence, project, client := s.WatchSequence, s.Project, s.Client
	q := records.ProjectTaskWatch{Task: s.Task.ID, Attempt: s.Task.Attempt, Version: records.ProjectTaskVersion(*s.Task)}
	type position struct {
		version string
		keep    bool
	}
	go func() {
		defer cancel()
		for ctx.Err() == nil {
			readCtx, stop := context.WithTimeout(ctx, 8*time.Second)
			observation, err := client.Watch(readCtx, q)
			stop()
			if ctx.Err() != nil {
				return
			}
			feedback := make(chan position, 1)
			expected := q.Version
			a.dispatch(func() {
				current := &a.projectOrganizer
				if ctx.Err() != nil || !current.Open || current.WatchSequence != sequence || current.Project != project || current.SelectedTask != q.Task || current.Task == nil || current.Task.Attempt != q.Attempt {
					feedback <- position{}
					return
				}
				if err != nil || observation.Task.ID != q.Task || observation.Task.Attempt != q.Attempt || observation.Task.Snapshot.Project != project || observation.Version != records.ProjectTaskVersion(observation.Task) {
					current.WatchCancel = nil
					current.Error = "项目整理状态读取未完成，已加载内容保留。"
					feedback <- position{}
					return
				}
				// A periodic read or cancel receipt may already have advanced the
				// view. Ask again from that snapshot instead of replacing it with
				// a late observation whose order cannot be inferred from a hash.
				version := records.ProjectTaskVersion(*current.Task)
				if version == expected && version != observation.Version {
					current.Sequence++
					current.Loading = false
					current.Task = &observation.Task
					if current.Operation == nil || current.Operation.Error == "" {
						current.Error = ""
					}
					version = observation.Version
				}
				keep := (projectTaskRunning(current.Task) || projectIndexRecoveryRunning(current.Task)) && current.Task.AccessState == "available"
				if !keep {
					current.WatchCancel = nil
				}
				feedback <- position{version: version, keep: keep}
			})
			select {
			case <-ctx.Done():
				return
			case next := <-feedback:
				if !next.keep {
					return
				}
				q.Version = next.version
			}
		}
	}()
}

// Hiding a window suspends observation and visual loops, never its daemon task.
func (a *App) SetWindowVisible(visible bool) {
	a.windowHidden = !visible
	if !visible {
		a.persistExperiencePreview(nil)
		a.projectOrganizer.stopWatch()
	} else {
		a.projectOrganizer.Motion.Reenter = true
		a.projectPreview.Reenter = true
		a.refreshProjectOrganizer()
		a.ensureProjectTaskWatch()
	}
}
