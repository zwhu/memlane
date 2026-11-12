package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

func (a *App) returnFromSourceReader() {
	direct := a.reader.DirectRaw
	a.closeSourceReader()
	if direct {
		a.switchReadingKind("knowledge")
		return
	}
	a.reader.ReturnFocus = true
}

func (a *App) sourceReaderReturnLabel() string {
	if !a.reader.DirectRaw && a.reader.Query.Owner == a.reader.Query.Memory {
		return "返回原始记录"
	}
	return "返回知识"
}

func (a *App) selectSourceRecord(source records.OrganizationSource) {
	s := &a.reader
	if source.Memory == s.Query.Memory && source.Revision == s.Query.Revision && !sourceReaderCanRetry(*s) {
		return
	}
	if s.Cancel != nil {
		s.Cancel()
	}
	s.Sequence++
	s.Query.Memory, s.Query.Revision = source.Memory, source.Revision
	s.Result, s.Window, s.Search, s.Matches = records.SourceRecord{}, nil, "", nil
	s.Match, s.FocusOffset, s.FocusLength = 0, 0, 0
	s.Reveal, s.Scroll.Y = false, 0
	s.Error, s.ErrorDetail, s.ErrorCode, s.Loading, s.Technical = "", "", "", false, false
	s.HadSessionOriginal = false
	a.refreshSourceReader()
}

