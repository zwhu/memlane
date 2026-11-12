package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

func (a *App) switchReadingKind(kind string) {
	if kind == a.recordKind || (kind == "knowledge" && a.recordKind == "") {
		return
	}
	if kind != "raw" {
		s := &a.conversations
		if s.Cancel != nil {
			s.Cancel()
		}
		if s.ReadCancel != nil {
			s.ReadCancel()
		}
		s.Sequence++
		s.ReadSequence++
		s.Loading, s.Reading = false, false
		s.Cancel, s.ReadCancel = nil, nil
	}
	a.closeKnowledgeHistory()
	a.closeProjectOrganizer()
	a.closeSourceReader()
	a.rememberDraft()
	if a.readingSelections == nil {
		a.readingSelections = map[string]string{}
	}
	oldKind := a.recordKind
	if oldKind == "" {
		oldKind = "knowledge"
	}
	route := a.page + ":" + a.scope + ":"
	a.readingSelections[route+oldKind] = a.selectedID
	a.recordKind, a.selectedID = kind, a.readingSelections[route+kind]
	if a.page == "通用知识" {
		a.commonSelectMatch = true
	}
	a.selectedItem, a.history = nil, nil
	a.editing, a.detailMode, a.nextCursor = false, "view", ""
	if a.projectSettings.Open {
		a.projectSettings.ReturnEditing, a.projectSettings.ReturnMode = false, "view"
	}
	if !a.visualFixture {
		if a.page == "搜索" && a.searchQuery != "" {
			a.applySearchKind()
		} else {
			a.items = nil
		}
	}
	if kind == "raw" && a.page == "项目" && a.libraryContext != nil && !a.visualFixture {
		a.openConversations()
	}
	a.refresh(false)
}

func knowledgeState(item MemoryItem) string {
	if item.Derived != nil && item.Derived.State == "stale" {
		return "来源已变化"
	}
	switch item.State {
	case "archived":
		return "已归档"
	case "deleted":
		return "已删除 · 保留历史"
	default:
		return "当前有效"
	}
}

// Original is the first saved knowledge version, not an independent source.
// Only the service's lineage receipt provides an original-material identity.
func (a *App) knowledgeProvenance(c *ui.Context, item MemoryItem) {
	if item.Origin == "raw" {
		return
	}
	matched := a.organizationSourcesFor == item.ID && a.organizationSourcesRevision == item.Revision
	officialKnowledge := item.SourceDetail != nil && item.SourceDetail.Official != nil
	card := ui.Column(c).Key("knowledge-provenance").Padding(20, 0, 0, 0).Gap(12).BorderWidth(1, 0, 0, 0).BorderColor(design.Border)
	if officialKnowledge {
		card.Padding(14).Border(1, design.Border).Radius(6)
	}
	card.Children(func() {
		ui.Row(c).Gap(10).Children(func() {
			title := "原始出处"
			if officialKnowledge {
				title = "原始依据"
			}
			ui.Text(c, title).FontSize(11).Bold().TextColor(design.Muted).Grow(1)
			if matched && a.organizationSourcesError == "" {
				for _, source := range a.organizationSources {
					if source.Input.Origin != "raw" {
						continue
					}
					officialConversation := a.page == "项目" && source.Input.SourceDetail != nil && source.Input.SourceDetail.Official != nil
					label := "阅读原文"
					if officialConversation {
						label = "阅读对话"
					}
					button := quietButton(c, label).Key("knowledge-source-" + source.Memory).OnClick(func() {
						if officialConversation {
							a.openConversationSource(item, records.MergeTarget{Memory: source.Memory, Revision: source.Revision})
						} else {
							a.openSourceReader(item, records.MergeTarget{Memory: source.Memory, Revision: source.Revision})
						}
					})
					if a.reader.ReturnFocus && a.reader.Query.Owner == item.ID {
						button.Focus()
						a.reader.ReturnFocus = false
					}
					break
				}
			}
		})
		if !matched && a.libraryContext != nil {
			ui.Text(c, "正在核对这条知识的出处…").FontSize(11).TextColor(design.Muted)
			return
		}
		if matched && a.organizationSourcesError != "" {
			ui.Text(c, "出处读取未完成").FontSize(12)
			ui.Text(c, a.organizationSourcesError).FontSize(11).TextColor(design.Muted)
			quietButton(c, "重试出处读取").OnClick(func() { a.refresh(false) })
			return
		}
		if matched && len(a.organizationSources) > 0 {
			for _, source := range a.organizationSources {
				ui.Row(c).Gap(10).Padding(10, 0).Children(func() {
					ui.Text(c, source.Input.Title+" · "+sourceDisplayName(source.Input.Source)).FontSize(11).TextColor(design.Muted).Grow(1).MinWidth(0)
					label := fmt.Sprintf("引用版本 v%d", source.Revision)
					if source.Changed {
						label += " · 来源已变化"
					}
					ui.Text(c, label).FontSize(10).TextColor(design.Muted).Shrink(0)
					if source.Input.Origin != "raw" {
						quietButton(c, "查看来源知识").Key("knowledge-source-" + source.Memory).OnClick(func() { a.openMergeMemory(source.Memory, source.Project) })
					}
				})
				if officialKnowledge && source.Quote != "" {
					ui.Text(c, "“"+source.Quote+"”").FontSize(11).LineHeight(1.8).Selectable()
				}
			}
			return
		}
		ui.Text(c, "由 "+item.Source+" 直接保存。").FontSize(12).TextColor(design.Muted)
		ui.Text(c, "这条知识未附完整会话原文。").FontSize(11).TextColor(design.Muted)
	})
}

