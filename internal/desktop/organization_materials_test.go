package desktop

import (
	"context"
	"errors"
	"testing"

	"github.com/zwhu/memlane/internal/records"
)

func TestOrganizationMaterialsIncludesBothKindsAndDoesNotReplayExhaustedPage(t *testing.T) {
	calls := 0
	list := func(_ context.Context, q records.ListQuery) (records.Page, error) {
		calls++
		if len(q.Datasets) != 1 || q.Datasets[0] != "project" || q.Limit != 50 {
			t.Fatal("material query lost project or bound")
		}
		if q.Kind == "raw" {
			return records.Page{Items: []records.Version{{Title: "raw"}}}, nil
		}
		if q.Kind != "knowledge" {
			t.Fatal("unexpected record kind")
		}
		if q.Cursor == "knowledge-next" {
			return records.Page{Items: []records.Version{{Title: "knowledge second page"}}}, nil
		}
		return records.Page{Items: []records.Version{{Title: "knowledge"}}, NextCursor: "knowledge-next"}, nil
	}
	page, err := listOrganizationMaterials(context.Background(), list, "project", "")
	if err != nil || len(page.Items) != 2 || page.Items[1].Title != "raw" || page.NextCursor == "" {
		t.Fatal("J01 omitted a material kind", page, err)
	}
	page, err = listOrganizationMaterials(context.Background(), list, "project", page.NextCursor)
	if err != nil || len(page.Items) != 1 || page.Items[0].Title != "knowledge second page" || page.NextCursor != "" || calls != 3 {
		t.Fatal("material pagination repeated exhausted raw records", page, calls, err)
	}
}

func TestOrganizationMaterialsDoesNotPresentPartialReadAsComplete(t *testing.T) {
	page, err := listOrganizationMaterials(context.Background(), func(_ context.Context, q records.ListQuery) (records.Page, error) {
		if q.Kind == "raw" {
			return records.Page{}, errors.New("synthetic read failure")
		}
		return records.Page{Items: []records.Version{{Title: "knowledge"}}}, nil
	}, "project", "")
	if err == nil || len(page.Items) != 0 {
		t.Fatal("partial material read was presented as complete")
	}
}
