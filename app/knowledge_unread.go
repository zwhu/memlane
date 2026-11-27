package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/api"
	"github.com/zwhu/memlane/app/reading"
	"github.com/zwhu/memlane/app/shell"
)

type unreadFence struct{ appShellAdapter }

func (s unreadFence) AuthEpoch() uint64 { return s.app.unreadAuthEpoch.Load() }

func (a *App) ensureKnowledgeWriter() *reading.ReadWriter {
	if a.knowledgeWriter == nil {
		a.unreadAuthEpoch.Store(a.authEpoch)
		a.knowledgeWriter = reading.NewReadWriter(unreadFence{appShellAdapter{a}})
	}
	return a.knowledgeWriter
}

func (a *App) knowledgeWriterHasPending(projectID, datasetID string) bool {
	return a.knowledgeWriter.HasPending(projectID, datasetID)
}

func (a *App) waitKnowledgeReadFlush() { a.knowledgeWriter.WaitFlush() }

func (a *App) flushKnowledgeReadWithTimeout(timeout time.Duration) error {
	return a.knowledgeWriter.FlushWithTimeout(timeout)
}

func applyServerUnreadSnapshot(q *ProjectQueueState, count int, rev int64, pending bool) bool {
	return q != nil && reading.ApplyServerSnapshot(&q.UnreadState, count, rev, pending)
}

type projectUnreadAPI struct {
	control  ProjectControlClient
	identity string
}

func (t projectUnreadAPI) ReadingState(ctx context.Context, projectID string) (api.ReadingStateResponse, error) {
	client, err := newAppAPIClient(t.control, t.identity)
	if err != nil {
		return api.ReadingStateResponse{}, err
	}
	return client.ReadingState(ctx, projectID)
}

func legacyReadingRouteMissing(err error) bool {
	var e *api.APIError
	return errors.As(err, &e) && e.IsNotFound() && e.Code == ""
}

func legacyReadingError(err error) error {
	var e *ControlResponseError
	if errors.As(err, &e) {
		return &api.APIError{Status: e.Status, Code: e.Code, Message: e.Message}
	}
	return err
}

func legacyReadingReceipt(req api.MarkReadRequest, count int, known bool, rev int64) api.MarkReadResponse {
	if !known {
		count = -1
	}
	return api.MarkReadResponse{ReadingStateView: api.ReadingStateView{
		ProjectID: req.ProjectID, DatasetID: req.DatasetID, UnreadCount: count, StateRev: rev,
	}}
}

func (t projectUnreadAPI) MarkRead(ctx context.Context, req api.MarkReadRequest) (api.MarkReadResponse, error) {
	client, err := newAppAPIClient(t.control, t.identity)
	if err != nil {
		return api.MarkReadResponse{}, err
	}
	resp, err := client.MarkRead(ctx, req)
	if legacyReadingRouteMissing(err) {
		old, err := t.control.readingStateMarkReadV2(ctx, v2ReadingStateMarkReadRequest(req))
		return legacyReadingReceipt(req, old.UnreadCount, old.unreadCountKnown(), old.StateRev), legacyReadingError(err)
	}
	return resp, err
}

func (t projectUnreadAPI) MarkAllRead(ctx context.Context, req api.MarkReadRequest) (api.MarkReadResponse, error) {
	client, err := newAppAPIClient(t.control, t.identity)
	if err != nil {
		return api.MarkReadResponse{}, err
	}
	resp, err := client.MarkAllRead(ctx, req)
	if legacyReadingRouteMissing(err) {
		old, err := t.control.readingStateMarkAllReadV2(ctx, v2ReadingStateMarkAllReadRequest{
			ReadingIdentity: req.ReadingIdentity, ProjectID: req.ProjectID, DatasetID: req.DatasetID,
		})
		return legacyReadingReceipt(req, old.UnreadCount, old.unreadCountKnown(), old.StateRev), legacyReadingError(err)
	}
	return resp, err
}

func (a *App) projectUnreadTransport(p AccessProject) (projectUnreadAPI, api.MarkReadRequest, error) {
	control, ok := a.accessClient()
	if !ok {
		return projectUnreadAPI{}, api.MarkReadRequest{}, errors.New("未读状态服务不可用")
	}
	identity := a.cachedBaseReadingIdentity()
	if identity == "" {
		return projectUnreadAPI{}, api.MarkReadRequest{}, errors.New("读取身份暂不可用")
	}
	return projectUnreadAPI{control, identity}, api.MarkReadRequest{
		ReadingIdentity: identity, ProjectID: p.ID, DatasetID: p.Dataset,
	}, nil
}

