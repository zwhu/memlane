package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

type BrowseSelection struct {
	View   string `json:"view"`
	Memory string `json:"memory,omitempty"`
	Entity string `json:"entity,omitempty"`
}
type BrowsePreferences struct {
	Project    string                     `json:"project,omitempty"`
	Selections map[string]BrowseSelection `json:"selections"`
}
type BrowsingUI struct {
	Preferences                                                     BrowsePreferences
	Project, View, Entity, Relation, Search, Error, PreferenceError string
	Graph                                                           records.CogneeGraph
	Loading                                                         bool
	Sequence                                                        int64
	Cancel                                                          context.CancelFunc
}

func (a *App) configureBrowsing() {
	a.browsing.Preferences.Selections = map[string]BrowseSelection{}
	a.browsing.View = "memories"
	if a.guidanceRoot == "" {
		return
	}
	raw, err := os.ReadFile(filepath.Join(a.guidanceRoot, "browsing.json"))
	if os.IsNotExist(err) {
		return
	}
	if err != nil || len(raw) > 65536 || json.Unmarshal(raw, &a.browsing.Preferences) != nil {
		a.browsing.PreferenceError = "浏览位置读取失败，请重新选择项目。"
		return
	}
	if a.browsing.Preferences.Selections == nil {
		a.browsing.Preferences.Selections = map[string]BrowseSelection{}
	}
	if a.browsing.Preferences.Project != "" {
		a.navigate("项目", a.browsing.Preferences.Project)
	}
}
func (a *App) saveBrowsing() {
	s := &a.browsing
	if a.page != "项目" || a.scope == "" || a.guidanceRoot == "" {
		return
	}
	if s.Preferences.Selections == nil {
		s.Preferences.Selections = map[string]BrowseSelection{}
	}
	s.Preferences.Project = a.scope
	s.Preferences.Selections[a.scope] = BrowseSelection{View: s.View, Memory: a.selectedID, Entity: s.Entity}
	if err := serviceipc.WriteJSON(filepath.Join(a.guidanceRoot, "browsing.json"), s.Preferences); err != nil {
		s.PreferenceError = "浏览位置未能保存。"
	} else {
		s.PreferenceError = ""
	}
}
func (a *App) browseProjectChanged() {
	s := &a.browsing
	if s.Cancel != nil {
		s.Cancel()
	}
	s.Cancel = nil
	s.Sequence++
	s.Loading = false
	s.Graph = records.CogneeGraph{}
	s.Project = a.scope
	s.Relation = ""
	s.Search = ""
	s.Error = ""
	state := s.Preferences.Selections[a.scope]
	s.View, s.Entity = state.View, state.Entity
	if s.View != "entities" && s.View != "relations" {
		s.View = "memories"
	}
	if a.page == "项目" && state.Memory != "" {
		a.selectedID = state.Memory
		a.selectedItem = nil
	}
	a.refreshBrowsing()
}
func (a *App) refreshBrowsing() {
	s := &a.browsing
	if a.page != "项目" || a.scope == "" || s.Loading || a.libraryContext == nil || a.libraryContext.Err() != nil {
		return
	}
	project := a.scope
	s.Sequence++
	sequence := s.Sequence
	s.Loading = true
	ctx, cancel := context.WithTimeout(a.libraryContext, 15*time.Second)
	s.Cancel = cancel
	go func() {
		defer cancel()
		graph, err := a.library.CogneeGraph(ctx, records.CogneeGraphQuery{Project: project})
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			s := &a.browsing
			if sequence != s.Sequence || a.page != "项目" || a.scope != project {
				return
			}
			s.Loading, s.Cancel = false, nil
			if err != nil {
				s.Graph = records.CogneeGraph{}
				s.Error = "实体材料读取失败：" + err.Error()
				return
			}
			if graph.Project != project {
				s.Graph = records.CogneeGraph{}
				s.Error = "返回材料的项目不一致。"
				return
			}
			s.Graph, s.Error = graph, ""
		})
	}()
}
func (a *App) browseView(view string) {
	a.closeProjectOrganizer()
	a.browsing.View = view
	a.browsing.Relation = ""
	a.closeSourceReader()
	a.saveBrowsing()
	a.refreshBrowsing()
}
func (a *App) browseEntity(id string) { a.browsing.Entity = id; a.browseView("entities") }
func (a *App) browseMemory(id string) {
	a.closeProjectOrganizer()
	a.rememberDraft()
	a.selectedID = id
	a.selectedItem = nil
	a.detailMode = "view"
	a.editing = false
	a.recordKind = "knowledge"
	a.browsing.View = "memories"
	a.browsing.Relation = ""
	a.history = nil
	if a.onSelect != nil {
		a.onSelect(id)
	}
	a.saveBrowsing()
}
func (a *App) browseMemoryName(id string) string {
	if current := a.current(); current != nil && current.ID == id {
		return current.Title
	}
	for _, item := range a.items {
		if item.ID == id {
			return item.Title
		}
	}
	return "关联知识 · " + id[:min(8, len(id))]
}
func (a *App) entityMentions(id string) []records.CogneeMention {
	out := []records.CogneeMention{}
	for _, mention := range a.browsing.Graph.Mentions {
		if mention.Entity == id {
			out = append(out, mention)
		}
	}
	return out
}
func (a *App) memoryEntities(item MemoryItem) []records.CogneeEntity {
	ids := map[string]bool{}
	for _, mention := range a.browsing.Graph.Mentions {
		if mention.Memory == item.ID && mention.Revision == item.Revision {
			ids[mention.Entity] = true
		}
	}
	out := []records.CogneeEntity{}
	for _, entity := range a.browsing.Graph.Entities {
		if ids[entity.ID] {
			out = append(out, entity)
		}
	}
	return out
}

