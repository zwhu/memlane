package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// The reading shell is shared with conversations. Editors, source readers and
// project task panels retain their own actions and return paths.
func (a *App) projectKnowledgeReading() bool {
	return !a.officialKnowledgeVisible() && a.page == "项目" && a.scope != "" && a.recordKind != "raw" &&
		a.browsing.View == "memories" && !a.editing && a.detailMode == "view" &&
		!a.reader.Open && !a.projectSettings.Open && !a.projectOrganizer.Open &&
		!a.projectKnowledgePreviewVisible() && !a.projectCreationOpen() &&
		!a.projectPreferencePanelOpen() && !a.commonDistillOpen() && !a.commonCopyOpen()
}

func (a *App) projectKnowledgeListHeader(c *ui.Context) {
	ui.Row(c).Gap(8).Children(func() {
		ui.Text(c, "项目内容").FontSize(10).TextColor(design.Muted).Grow(1)
		a.contentExplanationEntry(c)
	})
	ui.Row(c).Gap(4).Padding(3).Radius(6).AlignSelf(ui.Start).Background(ui.Hex("#eef2f6")).Children(func() {
		quietButton(c, "知识").Key("reading-tab-knowledge").Padding(7, 12).Background(ui.Hex("#ffffff")).Radius(4).TextColor(design.Ink)
		quietButton(c, "原始记录").Key("reading-tab-raw").Padding(7, 12).OnClick(func() { a.switchReadingKind("raw") })
	})
	quietButton(c, "会话整理结果").Key("official-knowledge-open").OnClick(a.openOfficialKnowledge)
	ui.TextInput(c.Key("reading-search"), &a.readingSearch).Label("搜索项目内容").Placeholder("搜索知识标题与正文").Height(36).FontSize(11).Background(design.Surface).Border(1, design.Border).Radius(6).OnSubmit(func() {
		a.readingFilter = strings.TrimSpace(a.readingSearch)
		a.nextCursor = ""
		a.refresh(false)
	})
	if a.readingFilter != "" {
		quietButton(c, "清除内容筛选").OnClick(func() { a.readingSearch, a.readingFilter, a.nextCursor = "", "", ""; a.refresh(false) })
	}
	ui.Row(c).Height(24).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, "项目知识").FontSize(10).TextColor(design.Muted).Grow(1)
		ui.Select(c.Key("memory-sort"), &a.listSort, []string{"最近更新", "常用", "重要优先"}).Width(84).FontSize(10).Background(ui.Transparent).BorderWidth(0).Padding(0).OnChange(a.readingFiltersChanged)
		quietButton(c, "更多").Key("knowledge-list-more").OnClick(func() { a.readingFilters.Open = !a.readingFilters.Open })
		a.readingListFailureAction(c)
	})
	// Existing creation and filtering capabilities remain available on demand.
	if a.readingFilters.Open || a.readingFilters.active() {
		a.readingFilterControls(c)
	}
}

func knowledgeMarkdownOptions() *markdown.Options {
	options := conversationMarkdownOptions()
	options.ShareSelection = false
	for i, size := range [6]float32{18, 15, 13, 12, 12, 12} {
		options.Theme.Headings[i] = markdown.HeadingStyle{Size: size / 12, Weight: 600, Color: design.Ink, Top: 1.4, Bottom: .55}
	}
	return &options
}

func (a *App) projectKnowledgeActions(c *ui.Context) {
	item := a.current()
	if item == nil {
		return
	}
	quietButton(c, "编辑").Key("knowledge-edit-open").Disabled(item.State != "active").OnClick(a.editMemory)
	history := quietButton(c, "历史").Key("knowledge-history-open").OnClick(a.openKnowledgeHistory)
	h := &a.knowledgeHistory
	if h.ReturnFocus && !h.Open && h.Root == a.library.Root && h.Epoch == a.libraryEpoch && h.Owner.ID == item.ID && h.Owner.Dataset == item.Dataset {
		history.Focus()
	}
}

func (a *App) projectKnowledgeListFooter(c *ui.Context) {
	ui.Row(c).Height(48).Shrink(0).Padding(0, 20).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		ui.Text(c, fmt.Sprintf("已载入 %d 条知识", len(a.items))).Key("reading-list-count").FontSize(10).TextColor(design.Muted)
	})
}

func (a *App) projectKnowledgeRowContent(c *ui.Context, item MemoryItem) {
	ui.Column(c).Grow(1).MinWidth(0).Gap(10).Children(func() {
		ui.Text(c, item.Title).FontSize(12).FontWeight(550).MaxLines(1).MinWidth(0)
		text := item.Text
		if item.SourceDetail != nil && item.SourceDetail.Official != nil {
			text = strings.ReplaceAll(strings.SplitN(text, "\n\n", 2)[0], "**", "")
		}
		ui.Text(c, preview(text)).FontSize(11).LineHeight(1.7).TextColor(design.Muted).MaxLines(1).MinWidth(0)
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "来源："+sourceDisplayName(item.Source)).FontSize(10).TextColor(design.Muted).Grow(1).SingleLine().MinWidth(0)
			ui.Text(c, knowledgeState(item)).FontSize(10).TextColor(design.Muted).SingleLine()
		})
	})
}