func (a *App) enqueueKnowledgeReadMark(id string, q *ProjectQueueState, p AccessProject) {
	a.enqueueKnowledgeReadMarks([]string{id}, q, p)
}

func (a *App) enqueueKnowledgeReadMarks(ids []string, q *ProjectQueueState, p AccessProject) {
	transport, req, err := a.projectUnreadTransport(p)
	if err != nil {
		q.UnreadState.Error = "已读状态未保存"
		return
	}
	req.DocIDs = ids
	auth, update := a.authEpoch, a.Update
	a.ensureKnowledgeWriter().MarkRead(transport, req, func(resp api.MarkReadResponse, err error) {
		if update == nil {
			return
		}
		update(func() {
			if auth != a.authEpoch {
				return
			}
			if reading.Status(err, 401, 403) {
				appShellAdapter{a}.InvalidateReading()
				return
			}
			if err != nil {
				q.UnreadState.Error = "已读状态未保存"
			} else {
				q.UnreadState.Error = ""
				if resp.UnreadCount >= 0 {
					applyServerUnreadSnapshot(q, resp.UnreadCount, resp.StateRev, a.knowledgeWriterHasPending(p.ID, p.Dataset))
				} else if resp.StateRev > q.ServerUnreadStateRev {
					q.ServerUnreadStateRev = resp.StateRev
					q.ServerUnreadSnapshot++
				}
			}
			q.StateVersion++
		})
	})
}

func (a *App) syncV2ProjectUnreadCount(projectID, datasetID string, unreadCount int, stateRev int64, catalogVersion string) {
	for _, p := range a.access.Directory.Projects {
		if (projectID != "" && p.ID == projectID) || (datasetID != "" && p.Dataset == datasetID) {
			q := a.projectQueueStateForAsync(p)
			applyServerUnreadSnapshot(q, unreadCount, stateRev, a.knowledgeWriterHasPending(p.ID, p.Dataset))
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
		if sc := s.DetailScroll[s.Selected.Key()]; sc != nil && sc.Y > 2 {
			detailScrolled = true
		}
	}
	if reading.ShouldSilentRefreshOnCatalogChange(s.ListScroll.Y, s.Knowledge.Detail || detailScrolled, s.Loading || s.Knowledge.Appending) {
		a.loadKnowledgeFeed(s)
		return
	}
	s.Knowledge.NewCatalogAvailable = true
	a.verifySelectedKnowledgeVersion(s)
}

func (a *App) verifySelectedKnowledgeVersion(s *pane) {
	if s.Selected == nil || s.Selected.Document == nil || !s.Selected.BodyLoaded || a.Update == nil {
		return
	}
	control, ok := a.accessClient()
	if !ok {
		return
	}
	s.Knowledge.CatalogCheck++
	catalogCheck := s.Knowledge.CatalogCheck
	selected, update := *s.Selected, a.Update
	identity := a.cachedBaseReadingIdentity()
	request, rev := s.readRequest, a.readingRevisions[selected.Key()]
	adapter := appShellAdapter{a}
	fence := shell.CaptureFence(adapter)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		client, err := newAppAPIClient(control, identity)
		var detail api.DocumentDetail
		if err == nil {
			controller := reading.KnowledgeController{KnowledgeDeps: reading.KnowledgeDeps{Client: client}}
			detail, err = controller.Document(ctx, selected.Dataset, selected.ID)
		}
		if legacyReadingRouteMissing(err) {
			return
		}
		update(func() {
			if !fence.Valid(adapter) || s.Knowledge.CatalogCheck != catalogCheck || s.readRequest != request || a.readingRevisions[selected.Key()] != rev ||
				s.Selected == nil || s.Selected.Key() != selected.Key() {
				return
			}
			if err != nil {
				a.handleReadingFailure(selected, err)
				return
			}
			row := catalogRowFromAPI(detail.CatalogItem)
			if row.DatasetID != selected.Dataset || row.DocID != selected.ID {
				a.handleReadingFailure(selected, reading.ErrInvalidBody)
				return
			}
			old := selected.Document
			if reading.BodyVersionChanged(old.ContentHash, row.ContentHash, old.Updated, row.UpdatedAt) ||
				detail.Content != selected.Body && !selected.BodyTruncated {
				a.unavailableReadingItem(selected)
				a.selectReadingItem(s, row.toItem(a.overviewState.V2RecallStartedAt), false)
			}
		})
	}()
}

