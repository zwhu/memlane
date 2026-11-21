package simpledesktop

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type profileParsedEntry struct {
	Date string
	Text string
}

type profileParsedSection struct {
	Heading string
	Items   []profileParsedEntry
}

var profileDatedLineRe = regexp.MustCompile(`^(?:[-*]\s+)?\[(\d{4}-\d{2}-\d{2})\]\s*[-–—]\s*(.+)$`)

// parseProfileDisplaySections splits display text into ## headings and
// "[YYYY-MM-DD] - ..." dated entries. Returns (sections, true) only when at
// least one dated entry is found; otherwise callers fall back to Markdown.
func parseProfileDisplaySections(body string) ([]profileParsedSection, bool) {
	lines := strings.Split(body, "\n")
	sections := make([]profileParsedSection, 0, 4)
	current := profileParsedSection{}
	datedCount := 0
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "# ") {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			if current.Heading != "" || len(current.Items) > 0 {
				sections = append(sections, current)
			}
			current = profileParsedSection{Heading: strings.TrimSpace(strings.TrimPrefix(line, "## "))}
			continue
		}
		if m := profileDatedLineRe.FindStringSubmatch(line); m != nil {
			datedCount++
			current.Items = append(current.Items, profileParsedEntry{
				Date: m[1],
				Text: strings.TrimSpace(m[2]),
			})
			continue
		}
		cleaned := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "- "), "* "))
		current.Items = append(current.Items, profileParsedEntry{Text: cleaned})
	}
	if current.Heading != "" || len(current.Items) > 0 {
		sections = append(sections, current)
	}
	if datedCount == 0 {
		return nil, false
	}
	return sections, true
}

func (s *userProfilesState) cachedDisplaySections() (unwrapped string, sections []profileParsedSection, structured bool, entryCount int) {
	if s.Body == "" {
		return "", nil, false, 0
	}
	if s.parsedBodyKey == s.BodyKey && s.parsedBodyRaw == s.Body {
		return s.parsedUnwrapped, s.parsedSections, s.parsedStructured, s.parsedCount
	}
	unwrapped = profileReadingMarkdown(s.Body)
	sections, structured = parseProfileDisplaySections(unwrapped)
	if structured {
		for _, sec := range sections {
			entryCount += len(sec.Items)
		}
	}
	s.parsedBodyKey = s.BodyKey
	s.parsedBodyRaw = s.Body
	s.parsedUnwrapped = unwrapped
	s.parsedSections = sections
	s.parsedStructured = structured
	s.parsedCount = entryCount
	return unwrapped, sections, structured, entryCount
}

func profileUpdatedDay(ts float64) string {
	if ts <= 0 {
		return ""
	}
	t := time.Unix(int64(ts), 0).In(time.Local)
	now := time.Now().In(time.Local)
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return "今天"
	}
	return t.Format("2006-01-02")
}

func profileSourceMeta(p userProfile, entryCount int) (text string, fail bool, dotState string) {
	switch {
	case p.Status == "generating":
		return fmt.Sprintf("正在导出 · %d 秒", p.Progress.Elapsed), false, "run"
	case p.Status == "saving":
		return "正在保存", false, "run"
	case p.Status == "queued":
		return "等待导出", false, "run"
	case p.Status == "failed":
		return "导入未完成", true, "fail"
	case p.Status == "unknown":
		return "结果待核对", true, "warn"
	case p.Status == "ready":
		day := profileUpdatedDay(p.Updated)
		if day == "" {
			day = "已导入"
		}
		suffix := "自动生成"
		if p.Method == "manual" {
			suffix = "手动导入"
		}
		if entryCount > 0 {
			if p.Method == "manual" {
				suffix = fmt.Sprintf("手动导入 · %d 条", entryCount)
			} else {
				suffix = fmt.Sprintf("%d 条", entryCount)
			}
		}
		return day + " · " + suffix, false, "ok"
	default:
		if p.CanGenerate {
			return "尚未导入", false, ""
		}
		return "待手动导入", false, ""
	}
}

