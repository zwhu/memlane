package desktop

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zwhu/memlane/internal/records"
)

// J01 presents saved knowledge and raw records in one material checklist.
// Keep the library's separate kind queries and their independent cursors.
func listOrganizationMaterials(ctx context.Context, list func(context.Context, records.ListQuery) (records.Page, error), project, cursor string) (records.Page, error) {
	var next [2]string
	if cursor != "" {
		if err := json.Unmarshal([]byte(cursor), &next); err != nil {
			return records.Page{}, fmt.Errorf("invalid material cursor")
		}
	}
	var result records.Page
	for i, kind := range []string{"knowledge", "raw"} {
		if cursor != "" && next[i] == "" {
			continue
		}
		page, err := list(ctx, records.ListQuery{Datasets: []string{project}, Limit: 50, Kind: kind, Cursor: next[i]})
		if err != nil {
			return records.Page{}, err
		}
		result.Items = append(result.Items, page.Items...)
		next[i] = page.NextCursor
	}
	if next[0] != "" || next[1] != "" {
		encoded, err := json.Marshal(next)
		if err != nil {
			return records.Page{}, err
		}
		result.NextCursor = string(encoded)
	}
	return result, nil
}