// Leaving an editor preserves its draft. Make that return path visible without
// replacing the currently effective knowledge with unsaved content.
func (a *App) knowledgeDraftNotice(c *ui.Context, item MemoryItem) {
	draft, ok := a.drafts[item.ID]
	if !ok || a.editing || item.Origin == "raw" || item.State != "active" {
		return
	}
	conditions := ""
	if item.Experience != nil {
		conditions = item.Experience.Conditions
	}
	if draft.Title == item.Title && draft.Text == item.Text && draft.Conditions == conditions {
		return
	}
	ui.Column(c).Key("knowledge-unsaved-draft-"+item.ID).Padding(16).Gap(8).Background(design.Surface).Border(1, design.Border).Radius(8).Children(func() {
		ui.Text(c, "有未保存草稿").FontSize(11).Bold()
		ui.Text(c, "可以返回继续修改；下方仍为当前已保存的知识。").FontSize(11).TextColor(design.Muted)
		quietButton(c, "继续编辑").Key("knowledge-resume-draft-" + item.ID).AlignSelf(ui.Start).OnClick(a.editMemory)
	})
}

// Distinguish a verified empty observation from not having an observation.
// Cached entities are never links during a failed/new read or source mismatch.
func (a *App) knowledgeRelationState(item MemoryItem) string {
	if a.page != "项目" || a.scope != item.Dataset {
		return "outside"
	}
	if item.State != "active" || item.Derived != nil && item.Derived.State == "stale" {
		return "changed"
	}
	if a.runtimeStatus.Phase == "stopped" {
		return "stopped"
	}
	if a.browsing.Loading {
		return "loading"
	}
	if a.browsing.Error != "" {
		return "failed"
	}
	g := a.browsing.Graph
	if g.Project != item.Dataset || a.browsing.Dataset != "" && g.Dataset != a.browsing.Dataset {
		return "unread"
	}
	if g.State == "unconnected" {
		return "unconnected"
	}
	if (g.State != "available" && g.State != "empty") || g.CogneeVersion != "1.6.7" {
		return "unread"
	}
	if len(a.memoryEntities(item)) == 0 {
		return "empty"
	}
	return "available"
}
func (a *App) knowledgeRelations(c *ui.Context, item MemoryItem) {
	state := a.knowledgeRelationState(item)
	if state == "outside" {
		return
	}
	ui.Column(c).Key("knowledge-relations").Gap(6).Children(func() {
		title := "这条知识涉及的对象"
		if item.Origin == "raw" {
			title = "这条原始记录涉及的对象"
		}
		ui.Text(c, title).FontSize(design.Small).Bold()
		switch state {
		case "loading":
			ui.Text(c, "正在读取当前项目的关联对象…").FontSize(design.Small).TextColor(design.Muted)
		case "failed":
			ui.Text(c, "关联对象读取失败，数量未知。之前缓存的对象暂不作为当前版本入口。").FontSize(design.Small).TextColor(design.Muted)
			quietButton(c, "重试关联对象").Key("knowledge-relations-retry").OnClick(a.refreshBrowsing)
		case "stopped":
			ui.Text(c, "服务已停止，关联对象暂不可读；请先明确启动服务。").FontSize(design.Small).TextColor(design.Muted)
		case "changed":
			ui.Text(c, "知识或来源已变化，当前版本的关联尚未核验。").FontSize(design.Small).TextColor(design.Muted)
		case "unconnected":
			ui.Text(c, "尚未接入当前项目的实体材料，关联情况未知。").FontSize(design.Small).TextColor(design.Muted)
			quietButton(c, "查看材料接入").OnClick(a.openGraphMaterial)
		case "unread":
			ui.Text(c, "尚未取得当前项目的关联读取回执，不能判断是否为空。").FontSize(design.Small).TextColor(design.Muted)
			quietButton(c, "读取关联对象").OnClick(a.refreshBrowsing)
		case "empty":
			ui.Text(c, fmt.Sprintf("本次读取未找到当前版本 v%d 的关联对象。", item.Revision)).FontSize(design.Small).TextColor(design.Muted)
			quietButton(c, "刷新关联对象").OnClick(a.refreshBrowsing)
		case "available":
			entities := a.memoryEntities(item)
			ui.Text(c, fmt.Sprintf("已核验当前版本 v%d · %d 个对象", item.Revision, len(entities))).FontSize(10).TextColor(design.Muted)
			for start := 0; start < len(entities); start += 2 {
				ui.Row(c).Gap(8).Children(func() {
					for _, entity := range entities[start:min(start+2, len(entities))] {
						name := []rune(entity.Name)
						label := string(name[:min(28, len(name))])
						if len(name) > 28 {
							label += "…"
						}
						quietButton(c, label).Key("browse-memory-entity-"+entity.ID).Tooltip(entity.Name).Padding(5, 8).Background(design.Surface).Grow(1).MinWidth(0).Height(28).OnClick(func() { a.browseEntity(entity.ID) })
					}
				})
			}
		}
	})
}
