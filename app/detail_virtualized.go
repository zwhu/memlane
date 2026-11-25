package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	kitui "github.com/zwhu/memlane/kit/ui"
)

type detailContentKey = kitui.ContentKey
type detailChunkCache = kitui.ChunkCache

func newDetailChunkCache() *detailChunkCache { return kitui.NewChunkCache() }

func markdownContentKey(src string) detailContentKey { return kitui.MarkdownContentKey(src) }

func traceContentKey(t Trace) detailContentKey {
	return kitui.TraceContentKey(t.Params, t.Result, t.Error)
}

func shouldVirtualizeMarkdown(src string) bool { return kitui.ShouldVirtualizeMarkdown(src) }

func splitMarkdownChunks(src string) []string { return kitui.SplitMarkdownChunks(src) }

func splitTraceBodyChunks(t Trace) []string {
	return kitui.SplitJSONTraceChunks(t.Params, t.Result, t.Error)
}

func isFenceSplitBoundary(prev, next string) bool { return kitui.IsFenceSplitBoundary(prev, next) }

func projectDetailScroll(s *pane, key string) *ui.ScrollState {
	return kitui.ScrollStateFor(&s.DetailScroll, key)
}

func projectDetailList(s *pane, key string) *ui.ListState {
	return kitui.ListStateFor(&s.DetailLists, key)
}

type detailRowKind uint8

const (
	detailRowDocChunk detailRowKind = iota
	detailRowUnrecognized
	detailRowTail
	detailRowSessionCoverage
	detailRowSessionEmpty
	detailRowQAPrompt
	detailRowQAAnswer
	detailRowTraceHeader
	detailRowTraceBody
	detailRowSubagentIntro
	detailRowSubagentCard
	detailRowSessionFooter
)

type detailRow struct {
	kind         detailRowKind
	key          string
	text         string
	roleLabel    string
	toolKey      string
	trace        Trace
	child        Item
	expanded     bool
	first        bool
	last         bool
	truncated    bool
	showLoadFull bool
	codeSplit    bool
}

func hasDetailTail(i Item) bool {
	return len(knowledgeBasisFields(i)) > 1 || i.Source != "" || i.SourceDocument != "" || i.Kind == "知识"
}

func buildDocumentDetailRows(a *App, i Item, pres Presentation) []detailRow {
	chunks := kitui.ResolveMarkdownChunks(pres.VisibleBody, kitui.VirtualizedMarkdownOptions{
		Cache: a.detailChunks, Dataset: i.Dataset, ID: i.ID, Slot: "doc",
	})
	rows := make([]detailRow, 0, len(chunks)+2)
	keyPrefix := i.Key()
	for idx, ch := range chunks {
		codeSplit := idx+1 < len(chunks) && isFenceSplitBoundary(ch, chunks[idx+1])
		rows = append(rows, detailRow{
			kind:      detailRowDocChunk,
			key:       fmt.Sprintf("doc:%s:%d", keyPrefix, idx),
			text:      ch,
			first:     idx == 0,
			last:      idx == len(chunks)-1,
			codeSplit: codeSplit,
		})
	}
	if pres.Unrecognized {
		rows = append(rows, detailRow{kind: detailRowUnrecognized, key: "unrecognized:" + keyPrefix})
	}
	if hasDetailTail(i) {
		rows = append(rows, detailRow{kind: detailRowTail, key: "tail:" + keyPrefix})
	}
	return rows
}

func appendTraceRows(rows []detailRow, a *App, s *pane, i Item, traces []Trace, uniqueKey func(string) string) []detailRow {
	for _, trace := range traces {
		t := trace
		toolKey := i.Key() + "/" + t.ID
		expanded := s.Tools != nil && s.Tools[toolKey]
		headKey := uniqueKey("th:" + t.ID)
		rows = append(rows, detailRow{
			kind:     detailRowTraceHeader,
			key:      headKey,
			toolKey:  toolKey,
			trace:    t,
			expanded: expanded,
		})
		if expanded {
			chunks := a.detailChunks.GetOrSplitJSONTrace(i.Dataset, i.ID, "tr:"+headKey, t.Params, t.Result, t.Error)
			for idx, ch := range chunks {
				rows = append(rows, detailRow{
					kind:  detailRowTraceBody,
					key:   fmt.Sprintf("%s:b:%d", headKey, idx),
					text:  ch,
					first: idx == 0,
					last:  idx == len(chunks)-1,
				})
			}
		}
	}
	return rows
}

