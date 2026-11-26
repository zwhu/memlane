package app

import (
	"context"

	"github.com/zwhu/memlane/app/api"
	"github.com/zwhu/memlane/app/reading"
)

func catalogRowFromAPI(row api.CatalogItem) v2KnowledgeItem {
	id := row.DocID
	if id == "" {
		id = row.DocumentID
	}
	return v2KnowledgeItem{
		DocID: id, DatasetID: row.DatasetID, ProjectID: row.ProjectID,
		Title: row.Title, DocName: row.DocName, DocLabel: row.DocLabel,
		EffectiveDate: row.EffectiveDate, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		ContentHash: row.ContentHash, Kind: row.Kind, Category: row.Category,
		CommonCategory: row.CommonCategory, LessonType: row.LessonType, StateKey: row.StateKey,
		SessionID: row.SessionID, RecallCount: int64(row.RecallCount), IsUnread: row.IsUnread,
	}
}

func catalogRowsFromAPI(rows []api.CatalogItem) []v2KnowledgeItem {
	if rows == nil {
		return nil
	}
	out := make([]v2KnowledgeItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, catalogRowFromAPI(row))
	}
	return out
}

func (c ProjectControlClient) knowledgePage(ctx context.Context, identity string, opts api.KnowledgePageOptions) (v2ProjectKnowledgeResponse, error) {
	return reading.FetchKnowledgePage(ctx, opts, func(ctx context.Context, opts api.KnowledgePageOptions) (v2ProjectKnowledgeResponse, error) {
		client, err := newAppAPIClient(c, identity)
		if err != nil {
			return v2ProjectKnowledgeResponse{}, err
		}
		controller := reading.KnowledgeController{KnowledgeDeps: reading.KnowledgeDeps{Client: client}}
		page, err := controller.List(ctx, opts)
		if legacyReadingRouteMissing(err) {
			return c.projectKnowledgePageV2(ctx, identity, opts.ProjectID, opts.Sort, opts.Category, opts.UnreadOnly,
				opts.RecallAgentID, opts.RecallEventID, opts.Limit, v2KnowledgePageOpts{
					Cursor: opts.Cursor, Direction: opts.Direction, AnchorDocID: opts.AnchorDocID,
				})
		}
		return v2ProjectKnowledgeResponse{
			ProjectID: page.ProjectID, DatasetID: page.DatasetID, CatalogVersion: page.CatalogVersion,
			TotalFiltered: page.TotalFiltered, UnreadCount: page.UnreadCount, UnreadStateRev: page.UnreadStateRev,
			CategoryCounts: page.CategoryCounts, Items: catalogRowsFromAPI(page.Items),
			NextCursor: page.NextCursor, HasMore: page.HasMore, PrevCursor: page.PrevCursor, HasPrev: page.HasPrev,
		}, err
	})
}

func (c ProjectControlClient) commonPage(ctx context.Context, dataset, category string, limit int, opts v2KnowledgePageOpts) (v2CommonKnowledgeResponse, error) {
	client, err := newAppAPIClient(c, "")
	if err != nil {
		return v2CommonKnowledgeResponse{}, err
	}
	page, err := (reading.CommonController{Client: client}).List(ctx, api.KnowledgePageOptions{
		CommonCategory: category, Limit: limit, Cursor: opts.Cursor, Direction: opts.Direction,
		AnchorDocID: opts.AnchorDocID, RecallAgentID: opts.RecallAgentID, RecallEventID: opts.RecallEventID,
	})
	if legacyReadingRouteMissing(err) {
		return c.commonKnowledgePageV2(ctx, dataset, category, limit, opts)
	}
	return v2CommonKnowledgeResponse{
		DatasetID: page.DatasetID, CatalogVersion: page.CatalogVersion, TotalFiltered: page.TotalFiltered,
		CategoryCounts: page.CategoryCounts, Items: catalogRowsFromAPI(page.Items),
		NextCursor: page.NextCursor, HasMore: page.HasMore, PrevCursor: page.PrevCursor, HasPrev: page.HasPrev,
	}, err
}

func (c ProjectControlClient) listKnowledgePage(ctx context.Context, identity, project, sort, category string, unreadOnly bool, agent, event string, limit int, opts v2KnowledgePageOpts) (v2ProjectKnowledgeResponse, error) {
	return c.knowledgePage(ctx, identity, api.KnowledgePageOptions{
		ProjectID: project, Sort: sort, Category: category, UnreadOnly: unreadOnly,
		RecallAgentID: agent, RecallEventID: event, Limit: limit, Cursor: opts.Cursor,
		Direction: opts.Direction, AnchorDocID: opts.AnchorDocID,
	})
}

