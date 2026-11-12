package desktop

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

// The document remains in its original view. Only the source reading state is
// separate; neither route persists a second copy of the official content.
type officialDocumentSource struct {
	Query     records.OfficialDocumentQuery
	Binding   records.CogneeBinding
	Reference string
	Reading   ConversationUI
	Sequence  int64
	Cancel    context.CancelFunc
	Imported  *officialDocumentView
}

func (s *officialDocumentSource) matches(view *officialDocumentView) bool {
	if view == nil || view.Content == nil || view.Binding != s.Binding || records.MergeDigest(view.Content.Document) != records.MergeDigest(s.Query.Selected) {
		return false
	}
	if s.Query.SourceDocument == "" {
		return view.Content.SessionReference == s.Reference
	}
	for _, ref := range official.ImportedKnowledgeSources(*view.Content) {
		if ref.Document == s.Query.SourceDocument {
			return true
		}
	}
	return false
}

func (a *App) openImportedDocumentSource(target string) {
	if target == "" {
		return
	}
	view, valid := a.currentOfficialDocument()
	d := a.conversations.Documents
	if !a.conversationPageVisible() || !valid || d.Source != nil {
		return
	}
	row := view.Content.Document
	source := &officialDocumentSource{Query: records.OfficialDocumentQuery{Project: a.conversations.Project, Instance: view.Binding.Instance, Dataset: view.Binding.Dataset, Selected: &row, SourceDocument: target}, Binding: view.Binding}
	if !source.matches(view) {
		return
	}
	d.Source = source
	a.refreshOfficialDocumentSource()
}

func (d *officialDocumentsUI) closeSource() {
	if source := d.Source; source != nil {
		if source.Cancel != nil {
			source.Cancel()
		}
		source.Sequence++
		source.Reading.Active = false
		source.Reading.Views = nil
		d.Source = nil
	}
}

func (a *App) openOfficialDocumentSource() {
	view, valid := a.currentOfficialDocument()
	d := a.conversations.Documents
	if !a.conversationPageVisible() || !valid || view.Content.SessionReference == "" || d.Source != nil {
		return
	}
	row := view.Content.Document
	d.Source = &officialDocumentSource{
		Query:   records.OfficialDocumentQuery{Project: a.conversations.Project, Instance: view.Binding.Instance, Dataset: view.Binding.Dataset, Selected: &row},
		Binding: view.Binding, Reference: view.Content.SessionReference,
		Reading: ConversationUI{Active: true, Project: a.conversations.Project, Binding: &view.Binding,
			Views: map[string]*conversationReadingState{}, Markdown: conversationMarkdownOptions()},
	}
	a.refreshOfficialDocumentSource()
}

func (a *App) refreshOfficialDocumentSource() {
	d := a.conversations.Documents
	if !a.conversationPageVisible() || d == nil || d.Source == nil || a.libraryContext == nil {
		return
	}
	source := d.Source
	view, valid := a.currentOfficialDocument()
	if !valid || !source.matches(view) {
		d.closeSource()
		return
	}
	if source.Reading.Reading {
		return
	}
	if source.Query.SourceDocument != "" {
		a.refreshImportedDocumentSource(d, source)
		return
	}
	source.Reading.Reading = true
	source.Sequence++
	sequence, epoch, client, life, dispatch := source.Sequence, a.libraryEpoch, a.library, a.libraryContext, a.dispatch
	ctx, cancel := context.WithTimeout(life, 15*time.Second)
	source.Cancel = cancel
	go func() {
		defer cancel()
		result, err := client.OfficialDocumentSession(ctx, source.Query)
		if life.Err() != nil {
			return
		}
		dispatch(func() {
			if a.libraryEpoch != epoch || a.library.Root != client.Root || a.conversations.Documents != d || d.Source != source || source.Sequence != sequence || !a.conversationPageVisible() {
				return
			}
			current, valid := a.currentOfficialDocument()
			if !valid || current.Binding != source.Binding || current.Content.SessionReference != source.Reference || records.MergeDigest(current.Content.Document) != records.MergeDigest(source.Query.Selected) {
				d.closeSource()
				return
			}
			s := &source.Reading
			s.Reading, source.Cancel = false, nil
			if err == nil && (records.MergeDigest(result.Document) != records.MergeDigest(source.Query.Selected) || result.Detail.Session != source.Reference || result.Detail.Dataset != source.Binding.Dataset || result.Detail.User == "") {
				err = &serviceipc.ServiceError{Code: "stale_source", Message: "document source receipt changed"}
			}
			if err != nil {
				s.SourceError = sourceReaderErrorCode(err)
				if !sourceReaderTransientFailure(s.SourceError) {
					s.Views = map[string]*conversationReadingState{}
					s.Selected, s.SelectedRow = "", nil
				}
				if officialDocumentPermissionFailure(s.SourceError) {
					d.ErrorCode = s.SourceError
					a.withdrawOfficialDocumentAccess(d)
				}
				return
			}
			s.SourceError = ""
			key := conversationKey(source.Binding, result.Detail.SessionSummary)
			reading := s.Views[key]
			if reading == nil {
				reading = &conversationReadingState{}
				s.Views = map[string]*conversationReadingState{key: reading}
			}
			reading.Detail = &result.Detail
			s.Selected, s.SelectedRow = key, &result.Detail.SessionSummary
		})
	}()
}

