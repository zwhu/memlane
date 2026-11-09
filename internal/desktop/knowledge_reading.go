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
	a.selectedItem, a.history = nil, nil
	a.editing, a.detailMode, a.nextCursor = false, "view", ""
	if a.projectSettings.Open {
		a.projectSettings.ReturnEditing, a.projectSettings.ReturnMode = false, "view"
	}
	if !a.visualFixture {
		a.items = nil
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
	ui.Column(c).Key("knowledge-provenance").Padding(20, 0, 0, 0).Gap(12).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		ui.Text(c, "原始出处").FontSize(11).Bold().TextColor(design.Muted)
		matched := a.organizationSourcesFor == item.ID && a.organizationSourcesRevision == item.Revision
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
				ui.Column(c).Gap(8).Children(func() {
					ui.Row(c).Gap(10).Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
							ui.Text(c, source.Input.Title).FontSize(12).Bold()
							kind := "来源知识"
							if source.Input.Origin == "raw" {
								kind = "原始记录"
							}
							if source.Changed {
								kind += " · 来源已变化"
							}
							ui.Text(c, fmt.Sprintf("%s · v%d · %s", source.Input.Source, source.Revision, kind)).FontSize(10).TextColor(design.Muted)
						})
						if source.Input.Origin == "raw" {
							button := quietButton(c, "阅读原文").Key("knowledge-source-" + source.Memory).OnClick(func() {
								a.openSourceReader(item, records.MergeTarget{Memory: source.Memory, Revision: source.Revision})
							})
							if a.reader.ReturnFocus && a.reader.Query.Owner == item.ID && a.reader.Query.Memory == source.Memory {
								button.Focus()
								a.reader.ReturnFocus = false
							}
						} else {
							quietButton(c, "查看来源知识").Key("knowledge-source-" + source.Memory).OnClick(func() { a.openMergeMemory(source.Memory, source.Project) })
						}
					})
					if source.Quote != "" {
						ui.Text(c, source.Quote).Selectable().FontSize(12).LineHeight(1.9).Padding(0, 0, 0, 12).BorderWidth(0, 0, 0, 2).BorderColor(design.Border)
					}
				})
			}
			return
		}
		ui.Text(c, "由 "+item.Source+" 直接保存。").FontSize(12).TextColor(design.Muted)
		ui.Text(c, "这条知识未附完整会话原文。").FontSize(11).TextColor(design.Muted)
	})
}