func (c ProjectControlClient) readingBody(ctx context.Context, dataset, id string) (v2DocumentBodyResponse, error) {
	client, err := newAppAPIClient(c, "")
	if err != nil {
		return v2DocumentBodyResponse{}, err
	}
	body, err := (reading.KnowledgeController{KnowledgeDeps: reading.KnowledgeDeps{Client: client}}).Body(ctx, dataset, id)
	if legacyReadingRouteMissing(err) {
		return c.documentBodyV2(ctx, dataset, id)
	}
	return v2DocumentBodyResponse{ID: body.DocumentID, DatasetID: body.DatasetID, Body: &body.Content, MIMEType: body.MimeType}, err
}

func rawSessionFromAPI(row api.RawItem) rawSessionEntry {
	label := row.Label
	if label == "" && row.Title != row.SessionID {
		label = row.Title
	}
	return rawSessionEntry{
		Session: Session{
			ID: row.SessionID, Dataset: row.DatasetID, User: row.UserID, Agent: row.AgentID,
			Started: row.StartedAt, Updated: row.LastActivityAt, IsSubagent: row.IsSubagent || row.ParentSessionID != "",
			ParentSessionID: row.ParentSessionID, SubagentRole: row.SubagentRole, NestingDepth: row.NestingDepth,
		},
		Label:     label,
		MsgCount:  row.MsgCount,
		ToolCalls: row.ToolCalls,
	}
}

func (c ProjectControlClient) rawPage(ctx context.Context, project, kind, cursor string) (v2RawRecordsResponse, error) {
	client, err := newAppAPIClient(c, "")
	if err != nil {
		return v2RawRecordsResponse{}, err
	}
	reader := reading.RawRecordsController{Client: client}
	page, err := reader.Page(ctx, "", api.RawRecordsOptions{ProjectID: project, Kind: kind, Cursor: cursor})
	if legacyReadingRouteMissing(err) {
		return c.rawRecordsV2(ctx, project, kind, reading.RawBatchSize, cursor)
	}
	out := v2RawRecordsResponse{CacheReady: true, NextCursor: page.NextCursor, HasMore: page.HasMore}
	for _, row := range page.Items {
		entry := v2RawRecordItem{ItemKind: row.Kind}
		switch row.Kind {
		case "session":
			session := rawSessionFromAPI(row)
			var childrenPreview []rawSessionEntry
			if len(row.ChildrenPreview) > 0 {
				childrenPreview = make([]rawSessionEntry, 0, len(row.ChildrenPreview))
				for _, childRow := range row.ChildrenPreview {
					childrenPreview = append(childrenPreview, rawSessionFromAPI(childRow))
				}
			}
			entry.Session = &v2RawSessionEntry{
				Session: session.Session, Label: session.Label,
				MsgCount: session.MsgCount, ToolCalls: session.ToolCalls,
				ChildCount:      row.ChildCount,
				ChildrenPreview: childrenPreview,
				ChildrenHasMore: row.ChildrenHasMore || row.ChildCount > len(childrenPreview),
			}
		case "document":
			id := row.DocID
			if id == "" {
				id = row.DocumentID
			}
			label := row.DocLabel
			if label == "" {
				label = row.Title
			}
			entry.Document = &Document{ID: id, Dataset: row.DatasetID, Label: &label, Name: row.DocName,
				Created: row.CreatedAt, Updated: row.UpdatedAt, FirstSourceDate: row.EffectiveDate}
		}
		out.Items = append(out.Items, entry)
	}
	return out, err
}

func (c ProjectControlClient) rawChildren(ctx context.Context, project, dataset, session, user, cursor string) (v2SessionChildrenResponse, error) {
	client, err := newAppAPIClient(c, "")
	if err != nil {
		return v2SessionChildrenResponse{}, err
	}
	reader := reading.RawRecordsController{Client: client}
	page, err := reader.Page(ctx, session, api.RawRecordsOptions{ProjectID: project, UserID: user, Cursor: cursor})
	if legacyReadingRouteMissing(err) {
		return c.sessionChildrenV2(ctx, project, session, user, reading.RawBatchSize, cursor)
	}
	out := v2SessionChildrenResponse{ProjectID: project, DatasetID: dataset, ParentSessionID: session,
		NextCursor: page.NextCursor, HasMore: page.HasMore}
	for _, item := range page.Items {
		out.Items = append(out.Items, rawSessionFromAPI(item))
	}
	return out, err
}

func commonControllerFor(control ProjectControlClient) (reading.CommonController, error) {
	client, err := newAppAPIClient(control, "")
	return reading.CommonController{Client: client}, err
}

func (c ProjectControlClient) readingSourceSession(ctx context.Context, dataset, session, user string) (Session, error) {
	client, err := newAppAPIClient(c, "")
	if err != nil {
		return Session{}, err
	}
	row, err := (reading.KnowledgeController{KnowledgeDeps: reading.KnowledgeDeps{Client: client}}).SourceSession(ctx, dataset, session, user)
	if legacyReadingRouteMissing(err) {
		old, err := c.sessionLookupV2(ctx, dataset, session, user)
		return old.Session, err
	}
	return rawSessionFromAPI(row).Session, err
}
