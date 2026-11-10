package desktop

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func (s *ProjectOrganizerUI) clearHistory() {
	if s.HistoryCancel != nil {
		s.HistoryCancel()
		s.HistoryCancel = nil
	}
	s.HistorySequence++
	s.IncludeHistory = false
	s.Sessions = nil
	s.HistoryLoading = false
	s.HistoryLoaded = false
	s.HistoryError = ""
	s.HistoryWorkspaceID = ""
	s.HistoryUnavailable = 0
	s.HistoryUnattributed = 0
}

func (a *App) chooseProjectHistory(include bool) {
	s := &a.projectOrganizer
	if !s.Open || s.Task != nil || !s.Loaded || s.Binding.State != "available" || s.Binding.Agent != "codex" || a.libraryContext == nil || a.dispatch == nil {
		return
	}
	s.clearHistory()
	s.IncludeHistory = include
	if !include {
		return
	}
	s.HistoryLoading = true
	s.HistorySequence++
	sequence, project, binding := s.HistorySequence, s.Project, s.Binding
	ownerContext, client := a.libraryContext, s.Client
	ctx, cancel := context.WithTimeout(ownerContext, 60*time.Second)
	s.HistoryCancel = cancel
	go func() {
		defer cancel()
		q := records.ProjectHistoryQuery{Project: project, BindingRevision: binding.Revision}
		selected := []records.ProjectSessionSelection{}
		seen := map[string]bool{}
		identity := ""
		total, unavailable, unattributed, read := -1, 0, 0, 0
		var err error
		for {
			page, e := client.History(ctx, q)
			if e != nil {
				err = e
				break
			}
			if total < 0 {
				total = page.Total
				identity = page.WorkspaceID
				unattributed = page.Unattributed
			}
			if total != page.Total || identity == "" || identity != page.WorkspaceID || unattributed != page.Unattributed {
				err = errors.New("历史目录在选择期间变化")
				break
			}
			for _, item := range page.Items {
				if seen[item.ID] {
					err = errors.New("历史目录重复会话")
					break
				}
				seen[item.ID] = true
				read++
				if item.State == "available" && item.Digest != "" {
					selected = append(selected, item.ProjectSessionSelection)
				} else {
					unavailable++
				}
			}
			if err != nil {
				break
			}
			if page.Next == "" {
				if read != total {
					err = errors.New("历史目录读取不完整")
				}
				break
			}
			if page.Next == q.After || len(page.Items) == 0 {
				err = errors.New("历史目录分页未推进")
				break
			}
			q.After = page.Next
		}
		if ownerContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			if ownerContext.Err() != nil {
				return
			}
			current := &a.projectOrganizer
			if !current.Open || current.Project != project || current.HistorySequence != sequence || current.Binding != binding || current.Task != nil {
				return
			}
			current.HistoryLoading = false
			current.HistoryCancel = nil
			if err != nil {
				current.HistoryError = "历史会话读取未完成，可重新读取或取消选择。"
				return
			}
			current.Sessions = selected
			current.HistoryWorkspaceID = identity
			current.HistoryLoaded = true
			current.HistoryUnavailable = unavailable
			current.HistoryUnattributed = unattributed
			if op := current.Operation; op != nil && op.Project == project && op.Kind == "start" && op.FailureCode == "history_changed" {
				current.Operation = nil
				current.OperationID = uuid.NewString()
				current.Error = ""
			}
		})
	}()
}
