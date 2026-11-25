package simpledesktop

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/zwhu/memlane/app/reading"
)

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

func profileSourceMeta(p userProfile) (text string, fail bool, dotState string) {
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

	pageCol := ui.Column(c.Key("user-profiles-page")).Fill().MinHeight(0)
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

	pageCol.Children(func() {
		readingSplit(c, "profile-reading-split", &a.layout.ProfileListWidth, func() {
			a.userProfileSourceColumn(c)
		}, func() {
			a.userProfileReaderPane(c)
		})
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
	ui.Column(c.Key("profile-source-col")).Fill().Background(tok.Bg).Children(func() {
		// Header (36px)
		ui.Row(c).Label("用户画像来源头部").Height(listHeaderH).Shrink(0).Padding(0, sp3, 0, sp4).AlignItems(ui.Center).
			BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
			ui.Text(c, "来源").Font("system-ui").FontSize(fsBody).FontWeight(550).TextColor(tok.Text1).SingleLine()
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
				indW := a.layout.ProfileListWidth
				if indW <= 0 {
					indW = listPaneW
				}
				ui.Box(c.Key("profile-ind-bg")).Size(0, 0).Shrink(0).PassThrough().Children(func() {
					ind = ui.Box(c.Key("profile-ind")).Width(max(float32(180), indW-2*sp2)).Height(52).
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
					metaText, isFail, dotState := profileSourceMeta(p)

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
									v4StatusDot(c, dotState).Tooltip(p.Label())
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
			targetY := s.LastIndicatorY
			fade := float32(1)
			if hasActive {
				targetY = rowBox.Y - hostBox.Y
				s.LastIndicatorY = targetY
				fade = 0
				ind.Width(rowBox.W).Height(rowBox.H)
			}
			ind.AnimatePose(ui.Pose{Y: targetY, Scale: 1, Fade: fade}, v4Snappy)
		}
		appFooter(c, "用户画像来源状态").Padding(0, sp4).Children(func() {
			if !s.Loaded && s.Loading {
				homeText(c, "正在读取…", homeMeta)
			} else {
				homeText(c, fmt.Sprintf("共 %d 个来源", len(s.Directory.Profiles)), homeMeta)
			}
		})
	})
}

var (
	profileReaderThemeBase *ui.Theme
	profileReaderThemeVal  *ui.Theme
)

func profileReaderListTheme() *ui.Theme {
	if profileReaderThemeVal != nil && profileReaderThemeBase == homeTheme {
		return profileReaderThemeVal
	}
	t := *homeTheme
	t.SurfaceHover = ui.Transparent
	t.SurfacePressed = ui.Transparent
	t.Accent = ui.Transparent
	t.AccentText = tok.Text1
	profileReaderThemeBase = homeTheme
	profileReaderThemeVal = &t
	return profileReaderThemeVal
}

var splitProfileMarkdownChunks = reading.SplitProfileMarkdownChunks

func profileCachedMarkdownChunks(s *userProfilesState) (string, []string) {
	return s.Markdown.Resolve(s.BodyKey, s.Body)
}

func (a *App) userProfileReaderHeader(c *ui.Context, p userProfile, s *userProfilesState) {
	// Top meta bar
	groupLabel := "编码 Agent"
	if p.Kind == "manual" {
		groupLabel = "其他 AI"
	}
	methodLabel := p.Label()
	if p.Status == "ready" {
		if p.Method == "manual" {
			methodLabel = "手动导入"
		} else {
			methodLabel = "自动生成"
		}
	} else if p.Status == "failed" {
		methodLabel = "导入未完成"
	}
	ui.Row(c).Label("用户画像内容工具栏").FillWidth().MinHeight(28).Padding(0, 0, sp4, 0).Gap(sp2).AlignItems(ui.Center).
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
	message := p.FailureLabel()
	if message == "" && !p.Working() {
		message = s.Message
	}
	if message != "" {
		ui.Box(c).FillWidth().Padding(sp3).Radius(radius).Background(v4DangerSoftBg()).Children(func() {
			homeText(c, message, homeBody).TextColor(tok.Danger)
		})
	}
}

func (a *App) userProfileReaderPane(c *ui.Context) {
	s := &a.userProfiles
	ui.Column(c.Key("profile-reader-pane")).Grow(1).MinWidth(0).MinHeight(0).FillHeight().Background(tok.Bg).Children(func() {
		p, ok := a.selectedUserProfile()
		if !ok {
			v4EmptyState(c, "profile-empty-select", "选择一个 AI，查看它对你的了解", s.Message)
			appFooter(c, "用户画像操作栏").Padding(0, sp6).Children(func() {
				homeText(c, "独立来源 · 仅作个人参考", homeMeta)
			})
			return
		}
		if s.DocumentKey != p.ID {
			s.DocumentKey = p.ID
			s.DocumentEnter++
		}

		normalized, chunks := profileCachedMarkdownChunks(s)
		if s.Body != "" && len(chunks) > 1 {
			ui.Themed(c, profileReaderListTheme()).Grow(1).MinHeight(0).FillWidth().Background(ui.Transparent).Children(func() {
				reading.RenderProfileWithHeader(c, p.ID, s.BodyKey, chunks, &s.ReaderList, &s.Scroll, readerMaxW,
					a.v4MarkdownOptions(fsReading), func() { a.userProfileReaderHeader(c, p, s) })
			})
			a.userProfileFooterBar(c, p)
			return
		}

		// Scrollable reading body (680px max width, smooth fade-in on switch)
		ui.Scroll(c.Key("user-profile-document-"+p.ID+"-"+s.BodyKey)).Grow(1).MinHeight(0).
			TrackScroll(&s.Scroll).Padding(sp6, sp10, sp8, sp10).Children(func() {
			docKey := fmt.Sprintf("profile-doc-%d-%s", s.DocumentEnter, p.ID)
			doc := ui.Column(c.Key(docKey)).FillWidth().MaxWidth(readerMaxW).Gap(sp4)
			a.poseEnter.apply(c, doc, "profile-doc", s.DocumentEnter, ui.Pose{Y: 6, Scale: 1, Fade: 1}, ui.Pose{Scale: 1}, 0, v4Smooth)
			doc.Children(func() {
				a.userProfileReaderHeader(c, p, s)

				// Body content: unified V4 Markdown reading
				switch {
				case s.Body != "":
					markdown.View(c.Key("user-profile-markdown-"+s.BodyKey), normalized, a.v4MarkdownOptions(fsReading))
				case s.Reading:
					v4SkeletonGroup(c, 3, 2, false, false)
				case !p.Working():
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
	if p.Working() {
		automatic = "停止导入"
	}
	if p.SaveUnknown {
		automatic = "核对保存结果"
	}
	automaticLabel := "从" + p.Name + "导入画像"
	if p.Working() {
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
		if p.CanGenerate || p.SaveUnknown || p.Working() || p.Status == "unknown" {
			onAuto := func() {
				if p.Working() {
					a.userProfileAction(p, "cancel", "")
				} else if p.SaveUnknown {
					a.userProfileAction(p, "reconcile", "")
				} else {
					a.userProfileAction(p, "generate", "")
				}
			}
			disabled := s.Busy || p.Status == "saving" || (!p.CanGenerate && !p.SaveUnknown) || (p.Status == "unknown" && !p.SaveUnknown)
			if p.Working() {
				v4DangerOutlineButton(c, automatic, false, onAuto).Label(automaticLabel).Tooltip("使用 Agent 默认模型").Disabled(disabled)
			} else {
				v4OutlineButton(c, automatic, v4IconRefresh, onAuto).Label(automaticLabel).Tooltip("使用 Agent 默认模型").Disabled(disabled)
			}
		}
		v4OutlineButton(c, "手动导入", v4IconUpload, func() { a.beginProfileImport(p) }).
			Label("导入此来源画像").Disabled(s.Busy || p.Working() || p.SaveUnknown)
		if s.Gateway && p.Document != nil {
			v4DangerOutlineButton(c, "删除画像", false, func() { s.DeleteOpen = true }).Disabled(s.Busy || p.Working())
		}

		v4OutlineButton(c, "复制正文", v4IconCopy, func() {
			if body, ok := reading.ProfileCopy(s.Body); ok {
				c.WriteClipboard(body)
			}
			s.Copied = "正文"
			s.CopyUntil = time.Now().Add(3 * time.Second)
		}).Disabled(s.Body == "")
	})
	if s.DeleteOpen {
		v4Dialog(c, &s.DeleteOpen, v4DialogOptions{
			Key: "profile-delete-" + p.ID, Title: "删除此来源的画像？", Danger: true,
			CancelText: "保留画像", ConfirmText: "删除画像", ConfirmDisabled: s.Busy || p.Working(),
			OnConfirm: func() { s.DeleteOpen = false; a.userProfileAction(p, "delete", "") },
		})
	}

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
		Key:             "user-profile-import-" + s.DraftSource,
		Title:           "手动导入 " + source.Name + " 用户画像",
		Width:           v4DialogLarge,
		ScrollBody:      true,
		Hint:            "支持直接粘贴任意 Markdown 正文，保留原文不做改写",
		CancelText:      "取消",
		CancelDisabled:  s.Busy,
		ConfirmText:     "保存画像",
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
				Label("用户画像文档").Placeholder("粘贴 " + source.Name + " 返回的用户画像正文…").
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
