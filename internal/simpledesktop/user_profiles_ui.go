package simpledesktop

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
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
		appFooter(c, "用户画像来源状态").Padding(0, sp4).Children(func() {
			if !s.Loaded && s.Loading {
				homeText(c, "正在读取…", homeMeta)
			} else {
				homeText(c, fmt.Sprintf("共 %d 个来源", len(s.Directory.Profiles)), homeMeta)
			}
		})
	})
}

const (
	profileMarkdownVirtualizeThreshold = 4096
	profileMarkdownChunkTargetBytes    = 1400
	profileMarkdownChunkHardLimitBytes = 3600
)

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

// splitProfileMarkdownChunks splits large Markdown profiles into fence-safe
// chunks for virtualized ui.List rendering, never breaking fenced code blocks.
func splitProfileMarkdownChunks(md string) []string {
	if md == "" {
		return nil
	}
	lines := strings.Split(md, "\n")
	var chunks []string
	var buf strings.Builder
	fenceChar := byte(0)
	fenceLen := 0

	flush := func() {
		chunk := strings.TrimRight(buf.String(), "\n")
		if strings.TrimSpace(chunk) != "" {
			chunks = append(chunks, chunk)
		}
		buf.Reset()
	}

	for idx, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if fenceChar == 0 {
			if len(trimmed) >= 3 && (trimmed[0] == '`' || trimmed[0] == '~') {
				ch := trimmed[0]
				n := 0
				for n < len(trimmed) && trimmed[n] == ch {
					n++
				}
				if n >= 3 {
					fenceChar = ch
					fenceLen = n
				}
			}
		} else if len(trimmed) >= fenceLen && trimmed[0] == fenceChar {
			n := 0
			for n < len(trimmed) && trimmed[n] == fenceChar {
				n++
			}
			if n >= fenceLen && strings.TrimSpace(trimmed[n:]) == "" {
				fenceChar = 0
				fenceLen = 0
			}
		}

		isHeading := fenceChar == 0 && (strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "### "))
		if isHeading && buf.Len() >= profileMarkdownChunkTargetBytes/2 {
			flush()
		}

		if buf.Len() > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteString(line)

		if fenceChar == 0 {
			isBlank := strings.TrimSpace(line) == ""
			if (isBlank && buf.Len() >= profileMarkdownChunkTargetBytes) || buf.Len() >= profileMarkdownChunkHardLimitBytes {
				if idx < len(lines)-1 {
					flush()
				}
			}
		}
	}
	flush()
	if len(chunks) == 0 {
		return []string{md}
	}
	return chunks
}

func profileCachedMarkdownChunks(s *userProfilesState) (normalized string, chunks []string) {
	if s.Body == "" {
		return "", nil
	}
	if s.cachedBody == s.Body && s.cachedNormalized != "" {
		return s.cachedNormalized, s.cachedChunks
	}
	normalized = profileReadingMarkdown(s.Body)
	if len(normalized) > profileMarkdownVirtualizeThreshold {
		chunks = splitProfileMarkdownChunks(normalized)
	}
	s.cachedBody = s.Body
	s.cachedNormalized = normalized
	s.cachedChunks = chunks
	return normalized, chunks
}

func (a *App) userProfileReaderHeader(c *ui.Context, p userProfile, s *userProfilesState) {
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
		if s.docKey != p.ID {
			s.docKey = p.ID
			s.docEnter++
		}

		normalized, chunks := profileCachedMarkdownChunks(s)
		if s.Body != "" && len(chunks) > 1 {
			mdOpts := a.v4MarkdownOptions(fsReading)
			s.ReaderList.KeepOffset = true
			s.ReaderList.Selected = nil
			s.ReaderList.Key = func(row int) any {
				if row == 0 {
					return "header/" + p.ID + "/" + s.BodyKey
				}
				return fmt.Sprintf("chunk/%s/%s/%d", p.ID, s.BodyKey, row-1)
			}
			ui.Themed(c, profileReaderListTheme()).Grow(1).MinHeight(0).FillWidth().Background(ui.Transparent).Children(func() {
				ui.List(c.Key("user-profile-list-"+p.ID+"-"+s.BodyKey), &s.ReaderList, 1+len(chunks), func(row int) {
					ui.Column(c.Key(fmt.Sprintf("profile-vrow-%s-%d", s.BodyKey, row))).FillWidth().MaxWidth(readerMaxW).Padding(0, sp10).Gap(sp4).Children(func() {
						if row == 0 {
							ui.Column(c).FillWidth().Padding(sp6, 0, sp2, 0).Gap(sp4).Children(func() {
								a.userProfileReaderHeader(c, p, s)
							})
							return
						}
						idx := row - 1
						padBottom := float32(sp2)
						if idx == len(chunks)-1 {
							padBottom = sp8
						}
						ui.Column(c).FillWidth().Padding(0, 0, padBottom, 0).Children(func() {
							markdown.View(c.Key(fmt.Sprintf("user-profile-markdown-%s-%d", s.BodyKey, idx)), chunks[idx], mdOpts)
						})
					})
				}).Label("用户画像内容").Grow(1).MinHeight(0).TrackScroll(&s.Scroll)
			})
			a.userProfileFooterBar(c, p)
			return
		}

		// Scrollable reading body (680px max width, smooth fade-in on switch)
		ui.Scroll(c.Key("user-profile-document-"+p.ID+"-"+s.BodyKey)).Grow(1).MinHeight(0).
			TrackScroll(&s.Scroll).Padding(sp6, sp10, sp8, sp10).Children(func() {
			docKey := fmt.Sprintf("profile-doc-%d-%s", s.docEnter, p.ID)
			doc := ui.Column(c.Key(docKey)).FillWidth().MaxWidth(readerMaxW).Gap(sp4)
			a.poseEnter.apply(c, doc, "profile-doc", s.docEnter, ui.Pose{Y: 6, Scale: 1, Fade: 1}, ui.Pose{Scale: 1}, 0, v4Smooth)
			doc.Children(func() {
				a.userProfileReaderHeader(c, p, s)