func buildSessionDetailRows(a *App, s *pane, i Item, paged bool) []detailRow {
	seen := map[string]int{}
	uniqueKey := func(base string) string {
		n := seen[base]
		seen[base] = n + 1
		if n == 0 {
			return base
		}
		return fmt.Sprintf("%s#%d", base, n)
	}

	if paged && s.SessionSubagents && len(i.Subagents) > 0 {
		rows := make([]detailRow, 0, len(i.Subagents)+2)
		rows = append(rows, detailRow{kind: detailRowSubagentIntro, key: "sub-intro:" + i.Key()})
		for _, child := range i.Subagents {
			rows = append(rows, detailRow{
				kind:  detailRowSubagentCard,
				key:   uniqueKey("sub:" + child.Key()),
				child: child,
			})
		}
		if hasDetailTail(i) {
			rows = append(rows, detailRow{kind: detailRowTail, key: "tail:" + i.Key()})
		}
		return rows
	}

	if paged && s.SessionTools {
		rows := make([]detailRow, 0, len(i.Detail.Traces)+3)
		if i.Detail.ToolCount == 0 && len(i.Detail.Traces) == 0 {
			rows = append(rows, detailRow{
				kind: detailRowSessionEmpty,
				key:  "empty-tools:" + i.Key(),
				text: "此会话尚无已保存的工具记录。",
			})
		} else {
			rows = appendTraceRows(rows, a, s, i, i.Detail.Traces, uniqueKey)
		}
		rows = append(rows, detailRow{kind: detailRowSessionFooter, key: "footer-tools:" + i.Key()})
		if hasDetailTail(i) {
			rows = append(rows, detailRow{kind: detailRowTail, key: "tail:" + i.Key()})
		}
		return rows
	}

	rows := make([]detailRow, 0, len(i.Detail.QAs)*2+4)
	if !paged {
		showLoadFull := len(i.Detail.QAs) < i.Detail.Count || len(i.Detail.Traces) < i.Detail.ToolCount
		rows = append(rows, detailRow{
			kind:         detailRowSessionCoverage,
			key:          "coverage:" + i.Key(),
			showLoadFull: showLoadFull,
		})
	}
	if len(i.Detail.QAs) == 0 {
		message := "此会话尚无已保存的问答；下方仅为已保存的工具记录。"
		if paged {
			message = "此会话尚无已保存的问答，可切换到工具记录查看。"
		}
		rows = append(rows, detailRow{
			kind: detailRowSessionEmpty,
			key:  "empty-qas:" + i.Key(),
			text: message,
		})
	}
	promptRole := "用户"
	if i.Session != nil && i.Session.IsSubagent {
		promptRole = "上游消息"
	}
	for _, q := range i.Detail.QAs {
		qBase := uniqueKey("q:" + q.ID)
		var qChunks []string
		if shouldVirtualizeMarkdown(q.Question) {
			qChunks = a.detailChunks.GetOrSplitMarkdown(i.Dataset, i.ID, qBase, q.Question)
		} else {
			qChunks = []string{q.Question}
		}
		for idx, ch := range qChunks {
			rows = append(rows, detailRow{
				kind:      detailRowQAPrompt,
				key:       fmt.Sprintf("%s:%d", qBase, idx),
				text:      ch,
				roleLabel: promptRole,
				first:     idx == 0,
				last:      idx == len(qChunks)-1,
				truncated: q.QuestionTruncated,
			})
		}
		aBase := uniqueKey("a:" + q.ID)
		var aChunks []string
		if shouldVirtualizeMarkdown(q.Answer) {
			aChunks = a.detailChunks.GetOrSplitMarkdown(i.Dataset, i.ID, aBase, q.Answer)
		} else {
			aChunks = []string{q.Answer}
		}
		for idx, ch := range aChunks {
			rows = append(rows, detailRow{
				kind:      detailRowQAAnswer,
				key:       fmt.Sprintf("%s:%d", aBase, idx),
				text:      ch,
				first:     idx == 0,
				last:      idx == len(aChunks)-1,
				truncated: q.AnswerTruncated,
			})
		}
	}
	if !paged {
		rows = appendTraceRows(rows, a, s, i, i.Detail.Traces, uniqueKey)
	}
	if paged {
		rows = append(rows, detailRow{kind: detailRowSessionFooter, key: "footer-qa:" + i.Key()})
	}
	if hasDetailTail(i) {
		rows = append(rows, detailRow{kind: detailRowTail, key: "tail:" + i.Key()})
	}
	return rows
}

