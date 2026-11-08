package desktop

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

type SourceReaderUI struct {
	Open, Loading, Technical bool
	Full                     bool
	Sequence                 int64
	Query                    records.SourceRead
	Result                   records.SourceRecord
	Error, Search            string
	Sources                  []records.OrganizationSource
	Window                   *textWindow
	Matches                  []int
	Match                    int
	Scroll                   ui.ScrollState
	Cancel                   context.CancelFunc
	FocusOffset, FocusLength int
	Reveal                   bool
}

func (a *App) closeSourceReader() {
	if a.reader.Cancel != nil {
		a.reader.Cancel()
	}
	a.reader.Sequence++
	a.reader.Open = false
	a.reader.Result = records.SourceRecord{}
	a.reader.Window = nil
	a.reader.Loading = false
}

func (a *App) openSourceReader(owner MemoryItem, source records.MergeTarget) {
	a.closeSourceReader()
	a.reader = SourceReaderUI{Open: true, Full: true, Sequence: a.reader.Sequence + 1,
		Query: records.SourceRead{Project: owner.Dataset, Owner: owner.ID, OwnerRevision: owner.Revision, Memory: source.Memory, Revision: source.Revision}}
	if a.organizationSourcesFor == owner.ID && a.organizationSourcesRevision == owner.Revision {
		a.reader.Sources = append([]records.OrganizationSource(nil), a.organizationSources...)
		// QA timestamps and trace sequence are separate official channels.
		// Preserve SDK order within each; never guess an interleaved clock.
		sort.SliceStable(a.reader.Sources, func(i, j int) bool {
			left, right := a.reader.Sources[i].Input.SourceDetail, a.reader.Sources[j].Input.SourceDetail
			if left == nil || left.Official == nil {
				return false
			}
			if right == nil || right.Official == nil {
				return true
			}
			if left.Official.Session != right.Official.Session {
				return left.Official.Session < right.Official.Session
			}
			if left.Official.Kind != right.Official.Kind {
				return left.Official.Kind == "qa"
			}
			return left.Official.Sequence < right.Official.Sequence
		})
	}
	a.refreshSourceReader()
}

func (a *App) refreshSourceReader() {
	s := &a.reader
	if !s.Open || s.Loading || a.libraryContext == nil || a.libraryContext.Err() != nil {
		return
	}
	s.Sequence++
	sequence, query := s.Sequence, s.Query
	s.Loading = true
	ctx, cancel := context.WithTimeout(a.libraryContext, 15*time.Second)
	s.Cancel = cancel
	go func() {
		defer cancel()
		result, err := a.library.ReadSource(ctx, query)
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			s := &a.reader
			if !s.Open || s.Sequence != sequence || s.Query != query {
				return
			}
			s.Loading, s.Cancel = false, nil
			if err != nil {
				s.Result, s.Window = records.SourceRecord{}, nil
				s.Error = "原始记录读取失败：" + err.Error()
				return
			}
			s.Error, s.Result = "", result
			if result.State != "available" {
				s.Window = nil
				return
			}
			shown := a.readerRecord()
			if s.Window == nil || s.Window.Body != shown.Text {
				s.Window = newTextWindow(shown.Text)
				s.Scroll.Y = 0
				if !s.Full || result.SessionOriginal == nil {
					a.locateSourceOffset(result.QuoteOffset)
				}
				s.Reveal = false // Open at metadata; explicit locate/search scrolls.
			}
		})
	}()
}

func (a *App) readerRecord() records.Version {
	if a.reader.Full && a.reader.Result.SessionOriginal != nil {
		return *a.reader.Result.SessionOriginal
	}
	return a.reader.Result.Record
}
func (a *App) sourceReaderMode(full bool) {
	s := &a.reader
	s.Full = full
	s.Window = newTextWindow(a.readerRecord().Text)
	s.Search, s.Matches, s.Match = "", nil, 0
	s.FocusOffset, s.FocusLength, s.Reveal, s.Scroll.Y = 0, 0, false, 0
}

func (a *App) locateSourceOffset(offset int) {
	s := &a.reader
	if s.Window == nil || offset < 0 || offset >= len(s.Window.Body) {
		return
	}
	s.Window.Page = max(0, sort.Search(len(s.Window.Offsets), func(i int) bool { return s.Window.Offsets[i] > offset })-1)
	quote := []rune(s.Result.Quote)
	s.FocusOffset, s.FocusLength = offset, len(string(quote[:min(240, len(quote))]))
	if s.Search != "" && strings.HasPrefix(s.Window.Body[offset:], s.Search) {
		s.FocusLength = len(s.Search)
	}
	s.Reveal = true
	s.Scroll.Y = 0
}