func (a *App) userProfilesPage(c *ui.Context) {
	s := &a.userProfiles
	if !time.Now().Before(s.Next) && a.page == "用户画像" {
		a.refreshUserProfiles()
	}

	pageRow := ui.Row(c.Key("user-profiles-page")).Fill().MinHeight(0)
	// Window-level ↑ / ↓ navigation across profile sources when no dialog is open,
	// so switching works immediately upon entering from the sidebar.
	if len(s.Directory.Profiles) > 0 && !s.Open && !a.searchUI.Open {
		if c.Shortcut(0, ui.KeyDown) {
			a.stepUserProfile(1)
		}
		if c.Shortcut(0, ui.KeyUp) {
			a.stepUserProfile(-1)
		}
	}

	pageRow.Children(func() {
		a.userProfileSourceColumn(c)
		a.userProfileReaderPane(c)
	})
}

func (a *App) stepUserProfile(delta int) {
	profiles := a.userProfiles.Directory.Profiles
	if len(profiles) == 0 {
		return
	}
	idx := 0
	for n, p := range profiles {
		if p.ID == a.userProfiles.Selected {
			idx = n
			break
		}
	}
	next := (idx + delta + len(profiles)) % len(profiles)
	a.selectUserProfile(profiles[next].ID)
}

func (a *App) userProfileSourceColumn(c *ui.Context) {
	s := &a.userProfiles
	ui.Column(c.Key("profile-source-col")).Width(216).Shrink(0).FillHeight().
		BorderWidth(0, 1, 0, 0).BorderColor(tok.Border).Background(tok.Bg).Children(func() {
		// Header (36px)
		ui.Row(c).Height(36).Shrink(0).Padding(0, sp3, 0, sp4).AlignItems(ui.Center).
			BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
			ui.Text(c, "来源").Font("system-ui").FontSize(fsCaption).FontWeight(600).TextColor(tok.Text3)
			ui.Spacer(c)
			refreshAction(c, "刷新画像列表", func() { s.BodyKey = ""; a.refreshUserProfiles() }).Disabled(s.Loading)
		})

		// Source list with paint-only spring selection indicator
		var ind ui.Element
		var activeBtn ui.Element
		var listHost ui.Element
		hasList := false
		ui.Scroll(c.Key("profile-sources")).Grow(1).MinHeight(0).Padding(sp2).Children(func() {
			if !s.Loaded {
				v4SkeletonGroup(c, 4, 2, true, false)
				return
			}
			hasList = true
			listHost = ui.Column(c.Key("profile-sources-host")).FillWidth().MinWidth(0)
			listHost.Children(func() {
				ui.Box(c.Key("profile-ind-bg")).Size(0, 0).Shrink(0).PassThrough().Children(func() {
					ind = ui.Box(c.Key("profile-ind")).Width(216 - 2*sp2).Height(52).
						Radius(radius).Background(tok.AccentSoft).PassThrough()
				})
				previousGroup := ""
				for _, profile := range s.Directory.Profiles {
					p := profile
					group := "编码 Agent"
					if p.Kind == "manual" {
						group = "其他 AI"
					}
					if group != previousGroup {
						topPad := sp3
						if previousGroup == "" {
							topPad = sp1
						}
						ui.Text(c, group).Font("system-ui").FontSize(fsCaption).FontWeight(600).
							TextColor(tok.Text3).Padding(topPad, sp2, sp1, sp2)
						previousGroup = group
					}
					on := s.Selected == p.ID
					entryCount := 0
					if on && s.Body != "" {
						_, _, _, entryCount = s.cachedDisplaySections()
					}
					metaText, isFail, dotState := profileSourceMeta(p, entryCount)

					btn := ui.ButtonBase(c.Key("profile-source-"+p.ID)).Role(ui.RoleButton).
						Label(p.Name+"用户画像").Cursor(ui.CursorPointer).
						FillWidth().MinHeight(52).Padding(sp2, sp2).Gap(10).
						Justify(ui.Start).AlignItems(ui.Center).BorderWidth(0).Radius(radius)
					if on {
						btn.Background(ui.Transparent)
						activeBtn = btn
					} else {
						btn.Background(homeHoverColor(btn, "profile-src-bg", ui.Transparent, tok.Hover))
					}
					v4ButtonPress(btn, 0.99)
					nameCol := tok.Text1
					if on {
						nameCol = tok.Accent
					}
					metaCol := tok.Text3
					if isFail {
						metaCol = tok.Danger
					}
					btn.Children(func() {
						agentLogo(c, agentBrandIconFor(p.Kind, p.Name), 22)
						ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
							ui.Row(c).FillWidth().MinWidth(0).AlignItems(ui.Center).Gap(6).Children(func() {
								ui.Text(c, p.Name).Font("system-ui").FontSize(fsBody).FontWeight(500).
									TextColor(nameCol).SingleLine().Grow(1).MinWidth(0)
								if dotState != "" {
									v4StatusDot(c, dotState).Tooltip(p.label())
								}
							})
							ui.Text(c, metaText).Font("system-ui").FontSize(fsCaption).FontFeatures("tnum").
								TextColor(metaCol).SingleLine()
						})
					}).OnClick(func() { a.selectUserProfile(p.ID) })
				}
			})
		})
		if hasList {
			hostBox := listHost.Bounds()
			rowBox := activeBtn.Bounds()
			hasActive := rowBox.H > 0 && hostBox.W > 0
			targetY := s.lastIndY
			fade := float32(1)
			if hasActive {
				targetY = rowBox.Y - hostBox.Y
				s.lastIndY = targetY
				fade = 0
				ind.Width(rowBox.W).Height(rowBox.H)
			}
			ind.AnimatePose(ui.Pose{Y: targetY, Scale: 1, Fade: fade}, v4Snappy)
		}
	})
}

