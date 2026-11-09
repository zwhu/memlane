package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

func (a *App) returnFromSourceReader() {
	a.closeSourceReader()
	a.reader.ReturnFocus = true
}

func (a *App) sourceReaderReturnLabel() string {
	if a.reader.Query.Owner == a.reader.Query.Memory {
		return "返回原始记录"
	}
	return "返回知识"
}

func (a *App) selectSourceRecord(source records.OrganizationSource) {
	s := &a.reader
	if s.Cancel != nil {
		s.Cancel()
	}
	s.Sequence++
	s.Query.Memory, s.Query.Revision = source.Memory, source.Revision
	s.Result, s.Window, s.Search, s.Matches = records.SourceRecord{}, nil, "", nil
	s.Match, s.FocusOffset, s.FocusLength = 0, 0, 0
	s.Reveal, s.Scroll.Y = false, 0
	s.Error, s.ErrorDetail, s.Loading, s.Technical = "", "", false, false
	s.HadSessionOriginal = false
	a.refreshSourceReader()
}

func (a *App) sourceReaderMessage() string {
	s := &a.reader
	if s.Error != "" {
		return s.Error
	}
	if s.Result.State == "available" && s.Full && s.HadSessionOriginal && s.Result.SessionOriginal == nil {
		return "完整会话暂不可读取，已保留来源身份。可以核对引用片段，或重新读取完整会话。"
	}
	switch s.Result.State {
	case "source_changed":
		return fmt.Sprintf("引用 v%d，当前 v%d 或已失效；来源身份保留，请核对当前来源。", s.Query.Revision, s.Result.CurrentRevision)
	case "owner_changed":
		return "所选知识已更新，请返回当前知识重新打开出处。"
	case "upstream_changed":
		return "来源内容已变化。本地版本保留，请核对并确认同步后重新读取。"
	case "upstream_unavailable":
		return "来源已移除或尚无保存回执。本地副本保留，当前无法读取该来源。"
	default:
		return "正在核验来源与访问权限…"
	}
}

