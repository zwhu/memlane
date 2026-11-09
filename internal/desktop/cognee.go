package desktop

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/records"
)

// Host-only approval. Credentials live only in the masked edit field until
// sent to the service, which owns the private credential and scope barrier.
type CogneeUI struct {
	FirstTask                                                          bool
	Workspace                                                          string
	Connection                                                         *records.Connection
	AgentExecutable, AuthDirectory, TaskPrompt, TaskOutput, AgentStage string
	Agent                                                              *official.AgentScope
	Project, Endpoint, Dataset, Credential, Session                    string
	Message                                                            string
	Binding                                                            *records.CogneeBinding
	Bindings                                                           []records.CogneeBinding
	Receipt                                                            *records.CogneeSyncReceipt
	Busy                                                               bool
	Authorized                                                         bool
	Sequence                                                           int64
	Cancel                                                             context.CancelFunc
}

func (a *App) openCognee() {
	if a.cognee.Cancel != nil {
		a.cognee.Cancel()
	}
	sequence := a.cognee.Sequence + 1
	project := a.scope
	if project == "" {
		project = a.browsing.Preferences.Project
	}
	a.cognee = CogneeUI{Project: project, Sequence: sequence}
	a.loadOfficialAgent()
	a.navigate("官方记忆", "")
	a.refreshCogneeBindings()
}
func (a *App) refreshCogneeBindings() {
	s := &a.cognee
	if s.Project == "" || a.libraryContext == nil {
		return
	}
	s.Sequence++
	sequence, project := s.Sequence, s.Project
	s.Busy = true
	ctx, cancel := context.WithTimeout(a.libraryContext, 5*time.Second)
	s.Cancel = cancel
	go func() {
		defer cancel()
		bindings, err := a.library.CogneeBindings(ctx, project)
		connections, connectionErr := a.library.Connections(ctx)
		if err == nil {
			err = connectionErr
		}
		a.dispatch(func() {
			if s.Sequence != sequence || s.Project != project {
				return
			}
			s.Busy, s.Cancel = false, nil
			if err != nil {
				s.Message = "绑定读取失败：" + err.Error()
				return
			}
			s.Bindings = bindings
			if len(bindings) > 0 {
				b := bindings[0]
				s.Binding = &b
				s.Endpoint = b.Endpoint
				s.Dataset = b.Dataset
				s.Authorized = false
				for _, connection := range connections {
					if connection.ID == b.ConnectionID && connection.Enabled && connection.Generation == b.Generation {
						s.Authorized = true
					}
				}
				if !s.Authorized {
					s.Message = "Memlane 接入已撤销；旧回执与官方独立材料保留，该范围不能继续同步或读取来源。"
				}
			}
		})
	}()
}
func (a *App) authorizeCognee() {
	s := &a.cognee
	if s.Busy || s.Project == "" || s.Credential == "" {
		return
	}
	var workspace string
	for _, project := range a.scopes {
		if project.ID == s.Project {
			workspace = project.Path
		}
	}
	if workspace == "" {
		s.Message = "请先登记真实本地项目。"
		return
	}
	project, endpoint, dataset, credential := s.Project, strings.TrimSpace(s.Endpoint), strings.TrimSpace(s.Dataset), s.Credential
	projectName := a.scopeLabel(project)
	s.Credential = ""
	s.Busy = true
	s.Sequence++
	sequence := s.Sequence
	ctx, cancel := context.WithTimeout(a.libraryContext, 35*time.Second)
	s.Cancel = cancel
	go func() {
		defer cancel()
		connections, err := a.library.Connections(ctx)
		var connection records.Connection
		if err == nil {
			for _, c := range connections {
				if c.Agent == "codex" && c.Project == project && c.Enabled {
					connection = c
					break
				}
			}
			if connection.ID == "" {
				connection, err = a.library.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "codex", Name: "官方 Cognee · " + projectName, Project: project, AllowedScopes: []string{project}})
			}
		}
		var binding records.CogneeBinding
		if err == nil {
			binding, err = a.library.AuthorizeCognee(ctx, records.CogneeAuthorization{Binding: records.CogneeBinding{Operation: uuid.NewString(), Dataset: dataset, Project: project, Endpoint: endpoint, ConnectionID: connection.ID, Generation: connection.Generation, Workspace: workspace}, Credential: credential})
		}
		credential = ""
		a.dispatch(func() {
			if s.Sequence != sequence || s.Project != project {
				return
			}
			s.Busy, s.Cancel = false, nil
			if err != nil {
				s.Message = "授权未完成：" + err.Error()
				return
			}
			s.Binding = &binding
			s.Authorized = true
			s.Bindings = []records.CogneeBinding{binding}
			s.Message = "官方服务身份与 dataset 授权已核验。只同步明确选择的会话；尚未导入材料。"
		})
	}()
}
func (a *App) syncCognee() {
	s := &a.cognee
	if s.Busy || s.Binding == nil || !s.Authorized || s.Session == "" {
		return
	}
	query := records.CogneeSync{Project: s.Project, Dataset: s.Binding.Dataset, Session: strings.TrimSpace(s.Session)}
	firstTask := s.FirstTask
	binding := *s.Binding
	var firstAgent *official.AgentScope
	if firstTask && s.Agent != nil {
		copy := *s.Agent
		firstAgent = &copy
	}
	s.Sequence++
	sequence := s.Sequence
	s.Busy = true
	s.Receipt = nil
	s.Message = "正在读取官方持久回执与完整材料…"
	ctx, cancel := context.WithTimeout(a.libraryContext, 5*time.Minute)
	s.Cancel = cancel
	go func() {
		defer cancel()
		receipt, err := awaitCogneeMaterial(ctx, query, a.library.SyncCognee, 10*time.Second, func(receipt records.CogneeSyncReceipt) {
			a.dispatch(func() {
				if s.Sequence != sequence || s.Project != query.Project || ctx.Err() != nil {
					return
				}
				s.Receipt = &receipt
				if receipt.OfficialState != "persisted" {
					s.Message = "正在等待官方原始材料的持久回执；缓冲与缓存尚不计为保存。可取消本轮接收。"
				} else {
					s.Message = fmt.Sprintf("已持久化 %d 条原始记录；%d 份官方材料仍待提炼，正在等待当前结果。", len(receipt.Raw), receipt.PendingDocuments)
				}
			})
		})
		var currentReadError error
		if err == nil && firstAgent != nil && len(receipt.Knowledge) > 0 {
			currentReadError = a.enableFirstTaskCurrentKnowledge(ctx, *firstAgent, binding)
		}
		cancelled := ctx.Err() == context.Canceled
		a.dispatch(func() {
			if s.Sequence != sequence || s.Project != query.Project {
				return
			}
			s.Busy, s.Cancel = false, nil
			if cancelled {
				s.Receipt = nil
				s.Message = "本轮接收已取消；官方已提交材料独立保留。已撤销接入范围的迟到结果会被拒绝。"
				return
			}
			s.Receipt = &receipt
			if err != nil {
				s.Message = "同步未完成；已持久化的逐项回执保留，重试会去重：" + err.Error()
				return
			}
			if receipt.OfficialState != "persisted" {
				s.Message = "有限等待已结束，官方材料尚未全部持久化。缓冲与缓存不计为保存；服务继续处理时可稍后重试接收。"
				return
			}
			if receipt.SessionOriginal != nil {
				s.Message = fmt.Sprintf("Memlane 已持久化 1 份完整会话原文、%d 份 SDK 分段、%d 条知识；%d 份官方材料仍待提炼。知识索引由 Rust 独立确认。", len(receipt.Raw), len(receipt.Knowledge), receipt.PendingDocuments)
			} else {
				s.Message = fmt.Sprintf("Memlane 已持久化 %d 份 SDK 采集分段、%d 条知识；%d 份官方材料仍待提炼。完整会话原文尚未接入。", len(receipt.Raw), len(receipt.Knowledge), receipt.PendingDocuments)
			}
			if firstTask {
				if currentReadError != nil {
					s.Message += " 当前知识读取尚未接通：" + currentReadError.Error()
				} else if len(receipt.Knowledge) > 0 {
					s.Message += " 后续任务会先读取 Memlane 当前有效知识；官方采集原文继续保留。"
				}
				a.refreshAgents(false)
			}
			a.refresh(false)
		})
	}()
}
func (a *App) cogneeDetail(c *ui.Context) {
	s := &a.cognee
	ui.Scroll(c.Key("cognee-detail")).Grow(1).Padding(24).Gap(16).Children(func() {
		ui.Text(c, "官方 Cognee 记忆").FontSize(design.Title).Bold()
		ui.Text(c, "本机环境准备与会话导入分别确认。官方 recall 查询 Cognee；App 知识搜索查询 Memlane 与 Rust。").LineHeight(1.7)
		if a.firstLaunch.Controller != nil {
			a.cogneePreparation(c)
		}
		ui.Text(c, "授权项目与材料范围").Bold()
		for _, project := range a.scopes {
			if project.Kind != "project" {
				continue
			}
			button := quietButton(c, project.Name).Key("cognee-project-" + project.ID).Disabled(s.Busy)
			if s.Project == project.ID {
				button.Background(design.Surface)
			}
			button.OnClick(func() {
				s.Project = project.ID
				s.Binding = nil
				s.Authorized = false
				s.Bindings = nil
				s.Dataset = ""
				s.Credential = ""
				s.Session = ""
				s.Receipt = nil
				s.Message = ""
				a.loadOfficialAgent()
				a.refreshCogneeBindings()
			})
		}
		quietButton(c, "添加本地项目").Key("cognee-add-project").Disabled(s.Busy).OnClick(func() { a.navigate("项目", ""); a.detailMode = "project" })
		a.cogneeAgent(c)
		ui.Text(c, "服务地址").FontSize(design.Small)
		ui.TextInput(c.Key("cognee-endpoint"), &s.Endpoint).Placeholder("http://127.0.0.1:端口").Disabled(s.Busy || s.Binding != nil)
		ui.Text(c, "Cognee dataset UUID").FontSize(design.Small)
		ui.TextInput(c.Key("cognee-dataset"), &s.Dataset).Placeholder("选择实际 dataset 身份").Disabled(s.Busy || s.Binding != nil)
		if s.Binding == nil {
			ui.TextInput(c.Key("cognee-credential"), &s.Credential).Password().Placeholder("Cognee 独立访问凭据").Disabled(s.Busy)
			ui.Text(c, "确认后仅创建此项目的 Codex 接入授权并绑定该 dataset，不修改 Agent 配置或模型。").FontSize(design.Small).LineHeight(1.7)
			actionButton(c, "确认范围并验证授权", nil).Key("cognee-authorize").Disabled(s.Busy || s.Project == "" || s.Credential == "" || s.Dataset == "" || s.Endpoint == "").OnClick(a.authorizeCognee)
		} else {
			meta(c, "已登记服务", s.Binding.Instance)
			if s.Authorized {
				meta(c, "已授权范围", a.scopeLabel(s.Project))
			} else {
				meta(c, "当前授权", "接入已撤销 · 旧绑定不恢复")
			}
			quietButton(c, "重新核验服务凭据").Key("cognee-refresh-auth").Disabled(s.Busy || !s.Authorized).OnClick(func() {
				s.Binding = nil
				s.Authorized = false
				s.Credential = ""
				s.Message = "请提供新的 Cognee 独立凭据；重新核验保留原项目和 dataset 身份。"
			})
		}
		ui.TextInput(c.Key("cognee-session"), &s.Session).Placeholder("本轮官方会话 ID").Disabled(s.Busy)
		actionButton(c, "确认同步此会话", nil).Key("cognee-sync").Disabled(s.Busy || s.Binding == nil || !s.Authorized || s.Session == "").OnClick(a.syncCognee)
		if s.Message != "" {
			ui.Text(c, s.Message).Key("cognee-result").Selectable().LineHeight(1.7)
		}
		if s.Binding != nil {
			quietButton(c, "进入项目记忆").Key("cognee-browse").Disabled(s.Busy).OnClick(func() { a.navigate("项目", s.Project); a.browseView("memories") })
		}
	})
}

func (a *App) cogneeSetupPage(c *ui.Context) {
	s := &a.cognee
	if s.FirstTask {
		a.firstTaskPage(c)
		return
	}
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Children(func() { a.cogneeDetail(c) })
		ui.Column(c).Width(176).Shrink(0).Padding(28, 18).Gap(24).BorderWidth(0, 0, 0, 1).BorderColor(design.Border).Children(func() {
			active := 0
			if a.officialPreparation.Status.Phase == "ready" {
				active = 1
			}
			if active == 1 && s.Agent != nil {
				active = 2
			}
			if s.Receipt != nil && s.Receipt.OfficialState == "persisted" && len(s.Receipt.Raw) > 0 {
				active = 3
			}
			for i, label := range []string{"准备官方环境", "确认项目接入", "真实任务与同步", "浏览记忆与出处"} {
				text := ui.Text(c, label).FontSize(design.Small).LineHeight(1.7)
				if i == active {
					text.Bold()
				} else {
					text.TextColor(design.Muted)
				}
			}
			ui.Spacer(c)
			ui.Text(c, "原始记录、知识和索引各自依据真实回执。历史导入另行确认。").FontSize(design.Small).TextColor(design.Muted).LineHeight(1.7)
		})
	})
}