func (a *App) projectExplorer(c *ui.Context) {
	s := &a.browsing
	if s.View == "memories" {
		a.details(c)
		return
	}
	ui.Column(c).Key("project-explorer-header").Shrink(0).Padding(18, 24, 0, 24).Gap(10).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
		ui.Row(c).Gap(12).Children(func() {
			ui.Text(c, a.pageTitle()).FontSize(design.Heading).Bold()
			ui.Spacer(c)
			ui.Text(c, fmt.Sprintf("%d 个实体 · %d 条有依据关系", len(s.Graph.Entities), len(s.Graph.Relations))).FontSize(design.Small).TextColor(design.Muted)
		})
		ui.Text(c, "项目限定记忆与材料的归属；原始记录保留出处，知识保存可复用陈述。").FontSize(design.Small).TextColor(design.Muted)
		ui.Row(c).Gap(8).Children(func() {
			for _, tab := range []struct{ key, label string }{{"memories", "记忆"}, {"entities", "实体"}, {"relations", "关系图"}} {
				button := quietButton(c, tab.label).Key("browse-tab-"+tab.key).Padding(8, 12)
				if s.View == tab.key {
					button.Bold().BorderWidth(0, 0, 2, 0).BorderColor(design.Ink)
				}
				button.OnClick(func() { a.browseView(tab.key) })
			}
			ui.Spacer(c)
			quietButton(c, "官方接入与同步").Key("browse-cognee").OnClick(a.openCognee)
			quietButton(c, "刷新材料").Key("browse-refresh").Disabled(s.Loading).OnClick(a.refreshBrowsing)
		})
		if s.PreferenceError != "" {
			ui.Text(c, s.PreferenceError).FontSize(design.Small)
		}
	})
	if s.View == "memories" {
		a.details(c)
		return
	}
	if s.Error != "" {
		a.empty(c, "材料暂不可用", s.Error)
		return
	}
	if len(s.Graph.Entities) == 0 {
		text := "这个项目没有能关联到当前知识版本的实体。原始记录与知识仍可在记忆视图阅读。"
		if s.Loading {
			text = "正在读取项目授权范围内的实际实体与材料…"
		} else if s.Graph.State == "unconnected" {
			text = "这个项目尚未确认 Cognee 材料接入。可先阅读已有记忆；实体不会由客户端补造。"
		}
		a.empty(c, "项目实体", text)
		return
	}
	if s.View == "entities" {
		a.entityExplorer(c)
	} else {
		a.relationExplorer(c)
	}
}