func (a *App) userProfileReaderPane(c *ui.Context) {
	s := &a.userProfiles
	ui.Column(c.Key("profile-reader-pane")).Grow(1).MinWidth(0).FillHeight().Background(tok.Bg).Children(func() {
		p, ok := a.selectedUserProfile()
		if !ok {
			v4EmptyState(c, "profile-empty-select", "选择一个 AI，查看它对你的了解", s.Message)
			return
		}
		unwrapped, sections, structured, entryCount := s.cachedDisplaySections()
		if s.docKey != p.ID {
			s.docKey = p.ID
			s.docEnter++
		}

		// Scrollable reading body (680px max width, smooth fade-in on switch)
		ui.Scroll(c.Key("user-profile-document-"+p.ID+"-"+s.BodyKey)).Grow(1).MinHeight(0).
			TrackScroll(&s.Scroll).Padding(sp6, sp10, sp8, sp10).Children(func() {
			docKey := fmt.Sprintf("profile-doc-%d-%s", s.docEnter, p.ID)
			doc := ui.Column(c.Key(docKey)).FillWidth().MaxWidth(readerMaxW).Gap(sp4)
			a.poseEnter.apply(c, doc, "profile-doc", s.docEnter, ui.Pose{Y: 6, Scale: 1, Fade: 1}, ui.Pose{Scale: 1}, 0, v4Smooth)
			doc.Children(func() {
				// Top meta bar
				groupLabel := "编码 Agent"
				if p.Kind == "manual" {
					groupLabel = "其他 AI"
				}
				methodLabel := p.label()
				if p.Status == "ready" {
					if p.Method == "manual" {
						methodLabel = "手动导入"
					} else {
						methodLabel = "自动生成"
					}
				} else if p.Status == "failed" {
					methodLabel = "导入未完成"
				}
				ui.Row(c).FillWidth().Padding(0, 0, sp4, 0).Gap(sp2).AlignItems(ui.Center).
					BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
					agentLogo(c, agentBrandIconFor(p.Kind, p.Name), 22)
					ui.Text(c, p.Name+" 对你的了解").Font("system-ui").FontSize(fsHeading).FontWeight(600).TextColor(tok.Text1).SingleLine()
					homeText(c, "·", homeMeta)
					homeText(c, groupLabel, homeMeta).SingleLine()
					homeText(c, "·", homeMeta)
					v4CategoryChip(c, methodLabel)
					if day := profileUpdatedDay(p.Updated); day != "" {
						homeText(c, "更新于 "+day, homeMeta).FontFeatures("tnum").SingleLine()
					}
					if entryCount > 0 {
						homeText(c, "·", homeMeta)
						homeText(c, fmt.Sprintf("%d 条记录", entryCount), homeMeta).FontFeatures("tnum").SingleLine()
					}
				})

				// Progress indicator when generating
				if p.Status == "generating" {
					label := fmt.Sprintf("正在导出 · %d 秒", p.Progress.Elapsed)
					if p.Progress.Idle >= 30 {
						label = fmt.Sprintf("等待 Agent 返回 · %d 秒，%d 秒未收到新输出", p.Progress.Elapsed, p.Progress.Idle)
					}
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
						v4StageBlocks(c)
						homeText(c, label, homeBody)
					})
					v4SkeletonGroup(c, 4, 2, false, false)
				}

				// Error / warning banner
				message := p.failureLabel()
				if message == "" && !p.working() {
					message = s.Message
				}
				if message != "" {
					ui.Box(c).FillWidth().Padding(sp3).Radius(radius).Background(v4DangerSoftBg()).Children(func() {
						homeText(c, message, homeBody).TextColor(tok.Danger)
					})
				}

				// Body content: structured date-column entries or Markdown fallback
				switch {
				case s.Body != "" && structured:
					ui.Column(c.Key("user-profile-structured-"+s.BodyKey)).FillWidth().Gap(sp6).Children(func() {
						for secIdx, sec := range sections {
							section := sec
							ui.Column(c.Key(fmt.Sprintf("profile-sec-%d", secIdx))).FillWidth().Children(func() {
								if section.Heading != "" {
									ui.Text(c, section.Heading).Font("system-ui").FontSize(fsCaption).FontWeight(600).
										TextColor(tok.Text3).FillWidth().Padding(0, 0, sp2, 0).
										BorderWidth(0, 0, 1, 0).BorderColor(tok.Border)
								}
								for itemIdx, it := range section.Items {
									if it.Date != "" {
										ui.Row(c.Key(fmt.Sprintf("profile-entry-%d-%d", secIdx, itemIdx))).
											FillWidth().Padding(10, 0).Gap(sp4).AlignItems(ui.Start).
											BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
											ui.Text(c, it.Date).Font("SF Mono").FontSize(fsCaption).FontFeatures("tnum").
												TextColor(tok.Text3).Width(78).Shrink(0).SingleLine().Margin(3, 0, 0, 0)
											v4InlineMarkdown(c, it.Text, fsReading, 400, 1.75, tok.Text1).Grow(1).MinWidth(0)
										})
									} else {
										v4InlineMarkdown(c, it.Text, fsReading, 400, 1.8, tok.Text1).Margin(sp2, 0, 0, 0)
									}
								}
							})
						}
					})
				case s.Body != "":
					a.commonMarkdown(c, "user-profile-markdown-"+s.BodyKey, unwrapped)
				case !p.working():
					emptyMsg := fmt.Sprintf("尚未从 %s 导入画像，可点击底部「尝试导入」或「手动导入」", p.Name)
					if !p.CanGenerate {
						emptyMsg = "该来源支持手动导入：复制提示词后粘贴画像正文"
					}
					switch p.Status {
					case "failed":
						emptyMsg = "可点击下方「重新导入」重试，或使用「手动导入」粘贴正文"
					case "unknown":
						emptyMsg = "上次导入结果待核对，请等待确认或使用底部核对操作"
					}
					v4EmptyState(c, "profile-empty-body-"+p.ID, emptyMsg, "")
				}
			})
		})

		// Bottom status bar with actions: 重新导入 / 手动导入 / 复制正文
		a.userProfileFooterBar(c, p)
	})
}

