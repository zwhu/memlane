package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

type BrowseSelection struct {
	TypeFilter  string  `json:"type_filter,omitempty"`
	Dataset     string  `json:"dataset,omitempty"`
	GraphX      float32 `json:"graph_x,omitempty"`
	View        string  `json:"view"`
	Memory      string  `json:"memory,omitempty"`
	Entity      string  `json:"entity,omitempty"`
	Relation    string  `json:"relation,omitempty"`
	Search      string  `json:"search,omitempty"`
	EntityListY float32 `json:"entity_list_y,omitempty"`
	EntityBodyY float32 `json:"entity_body_y,omitempty"`
	GraphY      float32 `json:"graph_y,omitempty"`
	GraphBodyY  float32 `json:"graph_body_y,omitempty"`
}
type BrowsePreferences struct {
	Project    string                     `json:"project,omitempty"`
	Selections map[string]BrowseSelection `json:"selections"`
}
type BrowseKnowledgeContext struct {
	Project, Memory, Kind string
	BodyY                 float32
}
type BrowseExplorerContext struct {
	Project, View, Entity, Relation, Memory, Target string
	Revision                                        int64
}

type GraphMaterialUI struct {
	Legacy                                                                 bool
	Submission                                                             *GraphSubmissionUI
	Project, Endpoint, Dataset, Credential, Session, ConnectionID, Message string
	Bindings                                                               []records.CogneeBinding
	Connections                                                            []records.Connection
	Binding                                                                *records.CogneeBinding
	Receipt                                                                *records.CogneeSyncReceipt
	Open, Loading, Busy, Loaded                                            bool
	Sequence                                                               uint64
	Cancel                                                                 context.CancelFunc
	Scroll                                                                 ui.ScrollState
}

type BrowsingUI struct {
	EntityTypes                                                     *entityTypesReadView
	Submissions                                                     map[string]*GraphSubmissionUI
	Dataset                                                         string
	RelationsOpen                                                   bool
	Material                                                        *GraphMaterialUI
	TypeFilter                                                      string
	Knowledge                                                       *BrowseKnowledgeContext
	Explorer                                                        *BrowseExplorerContext
	EntityList, EntityBody, GraphScroll, GraphBody                  ui.ScrollState
	Preferences                                                     BrowsePreferences
	Project, View, Entity, Relation, Search, Error, PreferenceError string
	Graph                                                           records.CogneeGraph
	Loading                                                         bool
	Sequence                                                        int64
	Cancel                                                          context.CancelFunc
}

func (a *App) configureBrowsing() {
	a.retireGraphSubmissions()
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
		// Restoring the last reading route is startup work, not a user
		// navigation. A later durable open preview may take precedence.
		materialTouched := a.materialPersistence != nil && a.materialPersistence.Touched
		experienceTouched := a.experiencePersistence != nil && a.experiencePersistence.Touched
		a.navigate("项目", a.browsing.Preferences.Project)
		if a.materialPersistence != nil {
			a.materialPersistence.Touched = materialTouched
		}
		if a.experiencePersistence != nil {
			a.experiencePersistence.Touched = experienceTouched
		}
	}
}
func (a *App) saveBrowsing() {
	s := &a.browsing
	if a.page != "项目" || a.scope == "" {
		return
	}
	if s.Preferences.Selections == nil {
		s.Preferences.Selections = map[string]BrowseSelection{}
	}
	s.Preferences.Project = a.scope
	s.Preferences.Selections[a.scope] = BrowseSelection{TypeFilter: s.TypeFilter, Dataset: s.Dataset, GraphX: s.GraphScroll.X, View: s.View, Memory: a.selectedID, Entity: s.Entity, Relation: s.Relation, Search: s.Search, EntityListY: s.EntityList.Y, EntityBodyY: s.EntityBody.Y, GraphY: s.GraphScroll.Y, GraphBodyY: s.GraphBody.Y}
	if a.guidanceRoot == "" {
		return
	}
	if err := serviceipc.WriteJSON(filepath.Join(a.guidanceRoot, "browsing.json"), s.Preferences); err != nil {
		s.PreferenceError = "浏览位置未能保存。"
	} else {
		s.PreferenceError = ""
	}
}
func (a *App) browseProjectChanged() {
	s := &a.browsing
	if s.Material != nil && s.Material.Project != a.scope {
		if s.Material.Cancel != nil {
			s.Material.Cancel()
		}
		s.Material.Credential = ""
		s.Material.Sequence++
		s.Material = nil
	}
	if s.Cancel != nil {
		s.Cancel()
	}
	s.Cancel = nil
	s.Sequence++
	s.Loading = false
	s.Graph = records.CogneeGraph{}
	s.Project = a.scope
	s.Knowledge, s.Explorer = nil, nil
	s.Relation = ""
	s.Search = ""
	s.TypeFilter = ""
	s.Error = ""
	state := s.Preferences.Selections[a.scope]
	s.Dataset = state.Dataset
	s.GraphScroll.X = state.GraphX
	s.View, s.Entity = state.View, state.Entity
	s.Relation, s.Search = state.Relation, state.Search
	s.TypeFilter = state.TypeFilter
	s.EntityList.Y, s.EntityBody.Y, s.GraphScroll.Y, s.GraphBody.Y = state.EntityListY, state.EntityBodyY, state.GraphY, state.GraphBodyY
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
	project, dataset := a.scope, s.Dataset
	s.Sequence++
	sequence := s.Sequence
	s.Loading = true
	ctx, cancel := context.WithTimeout(a.libraryContext, 15*time.Second)
	s.Cancel = cancel
	life, client, deliver, epoch := a.libraryContext, a.library, a.dispatch, a.libraryEpoch
	go func() {
		defer cancel()
		graph, err := client.CogneeGraph(ctx, records.CogneeGraphQuery{Project: project, Dataset: dataset})
		if life.Err() != nil {
			return
		}
		deliver(func() {
			s := &a.browsing
			if a.libraryEpoch != epoch || life.Err() != nil || sequence != s.Sequence || a.page != "项目" || a.scope != project || s.Dataset != dataset {
				return
			}
			s.Loading, s.Cancel = false, nil
			if err != nil {
				s.Graph = records.CogneeGraph{}
				s.Error = "尚未取得当前项目的实体材料，请核对服务与项目授权后重试。"
				return
			}
			if graph.Project != project || dataset != "" && graph.Dataset != dataset && graph.State != "unconnected" {
				s.Graph = records.CogneeGraph{}
				s.Error = "返回材料的项目不一致。"
				return
			}
			s.Graph, s.Error = graph, ""
			found := false
			for _, entity := range graph.Entities {
				if entity.ID == s.Entity {
					found = true
					break
				}
			}
			if !found {
				s.Entity = ""
				if len(graph.Entities) > 0 {
					s.Entity = graph.Entities[0].ID
				}
				s.Relation = ""
			}
		})
	}()
}
func (a *App) browseView(view string) {
	if view == "memories" && a.browsing.Knowledge != nil && a.browsing.Knowledge.Project == a.scope {
		a.returnToBrowsingKnowledge()
		return
	}
	if a.browsing.View == "memories" && view != "memories" && a.page == "项目" && a.scope != "" {
		a.rememberDraft()
		a.browsing.Knowledge = &BrowseKnowledgeContext{Project: a.scope, Memory: a.selectedID, Kind: a.recordKind, BodyY: a.detailScroll.Y}
		a.editing, a.detailMode = false, "view"
	}
	a.closeProjectOrganizer()
	a.browsing.View = view
	a.browsing.Relation = ""
	a.closeSourceReader()
	a.saveBrowsing()
	a.refreshBrowsing()
}
func (a *App) browseEntity(id string) {
	if a.page != "项目" || a.scope == "" {
		return
	}
	a.rememberDraft()
	if a.browsing.View == "memories" {
		a.browsing.Knowledge = &BrowseKnowledgeContext{Project: a.scope, Memory: a.selectedID, Kind: a.recordKind, BodyY: a.detailScroll.Y}
	}
	a.browsing.Entity = id
	a.browseView("entities")
}
func (a *App) browseMemory(id string) {
	s := &a.browsing
	kind := "knowledge"
	if s.View == "entities" || s.View == "relations" {
		if a.page != "项目" || s.Graph.Project != a.scope {
			s.Error = "关联材料的项目已变化，请刷新后重试。"
			return
		}
		revision := int64(0)
		for _, relation := range s.Graph.Relations {
			if relation.ID != s.Relation {
				continue
			}
			for _, source := range relation.Sources {
				if source.Memory == id {
					revision = source.Revision
					break
				}
			}
		}
		for _, mention := range s.Graph.Mentions {
			if revision == 0 && mention.Memory == id && (s.Entity == "" || mention.Entity == s.Entity) {
				revision = mention.Revision
				break
			}
		}
		kind = a.graphMemoryKind(id)
		if revision <= 0 {
			s.Error = "这条记录的关联依据已变化，请刷新材料后重试。"
			return
		}
		s.Explorer = &BrowseExplorerContext{Project: a.scope, View: s.View, Entity: s.Entity, Relation: s.Relation, Memory: a.selectedID, Target: id, Revision: revision}
	}
	a.closeSourceReader()
	a.closeKnowledgeHistory()
	a.detailScroll.Y = 0
	a.closeProjectOrganizer()
	a.rememberDraft()
	a.selectedID = id
	a.selectedItem = nil
	a.detailMode = "view"
	a.editing = false
	a.recordKind = kind
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
	label := "关联知识"
	if a.graphMemoryKind(id) == "raw" {
		label = "关联原始记录"
	}
	return label + " · " + id[:min(8, len(id))]
}

