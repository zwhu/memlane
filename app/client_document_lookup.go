package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zwhu/memlane/app/settings"
)

func (c Client) documentByID(ctx context.Context, dataset, id string) (Item, error) {
	i, err := c.documentInfoByID(ctx, dataset, id)
	if err != nil {
		return i, err
	}
	return c.read(ctx, i)
}
func (c Client) documentInfoByID(ctx context.Context, dataset, id string) (Item, error) {
	if control, ok := c.controlClient(); ok && dataset != "" && id != "" {
		res, err := control.documentLookupV2(ctx, dataset, id)
		if err == nil {
			d := res.Document
			if d.Dataset != dataset || d.ID != id {
				return Item{}, fmt.Errorf("official document dataset changed")
			}
			if res.EffectiveDate != "" {
				d.FirstSourceDate = res.EffectiveDate
			}
			if e := c.rememberKnowledgeDays([]Document{d}); e != nil {
				return Item{}, e
			}
			kind := "文档"
			if documentKnowledge(d) {
				kind = "知识"
			}
			date := documentDate(d)
			if date == "" && res.EffectiveDate != "" {
				date = res.EffectiveDate
			}
			source := documentSession(d)
			if source == "" {
				source = res.SessionID
			}
			item := Item{
				ID:             d.ID,
				Dataset:        dataset,
				Kind:           kind,
				Title:          documentTitle(d),
				Date:           date,
				CommonCategory: res.CommonCategory,
				Document:       &d,
				Source:         source,
			}
			if res.RecallCount > 0 {
				item.RecallStats = &KnowledgeRecallStats{Count: res.RecallCount}
			}
			return item, nil
		}
		if settings.IsControlAuthDenied(err) {
			return Item{}, err
		}
		if settings.IsControlErrorCode(err, http.StatusNotFound, "document_not_found") {
			return Item{}, fmt.Errorf("官方未提供可定位的文档出处")
		}
		return Item{}, err
	}
	for offset := 0; offset <= 100000; offset += 100 {
		var rows []Document
		if e := c.get(ctx, "/api/v1/datasets/"+url.PathEscape(dataset)+"/data?limit=100&offset="+strconv.Itoa(offset), &rows); e != nil {
			return Item{}, e
		}
		if e := c.rememberKnowledgeDays(rows); e != nil {
			return Item{}, e
		}
		for _, d := range rows {
			if d.Dataset != dataset {
				return Item{}, fmt.Errorf("official document dataset changed")
			}
			if d.ID == id {
				kind := "文档"
				if documentKnowledge(d) {
					kind = "知识"
				}
				return Item{ID: d.ID, Dataset: dataset, Kind: kind, Title: documentTitle(d), Date: documentDate(d), Document: &d, Source: documentSession(d)}, nil
			}
		}
		if len(rows) < 100 {
			break
		}
	}
	return Item{}, fmt.Errorf("官方未提供可定位的文档出处")
}

func documentDate(d Document) string {
	if documentKnowledge(d) {
		if day, ok := documentKnowledgeDay(d, time.Local); ok {
			return day
		}
		return ""
	}
	if d.Updated != "" {
		return d.Updated
	}
	return d.Created
}

func sessionTitle(question string) string {
	text := strings.TrimSpace(question)
	for _, sep := range []string{"\n", "。", "！", "？"} {
		if before, _, ok := strings.Cut(text, sep); ok {
			text = before
		}
	}
	chars := []rune(text)
	if len(chars) > 64 {
		return string(chars[:64]) + "…"
	}
	return text
}
