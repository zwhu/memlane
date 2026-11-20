package simpledesktop

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type userProfileDocument struct {
	Dataset string `json:"dataset_id"`
	ID      string `json:"data_id"`
}

type userProfile struct {
	ID        string `json:"instance_id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Error     string `json:"error_code"`
	Operation string `json:"operation_id"`
	Progress  struct {
		Elapsed int    `json:"elapsed_seconds"`
		Idle    int    `json:"idle_seconds"`
		Process string `json:"process_state"`
	} `json:"progress"`
	FailureReason string               `json:"failure_reason"`
	ErrorDetail   string               `json:"error_detail"`
	ExitCode      *int                 `json:"exit_code"`
	Method        string               `json:"method"`
	CanGenerate   bool                 `json:"can_generate"`
	SaveUnknown   bool                 `json:"save_unconfirmed"`
	Updated       float64              `json:"updated_at"`
	Document      *userProfileDocument `json:"document"`
}

type userProfilesDirectory struct {
	Profiles []userProfile `json:"profiles"`
	Prompt   string        `json:"prompt"`
}

type userProfilesState struct {
	Directory                            userProfilesDirectory
	Selected, Body, BodyKey, Message     string
	Loading, Loaded, Reading, Busy, Open bool
	Next                                 time.Time
	ReadAt                               time.Time
	ReadGeneration                       uint64
	Scroll                               ui.ScrollState
	Drafts                               map[string]string
	Draft                                string
	DraftSource                          string
}

var userProfileIcon = homeSVG(`<circle cx="12" cy="8" r="4"/><path d="M4 21v-2a8 8 0 0 1 16 0v2"/>`)

func (p userProfile) working() bool {
	return p.Status == "queued" || p.Status == "generating" || p.Status == "saving"
}

func (p userProfile) label() string {
	switch p.Status {
	case "queued":
		return "等待生成"
	case "generating":
		return "正在生成"
	case "saving":
		return "正在保存"
	case "ready":
		if p.Method == "manual" {
			return "已导入"
		}
		return "已生成"
	case "unknown":
		return "结果待核对"
	case "failed":
		return "上次导入未完成"
	default:
		if p.CanGenerate {
			return "尚未导入"
		}
		return "手动导入"
	}
}

func (p userProfile) failureLabel() string {
	if p.Status == "unknown" {
		if p.Error != "" {
			return "导入结果未确认：" + p.Error
		}
		return "导入结果未确认"
	}
	if p.Status != "failed" {
		return ""
	}
	message := p.ErrorDetail
	if message == "" {
		message = map[string]string{
			"arguments_rejected":      "Agent 不接受调用参数",
			"authentication_required": "Agent 登录失效",
			"quota_unavailable":       "Agent 额度不足",
			"connection_failed":       "无法连接 Agent 服务",
			"permission_denied":       "Agent 权限受限，未返回正文",
			"empty_response":          "Agent 未返回画像正文",
			"agent_exited":            "Agent 异常退出",
		}[p.FailureReason]
	}
	if message == "" && p.Error == "profile_generation_timeout" {
		message = "Agent 导出超时，未生成完整文档（profile_generation_timeout）"
	}
	if message == "" {
		message = p.Error
	}
	if message == "" {
		message = "未返回错误详情"
	}
	if p.ExitCode != nil {
		message += fmt.Sprintf("（退出码 %d）", *p.ExitCode)
	}
	return "导入失败：" + message
}

func (a *App) selectedUserProfile() (userProfile, bool) {
	for _, p := range a.userProfiles.Directory.Profiles {
		if p.ID == a.userProfiles.Selected {
			return p, true
		}
	}
	return userProfile{}, false
}

func (a *App) refreshUserProfiles() {
	s := &a.userProfiles
	if s.Loading || a.Config.ProjectControl == nil || a.Update == nil {
		return
	}
	s.Loading = true
	s.Next = time.Now().Add(4 * time.Second)
	client := ProjectControlClient{Config: *a.Config.ProjectControl, HTTP: a.Client.HTTP}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var result userProfilesDirectory
		err := client.request(ctx, "GET", "/v1/user-profiles", nil, &result)
		a.Update(func() {
			s.Loading = false
			if err != nil {
				s.Message = "用户画像暂时无法加载，请刷新重试"
				return
			}
			s.Loaded = true
			s.Directory = result
			if p, ok := a.selectedUserProfile(); ok && p.Status == "ready" && s.Message == "结果已接收，正在保存" {
				s.Message = ""
			}
			if _, ok := a.selectedUserProfile(); !ok {
				s.Selected = ""
				if len(result.Profiles) > 0 {
					s.Selected = result.Profiles[0].ID
				}
			}
			a.readUserProfile()
		})
	}()
}

func (a *App) selectUserProfile(id string) {
	s := &a.userProfiles
	if s.Selected == id {
		return
	}
	s.Selected = id
	s.Body, s.BodyKey, s.Message = "", "", ""
	s.Scroll = ui.ScrollState{}
	s.ReadGeneration++
	s.Reading = false
	a.readUserProfile()
}

func (a *App) readUserProfile() {
	s := &a.userProfiles
	p, ok := a.selectedUserProfile()
	if !ok || p.Document == nil {
		s.Body, s.BodyKey = "", ""
		return
	}
	key := p.ID + "/" + p.Document.Dataset + "/" + p.Document.ID
	if (key == s.BodyKey && time.Since(s.ReadAt) < 30*time.Second) || s.Reading || a.Config.ProjectControl == nil || a.Update == nil {
		return
	}
	s.ReadGeneration++
	generation, selected := s.ReadGeneration, p.ID
	s.Reading = true
	client := ProjectControlClient{Config: *a.Config.ProjectControl, HTTP: a.Client.HTTP}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var result struct {
			Body     string              `json:"body"`
			Document userProfileDocument `json:"document"`
		}
		err := client.request(ctx, "GET", "/v1/user-profiles/"+url.PathEscape(selected)+"/document", nil, &result)
		a.Update(func() {
			if generation != s.ReadGeneration || selected != s.Selected {
				return
			}
			s.Reading = false
			current, exists := a.selectedUserProfile()
			if !exists || current.Document == nil || *current.Document != *p.Document {
				a.readUserProfile()
				return
			}
			if err != nil || result.Document != *p.Document {
				s.Body, s.BodyKey = "", ""
				s.Message = "画像文档暂不可读，请刷新后核对"
				return
			}
			s.Body, s.BodyKey = result.Body, key
			s.ReadAt = time.Now()
		})
	}()
}

func (a *App) userProfileAction(p userProfile, action, body string) {
	s := &a.userProfiles
	if s.Busy || a.Config.ProjectControl == nil || a.Update == nil {
		return
	}
	payload := map[string]any{}
	if action != "reconcile" {
		payload["operation_id"] = integrationOperation()
	}
	if action == "cancel" {
		payload["operation_id"] = p.Operation
	}
	if action == "import" {
		payload["body"] = body
	}
	s.Busy, s.Message = true, ""
	client := ProjectControlClient{Config: *a.Config.ProjectControl, HTTP: a.Client.HTTP}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var result userProfilesDirectory
		err := client.request(ctx, "POST", "/v1/user-profiles/"+url.PathEscape(p.ID)+"/"+action, payload, &result)
		a.Update(func() {
			s.Busy = false
			if err != nil {
				// Inspect the journal first; never automatically replay a POST.
				s.Message = "操作未确认，正在核对结果"
				a.refreshUserProfiles()
				return
			}
			s.Directory, s.Loaded = result, true
			if action == "generate" {
				s.Message = ""
			}
			if action == "import" {
				s.Open = false
				s.Message = ""
			}
			a.readUserProfile()
		})
	}()
}

func (a *App) beginProfileImport(p userProfile) {
	s := &a.userProfiles
	if s.Drafts == nil {
		s.Drafts = map[string]string{}
	}
	s.DraftSource, s.Draft, s.Open = p.ID, s.Drafts[p.ID], true
}

func (a *App) userProfilesPage(c *ui.Context) {
	s := &a.userProfiles
	if !time.Now().Before(s.Next) && a.page == "用户画像" {
		a.refreshUserProfiles()
	}
	ui.Row(c.Key("user-profiles-page")).Fill().MinHeight(0).Children(func() {
		ui.Column(c).Width(216).Shrink(0).FillHeight().BorderWidth(0, 1, 0, 0).BorderColor(homeBorder).Children(func() {
			paneHeading(c, "来源", func() {
				refreshAction(c, "刷新画像列表", func() { s.BodyKey = ""; a.refreshUserProfiles() }).Disabled(s.Loading)
			}).Margin(pageTopInset, 0, 0, 0).Padding(0, paneInset)
			ui.Scroll(c.Key("profile-sources")).Grow(1).MinHeight(0).Padding(0, paneInset-listItemInset, paneInset, paneInset-listItemInset).Gap(3).Children(func() {
				if !s.Loaded {
					homeText(c, "正在加载来源…", homeMeta).Margin(8)
				}
				previousGroup := ""
				for _, profile := range s.Directory.Profiles {
					p := profile
					group := "编码 Agent"
					if p.Kind == "manual" {
						group = "其他 AI"
					}
					if group != previousGroup {
						homeText(c, group, homeMeta).FontSize(10).Margin(16, listItemInset, 8, listItemInset)
						previousGroup = group
					}
					button := ui.Button(c.Key("profile-source-"+p.ID), "").Label(p.Name+"用户画像").FillWidth().Height(40).Padding(0, listItemInset).Gap(10).Justify(ui.Start).BorderWidth(0).Radius(6).Cursor(ui.CursorPointer)
					if s.Selected == p.ID {
						button.Background(tok.AccentSoft)
					} else {
						button.Background(ui.Transparent)
					}
					button.Children(func() {
						ui.Box(c).Size(20, 20).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
							agentLogo(c, agentBrandIconFor(p.Kind, p.Name), 16)
						})
						homeText(c, p.Name, homeBody).FontSize(12).SingleLine().Grow(1).MinWidth(0)
						if p.Status == "ready" {
							ui.Box(c).Size(5, 5).Radius(3).Background(tok.OK).Tooltip(p.label())
						} else if p.Status == "failed" || p.Status == "unknown" {
							homeText(c, "!", homeMeta).FontSize(11).Tooltip(p.label())
						} else if p.working() {
							homeText(c, "…", homeMeta).Tooltip(p.label())
						}
					}).OnClick(func() { a.selectUserProfile(p.ID) })
				}
			})
		})
		ui.Column(c).Grow(1).MinWidth(0).FillHeight().Children(func() {
			p, ok := a.selectedUserProfile()
			if !ok {
				homeText(c, "选择一个 AI，查看它对你的了解", homeBody).Margin(paneInset)
				if s.Message != "" {
					homeText(c, s.Message, homeMeta).Margin(paneInset)
				}
				return
			}
			ui.Row(c).FillWidth().Padding(pageTopInset, paneInset, controlGap, paneInset).AlignItems(ui.Center).Gap(10).Children(func() {
				agentLogo(c, agentBrandIconFor(p.Kind, p.Name), 22)
				homeText(c, p.Name+" 对你的了解", homeHeading).FontSize(18)
				ui.Spacer(c)
				automatic := "尝试导入"
				if p.Document != nil {
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
				commonButton(c, automatic, true, func() {
					if p.working() {
						a.userProfileAction(p, "cancel", "")
					} else if p.SaveUnknown {
						a.userProfileAction(p, "reconcile", "")
					} else {
						a.userProfileAction(p, "generate", "")
					}
				}).Label(automaticLabel).Tooltip("使用 Agent 默认模型").Disabled(s.Busy || p.Status == "saving" || (!p.CanGenerate && !p.SaveUnknown) || (p.Status == "unknown" && !p.SaveUnknown))
				commonButton(c, "手动导入", false, func() { a.beginProfileImport(p) }).Label("导入此来源画像").Disabled(s.Busy || p.working() || p.SaveUnknown)
			})
			if p.Status == "generating" {
				label := fmt.Sprintf("正在导出 · %d 秒", p.Progress.Elapsed)
				if p.Progress.Idle >= 30 {
					label = fmt.Sprintf("等待 Agent 返回 · %d 秒，%d 秒未收到新输出", p.Progress.Elapsed, p.Progress.Idle)
				}
				homeText(c, label, homeMeta).Margin(0, paneInset, controlGap, paneInset)
			}
			message := p.failureLabel()
			if message == "" && !p.working() {
				message = s.Message
			}
			if message != "" {
				homeText(c, message, homeMeta).TextColor(tok.Danger).Margin(0, paneInset, controlGap, paneInset)
			}
			if s.Body != "" {
				ui.Scroll(c.Key("user-profile-document-"+s.BodyKey)).Grow(1).MinHeight(0).Padding(controlGap, paneInset, paneInset, paneInset).TrackScroll(&s.Scroll).Children(func() {
					a.commonMarkdown(c, "user-profile-markdown-"+s.BodyKey, profileReadingMarkdown(s.Body))
				})
			} else {
				ui.Spacer(c)
			}

		})
	})
}

// profileReadingMarkdown unwraps an export envelope, never saved content or inner code.
func profileReadingMarkdown(body string) string {
	text := strings.TrimSpace(body)
	lines := strings.Split(text, "\n")
	if len(lines) < 3 {
		return body
	}
	opener := strings.TrimSpace(lines[0])
	marker := ""
	if strings.HasPrefix(opener, "```") {
		marker = "```"
	} else if strings.HasPrefix(opener, "~~~") {
		marker = "~~~"
	} else {
		return body
	}
	language := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(opener, marker)))
	if language != "" && language != "text" && language != "plaintext" && language != "markdown" && language != "md" {
		return body
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == marker {
			end = i
			break
		}
	}
	if end < 0 {
		return body
	}
	inner := strings.Join(lines[1:end], "\n")
	// Only document-like exports qualify; a real fenced text/code sample stays intact.
	first := strings.TrimSpace(inner)
	if !strings.HasPrefix(first, "# ") && !strings.HasPrefix(first, "## ") {
		return body
	}
	tail := strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
	if tail != "" {
		return inner + "\n\n" + tail
	}
	return inner
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
	_, h := c.Size()
	ui.Modal(c.Key("user-profile-import-"+s.DraftSource), &s.Open, func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
				agentLogo(c, agentBrandIconFor(source.Kind, source.Name), 22)
				homeText(c, "导入 "+source.Name+" 用户画像", homeHeading).FontSize(20)
			})
			ui.Spacer(c)
			readingAction(c, "关闭画像导入", readingClose, func() { s.Open = false })
		})
		ui.Scroll(c.Key("profile-import-steps")).Width(600).MaxHeight(h - 240).Gap(14).Children(func() {
			textButton(c, "复制提示词", nil).OnClick(func() { c.WriteClipboard(s.Directory.Prompt) }).Disabled(s.Directory.Prompt == "")
			ui.TextArea(c.Key("profile-import-body-"+s.DraftSource), &s.Draft).Label("用户画像文档").Placeholder("粘贴画像内容").FillWidth().Height(190).FontSize(13).Disabled(s.Busy).OnChange(func() { s.Drafts[s.DraftSource] = s.Draft })
			if s.Message != "" {
				homeText(c, s.Message, homeMeta)
			}
		})
		ui.Row(c).FillWidth().Justify(ui.End).Gap(10).Children(func() {
			commonButton(c, "取消", false, func() { s.Open = false })
			commonButton(c, "保存画像", true, func() { a.userProfileAction(source, "import", s.Draft) }).Disabled(s.Busy || strings.TrimSpace(s.Draft) == "" || len(s.Draft) > 256*1024 || source.ID == "")
		})
	}).Width(656).Padding(24).Gap(20)
}