func (a *App) graphMemoryKind(id string) string {
	if a.browsing.Graph.Project != a.scope {
		return "knowledge"
	}
	for _, mention := range a.browsing.Graph.Mentions {
		if mention.Memory == id && mention.Origin == "raw" {
			return "raw"
		}
	}
	for _, relation := range a.browsing.Graph.Relations {
		for _, source := range relation.Sources {
			if source.Memory == id && source.Origin == "raw" {
				return "raw"
			}
		}
	}
	return "knowledge" // Legacy graph projection admits knowledge only.
}
func (a *App) entityMentions(id string) []records.CogneeMention {
	if a.browsing.Graph.Project != a.scope {
		return nil
	}
	out := []records.CogneeMention{}
	for _, mention := range a.browsing.Graph.Mentions {
		if mention.Entity == id && mention.Memory != "" && mention.Revision > 0 {
			out = append(out, mention)
		}
	}
	return out
}
func (a *App) memoryEntities(item MemoryItem) []records.CogneeEntity {
	if a.browsing.Graph.Project != a.scope || item.Dataset != a.scope {
		return nil
	}
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
	if s.View == "memories" || a.projectSettings.Open && a.projectSettings.Owner.ID == a.scope || a.projectOrganizer.Open && a.projectOrganizer.Project == a.scope {
		a.details(c)
		return
	}
	width, _ := c.Size()
	listWidth := float32(316)
	if width < 1000 {
		listWidth = 216
	}
	ui.Row(c).Key("project-entity-panes").Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Key("entity-list-pane").Width(listWidth).Shrink(0).BorderWidth(0, 1, 0, 0).BorderColor(design.Border).Children(func() { a.entityList(c) })
		ui.Column(c).Key("entity-detail-pane").Grow(1).MinWidth(0).Children(func() {
			ui.Row(c).Height(55).Shrink(0).Padding(0, 24).Gap(10).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
				title := "实体详情"
				if s.View == "relations" {
					title = "项目关系图"
				}
				if s.Material != nil && s.Material.Open {
					title = "材料接入与同步"
				}
				ui.Text(c, title).FontSize(12)
				ui.Spacer(c)
				if s.Material != nil && s.Material.Open {
					quietButton(c, "返回实体浏览").Key("graph-material-close").OnClick(func() { s.Material.Open = false })
				} else {
					quietButton(c, "返回知识").Key("browse-return-knowledge").OnClick(a.leaveProjectExplorer)
				}
			})
			if s.Material != nil && s.Material.Open {
				a.graphMaterialPanel(c)
			} else {
				body := &s.EntityBody
				if s.View == "relations" {
					body = &s.GraphBody
				}
				ui.Scroll(c).Key("entity-detail-scroll").Grow(1).MinHeight(0).TrackScroll(body).Padding(28, 24).Gap(20).Children(func() {
					if s.PreferenceError != "" {
						ui.Text(c, s.PreferenceError).FontSize(11).TextColor(design.Muted)
					}
					if a.runtimeStatus.Phase == "stopped" {
						a.entityReadState(c, "服务已主动停止", "实体列表暂不可读取。需要时明确启动服务，再核验当前项目的对象与关联。", false)
						return
					}
					if s.Error != "" {
						a.entityReadState(c, "实体读取失败", "尚未取得当前项目的实体列表，请核对服务与项目授权后重试。", true)
						return
					}
					if s.Loading && len(s.Graph.Entities) == 0 {
						a.entityReadState(c, "正在读取项目实体", "正在核对当前项目的实际材料与版本。", false)
						return
					}
					if s.Graph.Project != a.scope {
						a.entityReadState(c, "材料项目不一致", "当前项目尚未取得可核验材料，不展示上一个项目的对象。", true)
						return
					}
					if len(s.Graph.Entities) == 0 {
						text := "已有知识可继续阅读；只有带可核验出处的对象与关联才会显示。"
						if s.Graph.State == "unconnected" {
							text = "这个项目尚未绑定可核验的Cognee材料来源。可以接入已有材料；日常知识仍可正常阅读。"
						}
						a.entityReadState(c, "当前项目暂无关联对象", text, false)
						return
					}
					if s.View == "relations" {
						a.relationExplorer(c)
					} else {
						a.entityDetail(c)
					}
				})
				ui.Row(c).Key("entity-footer").Shrink(0).Padding(14, 24).Gap(10).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
					ui.Text(c, a.explorerProjectLabel()+" · 当前项目上下文").FontSize(10).TextColor(design.Muted).Grow(1).MinWidth(0).SingleLine()
					label, view := "查看关系图", "relations"
					if s.View == "relations" {
						label, view = "查看实体", "entities"
					}
					quietButton(c, label).Key("browse-footer-view").OnClick(func() { a.browseView(view) })
				})
			}
		})
	})
}
func (a *App) leaveProjectExplorer() {
	if a.browsing.Knowledge != nil && a.browsing.Knowledge.Project == a.scope {
		a.returnToBrowsingKnowledge()
		return
	}
	a.browseView("memories")
}
func (a *App) entityReadState(c *ui.Context, title, text string, retry bool) {
	ui.Text(c, title).FontSize(design.Title).Bold()
	ui.Text(c, text).FontSize(12).LineHeight(1.8).TextColor(design.Muted)
	if a.runtimeStatus.Phase == "stopped" {
		quietButton(c, "查看服务状态").Key("browse-service-status").AlignSelf(ui.Start).OnClick(func() {
			a.navigate("设置", "")
			a.settingsSection, a.maintenanceSection = "environment", "service"
		})
		return
	}
	if retry {
		quietButton(c, "重试实体读取").Key("browse-retry").AlignSelf(ui.Start).Disabled(a.browsing.Loading).OnClick(a.refreshBrowsing)
	}
	quietButton(c, "材料接入与同步").Key("browse-cognee").AlignSelf(ui.Start).OnClick(a.openGraphMaterial)
}