func (a *App) sourceReaderMessage() string {
	s := &a.reader
	if s.Loading {
		return "正在核验来源与访问权限…"
	}
	if s.Error != "" {
		return s.Error
	}
	if s.Result.State == "available" && s.Full && s.HadSessionOriginal && s.Result.SessionOriginal == nil {
		switch s.Result.OriginalState {
		case "source_changed", "owner_changed", "upstream_changed":
			return "完整会话来源已变化，旧完整正文不可读。请返回知识核对同步；也可明确选择已核验的引用片段。"
		case "upstream_unavailable":
			return "完整会话来源缺失或尚无保存回执。请返回知识核对来源；也可明确选择已核验的引用片段。"
		}
		return "完整会话暂不可读取，已保留来源身份。可以核对引用片段，或重新读取完整会话。"
	}
	switch s.Result.State {
	case "source_changed":
		if s.Result.CurrentRevision < 1 {
			return fmt.Sprintf("引用 v%d；当前来源版本未知或已失效，请返回知识核对来源。", s.Query.Revision)
		}
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
	if !s.Loading && sourceReaderPermissionBlocked(*s) {
		a.sourceReaderPermissionPage(c)
		return
	}
	// Keep the last verified content mounted during background verification.
	// Failed or changed-source receipts still replace it immediately.
	baseAvailable := (s.Error == "" || sourceReaderTransientFailure(s.ErrorCode)) && s.Result.State == "available" && s.Window != nil
	available := baseAvailable && !(s.Full && s.HadSessionOriginal && s.Result.SessionOriginal == nil)
	ui.Column(c).Key("source-reader-layout").Grow(1).MinHeight(0).MinWidth(0).Children(func() {
		ui.Scroll(c.Key("source-reader-controls")).Shrink(0).MaxHeight(265).Padding(29, 34, 15, 34).Gap(14).Children(func() {
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
			ui.Text(c, title).Key("source-reader-title").FontSize(23).FontWeight(550).MaxLines(2)
			if available && s.Error != "" {
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, "刷新暂未完成，正在显示上次核验的内容。").FontSize(10).TextColor(design.Muted).Grow(1).MinWidth(0)
					quietButton(c, "重试刷新").Key("source-reader-refresh-retry").Disabled(s.Loading).OnClick(a.refreshSourceReader)
				})
			}
			if s.DirectRaw && available {
				v := a.readerRecord()
				ui.Text(c, fmt.Sprintf("%s · %s · v%d · %s", a.scopeLabel(s.Query.Project), sourceDisplayName(v.Source), v.Revision, knowledgeState(displayVersion(v)))).FontSize(10).TextColor(design.Muted)
			}
			ui.Row(c).Wrap().Gap(6).Children(func() {
				for _, source := range s.Sources {
					if source.Project != s.Query.Project || source.Input.Origin != "raw" {
						continue
					}
					button := quietButton(c, source.Input.Title).Key("source-reader-record-"+source.Memory).Tooltip(source.Input.Title).MaxWidth(230).Height(29).Padding(5, 9).Radius(5).Border(1, design.Border)
					if s.Query.Memory == source.Memory {
						button.Background(design.ListSelected)
						if !sourceReaderCanRetry(*s) {
							button.Disabled(true)
						}
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
				ui.TextInput(c.Key("source-reader-search"), &s.Search).Label("在原文中查找").Placeholder("在原文中查找").Height(38).Grow(1).MinWidth(0).Disabled(!available).OnSubmit(a.searchSource)
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
		bodyScroll := ui.Scroll(c.Key(bodyKey)).Grow(1).MinHeight(0).Padding(20, 34).Gap(15)
		if available {
			bodyScroll.TrackScroll(&s.Scroll)
		}
		bodyScroll.Children(func() {
			if !available {
				ui.Text(c, a.sourceReaderMessage()).Key("source-reader-state").FontSize(12).LineHeight(1.8)
				if sourceReaderCanRetry(*s) {
					quietButton(c, "重新读取").Key("source-reader-retry").Disabled(s.Loading).OnClick(a.refreshSourceReader)
				} else if !s.Loading {
					ui.Text(c, "当前状态不能原地重试；返回知识后核对版本或授权。").FontSize(10).TextColor(design.Muted)
				}
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
				// Inline children share one shaped paragraph, including original newlines.
				// Reveal uses inline glyph bounds: SDK ScrollIntoView promotes an
				// inline child to its whole paragraph, which loses long-paragraph hits.
				ui.RichText(c).Key("source-reader-original").Selectable().FontSize(13).TextColor(ui.Hex("#536477")).LineHeight(1.95).Children(func() {
					ui.Text(c, body[start:s.FocusOffset])
					focus := ui.Text(c, body[s.FocusOffset:focusEnd]).Key("source-reader-focus-" + records.MergeDigest([]any{body[start:end], s.FocusOffset, s.FocusLength})).Label("原文当前匹配").Background(design.Heat[1])
					if s.Reveal {
						// Inline bounds are from the previous layout. A distinct match key
						// waits one frame instead of using the previous match's geometry.
						match, viewport := focus.LayoutBounds(), bodyScroll.LayoutBounds()
						if s.RevealLayout && match.H > 0 && viewport.H > 0 {
							if match.Y < viewport.Y+8 {
								s.Scroll.Y += match.Y - (viewport.Y + 8)
							} else if match.Y+match.H > viewport.Y+viewport.H-8 {
								s.Scroll.Y += match.Y + match.H - (viewport.Y + viewport.H - 8)
							}
							s.Scroll.Y = max(0, s.Scroll.Y)
							s.Reveal = false
						}
						s.RevealLayout = true
						c.After(0)
					}
					ui.Text(c, body[focusEnd:end])
				})
			} else {
				ui.Text(c, body[start:end]).Key("source-reader-original").Selectable().FontSize(13).TextColor(ui.Hex("#536477")).LineHeight(1.95)
			}
			if s.DirectRaw {
				ui.Text(c, "保留原始过程，提炼后的知识单独保存。").FontSize(10).TextColor(design.Muted)
			}
			quietButton(c, "来源详情").Key("source-reader-technical").OnClick(func() { s.Technical = !s.Technical })
			if s.Technical {
				a.sourceReaderDetails(c, v)
				if s.DirectRaw {
					quietButton(c, "编辑原始记录").Disabled(v.State != "active" || v.SourceDetail != nil && (v.SourceDetail.Official != nil || v.SourceDetail.Native != nil)).OnClick(func() { a.closeSourceReader(); a.editMemory() })
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
					quietButton(c, "复制全文").Key("source-reader-copy").Disabled(!available).OnClick(func() { c.WriteClipboard(s.Window.Body) })
				})
			}
		})
		ui.Row(c).Shrink(0).MinHeight(58).Padding(12, 28).Gap(10).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
			if s.DirectRaw {
				ui.Text(c, a.scopeLabel(s.Query.Project)+" · 原始材料").FontSize(10).TextColor(design.Muted).Grow(1).MinWidth(0)
				quietButton(c, "复制正文").Disabled(!available).OnClick(func() { c.WriteClipboard(s.Window.Body) })
				return
			}
			ui.Text(c, "当前知识与选中结果保持").FontSize(10).TextColor(design.Muted).Grow(1).MinWidth(0)
			quietButton(c, a.sourceReaderReturnLabel()).Key("source-reader-return").OnClick(a.returnFromSourceReader)
		})
	})
}

func sourceReaderPermissionBlocked(s SourceReaderUI) bool {
	switch s.ErrorCode {
	case "scope_denied", "permission_denied", "connection_revoked", "authorization_required", "cognee_authorization_required":
		return true
	}
	return false
}

// The permission scene keeps the current common knowledge/project selection,
// but shows no cached source title, paragraph, search controls or copy action.
func (a *App) sourceReaderPermissionPage(c *ui.Context) {
	ui.Column(c).Key("source-reader-permission").Grow(1).MinHeight(0).MinWidth(0).Children(func() {
		ui.Scroll(c.Key("source-permission-body")).Grow(1).MinHeight(0).Padding(60, 24).Gap(24).Children(func() {
			ui.Column(c).Width(68).Height(74).AlignItems(ui.Center).Justify(ui.Center).Border(1, design.Border).Radius(10).Background(design.Surface).Children(func() {
				ui.Icon(c, icons["全部记忆"]).FontSize(25).TextColor(design.Muted)
			})
			ui.Text(c, "没有这个项目的访问权限").Key("source-permission-title").FontSize(design.Heading).Bold()
			ui.Text(c, "来源关联仍保留，原文不在当前授权范围内。").FontSize(12).TextColor(design.Muted).LineHeight(1.8)
		})
		ui.Row(c).Shrink(0).Wrap().Padding(14, 24).Gap(10).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
			ui.Text(c, "当前知识与选中结果保持").FontSize(10).TextColor(design.Muted).Grow(1).MinWidth(0)
			quietButton(c, a.sourceReaderReturnLabel()).Key("source-reader-return").OnClick(a.returnFromSourceReader)
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
