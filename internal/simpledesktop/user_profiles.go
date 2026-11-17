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
	ID          string               `json:"instance_id"`
	Kind        string               `json:"kind"`
	Name        string               `json:"name"`
	Status      string               `json:"status"`
	Error       string               `json:"error_code"`
	Method      string               `json:"method"`
	Operation   string               `json:"operation_id"`
	Automatic   bool                 `json:"automatic"`
	SaveUnknown bool                 `json:"save_unconfirmed"`
	Updated     float64              `json:"updated_at"`
	Document    *userProfileDocument `json:"document"`
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
		return "生成未完成 · 可手动导入"
	default:
		if p.Automatic {
			return "等待自动生成"
		}
		return "手动导入"
	}
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
				s.Message = "操作未确认，正在核对；请保留粘贴的内容"
				a.refreshUserProfiles()
				return
			}
			s.Directory, s.Loaded = result, true
			if action == "import" {
				s.Open = false
				s.Message = "结果已接收，正在保存"
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
						button.Background(ui.Hex("#edf3fa"))
					} else {
						button.Background(ui.Transparent)
					}
					button.Children(func() {
						ui.Box(c).Size(20, 20).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
							ui.Icon(c, agentBrandIcon(p.Kind)).FontSize(16).TextColor(homeIconInk)
						})
						homeText(c, p.Name, homeBody).FontSize(12).SingleLine().Grow(1).MinWidth(0)
						if p.Status == "ready" {
							ui.Box(c).Size(5, 5).Radius(3).Background(ui.Hex("#72978a")).Tooltip(p.label())
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
				ui.Column(c).Grow(1).Gap(6).Children(func() {
					homeText(c, p.Name+" 对你的了解", homeHeading).FontSize(18)
					label := p.label()
					if p.Status == "manual" {
						label = "尚未导入"
					}
					if p.Updated > 0 {
						label += " · " + time.Unix(int64(p.Updated), 0).Local().Format("2006-01-02 15:04")
					}
					homeText(c, label, homeMeta)
				})
				if p.Automatic && p.Status != "unknown" {
					label := "自动生成"
					if p.Document != nil {
						label = "重新生成"
					}
					commonButton(c, label, false, func() { a.userProfileAction(p, "generate", "") }).Tooltip("使用此 Agent 当前账号和模型，会消耗相应额度").Disabled(s.Busy || p.working())
				}
				if s.Body != "" || p.working() || p.SaveUnknown {
					commonButton(c, "手动导入", false, func() { a.beginProfileImport(p) }).Label("导入此来源画像").Disabled(s.Busy || p.working() || p.SaveUnknown)
				}
			})
			if s.Message != "" {
				homeText(c, s.Message, homeMeta).Margin(0, paneInset, controlGap, paneInset)
			}
			if p.SaveUnknown {
				ui.Row(c).Padding(0, paneInset, controlGap, paneInset).Gap(12).Children(func() {
					homeText(c, "保存结果尚未确认，已保留本次文档", homeMeta)
					homeLink(c, "核对保存结果", func() { a.userProfileAction(p, "reconcile", "") })
				})
			}
			if s.Body != "" {
				ui.Scroll(c.Key("user-profile-document-"+s.BodyKey)).Grow(1).MinHeight(0).Padding(controlGap, paneInset, paneInset, paneInset).TrackScroll(&s.Scroll).Children(func() {
					a.commonMarkdown(c, "user-profile-markdown-"+s.BodyKey, s.Body)
				})
				ui.Row(c).FillWidth().Padding(12, paneInset).BorderWidth(1, 0, 0, 0).BorderColor(homeBorder).Children(func() {
					homeText(c, "来源独立保留 · 不自动合并为通用记忆", homeMeta)
					ui.Spacer(c)
					textButton(c, "复制文档", nil).OnClick(func() { c.WriteClipboard(s.Body) })
				})
			} else {
				ui.Scroll(c.Key("profile-import-overview")).Grow(1).FillWidth().MinHeight(0).Padding(controlGap, paneInset, paneInset, paneInset).Children(func() {
					ui.Column(c).FillWidth().MaxWidth(680).Padding(moduleInset).Gap(18).Border(1, homeBorder).Radius(10).Background(ui.Hex("#fafbfd")).Children(func() {
						title, explanation := "导入 "+p.Name+" 的用户画像", "将它记住的背景、偏好与协作约定，独立保存在这里。"
						if s.Reading {
							title, explanation = "正在加载画像", "正在读取已保存的文档"
						} else if p.working() {
							title, explanation = p.label(), "由该 Agent 使用自己的现有记忆生成，完成后自动显示。"
						} else if p.Status == "failed" || p.Status == "unknown" {
							title = "可以先手动导入"
						}
						homeText(c, title, homeHeading).FontSize(15)
						homeText(c, explanation, homeMeta).FontSize(12)
						ui.Column(c).Gap(14).Padding(4, 0).Children(func() {
							for n, step := range []string{"复制提示词，到 " + p.Name + " 的日常会话中运行", "将返回的内容粘贴保存，随时回来阅读"} {
								ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
									ui.Box(c).Size(22, 22).Radius(11).Background(ui.Hex("#eaf0f6")).AlignItems(ui.Center).Justify(ui.Center).Children(func() { homeText(c, fmt.Sprint(n+1), homeMeta).FontSize(11) })
									homeText(c, step, homeBody).FontSize(12)
								})
							}
						})
						ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
							commonButton(c, "开始导入", true, func() { a.beginProfileImport(p) }).Label("导入此来源画像").Disabled(s.Busy || p.working() || p.SaveUnknown)
							textButton(c, "复制提示词", func() {
								c.WriteClipboard(s.Directory.Prompt)
								s.Message = "提示词已复制，请到对应 AI 运行"
							}).Disabled(s.Directory.Prompt == "")
						})
					})
					homeText(c, "各家来源独立保留，未知信息不作推断。", homeMeta).FontSize(11).Margin(14, 2)
				})
			}
		})
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
	_, h := c.Size()
	ui.Modal(c.Key("user-profile-import-"+s.DraftSource), &s.Open, func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			homeText(c, "导入 "+source.Name+" 用户画像", homeHeading).FontSize(20)
			ui.Spacer(c)
			readingAction(c, "关闭画像导入", readingClose, func() { s.Open = false })
		})
		ui.Scroll(c.Key("profile-import-steps")).Width(600).MaxHeight(h - 240).Gap(14).Children(func() {
			homeText(c, "仅整理该 AI 自己对你的了解，尽量保留你的原话。", homeMeta)
			homeText(c, "1  复制提示词，到对应 AI 的聊天中运行", homeHeading).FontSize(13)
			ui.Column(c).Padding(14).Gap(10).Background(ui.Hex("#f5f8fb")).Border(1, homeBorder).Radius(8).Children(func() {
				homeText(c, s.Directory.Prompt, homeBody).FontSize(12).MaxLines(5)
				textButton(c, "复制提示词", nil).OnClick(func() { c.WriteClipboard(s.Directory.Prompt) }).Disabled(s.Directory.Prompt == "")
			})
			homeText(c, "2  将生成结果粘贴到下方", homeHeading).FontSize(13)
			ui.TextArea(c.Key("profile-import-body-"+s.DraftSource), &s.Draft).Label("用户画像文档").Placeholder("在此粘贴该 AI 返回的 Markdown 文档").FillWidth().Height(190).FontSize(13).Disabled(s.Busy).OnChange(func() { s.Drafts[s.DraftSource] = s.Draft })
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