func (a *App) officialDocumentSourceReading(c *ui.Context) {
	d := a.conversations.Documents
	source := d.Source
	if source.Query.SourceDocument != "" {
		a.importedDocumentSourceReading(c, d, source)
		return
	}
	ui.Row(c).Shrink(0).Padding(10, 20).Gap(8).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
		label := "返回文档"
		if d.Knowledge {
			label = "返回知识"
		}
		quietButton(c, label).OnClick(d.closeSource)
		ui.Text(c, "仅关联来源会话，未定位具体问答或工具。").FontSize(10).TextColor(design.Muted).Grow(1).MinWidth(0)
	})
	a.conversationReadingWith(c.Key("official-document-source-"+d.Selected), &source.Reading, a.refreshOfficialDocumentSource, a.refreshOfficialDocumentSource, func(code string) string {
		if sourceReaderTransientFailure(code) {
			return "来源会话暂不可读，可重试或返回文档。"
		}
		return "来源会话缺失、已过期或无法核验。文档原文保留，可返回文档查看。"
	})
}

func (a *App) refreshImportedDocumentSource(d *officialDocumentsUI, source *officialDocumentSource) {
	source.Reading.Reading = true
	source.Sequence++
	sequence, epoch, client, life, dispatch := source.Sequence, a.libraryEpoch, a.library, a.libraryContext, a.dispatch
	ctx, cancel := context.WithTimeout(life, 15*time.Second)
	source.Cancel = cancel
	go func() {
		defer cancel()
		result, err := client.OfficialDocumentSource(ctx, source.Query)
		if life.Err() != nil {
			return
		}
		dispatch(func() {
			if a.libraryEpoch != epoch || a.library.Root != client.Root || a.conversations.Documents != d || d.Source != source || source.Sequence != sequence || !a.conversationPageVisible() {
				return
			}
			view, valid := a.currentOfficialDocument()
			if !valid || !source.matches(view) {
				d.closeSource()
				return
			}
			source.Reading.Reading, source.Cancel = false, nil
			if err == nil && (records.MergeDigest(result.Document) != records.MergeDigest(source.Query.Selected) || result.Content.Document.Dataset != source.Binding.Dataset || result.Content.Document.ID != source.Query.SourceDocument || result.Reference.Document != source.Query.SourceDocument) {
				err = &serviceipc.ServiceError{Code: "stale_source", Message: "imported source receipt changed"}
			}
			if err != nil {
				code := officialDocumentError(err)
				source.Reading.SourceError = code
				if !sourceReaderTransientFailure(code) {
					source.Imported = nil
				}
				if officialDocumentPermissionFailure(code) {
					d.ErrorCode = code
					a.withdrawOfficialDocumentAccess(d)
				}
				return
			}
			source.Reading.SourceError = ""
			if source.Imported == nil {
				source.Imported = &officialDocumentView{}
			}
			source.Imported.Content = &result.Content
		})
	}()
}

func (a *App) importedDocumentSourceReading(c *ui.Context, d *officialDocumentsUI, source *officialDocumentSource) {
	ui.Row(c).Shrink(0).Padding(10, 20).Gap(8).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
		quietButton(c, "返回知识").OnClick(d.closeSource)
		ui.Text(c, "原始依据").FontSize(11).TextColor(design.Muted)
	})
	panel := ui.Scroll(c.Key("imported-source-"+source.Query.SourceDocument)).Grow(1).MinHeight(0).Padding(24, 20).Gap(16).Selectable()
	if source.Imported != nil {
		panel.TrackScroll(&source.Imported.Scroll)
	}
	panel.Children(func() {
		if source.Imported == nil || source.Imported.Content == nil {
			label := "正在读取原文…"
			if source.Reading.SourceError != "" {
				label = "原文暂不可读，请重试或返回知识。"
			}
			ui.Text(c, label).FontSize(12).TextColor(design.Muted)
			return
		}
		view := source.Imported
		presentation := presentImportedSource(*view.Content)
		ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
			ui.Text(c, officialDocumentTitle(view.Content.Document)).FontSize(18).Bold().Grow(1).MinWidth(0)
			quietButton(c, "记录信息").FontSize(10).OnClick(func() { view.Info = !view.Info })
		})
		if presentation.Role != "" {
			ui.Text(c, "Codex · "+presentation.Role).FontSize(10).TextColor(design.Muted)
		}
		if view.Info {
			ui.Column(c).Padding(12).Gap(8).Border(1, design.Border).Radius(6).Children(func() {
				meta(c, "保存位置", "Cognee")
				if presentation.Role != "" {
					meta(c, "来源会话", presentation.Session)
					meta(c, "轮次", presentation.Turn)
					meta(c, "工作目录", presentation.Workspace)
					meta(c, presentation.TimeLabel, presentation.Time)
				}
			})
		}
		if a.readingMarkdown == nil {
			a.readingMarkdown = knowledgeMarkdownOptions()
		}
		markdown.View(c.Key("imported-source-body-"+source.Query.SourceDocument), presentation.Body, a.readingMarkdown)
	})
	ui.Row(c).Height(48).Shrink(0).Padding(10, 20).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		ui.Text(c, "原始记录").FontSize(10).TextColor(design.Muted).Grow(1)
		if source.Reading.SourceError != "" {
			quietButton(c, "重试").Disabled(source.Reading.Reading).OnClick(a.refreshOfficialDocumentSource)
		}
		quietButton(c, "复制原文").Disabled(source.Imported == nil || source.Imported.Content == nil).OnClick(func() {
			view, valid := a.currentOfficialDocument()
			if d.Source == source && valid && source.matches(view) && source.Imported != nil && source.Imported.Content != nil {
				c.WriteClipboard(source.Imported.Content.Text)
			}
		})
	})
}