// Keep the project list and selected knowledge visible. Source controls and
// original text belong to the right pane and own their scrolling independently.
func (a *App) sourceReader(c *ui.Context) {
	s := &a.reader
	baseAvailable := !s.Loading && s.Error == "" && s.Result.State == "available" && s.Window != nil
	available := baseAvailable && !(s.Full && s.HadSessionOriginal && s.Result.SessionOriginal == nil)
	ui.Column(c).Key("source-reader-layout").Grow(1).MinHeight(0).MinWidth(0).Children(func() {
		ui.Scroll(c.Key("source-reader-controls")).Shrink(0).MaxHeight(235).Padding(23, 24, 15, 24).Gap(12).Children(func() {
			ui.Text(c, "原始记录").FontSize(10).TextColor(design.Muted)
			title := "原始出处"
			if s.Result.State == "available" {
				title = a.readerRecord().Title
			} else {
				for _, source := range s.Sources {
					if source.Memory == s.Query.Memory {
						title = source.Input.Title
					}
				}
			}
			ui.Text(c, title).Key("source-reader-title").FontSize(design.Title).Bold().MaxLines(2)
			ui.Row(c).Wrap().Gap(6).Children(func() {
				for _, source := range s.Sources {
					if source.Project != s.Query.Project || source.Input.Origin != "raw" {
						continue
					}
					button := quietButton(c, source.Input.Title).Key("source-reader-record-"+source.Memory).Tooltip(source.Input.Title).MaxWidth(230).Height(29).Padding(5, 9).Radius(5).Border(1, design.Border)
					if s.Query.Memory == source.Memory {
						button.Background(design.ListSelected)
					}
					button.OnClick(func() { a.selectSourceRecord(source) })
				}
			})
			if s.Result.SessionOriginal != nil || s.HadSessionOriginal {
				ui.Row(c).Wrap().Gap(8).Children(func() {
					full := quietButton(c, "完整会话").Key("source-reader-full").Padding(4, 8).Disabled(!baseAvailable || s.Result.SessionOriginal == nil).OnClick(func() { a.sourceReaderMode(true) })
					segment := quietButton(c, "引用片段").Key("source-reader-segment").Padding(4, 8).Disabled(!baseAvailable).OnClick(func() { a.sourceReaderMode(false) })
					if s.Full {
						full.Background(design.Surface)
					} else {
						segment.Background(design.Surface)
					}
				})
			}
			ui.Row(c).Gap(8).Children(func() {
				ui.TextInput(c.Key("source-reader-search"), &s.Search).Label("在原文中查找").Placeholder("在原文中查找").Height(32).Grow(1).MinWidth(0).Disabled(!available).OnSubmit(a.searchSource)
				quietButton(c, "查找").Key("source-reader-find").Disabled(!available || strings.TrimSpace(s.Search) == "").OnClick(a.searchSource)
				quietButton(c, "下一处").Key("source-reader-match").Disabled(!available || len(s.Matches) == 0).OnClick(func() { s.Match = (s.Match + 1) % len(s.Matches); a.locateSourceOffset(s.Matches[s.Match]) })
			})
			if s.Search != "" && available {
				ui.Text(c, fmt.Sprintf("%d 处匹配", len(s.Matches))).FontSize(10).TextColor(design.Muted)
			}
		})
		bodyKey := "source-reader-body"
		if !available {
			bodyKey = "source-reader-status"
		}
		bodyScroll := ui.Scroll(c.Key(bodyKey)).Grow(1).MinHeight(0).Padding(20, 24).Gap(15).BorderWidth(1, 0, 0, 0).BorderColor(design.Border)
		if available {
			bodyScroll.TrackScroll(&s.Scroll)
		}
		bodyScroll.Children(func() {
			if !available {
				ui.Text(c, a.sourceReaderMessage()).Key("source-reader-state").FontSize(12).LineHeight(1.8)
				quietButton(c, "重新读取").Key("source-reader-retry").Disabled(s.Loading).OnClick(a.refreshSourceReader)
				if s.ErrorDetail != "" {
					quietButton(c, "查看错误详情").Key("source-reader-technical").OnClick(func() { s.Technical = !s.Technical })
					if s.Technical {
						ui.Text(c, s.ErrorDetail).Selectable().FontSize(10)
					}
				}
				return
			}
			v := a.readerRecord()
			if s.Result.SessionOriginal == nil && v.SourceDetail != nil && v.SourceDetail.Official != nil {
				ui.Text(c, "当前来源片段未附完整会话。").FontSize(10).TextColor(design.Muted)
			}
			body := s.Window.Body
			start, end := s.Window.Offsets[s.Window.Page], s.Window.Offsets[s.Window.Page+1]
			if s.FocusLength > 0 && s.FocusOffset >= start && s.FocusOffset < end {
				focusEnd := min(end, s.FocusOffset+s.FocusLength)
				ui.Text(c, body[start:s.FocusOffset]).Key("source-reader-original").Selectable().LineHeight(1.85)
				focus := ui.Text(c, body[s.FocusOffset:focusEnd]).Key("source-reader-focus").Selectable().LineHeight(1.85).Background(design.Heat[1])
				if s.Reveal {
					focus.ScrollIntoView()
					s.Reveal = false
				}
				ui.Text(c, body[focusEnd:end]).Selectable().LineHeight(1.85)
			} else {
				ui.Text(c, body[start:end]).Key("source-reader-original").Selectable().LineHeight(1.85)
			}
			quietButton(c, "来源详情").Key("source-reader-technical").OnClick(func() { s.Technical = !s.Technical })
			if s.Technical {
				a.sourceReaderDetails(c, v)
			}
		})
		ui.Column(c).Shrink(0).Padding(10, 20).Gap(6).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
			if available {
				v := a.readerRecord()
				ui.Text(c, fmt.Sprintf("%s · v%d · 第 %d / %d 段", v.Source, v.Revision, s.Window.Page+1, len(s.Window.Offsets)-1)).FontSize(10).TextColor(design.Muted)
			}
			ui.Row(c).Wrap().Gap(8).Children(func() {
				quietButton(c, "上一段").Key("source-reader-previous").Disabled(!available || s.Window.Page == 0).OnClick(func() { s.Window.Page--; s.Scroll.Y = 0 })
				quietButton(c, "下一段").Key("source-reader-next").Disabled(!available || s.Window.Page >= len(s.Window.Offsets)-2).OnClick(func() { s.Window.Page++; s.Scroll.Y = 0 })
				quietButton(c, "定位引用").Key("source-reader-quote").Disabled(!available || s.Result.QuoteOffset < 0).OnClick(func() {
					if s.Full && s.Result.SessionOriginal != nil {
						a.sourceReaderMode(false)
					}
					a.locateSourceOffset(s.Result.QuoteOffset)
				})
				ui.Spacer(c)
				quietButton(c, "复制全文").Key("source-reader-copy").Disabled(!available).OnClick(func() { c.WriteClipboard(s.Window.Body) })
				quietButton(c, a.sourceReaderReturnLabel()).Key("source-reader-return").OnClick(a.returnFromSourceReader)
			})
		})
	})
}

func (a *App) sourceReaderDetails(c *ui.Context, v records.Version) {
	s := &a.reader
	meta(c, "项目", a.scopeLabel(s.Query.Project))
	meta(c, "保存时间", v.CreatedAt)
	meta(c, "来源记录", v.Memory)
	if v.SourceDetail != nil {
		if native := v.SourceDetail.Native; native != nil {
			meta(c, "会话", native.Session)
			meta(c, "Agent版本", native.CLIVersion)
		}
		if official := v.SourceDetail.Official; official != nil {
			meta(c, "来源状态", official.OfficialState)
			meta(c, "材料顺序", fmt.Sprintf("%s · 第 %d 项", official.Kind, official.Sequence+1))
		}
		for _, ref := range v.SourceDetail.References {
			ui.Text(c, ref).Selectable().FontSize(10)
		}
	}
}
