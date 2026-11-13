package simpledesktop

import (
	"context"
	"fmt"
	"sort"
)

// Enumerate metadata from the two official catalogs before slicing a merged
// page. Their independent offsets are never treated as a shared cursor.
// No raw bodies, index or additional persistent catalog is created.
func (c Client) projectRecords(ctx context.Context, p Project, offset int, kind string) ([]Item, bool, error) {
	rows := []Item{}
	seen := map[string]bool{}
	if kind != "会话" {
		docs, err := c.overviewDocuments(ctx, p)
		if err != nil {
			return nil, false, err
		}
		for _, d := range docs {
			if d.knowledge() || derivedMaterial(d) {
				continue
			}
			copy := d
			i := Item{ID: d.ID, Dataset: d.Dataset, Kind: "文档", Title: d.title(), Date: d.date(), Document: &copy}
			rows = append(rows, i)
			seen[i.key()] = true
		}
	}
	if kind != "文档" {
		complete := false
		for start := 0; start <= 1000000; start += 100 {
			var page struct {
				Sessions []Session `json:"sessions"`
				More     bool      `json:"has_more"`
			}
			if err := c.get(ctx, fmt.Sprintf("/api/v1/sessions?range=all&limit=100&offset=%d&order_by=last_activity_at&descending=true", start), &page); err != nil {
				return nil, false, err
			}
			for _, v := range page.Sessions {
				if v.Dataset != p.Dataset {
					continue
				}
				if v.ID == "" {
					return nil, false, fmt.Errorf("官方会话身份缺失")
				}
				copy := v
				i := Item{ID: v.ID, Dataset: v.Dataset, Kind: "会话", Title: v.ID, Date: v.Updated, Session: &copy}
				if !seen[i.key()] {
					rows = append(rows, i)
					seen[i.key()] = true
				}
			}
			if !page.More {
				complete = true
				break
			}
			if len(page.Sessions) == 0 {
				return nil, false, fmt.Errorf("官方会话分页不完整")
			}
		}
		if !complete {
			return nil, false, fmt.Errorf("官方会话分页未读完")
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, _ := officialTime(rows[i].Date)
		b, _ := officialTime(rows[j].Date)
		if a.Equal(b) {
			return rows[i].key() < rows[j].key()
		}
		return a.After(b)
	})
	start := min(max(0, offset), len(rows))
	end := min(start+30, len(rows))
	return rows[start:end], end < len(rows), nil
}
