package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestDesignV3DateKeepsCompleteDay(t *testing.T) {
	a := overviewFixture(t)
	a.overviewState.Day = "2026-11-16"
	a.overviewState.Snapshot.Knowledge = nil
	for n := 0; n < 80; n++ {
		d := Document{ID: fmt.Sprint(n), Dataset: "0", Created: "2026-11-16T12:00:00Z"}
		a.overviewState.Snapshot.Knowledge = append(a.overviewState.Snapshot.Knowledge, Item{ID: d.ID, Dataset: d.Dataset, Document: &d, Title: "date sample"})
	}
	if rows := a.overviewKnowledgeRows(time.Now()); len(rows) != 80 {
		t.Fatalf("80 dated memories reduced to %d", len(rows))
	}
}

func TestDesignV3RetryOnlyFailedLaneIncludingEmptySuccess(t *testing.T) {
	var lexical, semantic atomic.Int32
	var fail atomic.Bool
	fail.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]any
		json.NewDecoder(r.Body).Decode(&input)
		hits := []map[string]any{}
		if sem, _ := input["semantic"].(bool); !sem {
			lexical.Add(1)
		} else {
			semantic.Add(1)
			if fail.Load() {
				http.Error(w, "fixture unavailable", http.StatusServiceUnavailable)
				return
			}
			hits = append(hits, map[string]any{"dataset_id": "p", "document_id": "recovered", "snippet": "semantic result"})
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "items": []any{}, "semantic_hits": hits})
	}))
	defer server.Close()
	c := Client{Config: Config{ProjectControl: readingControlConfig(t, server.URL)}}
	first := c.searchLanes(context.Background(), []Project{{Dataset: "p"}}, "query", searchLaneResult{})
	if !first.KeywordOK || first.SemanticOK || first.message() != "暂时只能按关键词查找" {
		t.Fatal("incorrect partial result", first)
	}
	fail.Store(false)
	second := c.searchLanes(context.Background(), []Project{{Dataset: "p"}}, "query", first)
	if second.err() != nil || len(second.rows("query")) != 1 || lexical.Load() != 1 || semantic.Load() != 2 {
		t.Fatalf("retry repeated the successful empty lane: %+v, calls %d/%d", second, lexical.Load(), semantic.Load())
	}
}

func TestDesignV3DateExpansionAndReset(t *testing.T) {
	a := overviewFixture(t)
	v := &a.overviewState
	v.Day = "2026-11-16"
	v.Snapshot.Knowledge = nil
	for n := 0; n < 80; n++ {
		d := Document{ID: fmt.Sprint(n), Dataset: "0", Created: "2026-11-16T12:00:00Z"}
		v.Snapshot.Knowledge = append(v.Snapshot.Knowledge, Item{ID: d.ID, Dataset: d.Dataset, Document: &d, Title: fmt.Sprintf("Dated sample %d", n)})
	}
	tt := ui.NewTester(a.overviewHome, 980, 758)
	if !tt.HasText("已显示 20 条，共 80 条") || len(a.overviewDisplayedRows(time.Now())) != 20 {
		t.Fatal("initial range not clear")
	}
	clickProjectUI(t, tt, "显示全部")
	if !tt.HasText("已显示 80 条，共 80 条") || len(a.overviewDisplayedRows(time.Now())) != 80 {
		t.Fatal("show-all did not expose the full date")
	}
	clickProjectUI(t, tt, "返回最近记忆")
	if v.Day != "" || v.ShowAllForDay || len(a.overviewDisplayedRows(time.Now())) != 20 {
		t.Fatal("recent range did not reset")
	}
}

func TestDesignV3SearchRetainsSuccessfulLane(t *testing.T) {
	for _, failed := range []string{"CHUNKS_LEXICAL", "CHUNKS"} {
		t.Run(failed, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input map[string]any
				json.NewDecoder(r.Body).Decode(&input)
				sem, _ := input["semantic"].(bool)
				if (failed == "CHUNKS_LEXICAL" && !sem) || (failed == "CHUNKS" && sem) {
					http.Error(w, "fixture unavailable", http.StatusServiceUnavailable)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{
					"ok":            true,
					"items":         []any{},
					"semantic_hits": []map[string]any{{"dataset_id": "p", "document_id": "official-doc", "title": "kept", "snippet": "库存扣减使用原子更新"}},
				})
			}))
			defer server.Close()
			rows, err := (Client{Config: Config{ProjectControl: readingControlConfig(t, server.URL)}}).searchBoth(context.Background(), []Project{{Dataset: "p"}}, "库存")
			if err == nil || len(rows) != 1 || rows[0].SourceDocument != "official-doc" {
				t.Fatalf("lost successful lane or its official identity: %+v %v", rows, err)
			}
		})
	}
}

func TestDesignV3UnselectedCommonHasNoFirstMemoryAction(t *testing.T) {
	a := commonUIFixture()
	s := a.state()
	s.Selected = nil
	tt := ui.NewTester(func(c *ui.Context) { a.commonKnowledgeReader(c, s) }, 800, 700)
	if tt.HasText("新增第一条记忆") || tt.HasText("添加记忆") {
		t.Fatal("existing content with no selection masquerades as an empty library")
	}
}

func TestDesignV3SearchErrorIsNotEmptyResult(t *testing.T) {
	a := commonUIFixture()
	s := &a.searchUI
	s.Open, s.Loaded = true, false
	s.Query, s.Error = "query", "搜索失败"
	s.Submitted = a.searchSignature()
	tt := ui.NewTester(a.searchDialog, 1200, 820)
	if tt.HasText("没有匹配内容") || tt.HasText("没有找到结果") {
		t.Fatal("search failure rendered as a completed zero result")
	}
}