func (a *App) entityExplorer(c *ui.Context) {
	s := &a.browsing
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(280).Shrink(0).BorderWidth(0, 1, 0, 0).BorderColor(design.Border).Children(func() {
			ui.TextInput(c.Key("browse-entity-search"), &s.Search).Placeholder("查找对象").Margin(18)
			ui.Scroll(c).Grow(1).Padding(18).Gap(10).Children(func() {
				for _, entity := range s.Graph.Entities {
					if s.Search != "" && !strings.Contains(strings.ToLower(entity.Name), strings.ToLower(s.Search)) {
						continue
					}
					button := quietButton(c, entity.Name).Key("browse-entity-" + entity.ID).Padding(10)
					if s.Entity == entity.ID {
						button.Background(design.ListSelected)
					}
					button.OnClick(func() { s.Entity = entity.ID; s.Relation = ""; a.saveBrowsing() })
				}
			})
		})
		ui.Scroll(c).Grow(1).MinWidth(0).Padding(24).Gap(18).Children(func() { a.entityDetail(c) })
	})
}
func (a *App) entityDetail(c *ui.Context) {
	s := &a.browsing
	var entity *records.CogneeEntity
	for i := range s.Graph.Entities {
		if s.Graph.Entities[i].ID == s.Entity {
			entity = &s.Graph.Entities[i]
			break
		}
	}
	if entity == nil {
		ui.Text(c, "选择对象，查看相关知识与原始材料。").TextColor(design.Muted)
		return
	}
	ui.Text(c, entity.Name).FontSize(design.Title).Bold()
	ui.Text(c, "Cognee 识别的对象 · 依据来自具体材料").FontSize(design.Small).TextColor(design.Muted)
	quietButton(c, "在关系图定位").Key("browse-entity-graph").OnClick(func() {
		mentions := a.entityMentions(entity.ID)
		current := a.current()
		matched := false
		for _, mention := range mentions {
			if current != nil && mention.Memory == current.ID && mention.Revision == current.Revision {
				matched = true
			}
		}
		if !matched && len(mentions) > 0 {
			a.browseMemory(mentions[0].Memory)
		}
		a.browseView("relations")
	})
	seen := map[string]bool{}
	for _, mention := range a.entityMentions(entity.ID) {
		if seen[mention.Memory] {
			continue
		}
		seen[mention.Memory] = true
		ui.Column(c).Gap(10).Padding(16).Background(design.Surface).Children(func() {
			ui.Text(c, a.browseMemoryName(mention.Memory)).Bold()
			ui.Text(c, mention.Quote).Selectable().LineHeight(1.7)
			ui.Text(c, fmt.Sprintf("当前知识 v%d · 材料块 %d", mention.Revision, chunkIndex(mention.ChunkIndex))).FontSize(design.Small).TextColor(design.Muted)
			quietButton(c, "在记忆视图打开").Key("browse-memory-" + mention.Memory).OnClick(func() { a.browseMemory(mention.Memory) })
		})
	}
	ui.Text(c, "先打开相关知识，即可从同一出处阅读完整原始记录。对象和图节点不计为新增知识。").FontSize(design.Small).TextColor(design.Muted)
}
func chunkIndex(index *int) int {
	if index == nil {
		return 0
	}
	return *index
}