func (a *App) userProfileFooterBar(c *ui.Context, p userProfile) {
	s := &a.userProfiles
	automatic := "尝试导入"
	if p.Document != nil || p.Status == "failed" {
		automatic = "重新导入"
	}
	if p.Status == "unknown" && !p.SaveUnknown {
		automatic = "结果待确认"
	}
	if p.working() {
		automatic = "停止导入"
	}
	if p.SaveUnknown {
		automatic = "核对保存结果"
	}
	automaticLabel := "从" + p.Name + "导入画像"
	if p.working() {
		automaticLabel = "停止从" + p.Name + "导入画像"
	}
	if p.Status == "saving" {
		automatic = "保存中…"
		automaticLabel = "保存" + p.Name + "画像"
	}

	appFooter(c, "用户画像操作栏").Padding(0, sp6).Gap(sp2).Children(func() {
		homeText(c, "独立来源 · 仅作个人参考", homeMeta)
		ui.Spacer(c)
		if time.Now().Before(s.CopyUntil) {
			homeText(c, s.Copied+"已复制", homeMeta)
		}
		if p.CanGenerate || p.SaveUnknown || p.working() || p.Status == "unknown" {
			onAuto := func() {
				if p.working() {
					a.userProfileAction(p, "cancel", "")
				} else if p.SaveUnknown {
					a.userProfileAction(p, "reconcile", "")
				} else {
					a.userProfileAction(p, "generate", "")
				}
			}
			disabled := s.Busy || p.Status == "saving" || (!p.CanGenerate && !p.SaveUnknown) || (p.Status == "unknown" && !p.SaveUnknown)
			if p.working() {
				v4DangerOutlineButton(c, automatic, false, onAuto).Label(automaticLabel).Tooltip("使用 Agent 默认模型").Disabled(disabled)
			} else {
				v4OutlineButton(c, automatic, v4IconRefresh, onAuto).Label(automaticLabel).Tooltip("使用 Agent 默认模型").Disabled(disabled)
			}
		}
		v4OutlineButton(c, "手动导入", v4IconUpload, func() { a.beginProfileImport(p) }).
			Label("导入此来源画像").Disabled(s.Busy || p.working() || p.SaveUnknown)
		v4OutlineButton(c, "复制正文", v4IconCopy, func() {
			c.WriteClipboard(s.Body)
			s.Copied = "正文"
			s.CopyUntil = time.Now().Add(3 * time.Second)
		}).Disabled(s.Body == "")
	})
}