func (a *App) explorerProjectLabel() string {
	if name := strings.TrimSpace(a.scopeLabel(a.scope)); name != "" {
		return name
	}
	for _, project := range a.projects {
		if project.ID == a.scope && strings.TrimSpace(project.Name) != "" {
			return project.Name
		}
	}
	return "当前项目（名称待核验）"
}

func (a *App) entityCountLabel() string {
	s := &a.browsing
	if a.runtimeStatus.Phase == "stopped" {
		return "暂不可读"
	}
	if s.Loading {
		return "读取中"
	}
	if s.Error != "" || s.Graph.Project != a.scope {
		return "未核验"
	}
	if s.Graph.State == "unconnected" {
		return "未接入"
	}
	return fmt.Sprintf("%d 个", len(s.Graph.Entities))
}
func (a *App) entityList(c *ui.Context) {
	s := &a.browsing
	ui.Column(c).Shrink(0).Padding(20, 18).Gap(16).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "项目实体").FontSize(12)
			ui.Spacer(c)
			ui.Text(c, a.entityCountLabel()).FontSize(11).TextColor(design.Muted)
		})
		ui.Row(c).Gap(10).Children(func() {
			for _, tab := range []struct{ key, label string }{{"entities", "实体"}, {"relations", "关系图"}} {
				button := quietButton(c, tab.label).Key("browse-tab-" + tab.key)
				if s.View == tab.key {
					button.Bold()
				}
				button.OnClick(func() { a.browseView(tab.key) })
			}
			ui.Spacer(c)
		})
		ui.Text(c, "对象类型").FontSize(10).TextColor(design.Muted)
		if s.TypeFilter == "" {
			s.TypeFilter = "不限类型"
		}
		ui.Select(c.Key("entity-type"), &s.TypeFilter, a.entityTypeOptions()).Label("对象类型").Disabled(len(a.entityTypeOptions()) == 1).OnChange(func() { s.EntityList.Y = 0; a.saveBrowsing() })
		ui.TextInput(c.Key("browse-entity-search"), &s.Search).Label("搜索项目实体").Placeholder("查找对象").OnChange(func() { s.EntityList.Y = 0; a.saveBrowsing() })
	})
	ui.Scroll(c).Key("entity-list-scroll").Grow(1).MinHeight(0).TrackScroll(&s.EntityList).Padding(0, 12).Gap(6).Children(func() {
		if a.runtimeStatus.Phase == "stopped" {
			ui.Text(c, "服务已停止，等待明确启动").FontSize(11).TextColor(design.Muted)
			return
		}
		if s.Error != "" {
			ui.Text(c, "实体读取失败").FontSize(11).TextColor(design.Muted)
			return
		}
		if s.Loading && len(s.Graph.Entities) == 0 {
			ui.Text(c, "正在读取当前项目…").FontSize(11).TextColor(design.Muted)
			return
		}
		if s.Graph.Project != a.scope && len(s.Graph.Entities) > 0 {
			ui.Text(c, "当前项目材料未核验").FontSize(11).TextColor(design.Muted)
			return
		}
		shown := 0
		for _, entity := range s.Graph.Entities {
			if !entityMatchesType(entity, s.TypeFilter) {
				continue
			}
			query := strings.ToLower(strings.TrimSpace(s.Search))
			if query != "" && !strings.Contains(strings.ToLower(entity.Name+" "+entity.Description), query) {
				continue
			}
			shown++
			e := entity
			button := ui.Button(c, "").Label(entity.Name).Key("browse-entity-"+entity.ID).Padding(14, 12).BorderWidth(0).Radius(8).Justify(ui.Start).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(8).Children(func() {
					ui.Text(c, e.Name).FontSize(12).Bold().MaxLines(2)
					if e.Description != "" {
						ui.Text(c, e.Description).FontSize(11).TextColor(design.Muted).MaxLines(3)
					}
					ui.Row(c).Gap(8).Children(func() {
						ui.Text(c, entityTypeLabel(e, "实体")).SingleLine().Grow(1).MinWidth(0).FontSize(10).TextColor(design.Muted)
						ui.Spacer(c)
						seen, originals := map[string]bool{}, map[string]bool{}
						for _, m := range a.entityMentions(e.ID) {
							if m.Origin == "raw" {
								originals[m.Memory] = true
							} else {
								seen[m.Memory] = true
							}
						}
						count := fmt.Sprintf("%d 条知识", len(seen))
						if len(originals) > 0 {
							count += fmt.Sprintf(" · %d 条原始记录", len(originals))
						}
						ui.Text(c, count).SingleLine().Shrink(0).FontSize(10).TextColor(design.Muted)
					})
				})
			})
			if s.Entity == e.ID {
				button.Background(design.ListSelected)
			} else {
				button.Background(ui.Transparent)
			}
			button.OnClick(func() { s.Entity = e.ID; s.Relation = ""; s.EntityBody.Y = 0; a.saveBrowsing() })
		}
		if shown == 0 {
			title := "还没有可展示的实体"
			text := "有依据的对象和关联会在这里显示。"
			if strings.TrimSpace(s.Search) != "" || s.TypeFilter != "" && s.TypeFilter != "不限类型" {
				title = "没有匹配的对象"
				text = "调整类型或查找文字不会清除已有项目实体。"
			}
			ui.Text(c, title).FontSize(12).Bold()
			ui.Text(c, text).FontSize(11).LineHeight(1.7).TextColor(design.Muted)
		}
	})
	ui.Row(c).Shrink(0).Padding(12, 18).Gap(8).Children(func() {
		quietButton(c, "刷新").Key("browse-refresh").Disabled(s.Loading).OnClick(a.refreshBrowsing)
		quietButton(c, "材料接入与同步").Key("browse-material-settings").OnClick(a.openGraphMaterial)
	})
}
func (a *App) entityExplorer(c *ui.Context) { a.projectExplorer(c) }
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
	if a.entityTypesReadout(c, *entity) {
		return
	}
	a.entityTypesSummary(c, *entity)
	ui.Text(c, entity.Name).FontSize(design.Title).Bold()
	if entity.Description != "" {
		ui.Text(c, entity.Description).FontSize(12).LineHeight(1.8).TextColor(design.Muted)
	}
	ui.Column(c).Padding(14, 0).Gap(14).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		label := "关联知识"
		for _, m := range a.entityMentions(entity.ID) {
			if m.Origin == "raw" {
				label = "关联知识与原始记录"
				break
			}
		}
		ui.Text(c, label).FontSize(12).Bold()
		seen := map[string]bool{}
		for _, mention := range a.entityMentions(entity.ID) {
			if seen[mention.Memory] {
				continue
			}
			seen[mention.Memory] = true
			m := mention
			kind, action := "知识", "阅读知识"
			if m.Origin == "raw" {
				kind, action = "原始记录", "阅读原始记录"
			}
			ui.Column(c).Padding(12, 0).Gap(8).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
				ui.Row(c).Gap(10).Children(func() {
					ui.Text(c, a.browseMemoryName(m.Memory)).FontSize(12).Bold().Grow(1).MinWidth(0)
					quietButton(c, action).Label("在记忆视图打开").Key("browse-memory-" + m.Memory).OnClick(func() { a.browseMemory(m.Memory) })
				})
				ui.Text(c, fmt.Sprintf("%s · 引用%s v%d", a.explorerProjectLabel(), kind, m.Revision)).FontSize(10).TextColor(design.Muted)
				if m.Quote != "" {
					ui.Text(c, m.Quote).FontSize(11).LineHeight(1.7).TextColor(design.Muted)
				}
			})
		}
		if len(seen) == 0 {
			ui.Text(c, "当前没有可核验的关联知识。").FontSize(11).TextColor(design.Muted)
		}
	})
	ui.Column(c).Padding(14, 0).Gap(12).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		ui.Text(c, "关系与来源").FontSize(12).Bold()
		ui.Text(c, "与对象有关的知识，保留各自的原始出处。对象本身不代表新的事实证据。").FontSize(11).LineHeight(1.8).TextColor(design.Muted)
		quietButton(c, "在关系图中查看").Key("browse-entity-graph").AlignSelf(ui.Start).OnClick(func() { a.browseView("relations") })
	})
}
func chunkIndex(index *int) int {
	if index == nil {
		return 0
	}
	return *index
}

