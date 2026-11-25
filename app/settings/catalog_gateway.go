package settings

import (
	"context"
	"errors"

	"github.com/zwhu/memlane/app/api"
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
	client, err := newAppAPIClient(c, identity)
	if err != nil {
		return v2ProjectKnowledgeResponse{}, err
	}
	page, err := client.ListKnowledge(ctx, opts)
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
}

func legacyReadingRouteMissing(err error) bool {
	var e *api.APIError
	return errors.As(err, &e) && e.IsNotFound() && e.Code == ""
}