func (a *App) markKnowledgeRead(i Item) {
	if !reading.CanMarkRead(i.Kind, i.Document != nil, i.Document != nil && documentKnowledge(*i.Document), i.BodyLoaded, i.BodyTruncated) {
		return
	}
	for _, p := range a.access.Directory.Projects {
		if p.Dataset != i.Dataset {
			continue
		}
		q := a.projectQueueStateFor(p)
		if q.ReadIDs == nil {
			q.ReadIDs = map[string]bool{}
		}
		wasRead := q.ReadIDs[i.ID]
		wasServerUnread := i.ServerUnreadKnown && i.ServerUnread
		if wasRead && q.UnreadState.Error == "" {
			return
		}
		q.ReadIDs[i.ID] = true
		if q.ServerUnreadKnown && q.ServerUnreadCount > 0 && wasServerUnread && !wasRead {
			q.ServerUnreadCount--
		}
		if len(q.UnreadPreview) > 0 {
			filtered := q.UnreadPreview[:0]
			for _, row := range q.UnreadPreview {
				if row.ID != i.ID {
					filtered = append(filtered, row)
				}
			}
			q.UnreadPreview = filtered
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
		a.enqueueKnowledgeReadMark(i.ID, q, p)
		return
	}
}

func (a *App) projectUnread(p Project) int {
	identity := a.cachedBaseReadingIdentity()
	q := a.access.Queues[identity+"/"+p.ID+"/"+p.Dataset]
	if q == nil {
		return 0
	}
	return q.UnreadCount()
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
		if q != nil && q.UnreadState.Error != "" {
			return false
		}
		if q != nil {
			if q.ReadIDs[i.ID] {
				return false
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
	a.navigateWithUnreadFilter("知识", index, "", true)
	s := a.state()
	if a.Update != nil {
		return
	}
	a.reorderKnowledge(s)
	if len(s.Items) > 0 {
		a.selectReadingItem(s, s.Items[0], false)
	}
}

func (a *App) unreadProjectBadge(c *ui.Context, p Project, index int, active bool) {
	count := a.projectUnread(p)
	if count == 0 {
		return
	}
	identity := a.cachedBaseReadingIdentity()
	q := a.access.Queues[identity+"/"+p.ID+"/"+p.Dataset]
	usePopover := q != nil && len(q.UnreadPreview) > 0 && (count > 10 || q.UnreadHasMore)

	label := formatBadgeCount(count)
	bg, fg := tok.Fill2, tok.Text2
	if active {
		bg, fg = tok.Accent, tok.OnAccent
	}
	btn := ui.ButtonBase(c.Key("unread-badge-"+p.ID)).Role(ui.RoleButton).Cursor(ui.CursorPointer).
		Label("未读记忆："+p.Name).Tooltip(fmt.Sprintf("%d 条未读记忆，点击查看", count)).
		MinWidth(20).Height(18).Padding(0, 8).Radius(9).BorderWidth(0).AlignItems(ui.Center).Justify(ui.Center).Shrink(0)
	hoverBg := tok.Accent
	btn.Background(homeHoverColor(btn, "badge-bg", bg, hoverBg))
	if btn.Hovered() {
		fg = tok.OnAccent
	}
	v4ButtonPress(btn, 0.92)
	btn.Children(func() {
		ui.Text(c, label).Font(fontUI).FontSize(fsCaption).FontWeight(600).FontFeatures("tnum").TextColor(fg).SingleLine()
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
					ui.Text(c, fmt.Sprintf("未读记忆 · %d 条", count)).Font(fontUI).FontSize(fsFootnote).FontWeight(600).TextColor(tok.Text1).SingleLine()
					ui.Spacer(c)
				})
				previewLimit := min(10, len(q.UnreadPreview))
				for idx := 0; idx < previewLimit; idx++ {
					item := q.UnreadPreview[idx]
					row := ui.ButtonBase(c.Key("unread-preview-"+item.ID)).Role(ui.RoleButton).Cursor(ui.CursorPointer).
						Label(item.Title).FillWidth().Padding(4, 8).Radius(radiusSmall).BorderWidth(0).AlignItems(ui.Center).Justify(ui.Start).Gap(sp2)
					row.Background(homeHoverColor(row, "unread-prev-bg", ui.Transparent, tok.Hover))
					row.Children(func() {
						v4UnreadDot(c)
						ui.Text(c, item.Title).Font(fontUI).FontSize(fsCaption).TextColor(tok.Text1).SingleLine().Grow(1).MinWidth(0)
						ui.Text(c, displayDate(item.Date)).Font(fontUI).FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3).SingleLine().Shrink(0)
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
							ui.Text(c, viewAllLabel).Font(fontUI).FontSize(fsFootnote).FontWeight(500).TextColor(tok.Accent).SingleLine()
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