type projectGraphNode struct {
	id, label   string
	entity, raw bool
	x, y        float32
}

func (a *App) projectGraphNodes() ([]projectGraphNode, map[string]records.CogneeMention) {
	s := &a.browsing
	if s.Graph.Project != a.scope {
		return nil, nil
	}
	entities := s.Graph.Entities
	if len(entities) > 12 {
		entities = append([]records.CogneeEntity(nil), entities[:12]...)
		for _, e := range s.Graph.Entities[12:] {
			if e.ID == s.Entity {
				entities[len(entities)-1] = e
				break
			}
		}
	}
	shown := map[string]bool{}
	nodes := []projectGraphNode{}
	memories := map[string]records.CogneeMention{}
	for i, e := range entities {
		shown[e.ID] = true
		nodes = append(nodes, projectGraphNode{id: e.ID, label: e.Name, entity: true, x: 140, y: float32(70 + i*100)})
	}
	for _, m := range s.Graph.Mentions {
		if !shown[m.Entity] || m.Memory == "" || m.Revision <= 0 {
			continue
		}
		if _, ok := memories[m.Memory]; !ok && len(memories) < 16 {
			memories[m.Memory] = m
		}
	}
	ids := []string{}
	for id := range memories {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for i, id := range ids {
		nodes = append(nodes, projectGraphNode{id: id, label: a.browseMemoryName(id), raw: memories[id].Origin == "raw", x: 450, y: float32(70 + i*100)})
	}
	return nodes, memories
}
func (a *App) relationExplorer(c *ui.Context) {
	s := &a.browsing
	nodes, memories := a.projectGraphNodes()
	if len(nodes) == 0 || len(memories) == 0 {
		a.entityReadState(c, "当前没有可展示的关系", "不为填满画布补造关联；知识与原始出处仍可分别阅读。", false)
		return
	}
	ui.Text(c, "项目内关联").FontSize(10).TextColor(design.Muted)
	ui.Text(c, "看清知识与对象的关系").FontSize(design.Title).Bold()
	ui.Text(c, "选择节点，可以查看对应对象或阅读知识。").FontSize(12).TextColor(design.Muted)
	ui.Text(c, "圆角节点：实体 · 浅色方形：知识 · 白色方形：原始记录 · 曲线：材料提及 · 箭头：有材料支持的对象关系").FontSize(10).TextColor(design.Muted)
	windowWidth, _ := c.Size()
	listWidth := float32(316)
	if windowWidth < 1000 {
		listWidth = 216
	}
	canvasWidth := max(float32(260), min(float32(650), windowWidth-a.navigationWidth(windowWidth)-listWidth-48))
	viewportHeight := float32(350)
	if windowWidth < 1000 {
		viewportHeight = 300
	}
	nodes, height, nodeWidth, nodeHeight := layoutProjectGraph(nodes, canvasWidth, viewportHeight)
	scale := canvasWidth / 650
	points := map[string]projectGraphNode{}
	for _, n := range nodes {
		points[n.id] = n
	}
	ui.Scroll(c).Key("project-graph-canvas-scroll").Height(viewportHeight).TrackScroll(&s.GraphScroll).Border(1, design.Border).Radius(10).Children(func() {
		ui.Column(c).Key("project-graph-canvas").Width(canvasWidth).Height(height).Background(ui.Hex("#fafcff")).Draw(func(p *ui.Painter, r ui.Rect) {
			drawn := map[string]bool{}
			for _, m := range s.Graph.Mentions {
				key := m.Entity + ":" + m.Memory
				if drawn[key] {
					continue
				}
				from, ok := points[m.Entity]
				to, exists := points[m.Memory]
				known := memories[m.Memory]
				if !ok || !exists || known.Revision != m.Revision || m.Revision <= 0 {
					continue
				}
				drawn[key] = true
				route := projectGraphCurve(from, to, nodeWidth, nodeHeight, len(drawn), false)
				paintProjectGraphCurve(p, r, route, "材料提及", design.Border, 1, false, canvasWidth)
			}
			for edgeIndex, rel := range s.Graph.Relations {
				from, ok := points[rel.Source]
				to, exists := points[rel.Target]
				if !ok || !exists {
					continue
				}
				valid := false
				for _, src := range rel.Sources {
					if m, yes := memories[src.Memory]; yes && m.Revision == src.Revision && src.Quote != "" {
						valid = true
						break
					}
				}
				if !valid {
					continue
				}
				route := projectGraphCurve(from, to, nodeWidth, nodeHeight, edgeIndex, true)
				paintProjectGraphCurve(p, r, route, rel.Label, design.Heat[4], 1.5, true, canvasWidth)
			}
		}).Children(func() {
			for _, node := range nodes {
				n := node
				label := []rune(n.label)
				caption := string(label[:min(16, len(label))])
				if len(label) > 16 {
					caption += "…"
				}
				button := ui.Button(c, caption).Label("关系图节点：" + n.label).Tooltip(n.label).Key("project-graph-node-" + n.id).Absolute().Left(n.x - nodeWidth/2).Top(n.y).Width(nodeWidth).Height(nodeHeight).FontSize(max(float32(9), 11*scale)).Background(ui.Hex("#f8fbfe"))
				if n.entity {
					button.Radius(24).Background(ui.Hex("#eef5fd"))
				} else {
					button.Radius(8)
					if n.raw {
						button.Background(ui.Hex("#ffffff")).Border(1, design.Border)
					}
				}
				button.OnClick(func() {
					if n.entity {
						s.Entity = n.id
						s.View = "entities"
						s.EntityBody.Y = 0
						a.saveBrowsing()
					} else {
						a.openGraphKnowledge(n.id)
					}
				})
			}
		})
	})
	ui.Text(c, fmt.Sprintf("显示%d个实体与材料节点；全部对象可从左栏逐项查看。知识与原始记录保持各自类型，没有实际身份的原文节点不补造。", len(nodes))).FontSize(10).LineHeight(1.7).TextColor(design.Muted)
	ui.Collapsible(c, "对象关系与出处", &s.RelationsOpen, func() {
		count := 0
		for _, relation := range s.Graph.Relations {
			rel := relation
			valid := []records.CogneeGraphSource{}
			for _, source := range rel.Sources {
				if m, ok := memories[source.Memory]; ok && m.Revision == source.Revision && source.Quote != "" {
					valid = append(valid, source)
				}
			}
			if len(valid) == 0 {
				continue
			}
			count++
			button := quietButton(c, rel.Label).Key("browse-relation-" + rel.ID).AlignSelf(ui.Start)
			if s.Relation == rel.ID {
				button.Bold()
			}
			button.OnClick(func() { s.Relation = rel.ID; a.saveBrowsing() })
			if s.Relation == rel.ID {
				for _, source := range valid {
					src := source
					ui.Text(c, src.Quote).FontSize(11).LineHeight(1.7).Selectable()
					label := "阅读关系来源知识"
					if src.Origin == "raw" {
						label = "阅读关系来源原始记录"
					}
					quietButton(c, label).Key("browse-relation-source-" + src.Chunk).AlignSelf(ui.Start).OnClick(func() { a.openGraphKnowledge(src.Memory) })
				}
			}
		}
		if count == 0 {
			ui.Text(c, "当前没有可核验的对象关系。材料提及与对象关系分别标注，不补造关系。").FontSize(11).TextColor(design.Muted)
		}
	})
	if s.Graph.UnsupportedRelations > 0 {
		ui.Text(c, fmt.Sprintf("另有%d条关系尚无充分依据，暂未展示。", s.Graph.UnsupportedRelations)).FontSize(10).TextColor(design.Muted)
	}
}
func (a *App) openGraphKnowledge(id string) {
	s := &a.browsing
	old := s.Entity
	s.Entity = ""
	a.browseMemory(id)
	if s.Explorer != nil && s.Explorer.Target == id {
		s.Explorer.Entity = old
	}
}

func (a *App) returnToBrowsingKnowledge() {
	s := &a.browsing
	prior := s.Knowledge
	if prior == nil || a.page != "项目" || a.scope != prior.Project {
		return
	}
	a.closeSourceReader()
	a.closeKnowledgeHistory()
	a.rememberDraft()
	a.selectedID, a.selectedItem = prior.Memory, nil
	a.recordKind, a.detailScroll.Y = prior.Kind, prior.BodyY
	a.editing, a.detailMode = false, "view"
	s.View, s.Explorer = "memories", nil
	if a.onSelect != nil {
		a.onSelect(prior.Memory)
	}
	a.saveBrowsing()
}

func (a *App) returnToBrowsingExplorer() {
	s := &a.browsing
	prior := s.Explorer
	if prior == nil || a.page != "项目" || a.scope != prior.Project || s.Graph.Project != prior.Project {
		return
	}
	a.closeSourceReader()
	a.closeKnowledgeHistory()
	a.rememberDraft()
	a.editing, a.detailMode = false, "view"
	s.View, s.Entity, s.Relation = prior.View, prior.Entity, prior.Relation
	a.selectedID, a.selectedItem = prior.Memory, nil
	s.Explorer = nil
	if a.onSelect != nil && prior.Memory != "" {
		a.onSelect(prior.Memory)
	}
	s.Entity, s.Relation = prior.Entity, prior.Relation
	a.saveBrowsing()
}

func (a *App) browsingKnowledgeReturn(c *ui.Context, item MemoryItem) {
	prior := a.browsing.Explorer
	if prior == nil || a.page != "项目" || prior.Project != a.scope || item.Dataset != a.scope || item.ID != prior.Target {
		return
	}
	label := "返回实体"
	if prior.View == "relations" {
		label = "返回关系图"
	}
	ui.Column(c).Key("browse-knowledge-context").Gap(8).Children(func() {
		quietButton(c, label).Key("browse-return-explorer").AlignSelf(ui.Start).Disabled(a.browsing.Graph.Project != prior.Project).OnClick(a.returnToBrowsingExplorer)
		if prior.Revision > 0 && prior.Revision != item.Revision {
			ui.Text(c, fmt.Sprintf("关联材料引用 v%d；这里显示当前知识 v%d。原文按当前知识的出处核验。", prior.Revision, item.Revision)).FontSize(design.Small).TextColor(design.Muted)
		}
	})
}

func (a *App) openGraphMaterial() {
	if a.page != "项目" || a.scope == "" {
		return
	}
	s := &a.browsing
	if s.Material == nil || s.Material.Project != a.scope {
		s.Material = &GraphMaterialUI{Project: a.scope}
	}
	s.Material.Open = true
	if s.Submissions == nil {
		s.Submissions = map[string]*GraphSubmissionUI{}
	}
	if s.Submissions[a.scope] == nil {
		s.Submissions[a.scope] = &GraphSubmissionUI{Project: a.scope, Kind: "knowledge", Chosen: map[string]records.Version{}}
	}
	s.Material.Submission = s.Submissions[a.scope]
	if !s.Material.Legacy {
		a.loadGraphSubmission(s.Material.Submission)
	}
	if s.Material.Legacy && !s.Material.Loaded && !s.Material.Loading {
		a.loadGraphMaterial()
	}
}
func (a *App) loadGraphMaterial() {
	m := a.browsing.Material
	if m == nil || m.Loading || m.Busy || m.Project != a.scope || a.page != "项目" {
		return
	}
	if a.libraryContext == nil || a.dispatch == nil {
		m.Message = "尚未连接记忆服务，未读取或修改项目材料接入。"
		return
	}
	m.Loading = true
	m.Sequence++
	seq := m.Sequence
	ctx, cancel := context.WithTimeout(a.libraryContext, 10*time.Second)
	m.Cancel = cancel
	go func() {
		defer cancel()
		bindings, err := a.library.CogneeBindings(ctx, m.Project)
		var connections []records.Connection
		if err == nil {
			connections, err = a.library.Connections(ctx)
		}
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			if a.browsing.Material != m || m.Sequence != seq || a.scope != m.Project {
				return
			}
			m.Loading = false
			m.Cancel = nil
			if err != nil {
				m.Message = "材料接入读取失败，请重试：" + err.Error()
				return
			}
			m.Loaded = true
			m.Bindings = nil
			m.Connections = nil
			for _, b := range bindings {
				if b.Project == m.Project && !b.MaterialSubmission {
					m.Bindings = append(m.Bindings, b)
				}
			}
			for _, c := range connections {
				if c.Agent == "codex" && c.Enabled && connectionAllows(c, m.Project) {
					m.Connections = append(m.Connections, c)
				}
			}
			if len(m.Connections) == 1 {
				m.ConnectionID = m.Connections[0].ID
			}
			if m.Binding != nil {
				valid := false
				for _, b := range m.Bindings {
					if b == *m.Binding {
						valid = true
					}
				}
				if !valid {
					m.Binding = nil
				}
			}
			if m.Binding == nil && len(m.Bindings) == 1 {
				b := m.Bindings[0]
				m.Binding = &b
				m.Endpoint, m.Dataset = b.Endpoint, b.Dataset
			}
			m.Message = ""
		})
	}()
}
func graphBindingAuthorized(m *GraphMaterialUI) bool {
	if m == nil || !m.Loaded || m.Binding == nil || m.Binding.MaterialSubmission {
		return false
	}
	for _, c := range m.Connections {
		if c.ID == m.Binding.ConnectionID && c.Generation == m.Binding.Generation && c.Agent == "codex" && c.Enabled && connectionAllows(c, m.Project) {
			return true
		}
	}
	return false
}
func (a *App) graphAuthorization(m *GraphMaterialUI) (records.CogneeAuthorization, error) {
	if m == nil || m.Project != a.scope || a.page != "项目" {
		return records.CogneeAuthorization{}, fmt.Errorf("项目已变化，请返回当前项目重新核对")
	}
	var connection *records.Connection
	for i := range m.Connections {
		c := &m.Connections[i]
		if c.ID == m.ConnectionID && c.Agent == "codex" && c.Enabled && connectionAllows(*c, m.Project) {
			connection = c
			break
		}
	}
	if connection == nil {
		return records.CogneeAuthorization{}, fmt.Errorf("当前没有已获此项目授权的Codex连接；本页不会新建或扩大Agent授权")
	}
	workspace := ""
	for _, scope := range a.scopes {
		if scope.ID == m.Project && scope.Kind == "project" {
			workspace = scope.Path
			break
		}
	}
	if workspace == "" {
		for _, p := range a.projects {
			if p.ID == m.Project {
				workspace = p.Path
				break
			}
		}
	}
	if workspace == "" {
		return records.CogneeAuthorization{}, fmt.Errorf("当前项目未登记可核验的本地目录")
	}
	if strings.TrimSpace(m.Endpoint) == "" || strings.TrimSpace(m.Dataset) == "" || strings.TrimSpace(m.Credential) == "" {
		return records.CogneeAuthorization{}, fmt.Errorf("请填写已有服务地址、真实dataset和独立访问凭据")
	}
	return records.CogneeAuthorization{Binding: records.CogneeBinding{Operation: uuid.NewString(), Project: m.Project, Dataset: strings.TrimSpace(m.Dataset), Endpoint: strings.TrimSpace(m.Endpoint), ConnectionID: connection.ID, Generation: connection.Generation, Workspace: workspace}, Credential: strings.TrimSpace(m.Credential)}, nil
}
func (a *App) authorizeGraphMaterial() {
	m := a.browsing.Material
	if m == nil || m.Busy || m.Loading || a.libraryContext == nil {
		return
	}
	authorization, err := a.graphAuthorization(m)
	if err != nil {
		m.Message = err.Error()
		return
	}
	m.Credential = ""
	m.Busy = true
	m.Sequence++
	seq := m.Sequence
	m.Message = "正在核验当前项目、已有服务身份与材料范围…"
	ctx, cancel := context.WithTimeout(a.libraryContext, 35*time.Second)
	m.Cancel = cancel
	go func() {
		defer cancel()
		binding, err := a.library.AuthorizeCognee(ctx, authorization)
		authorization.Credential = ""
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			if a.browsing.Material != m || m.Sequence != seq || a.scope != m.Project {
				return
			}
			m.Busy = false
			m.Cancel = nil
			if err != nil {
				m.Message = "材料接入未确认；未扩大Agent授权，可重新核对并读取原绑定：" + err.Error()
				return
			}
			expected := authorization.Binding
			if binding.Project != expected.Project || binding.Dataset != expected.Dataset || binding.Endpoint != expected.Endpoint || binding.ConnectionID != expected.ConnectionID || binding.Generation != expected.Generation || binding.Workspace != expected.Workspace {
				m.Message = "材料接入回执与已确认范围不一致，未按成功采用；请重新读取绑定。"
				return
			}
			m.Binding = &binding
			replaced := false
			for i, b := range m.Bindings {
				if b.Instance == binding.Instance && b.Dataset == binding.Dataset {
					m.Bindings[i] = binding
					replaced = true
					break
				}
			}
			if !replaced {
				m.Bindings = append(m.Bindings, binding)
			}
			m.Message = "已核验此项目的材料来源。接入授权不代表已产生实体或关系；仅带当前知识与出处证明的实际材料会显示。"
			a.selectGraphBinding(binding)
		})
	}()
}
func graphReceiptMatches(m *GraphMaterialUI, r records.CogneeSyncReceipt, query records.CogneeSync) bool {
	return m != nil && m.Project == query.Project && r.Project == query.Project && r.Dataset == query.Dataset && r.Session == query.Session
}
func (a *App) syncGraphMaterial() {
	m := a.browsing.Material
	if m == nil || m.Busy || m.Loading || m.Project != a.scope || a.page != "项目" || !graphBindingAuthorized(m) || a.libraryContext == nil || strings.TrimSpace(m.Session) == "" {
		return
	}
	query := records.CogneeSync{Project: m.Project, Dataset: m.Binding.Dataset, Session: strings.TrimSpace(m.Session)}
	m.Busy = true
	m.Sequence++
	seq := m.Sequence
	m.Message = "正在接收你明确选择的已有会话材料…"
	ctx, cancel := context.WithTimeout(a.libraryContext, 5*time.Minute)
	m.Cancel = cancel
	go func() {
		defer cancel()
		receipt, err := awaitCogneeMaterial(ctx, query, a.library.SyncCognee, 10*time.Second, func(receipt records.CogneeSyncReceipt) {
			if !graphReceiptMatches(m, receipt, query) {
				return
			}
			a.dispatch(func() {
				if a.browsing.Material == m && m.Sequence == seq && a.scope == m.Project {
					m.Receipt = &receipt
				}
			})
		})
		cancelled := ctx.Err() == context.Canceled
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			if a.browsing.Material != m || m.Sequence != seq || a.scope != m.Project {
				return
			}
			m.Busy = false
			m.Cancel = nil
			if graphReceiptMatches(m, receipt, query) {
				m.Receipt = &receipt
			} else if err == nil {
				m.Message = "同步回执的项目、dataset或会话不一致，未采用该回执。"
				return
			}
			if cancelled {
				m.Message = "本轮接收已取消，已持久化的逐项结果保留。"
			} else if err != nil {
				m.Message = "同步未完成，已有持久回执保留；重新接收同一会话会去重：" + err.Error()
			} else if receipt.OfficialState != "persisted" {
				m.Message = "已有会话材料尚未全部持久化；本轮有限等待结束，可以稍后明确重试。"
			} else {
				m.Message = "本轮材料已接收。实体与关系仍以服务返回的当前知识／出处版本证明为准。"
			}
			if m.Binding != nil {
				a.selectGraphBinding(*m.Binding)
			}
			a.refresh(false)
		})
	}()
}
func (a *App) legacyGraphMaterialPanel(c *ui.Context) {
	m := a.browsing.Material
	if m == nil {
		return
	}
	ui.Scroll(c).Key("graph-material-panel").Grow(1).MinHeight(0).TrackScroll(&m.Scroll).Padding(28, 24).Gap(16).Children(func() {
		ui.Text(c, "当前项目材料").FontSize(10).TextColor(design.Muted)
		ui.Text(c, a.scopeLabel(m.Project)).FontSize(design.Title).Bold()
		ui.Text(c, "绑定已有Cognee材料来源，并只同步你明确选择的会话。不会启动Agent任务，也不会自动导入历史会话。").FontSize(12).LineHeight(1.8).TextColor(design.Muted)
		if m.Loading {
			ui.Text(c, "正在读取当前项目的绑定与授权…").FontSize(12)
		}
		if m.Message != "" {
			ui.Text(c, m.Message).Key("graph-material-message").FontSize(12).Selectable().LineHeight(1.8)
		}
		quietButton(c, "重新读取材料接入").Key("graph-material-refresh").AlignSelf(ui.Start).Disabled(m.Loading || m.Busy).OnClick(a.loadGraphMaterial)
		if len(m.Connections) == 0 {
			ui.Text(c, "当前没有已获此项目授权的Codex连接。现有图投影需要该连接与真实dataset；pi提交的知识仍可正常阅读，不会据此补造实体。").FontSize(12).LineHeight(1.8)
		} else {
			labels := []string{}
			selected := ""
			for _, connection := range m.Connections {
				label := connection.Name + " · " + connection.ID[:min(8, len(connection.ID))]
				labels = append(labels, label)
				if m.ConnectionID == connection.ID {
					selected = label
				}
			}
			ui.Text(c, "已有项目授权连接").FontSize(11).TextColor(design.Muted)
			ui.Select(c.Key("graph-material-connection"), &selected, labels).Label("已有项目授权连接").Disabled(m.Busy || m.Loading).OnChange(func() {
				for i, label := range labels {
					if selected == label {
						m.ConnectionID = m.Connections[i].ID
					}
				}
			})
		}
		if len(m.Bindings) > 1 {
			ui.Text(c, "选择此项目已绑定的材料来源").FontSize(11).TextColor(design.Muted)
			for _, binding := range m.Bindings {
				b := binding
				quietButton(c, b.Dataset+" · "+b.Instance[:min(8, len(b.Instance))]).Key("graph-material-binding-" + b.Instance + "-" + b.Dataset).Disabled(m.Busy).OnClick(func() {
					m.Binding = &b
					m.Endpoint, m.Dataset = b.Endpoint, b.Dataset
					a.selectGraphBinding(b)
				})
			}
		}
		ui.Text(c, "服务地址").FontSize(11).TextColor(design.Muted)
		ui.TextInput(c.Key("graph-material-endpoint"), &m.Endpoint).Label("已有Cognee服务地址").Placeholder("http://127.0.0.1:端口").Disabled(m.Busy || m.Binding != nil)
		ui.Text(c, "真实dataset UUID").FontSize(11).TextColor(design.Muted)
		ui.TextInput(c.Key("graph-material-dataset"), &m.Dataset).Label("真实Cognee dataset").Disabled(m.Busy || m.Binding != nil)
		if m.Binding != nil {
			ui.Text(c, "服务身份："+m.Binding.Instance).FontSize(10).TextColor(design.Muted)
			if graphBindingAuthorized(m) {
				ui.Text(c, "上次读取时项目授权有效；同步时会再次核验。").FontSize(11)
			} else {
				ui.Text(c, "原绑定的连接授权已失效，不能继续同步；旧材料与回执保留。").FontSize(11)
			}
		}
		if m.Binding != nil {
			quietButton(c, "接入另一个材料来源").AlignSelf(ui.Start).Disabled(m.Busy || m.Loading).OnClick(func() {
				m.Binding = nil
				m.Endpoint, m.Dataset, m.Credential, m.Session = "", "", "", ""
				m.Receipt = nil
				m.Message = "仅为当前项目核验另一个已有材料来源；原绑定保留。"
			})
		}
		ui.Text(c, "独立访问凭据").FontSize(11).TextColor(design.Muted)
		ui.TextInput(c.Key("graph-material-credential"), &m.Credential).Label("Cognee独立访问凭据").Password().Disabled(m.Busy || m.Loading)
		actionButton(c, "确认此项目材料范围", nil).Key("graph-material-authorize").AlignSelf(ui.Start).Disabled(m.Busy || m.Loading || len(m.Connections) == 0 || m.Dataset == "" || m.Endpoint == "" || m.Credential == "" || a.libraryContext == nil).OnClick(a.authorizeGraphMaterial)
		ui.Divider(c)
		ui.Text(c, "接收指定会话").FontSize(12).Bold()
		ui.TextInput(c.Key("graph-material-session"), &m.Session).Label("明确接收的已有会话ID").Placeholder("已有会话 ID").Disabled(m.Busy)
		actionButton(c, "确认同步此会话", nil).Key("graph-material-sync").AlignSelf(ui.Start).Disabled(m.Busy || !graphBindingAuthorized(m) || strings.TrimSpace(m.Session) == "" || a.libraryContext == nil).OnClick(a.syncGraphMaterial)
		if m.Busy {
			quietButton(c, "取消本轮接收").AlignSelf(ui.Start).OnClick(func() {
				if m.Cancel != nil {
					m.Cancel()
				}
			})
		}
		if m.Receipt != nil {
			r := m.Receipt
			ui.Text(c, fmt.Sprintf("会话%s回执：%d份原始记录 · %d条知识 · %d份材料待处理", r.Session, len(r.Raw), len(r.Knowledge), r.PendingDocuments)).FontSize(11).LineHeight(1.7)
			ui.Text(c, "保存、索引与实体图分别依据真实回执，不以授权或同步成功替代图来源证明。").FontSize(10).LineHeight(1.7).TextColor(design.Muted)
		}
	})
}