func (a *App) renderDetailRowList(c *ui.Context, s *pane, i Item, scrollKey string, scroll *ui.ScrollState, rows []detailRow) {
	listState := projectDetailList(s, scrollKey)
	mdOpts := a.v4MarkdownOptions(fsReading)
	kitui.VirtualizedList(c, "detail-list-"+scrollKey, listState, scroll, len(rows), func(n int) any {
		return rows[n].key
	}, func(row ui.ListRow) {
		a.renderDetailRow(row.Context, s, i, rows[row.Index], mdOpts)
	})
}

func (a *App) renderDetailRow(c *ui.Context, s *pane, i Item, r detailRow, mdOpts *markdown.Options) {
	switch r.kind {
	case detailRowDocChunk:
		padBot := sp2 + 2
		if r.last {
			padBot = 0
		} else if r.codeSplit {
			padBot = sp1
		}
		kitui.MarkdownChunkRow(c, r.key, r.text, mdOpts, readerMaxW, padBot)
	case detailRowUnrecognized:
		ui.Column(c).FillWidth().MaxWidth(readerMaxW).Padding(sp2, 0, 0, 0).Children(func() {
			ui.Text(c, "原文格式未识别，保留原文").FontSize(fsCaption).TextColor(tok.Text3)
		})
	case detailRowTail:
		ui.Column(c).FillWidth().MaxWidth(readerMaxW).Children(func() {
			a.projectDetailTail(c, s, &i)
		})
	case detailRowSessionCoverage:
		ui.Column(c).FillWidth().MaxWidth(readerMaxW).Gap(sp2).Padding(0, 0, sp3, 0).Children(func() {
			homeText(c, sessionCoverage(*i.Detail), homeBody).FontSize(fsFootnote)
			if r.showLoadFull {
				homeText(c, "当前是最近记录，可读取按时间排列的完整会话。", homeMeta)
				projectLink(c, "读取完整会话", func() { a.loadSessionPage(s, 0, 0) }).Disabled(s.Reading)
			}
		})
	case detailRowSessionEmpty:
		ui.Column(c).FillWidth().MaxWidth(readerMaxW).Padding(0, 0, sp3, 0).Children(func() {
			homeText(c, r.text, homeMeta)
		})
	case detailRowQAPrompt:
		padOut := float32(0)
		if r.last {
			padOut = sp3
		}
		padTop, padBot := float32(2), float32(2)
		bwTop, bwBot := float32(0), float32(0)
		if r.first {
			padTop, bwTop = 11, 1
		}
		if r.last {
			padBot, bwBot = 11, 1
		}
		ui.Column(c).FillWidth().MaxWidth(readerMaxW).Padding(0, 0, padOut, 0).Children(func() {
			card := ui.Column(c).FillWidth().Padding(padTop, 14, padBot, 14).Background(tok.Fill1).
				BorderWidth(bwTop, 1, bwBot, 1).BorderColor(tok.Border).Gap(6)
			if r.first && r.last {
				card.Radius(radius)
			}
			card.Children(func() {
				if r.first {
					ui.Text(c, r.roleLabel).Font("system-ui").FontSize(fsCaption).FontWeight(500).TextColor(tok.Text3)
				}
				markdown.View(c.Key(r.key), r.text, mdOpts)
				if r.last && r.truncated {
					ui.Text(c, "官方内容已截断").FontSize(fsCaption).TextColor(tok.Text3)
				}
			})
		})
	case detailRowQAAnswer:
		padBot := sp2 + 2
		if r.last {
			padBot = sp4
		}
		ui.Column(c).FillWidth().MaxWidth(readerMaxW).Padding(0, 0, padBot, 0).Gap(sp2).Children(func() {
			if r.first {
				sourceAgentLabel(c, i)
			}
			markdown.View(c.Key(r.key), r.text, mdOpts)
			if r.last && r.truncated {
				ui.Text(c, "官方内容已截断").FontSize(fsCaption).TextColor(tok.Text3)
			}
		})
	case detailRowTraceHeader:
		t := r.trace
		statusCol := tok.OK
		if strings.Contains(strings.ToLower(t.Status), "err") || strings.Contains(strings.ToLower(t.Status), "fail") || t.Error != "" {
			statusCol = tok.Danger
		}
		padOut := sp2
		bwBot := float32(1)
		if r.expanded {
			padOut = 0
			bwBot = 0
		}
		ui.Column(c).FillWidth().MaxWidth(readerMaxW).Padding(0, 0, padOut, 0).Children(func() {
			card := ui.Column(c).FillWidth().BorderWidth(1, 1, bwBot, 1).BorderColor(tok.Border).Background(tok.BgElevated)
			if !r.expanded {
				card.Radius(radius)
			}
			card.Children(func() {
				head := ui.ButtonBase(c.Key("tool-head-"+r.key)).Role(ui.RoleButton).Label(t.Name+" · "+t.Status).
					Cursor(ui.CursorPointer).FillWidth().Padding(sp2, sp3).Gap(sp2).AlignItems(ui.Center).Justify(ui.Start).BorderWidth(0)
				if !r.expanded {
					head.Radius(radius)
				}
				if head.Hovered() {
					head.Background(tok.Hover)
				} else {
					head.Background(ui.Transparent)
				}
				toolKey := r.toolKey
				head.Children(func() {
					ico := v4IconChev
					if r.expanded {
						ico = v4IconDown
					}
					ui.Icon(c, ico).FontSize(12).TextColor(tok.Text3).Shrink(0)
					ui.Box(c).Size(6, 6).Radius(3).Background(statusCol).Shrink(0)
					ui.Text(c, t.Name).Font("ui-monospace").FontSize(fsFootnote).TextColor(tok.Text1).SingleLine().Grow(1).MinWidth(0)
					if t.Status != "" {
						homeText(c, t.Status, homeMeta).SingleLine().Shrink(0)
					}
				}).OnClick(func() {
					if s.Tools == nil {
						s.Tools = map[string]bool{}
					}
					s.Tools[toolKey] = !s.Tools[toolKey]
				})
			})
		})
	case detailRowTraceBody:
		padOut := float32(0)
		padTop, padBot := sp1, sp1
		bwTop, bwBot := float32(0), float32(0)
		if r.first {
			padTop, bwTop = sp2, 1
		}
		if r.last {
			padBot, bwBot, padOut = sp3, 1, sp2
		}
		ui.Column(c).FillWidth().MaxWidth(readerMaxW).Padding(0, 0, padOut, 0).Children(func() {
			ui.Column(c).FillWidth().Padding(padTop, sp3, padBot, sp3).
				BorderWidth(bwTop, 1, bwBot, 1).BorderColor(tok.Border).Background(tok.BgElevated).
				Children(func() {
					markdown.View(c.Key(r.key), r.text, mdOpts)
				})
		})
	case detailRowSubagentIntro:
		ui.Column(c).FillWidth().MaxWidth(readerMaxW).Padding(0, 0, sp3, 0).Children(func() {
			homeText(c, "以下子会话由当前主会话派生，可能包含 Agent 之间的问答对话与工具调用，点击查看完整记录。", homeMeta)
		})
	case detailRowSubagentCard:
		ui.Column(c).FillWidth().MaxWidth(readerMaxW).Padding(0, 0, sp3, 0).Children(func() {
			a.renderSubagentCard(c, s, i, r.child)
		})
	case detailRowSessionFooter:
		ui.Column(c).FillWidth().MaxWidth(readerMaxW).Padding(sp1, 0, sp3, 0).Children(func() {
			a.sessionLoadMore(c, s, i)
		})
	}
}