func (a *App) relationExplorer(c *ui.Context) {
	s := &a.browsing
	item := a.current()
	if item == nil || item.Dataset != a.scope {
		a.empty(c, "关系图", "先在记忆中选择一条当前知识，再查看它的实际局部关系。")
		return
	}
	entities := a.memoryEntities(*item)
	if len(entities) == 0 {
		a.empty(c, "这条知识没有实体关联", "当前版本没有有效材料关联；保留知识正文，不补造图节点。")
		return
	}
	w, _ := c.Size()
	canvasWidth := max(float32(360), w-design.Sidebar-320)
	shown := append([]records.CogneeEntity(nil), entities[:min(8, len(entities))]...)
	// The local graph stays bounded, but locating an entity must show that
	// actual selected object even when it lies beyond the first eight.
	for i, entity := range entities {
		if entity.ID == s.Entity && i >= len(shown) {
			shown[len(shown)-1] = entity
			break
		}
	}
	points := map[string][2]float32{}
	for i, entity := range shown {
		angle := float64(i) * 2 * math.Pi / float64(len(shown))
		points[entity.ID] = [2]float32{canvasWidth/2 + float32(math.Cos(angle))*(canvasWidth/2-92), 310 + float32(math.Sin(angle))*190}
	}
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Scroll(c).Grow(1).MinWidth(0).Children(func() {
			ui.Column(c).Height(600).Width(canvasWidth).Background(ui.Hex("#fafcff")).Draw(func(p *ui.Painter, r ui.Rect) {
				for _, entity := range shown {
					at := points[entity.ID]
					x, y := r.X+at[0], r.Y+at[1]
					for step := float32(0); step < 1; step += 0.07 {
						end := min(step+0.035, float32(1))
						p.Line(r.X+canvasWidth/2+(x-r.X-canvasWidth/2)*step, r.Y+310+(y-r.Y-310)*step, r.X+canvasWidth/2+(x-r.X-canvasWidth/2)*end, r.Y+310+(y-r.Y-310)*end, 1, design.Heat[3])
					}
				}
				for _, relation := range s.Graph.Relations {
					from, ok := points[relation.Source]
					to, ok2 := points[relation.Target]
					if !ok || !ok2 {
						continue
					}
					matches := false
					for _, source := range relation.Sources {
						if source.Memory == item.ID && source.Revision == item.Revision {
							matches = true
						}
					}
					if !matches {
						continue
					}
					p.Line(r.X+from[0], r.Y+from[1], r.X+to[0], r.Y+to[1], 1.5, design.Heat[4])
					label := []rune(relation.Label)
					p.Text(r.X+(from[0]+to[0])/2, r.Y+(from[1]+to[1])/2, string(label[:min(22, len(label))]), 10, design.Muted)
				}
			}).Children(func() {
				ui.Button(c, a.browseMemoryName(item.ID)).Key("browse-graph-memory").Absolute().Left(canvasWidth/2 - 85).Top(280).Width(170).Height(60).Background(design.Heat[1]).OnClick(func() { a.browseView("memories") })
				for _, entity := range shown {
					at := points[entity.ID]
					button := ui.Button(c, entity.Name).Key("browse-graph-entity-" + entity.ID).Absolute().Left(at[0] - 72).Top(at[1] - 24).Width(144).Height(48).Radius(20).FontSize(11)
					if s.Entity == entity.ID {
						button.BorderColor(design.Heat[4]).BorderWidth(2)
					}
					button.OnClick(func() { s.Entity = entity.ID; s.Relation = ""; a.saveBrowsing() })
				}
				ui.Text(c, "虚线：材料提及对象；实线：官方有材料支持的关系").Absolute().Left(20).Top(20).FontSize(design.Small).TextColor(design.Muted)
				if len(entities) > len(shown) {
					ui.Text(c, fmt.Sprintf("局部展示 %d / %d 个对象；全部对象可在实体视图查看。", len(shown), len(entities))).Absolute().Left(20).Top(565).FontSize(design.Small).TextColor(design.Muted)
				}
			})
		})
		ui.Scroll(c).Width(320).Shrink(0).Padding(20).Gap(14).BorderWidth(0, 0, 0, 1).BorderColor(design.Border).Children(func() {
			if s.Entity != "" {
				a.entityDetail(c)
			} else {
				ui.Text(c, item.Title).FontSize(design.Title).Bold()
				quietButton(c, "返回记忆正文").Key("browse-graph-return").OnClick(func() { a.browseView("memories") })
			}
			ui.Divider(c)
			ui.Text(c, "有出处的实际关系").Bold()
			for _, relation := range s.Graph.Relations {
				sources := []records.CogneeGraphSource{}
				for _, source := range relation.Sources {
					if source.Memory == item.ID && source.Revision == item.Revision {
						sources = append(sources, source)
					}
				}
				if len(sources) == 0 {
					continue
				}
				quietButton(c, relation.Label).Key("browse-relation-" + relation.ID).OnClick(func() { s.Relation = relation.ID })
				if s.Relation == relation.ID {
					for _, source := range sources {
						ui.Text(c, source.Quote).Selectable().LineHeight(1.7)
						quietButton(c, "查看来源知识与原始记录").Key("browse-relation-source-" + source.Chunk).OnClick(func() { a.browseMemory(source.Memory) })
					}
				}
			}
			if s.Graph.UnsupportedRelations > 0 {
				ui.Text(c, fmt.Sprintf("另有 %d 条关系缺少可追溯依据，未展示。", s.Graph.UnsupportedRelations)).FontSize(design.Small).TextColor(design.Muted)
			}
		})
	})
}