func (a *App) selectGraphBinding(binding records.CogneeBinding) {
	s := &a.browsing
	if binding.Project != a.scope || a.page != "项目" {
		return
	}
	if s.Cancel != nil {
		s.Cancel()
	}
	s.Cancel = nil
	s.Sequence++
	s.Loading = false
	s.Dataset = binding.Dataset
	s.Graph = records.CogneeGraph{}
	s.Entity, s.Relation = "", ""
	s.Error = ""
	a.saveBrowsing()
	a.refreshBrowsing()
}

func entityMatchesType(entity records.CogneeEntity, selected string) bool {
	if selected == "" || selected == "不限类型" {
		return true
	}
	for _, kind := range entity.Types {
		if kind == selected {
			return true
		}
	}
	return false
}
func (a *App) entityTypeOptions() []string {
	kinds := map[string]bool{}
	if a.browsing.Graph.Project == a.scope && a.browsing.Error == "" && a.runtimeStatus.Phase != "stopped" {
		for _, entity := range a.browsing.Graph.Entities {
			for _, kind := range entity.Types {
				if strings.TrimSpace(kind) != "" {
					kinds[kind] = true
				}
			}
		}
	}
	options := []string{}
	for kind := range kinds {
		options = append(options, kind)
	}
	sort.Strings(options)
	return append([]string{"不限类型"}, options...)
}

func entityTypeLabel(entity records.CogneeEntity, fallback string) string {
	if len(entity.Types) == 0 {
		return fallback
	}
	return strings.Join(entity.Types, " · ")
}