func (a *App) searchSource() {
	s := &a.reader
	s.Matches, s.Match = nil, 0
	if s.Window == nil || s.Search == "" {
		return
	}
	for start := 0; start < len(s.Window.Body); {
		at := strings.Index(s.Window.Body[start:], s.Search)
		if at < 0 {
			break
		}
		at += start
		s.Matches = append(s.Matches, at)
		start = at + len(s.Search)
	}
	if len(s.Matches) > 0 {
		a.locateSourceOffset(s.Matches[0])
	}
}

func (a *App) sourceReader(c *ui.Context) {
	s := &a.reader
	ui.Row(c).Key("source-reader-toolbar").Height(design.Toolbar).Shrink(0).Padding(10, 24).Gap(12).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
		ui.Text(c, "原始记录").Bold()
		ui.Text(c, a.scopeLabel(s.Query.Project)).FontSize(design.Small).TextColor(design.Muted)
		ui.Spacer(c)
		quietButton(c, "返回所选记忆").Key("source-reader-close").OnClick(a.closeSourceReader)
	})
	ui.Row(c).Key("source-reader-layout").Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Scroll(c).Width(210).Shrink(0).Padding(18).Gap(12).BorderWidth(0, 1, 0, 0).BorderColor(design.Border).Children(func() {
			ui.Text(c, "本条知识的原始材料").Bold()
			ui.Text(c, "原始记录与知识分别计数。同源提炼仍是同一份依据。").FontSize(design.Small).TextColor(design.Muted).LineHeight(1.6)
			for _, source := range s.Sources {
				if source.Project != s.Query.Project || source.Input.Origin != "raw" {
					continue
				}
				button := quietButton(c, source.Input.Title).Key("source-reader-record-" + source.Memory)
				if s.Query.Memory == source.Memory {
					button.Background(design.Surface)
				}
				button.OnClick(func() {
					if s.Cancel != nil {
						s.Cancel()
					}
					s.Sequence++
					s.Query.Memory, s.Query.Revision = source.Memory, source.Revision
					s.Result, s.Window, s.Search, s.Matches = records.SourceRecord{}, nil, "", nil
					s.Loading = false
					a.refreshSourceReader()
				})
				ui.Text(c, fmt.Sprintf("%s · v%d", source.Input.Source, source.Revision)).FontSize(design.Small).TextColor(design.Muted)
			}
			quietButton(c, "重新读取").Key("source-reader-retry").Disabled(s.Loading).OnClick(a.refreshSourceReader)
		})
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			if s.Error != "" {
				a.empty(c, "来源暂不可用", s.Error)
				return
			}
			if s.Result.State != "available" || s.Window == nil {
				message := "正在读取完整原始记录…"
				if s.Result.State == "source_changed" {
					message = fmt.Sprintf("引用 v%d，当前 v%d 或已失效；保留来源身份，停止读取过期材料。", s.Query.Revision, s.Result.CurrentRevision)
				}
				if s.Result.State == "owner_changed" {
					message = "所选知识已更新，请返回当前知识重新打开出处。"
				}
				if s.Result.State == "upstream_changed" {
					message = "官方来源内容已变化。本地冻结版本保留，请核对并确认同步后重新读取。"
				}
				if s.Result.State == "upstream_unavailable" {
					message = "官方来源已移除或尚无持久回执。本地副本保留，当前停止读取该来源。"
				}
				a.empty(c, "原始记录", message)
				return
			}
			body := s.Window.Body
			if s.Result.SessionOriginal != nil {
				ui.Row(c).Shrink(0).Padding(10, 20).Gap(12).Children(func() {
					quietButton(c, "完整会话原文").Key("source-reader-full").OnClick(func() { a.sourceReaderMode(true) })
					quietButton(c, "官方采集分段").Key("source-reader-segment").OnClick(func() { a.sourceReaderMode(false) })
				})
				ui.Text(c, "完整会话来自本轮批准的 Codex 日志；官方 SDK 分段保持原样，两者属于同一份会话依据。").FontSize(design.Small).Padding(0, 20, 8, 20)
			} else if s.Result.Record.SourceDetail != nil && s.Result.Record.SourceDetail.Official != nil {
				ui.Text(c, "当前是官方 SDK 采集分段；完整会话日志尚未接入或不可用，不将截短的工具返回称为完整会话。").FontSize(design.Small).Padding(0, 20, 8, 20)
			}
			ui.Row(c).Shrink(0).Padding(12, 20).Gap(8).Children(func() {
				ui.TextInput(c.Key("source-reader-search"), &s.Search).Placeholder("搜索完整原文（精确文本）").Grow(1).OnSubmit(a.searchSource)
				quietButton(c, "查找").Key("source-reader-find").OnClick(a.searchSource)
				quietButton(c, "复制全文").Key("source-reader-copy").OnClick(func() { c.Services().WriteClipboard(body) })
			})
			ui.Row(c).Shrink(0).Padding(0, 20, 8, 20).Gap(8).Children(func() {
				pages := len(s.Window.Offsets) - 1
				ui.Text(c, fmt.Sprintf("完整原文 · 第 %d / %d 段", s.Window.Page+1, pages)).FontSize(design.Small).TextColor(design.Muted)
				quietButton(c, "上一段").Key("source-reader-previous").Disabled(s.Window.Page == 0).OnClick(func() { s.Window.Page--; s.Scroll.Y = 0 })
				quietButton(c, "下一段").Key("source-reader-next").Disabled(s.Window.Page >= pages-1).OnClick(func() { s.Window.Page++; s.Scroll.Y = 0 })
				quietButton(c, "定位引用").Key("source-reader-quote").Disabled(s.Result.QuoteOffset < 0).OnClick(func() {
					if s.Full && s.Result.SessionOriginal != nil {
						a.sourceReaderMode(false)
					}
					a.locateSourceOffset(s.Result.QuoteOffset)
				})
				if s.Search != "" {
					ui.Text(c, fmt.Sprintf("%d 处匹配", len(s.Matches))).FontSize(design.Small)
					quietButton(c, "下一处").Key("source-reader-match").Disabled(len(s.Matches) == 0).OnClick(func() { s.Match = (s.Match + 1) % len(s.Matches); a.locateSourceOffset(s.Matches[s.Match]) })
				}
			})
			ui.Scroll(c.Key("source-reader-body")).Grow(1).MinHeight(0).TrackScroll(&s.Scroll).Padding(24).Gap(16).Children(func() {
				v := a.readerRecord()
				ui.Text(c, v.Title).FontSize(design.Title).Bold()
				meta(c, "项目", a.scopeLabel(s.Query.Project))
				meta(c, "Agent", v.Source)
				meta(c, "版本", fmt.Sprintf("v%d · 已持久化原始记录", v.Revision))
				meta(c, "保存时间", v.CreatedAt)
				if v.SourceDetail != nil {
					if native := v.SourceDetail.Native; native != nil {
						meta(c, "原文来源", "本轮 Codex 完整对话与工具消息")
						meta(c, "会话", native.Session)
						meta(c, "CLI", native.CLIVersion)
						meta(c, "开始时间", native.CapturedAt)
					}
					if official := v.SourceDetail.Official; official != nil {
						meta(c, "官方保存", official.OfficialState)
						meta(c, "材料顺序", fmt.Sprintf("%s · 第 %d 项", official.Kind, official.Sequence+1))
						if official.CapturedAt != "" {
							meta(c, "官方记录时间", official.CapturedAt)
						}
					}
					for _, ref := range v.SourceDetail.References {
						u, err := url.Parse(ref)
						if err == nil && u.Scheme == "cognee" {
							parts := strings.Split(strings.Trim(u.Path, "/"), "/")
							if len(parts) == 4 {
								meta(c, "会话", parts[1])
								meta(c, "材料类别", parts[2])
							}
						}
					}
				}
				if s.Result.Quote != "" {
					ui.Text(c, "被引用摘录 · 预览").Bold()
					quote := []rune(s.Result.Quote)
					preview := string(quote[:min(240, len(quote))])
					if len(quote) > 240 {
						preview += "…（完整材料见下方原文）"
					}
					ui.Text(c, preview).Selectable().LineHeight(1.7).Padding(10).Background(design.Surface)
				}
				ui.Divider(c)
				start, end := s.Window.Offsets[s.Window.Page], s.Window.Offsets[s.Window.Page+1]
				if s.FocusLength > 0 && s.FocusOffset >= start && s.FocusOffset < end {
					focusEnd := min(end, s.FocusOffset+s.FocusLength)
					ui.Text(c, body[start:s.FocusOffset]).Key("source-reader-original").Selectable().LineHeight(1.8)
					focus := ui.Text(c, body[s.FocusOffset:focusEnd]).Key("source-reader-focus").Selectable().LineHeight(1.8).Background(design.Heat[1])
					if s.Reveal {
						focus.ScrollIntoView()
						s.Reveal = false
					}
					ui.Text(c, body[focusEnd:end]).Selectable().LineHeight(1.8)
				} else {
					ui.Text(c, body[start:end]).Key("source-reader-original").Selectable().LineHeight(1.8)
				}
				quietButton(c, "技术来源标识").Key("source-reader-technical").OnClick(func() { s.Technical = !s.Technical })
				if s.Technical {
					meta(c, "记录", s.Query.Memory)
					if v.SourceDetail != nil {
						for _, ref := range v.SourceDetail.References {
							ui.Text(c, ref).Selectable().FontSize(design.Small)
						}
					}
				}
			})
		})
	})
}