func (a *App) userProfileImportDialog(c *ui.Context) {
	s := &a.userProfiles
	if !s.Open {
		return
	}
	var source userProfile
	for _, p := range s.Directory.Profiles {
		if p.ID == s.DraftSource {
			source = p
		}
	}
	v4Dialog(c, &s.Open, v4DialogOptions{
		Key:         "user-profile-import-" + s.DraftSource,
		Title:       "手动导入 " + source.Name + " 用户画像",
		Width:       v4DialogLarge,
		ScrollBody:  true,
		Hint:        "支持 ## 二级标题与「- [YYYY-MM-DD] - 内容」格式",
		CancelText:  "取消",
		ConfirmText: "保存画像",
		ConfirmDisabled: s.Busy || strings.TrimSpace(s.Draft) == "" || len(s.Draft) > 256*1024 || source.ID == "",
		Leading: func() {
			agentLogo(c, agentBrandIconFor(source.Kind, source.Name), 22)
		},
		Body: func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
				homeText(c, "先将提示词发给 "+source.Name+"，再把返回的 Markdown 粘贴到下方：", homeBody).Grow(1).MinWidth(0)
				v4SmallButton(c, "复制提示词", v4IconCopy, func() { c.WriteClipboard(s.Directory.Prompt) }).Disabled(s.Directory.Prompt == "")
			})
			ui.TextArea(c.Key("profile-import-body-"+s.DraftSource), &s.Draft).
				Label("用户画像文档").Placeholder("粘贴 "+source.Name+" 返回的用户画像正文…").
				FillWidth().Height(220).FontSize(fsBody).Disabled(s.Busy).
				OnChange(func() { s.Drafts[s.DraftSource] = s.Draft })
			if s.Message != "" {
				homeText(c, s.Message, homeMeta).TextColor(tok.Danger)
			}
		},
		OnConfirm: func() {
			a.userProfileAction(source, "import", s.Draft)
		},
	})
}
