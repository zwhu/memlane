package desktop

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

// A project owns its selection and daemon operation independently of the open
// panel. No render callback starts work or recreates an unknown operation.
type GraphSubmissionUI struct {
	OperationCancel                                                    context.CancelFunc
	Rejected                                                           bool
	IntentError                                                        bool
	WatchCancel                                                        context.CancelFunc
	Identity, IntentRoot                                               string
	Recovering, RecoveryChecked                                        bool
	Project, Kind, Filter, Source, Cursor, Next, Message, ConnectionID string
	Items                                                              []records.Version
	Sources                                                            []string
	Total                                                              int
	Chosen                                                             map[string]records.Version
	Connections                                                        []records.Connection
	Binding                                                            *records.CogneeBinding
	Processing                                                         records.ProcessingConfiguration
	Task                                                               *records.CogneeSubmissionTask
	Request                                                            *records.CogneeSubmissionRequest
	Confirm, TokenConfirmed, Loading, Busy, Watching, Loaded           bool
	Sequence                                                           uint64
	Scroll                                                             ui.ScrollState
}

func (a *App) graphSubmissionCurrent(s *GraphSubmissionUI, epoch uint64) bool {
	return s != nil && a.libraryEpoch == epoch && a.browsing.Submissions[s.Project] == s
}
func graphSubmissionTerminal(state string) bool {
	switch state {
	case "completed", "succeeded", "failed", "cancelled", "canceled", "interrupted":
		return true
	}
	return false
}
func graphSubmissionLabel(c records.ProcessingConfiguration) string {
	r := c.Current.Revision
	if !c.Current.Active || r == nil {
		return "尚未明确启用记忆处理方式"
	}
	spec := r.Spec
	label := spec.Provider
	if spec.Mode == "agent" {
		label = spec.Agent
	}
	if spec.Mode == "magpie" {
		label = "Magpie"
	}
	if label == "" {
		label = spec.Mode
	}
	return label + " · " + spec.Model
}
func (a *App) loadGraphSubmission(s *GraphSubmissionUI) {
	if s == nil || s.Loading || s.Busy || a.libraryContext == nil || a.dispatch == nil {
		return
	}
	s.Loading = true
	s.Sequence++
	seq := s.Sequence
	life, client, dispatch, epoch := a.libraryContext, a.library, a.dispatch, a.libraryEpoch
	root := a.guidanceRoot
	if root == "" {
		root = client.Root
	}
	project, kind, text, source, cursor := s.Project, s.Kind, s.Filter, s.Source, s.Cursor
	go func() {
		ctx, cancel := context.WithTimeout(life, 12*time.Second)
		defer cancel()
		page, err := client.List(ctx, records.ListQuery{Datasets: []string{project}, Kind: kind, Text: text, Source: source, Cursor: cursor, Limit: 30, Sort: "updated"})
		var config records.ProcessingConfiguration
		var conns []records.Connection
		var bindings []records.CogneeBinding
		if err == nil {
			config, err = client.ProcessingConfiguration(ctx)
		}
		if err == nil {
			conns, err = client.Connections(ctx)
		}
		if err == nil {
			bindings, err = client.CogneeBindings(ctx, project)
		}
		var identity string
		var intent *records.CogneeSubmissionRequest
		if err == nil {
			var state records.OrganizationPreviewState
			state, err = (serviceipc.OrganizationClient{Root: client.Root}).MaterialPreview(ctx)
			identity = state.Identity
		}
		if err == nil {
			intent, err = graphIntent(ctx, root, identity, project, "read", nil)
		}
		if life.Err() != nil {
			return
		}
		dispatch(func() {
			if !a.graphSubmissionCurrent(s, epoch) || s.Sequence != seq {
				return
			}
			s.Loading = false
			if err != nil {
				s.Message = "项目材料读取未完成；保留当前选择，请确认服务可用后重试。"
				return
			}
			s.Identity, s.IntentRoot = identity, root
			if intent != nil {
				s.Request = intent
			}
			s.Items = nil
			for _, v := range page.Items {
				if v.Dataset == project && v.Origin == kind && v.Revision > 0 {
					s.Items = append(s.Items, v)
				}
			}
			s.Sources, s.Next, s.Total = page.Sources, page.NextCursor, page.Total
			s.Processing = config
			s.Connections = nil
			s.Binding = nil
			for _, c := range conns {
				if c.Enabled && (c.Agent == "pi" || c.Agent == "codex") && connectionAllows(c, project) {
					s.Connections = append(s.Connections, c)
				}
			}
			for _, b := range bindings {
				if b.Project == project && b.MaterialSubmission {
					copy := b
					s.Binding = &copy
					break
				}
			}
			if s.ConnectionID == "" && len(s.Connections) == 1 {
				s.ConnectionID = s.Connections[0].ID
			}
			s.Loaded = true
			s.Message = ""
			a.recoverGraphSubmission(s)
		})
	}()
}
func (a *App) selectGraphSubmissionMaterial(s *GraphSubmissionUI, v records.Version, selected bool) {
	if s == nil || s.Busy || s.Request != nil || v.Dataset != s.Project || v.Origin != s.Kind || v.Revision < 1 {
		return
	}
	if s.Chosen == nil {
		s.Chosen = map[string]records.Version{}
	}
	if !selected {
		delete(s.Chosen, v.Memory)
		s.Confirm = false
		s.TokenConfirmed = false
		return
	}
	if _, exists := s.Chosen[v.Memory]; !exists && len(s.Chosen) >= 16 {
		s.Message = "每次最多明确选择 16 份材料。"
		return
	}
	s.Chosen[v.Memory] = v
	s.Confirm = false
	s.TokenConfirmed = false
}
func graphSubmissionBindingValid(s *GraphSubmissionUI) bool {
	if s.Binding == nil || !s.Binding.MaterialSubmission || s.Binding.Project != s.Project || s.Binding.ConnectionID != s.ConnectionID {
		return false
	}
	for _, c := range s.Connections {
		if c.ID == s.Binding.ConnectionID && c.Enabled && c.Generation == s.Binding.Generation && connectionAllows(c, s.Project) {
			return true
		}
	}
	return false
}
func (a *App) authorizeGraphSubmission(s *GraphSubmissionUI) {
	if s == nil || s.Busy || !s.Loaded || a.libraryContext == nil {
		return
	}
	var chosen *records.Connection
	for _, c := range s.Connections {
		if c.ID == s.ConnectionID {
			copy := c
			chosen = &copy
			break
		}
	}
	if chosen == nil {
		s.Message = "请明确选择已获本项目授权的连接。"
		return
	}
	q := records.CogneeProjectAuthorization{Operation: uuid.NewString(), Project: s.Project, ConnectionID: chosen.ID, Generation: chosen.Generation}
	s.Busy = true
	life, client, dispatch, epoch := a.libraryContext, a.library, a.dispatch, a.libraryEpoch
	ctx, cancel := context.WithTimeout(life, 25*time.Second)
	s.OperationCancel = cancel
	go func() {
		defer cancel()
		binding, err := client.AuthorizeProjectCogneeMaterial(ctx, q)
		if life.Err() != nil {
			return
		}
		dispatch(func() {
			if !a.graphSubmissionCurrent(s, epoch) {
				return
			}
			s.Busy = false
			if err != nil {
				s.Message = "项目图材料授权未确认；请重新读取当前项目，不会自动重试授权。"
				return
			}
			if binding.Project != s.Project || !binding.MaterialSubmission || binding.ConnectionID != q.ConnectionID || binding.Generation != q.Generation {
				s.Message = "项目图材料授权回执不匹配，未允许提交。"
				return
			}
			s.Binding = &binding
			s.Message = "当前项目图材料范围已确认。"
		})
	}()
}
func (a *App) prepareGraphSubmission(s *GraphSubmissionUI) {
	if s == nil || s.Busy || s.Request != nil || !s.RecoveryChecked || !s.Loaded || len(s.Chosen) == 0 || len(s.Chosen) > 16 || !graphSubmissionBindingValid(s) || !s.Processing.Current.Active || s.Processing.Current.Revision == nil {
		return
	}
	s.Confirm = true
	s.TokenConfirmed = false
}
func (a *App) startGraphSubmission(s *GraphSubmissionUI) {
	if s == nil || !s.RecoveryChecked || !s.Confirm || !s.TokenConfirmed || s.Busy || s.Request != nil || !graphSubmissionBindingValid(s) || !s.Processing.Current.Active || s.Processing.Current.Revision == nil || len(s.Chosen) == 0 || len(s.Chosen) > 16 {
		return
	}
	sources := []records.MergeSource{}
	for _, v := range s.Chosen {
		if v.Dataset != s.Project || v.Revision < 1 || (v.Origin != "raw" && v.Origin != "knowledge") {
			s.Message = "材料身份需要重新核验。"
			return
		}
		sources = append(sources, records.MergeSource{Memory: v.Memory, Revision: v.Revision})
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Memory < sources[j].Memory })
	q := records.CogneeSubmissionRequest{Operation: uuid.NewString(), Project: s.Project, Dataset: s.Binding.Dataset, Sources: sources, Processing: records.ProcessingTaskAuthorization{Revision: s.Processing.Current.Revision.ID, Generation: s.Processing.Current.Generation}}
	s.Request = &q
	s.Busy = true
	s.Confirm = false
	a.dispatchGraphSubmission(s)
}
func (a *App) dispatchGraphSubmission(s *GraphSubmissionUI) {
	if s.Request == nil || a.libraryContext == nil {
		return
	}
	q := *s.Request
	s.Busy = true
	s.IntentError = false
	s.Rejected = false
	life, client, dispatch, epoch := a.libraryContext, a.library, a.dispatch, a.libraryEpoch
	root, identity := s.IntentRoot, s.Identity
	ctx, cancel := context.WithTimeout(life, 15*time.Second)
	s.OperationCancel = cancel
	go func() {
		defer cancel()
		_, err := graphIntent(ctx, root, identity, q.Project, "save", &q)
		persisted := err == nil
		var task records.CogneeSubmissionTask
		if err == nil {
			task, err = client.StartCogneeSubmission(ctx, q)
		}
		if life.Err() != nil {
			return
		}
		dispatch(func() {
			if !a.graphSubmissionCurrent(s, epoch) {
				return
			}
			s.Busy = false
			if !persisted {
				s.IntentError = true
				s.Message = "原操作标识尚未确认本地持久保存，未启动处理；请重试原操作保存。"
				return
			}
			if err != nil {
				var rejected *serviceipc.ServiceError
				if errors.As(err, &rejected) {
					switch rejected.Code {
					case "busy", "processing_changed", "processing_required", "processing_unavailable", "scope_denied", "cognee_authorization_required", "source_changed", "material_stale", "material_missing", "invalid_request":
						s.Rejected = true
						s.Message = "服务明确未接受本次提交，请重新读取并核对材料、授权与当前处理配置。"
						return
					}
				}
				s.Message = "启动回执尚未确认；只查询原操作，不会自动再次启动。"
				a.readGraphSubmission(s, false)
				return
			}
			a.adoptGraphSubmissionTask(s, task, q.Operation, epoch)
		})
	}()
}
func (a *App) adoptGraphSubmissionTask(s *GraphSubmissionUI, t records.CogneeSubmissionTask, operation string, epoch uint64) {
	if t.Project != s.Project || t.Operation != operation || !manualUUID(t.Operation) || !manualUUID(t.Dataset) || s.Request != nil && t.Dataset != s.Request.Dataset {
		s.Message = "任务回执与当前项目或操作不匹配。"
		return
	}
	s.Task = &t
	s.Message = ""
	if graphSubmissionTerminal(t.State) {
		if t.State == "completed" || t.State == "succeeded" {
			if a.page == "项目" && a.scope == s.Project && a.libraryEpoch == epoch {
				a.refreshBrowsing()
			}
		}
		return
	}
	a.watchGraphSubmission(s)
}
func (a *App) readGraphSubmission(s *GraphSubmissionUI, cancelTask bool) {
	if s == nil || s.Busy || a.libraryContext == nil {
		return
	}
	operation := ""
	if s.Request != nil {
		operation = s.Request.Operation
	}
	if s.Task != nil {
		operation = s.Task.Operation
	}
	if operation == "" {
		return
	}
	s.Busy = true
	life, client, dispatch, epoch := a.libraryContext, a.library, a.dispatch, a.libraryEpoch
	go func() {
		ctx, cancel := context.WithTimeout(life, 10*time.Second)
		defer cancel()
		var t records.CogneeSubmissionTask
		var err error
		if cancelTask {
			t, err = client.CancelCogneeSubmission(ctx, operation)
		} else {
			t, err = client.CogneeSubmission(ctx, operation)
		}
		if life.Err() != nil {
			return
		}
		dispatch(func() {
			if !a.graphSubmissionCurrent(s, epoch) {
				return
			}
			s.Busy = false
			if err != nil {
				s.Message = "原操作状态未确认，操作标识保留；请重试查询，不会另启任务。"
				return
			}
			a.adoptGraphSubmissionTask(s, t, operation, epoch)
		})
	}()
}
func (a *App) watchGraphSubmission(s *GraphSubmissionUI) {
	if s.Watching || s.Task == nil || graphSubmissionTerminal(s.Task.State) {
		return
	}
	s.Watching = true
	life, client, dispatch, epoch := a.libraryContext, a.library, a.dispatch, a.libraryEpoch
	watchLife, watchCancel := context.WithCancel(life)
	s.WatchCancel = watchCancel
	operation, project := s.Task.Operation, s.Project
	go func() {
		defer watchCancel()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-watchLife.Done():
				return
			case <-ticker.C:
			}
			ctx, cancel := context.WithTimeout(watchLife, 8*time.Second)
			t, err := client.CogneeSubmission(ctx, operation)
			cancel()
			if watchLife.Err() != nil {
				return
			}
			terminal := err == nil && t.Operation == operation && t.Project == project && graphSubmissionTerminal(t.State)
			dispatch(func() {
				if !a.graphSubmissionCurrent(s, epoch) {
					return
				}
				if err != nil {
					s.Message = "任务状态暂未读到，后台任务不因离页停止。"
					return
				}
				if terminal {
					s.Watching = false
				}
				a.adoptGraphSubmissionTask(s, t, operation, epoch)
			})
			if terminal {
				return
			}
		}
	}()
}

// Service-owned latest is supplied below once the current-library read returns.
func (a *App) recoverGraphSubmission(s *GraphSubmissionUI) {
	if s.Task != nil {
		a.watchGraphSubmission(s)
		return
	}
	if s.Request != nil {
		a.readGraphSubmission(s, false)
		return
	}
	if s.Recovering || s.RecoveryChecked {
		return
	}
	s.Recovering = true
	life, client, dispatch, epoch := a.libraryContext, a.library, a.dispatch, a.libraryEpoch
	project := s.Project
	go func() {
		ctx, cancel := context.WithTimeout(life, 8*time.Second)
		defer cancel()
		t, err := client.LatestCogneeSubmission(ctx, project)
		if life.Err() != nil {
			return
		}
		dispatch(func() {
			if !a.graphSubmissionCurrent(s, epoch) {
				return
			}
			s.Recovering = false
			if err != nil {
				s.Message = "历史任务状态未核验，暂不能新建提交；请重新读取。"
				return
			}
			s.RecoveryChecked = true
			if t.State != "none" {
				a.adoptGraphSubmissionTask(s, t, t.Operation, epoch)
			}
		})
	}()
}

func (a *App) graphMaterialPanel(c *ui.Context) {
	m := a.browsing.Material
	if m == nil {
		return
	}
	if m.Legacy {
		ui.Row(c).Shrink(0).Padding(8, 24).Children(func() { quietButton(c, "返回项目材料提交").OnClick(func() { m.Legacy = false }) })
		a.legacyGraphMaterialPanel(c)
		return
	}
	s := m.Submission
	if s == nil {
		return
	}
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Scroll(c).Key("graph-submission-body").Grow(1).MinHeight(0).TrackScroll(&s.Scroll).Padding(24).Gap(14).Children(func() {
			ui.Text(c, a.scopeLabel(s.Project)).FontSize(design.Title).Bold()
			ui.Text(c, "选择本项目的确切材料版本，提交到真实 Cognee 生成实体与关系。知识与原始记录保持各自类型。").FontSize(12).LineHeight(1.8)
			if s.Message != "" {
				ui.Text(c, s.Message).Key("graph-submission-message").FontSize(11)
			}
			if s.Loading {
				ui.Text(c, "正在读取本项目材料与授权…").FontSize(11)
			}
			if s.Task != nil {
				t := s.Task
				ui.Text(c, "本次图材料处理").Bold()
				label := graphSubmissionStateLabel(t.State)
				if t.CancelRequested && !graphSubmissionTerminal(t.State) {
					label = "正在取消并等待清理"
				}
				ui.Text(c, fmt.Sprintf("%s · %d/%d 份材料", label, t.Completed, t.Materials)).FontSize(12)
				ui.Text(c, "操作 "+t.Operation).FontSize(10).Selectable()
				ui.Text(c, "本次请求模型："+t.ProcessingMode+" · "+t.RequestedModel).FontSize(11)
				for _, v := range t.Sources {
					kind := "知识"
					if v.Origin == "raw" {
						kind = "原始记录"
					}
					ui.Text(c, fmt.Sprintf("%s · %s · v%d", kind, v.Title, v.Revision)).FontSize(11)
				}
				if !graphSubmissionTerminal(t.State) {
					ui.Text(c, "关闭面板或离开项目不会取消后台任务；取消须等待实际清理终态。").FontSize(11)
				}
			} else if s.Request != nil {
				ui.Text(c, "原操作回执未确认，保留操作 "+s.Request.Operation).FontSize(11).Selectable()
			}
			if s.Request == nil && s.Task == nil {
				ui.Text(c, "当前记忆处理："+graphSubmissionLabel(s.Processing)).FontSize(11)
				if !s.Processing.Current.Active || s.Processing.Current.Revision == nil {
					quietButton(c, "前往记忆处理").OnClick(func() { a.navigate("设置", ""); a.settingsSection = "processing"; a.refreshProcessing() })
				}
				labels := []string{}
				selected := ""
				for _, conn := range s.Connections {
					label := conn.Name + " · " + conn.ID[:min(8, len(conn.ID))]
					labels = append(labels, label)
					if conn.ID == s.ConnectionID {
						selected = label
					}
				}
				if len(labels) > 0 {
					ui.Select(c.Key("graph-submission-connection"), &selected, labels).Label("本项目已授权连接").Disabled(s.Busy).OnChange(func() {
						for i, l := range labels {
							if selected == l {
								s.ConnectionID = s.Connections[i].ID
								if s.Binding != nil && s.Binding.ConnectionID != s.ConnectionID {
									s.Binding = nil
								}
								s.Confirm = false
								s.TokenConfirmed = false
							}
						}
					})
				} else {
					ui.Text(c, "尚无明确授权本项目的 pi/Codex 连接。请在项目设置管理范围。").FontSize(11)
					quietButton(c, "管理本项目授权").OnClick(func() { a.openProjectSettings(s.Project) })
				}
				if graphSubmissionBindingValid(s) {
					ui.Text(c, "当前项目图材料范围已授权。").FontSize(11)
				} else {
					quietButton(c, "确认本项目图材料范围").Key("graph-submission-authorize").Disabled(s.Busy || s.Loading || len(s.Connections) == 0).OnClick(func() { a.authorizeGraphSubmission(s) })
				}
				kindLabel := "知识"
				if s.Kind == "raw" {
					kindLabel = "原始记录"
				}
				ui.Select(c.Key("graph-submission-kind"), &kindLabel, []string{"知识", "原始记录"}).Label("材料类型").Disabled(s.Busy || s.Loading).OnChange(func() {
					s.Kind = "knowledge"
					if kindLabel == "原始记录" {
						s.Kind = "raw"
					}
					s.Cursor, s.Source = "", ""
					s.Confirm = false
					a.loadGraphSubmission(s)
				})
				ui.TextInput(c.Key("graph-submission-filter"), &s.Filter).Label("搜索当前项目材料").Placeholder("按标题或正文搜索当前项目").Disabled(s.Busy || s.Loading)
				ui.Row(c).Wrap().Gap(8).Children(func() {
					quietButton(c, "筛选材料").Disabled(s.Busy || s.Loading).OnClick(func() { s.Cursor = ""; a.loadGraphSubmission(s) })
					quietButton(c, "重新读取").Disabled(s.Busy || s.Loading).OnClick(func() { a.loadGraphSubmission(s) })
				})
				sourceKeys, sourceOptions := readingSourceOptions(s.Sources, s.Source)
				sourceLabel := sourceOptions[0]
				for i, key := range sourceKeys {
					if key == s.Source {
						sourceLabel = sourceOptions[i]
					}
				}
				ui.Select(c.Key("graph-submission-source"), &sourceLabel, sourceOptions).Label("来源").Disabled(s.Busy || s.Loading).OnChange(func() {
					for i, label := range sourceOptions {
						if label == sourceLabel {
							s.Source = sourceKeys[i]
							break
						}
					}
					s.Cursor = ""
					a.loadGraphSubmission(s)
				})

				ui.Text(c, fmt.Sprintf("当前筛选 %d 份 · 已选择 %d/16", s.Total, len(s.Chosen))).FontSize(11)
				if s.Loaded && len(s.Items) == 0 {
					ui.Text(c, "没有匹配材料；切换类型或调整筛选，不会自动选择其他材料。").FontSize(11)
				}
				for _, v := range s.Items {
					v := v
					checked := s.Chosen[v.Memory].Revision == v.Revision
					kind := "知识"
					if v.Origin == "raw" {
						kind = "原始记录"
					}
					ui.Checkbox(c.Key("graph-material-"+v.Memory), &checked, fmt.Sprintf("%s · %s · v%d", kind, v.Title, v.Revision)).Disabled(s.Busy || s.Loading || !checked && len(s.Chosen) >= 16).OnChange(func() { a.selectGraphSubmissionMaterial(s, v, checked) })
				}
				ui.Row(c).Gap(8).Children(func() {
					quietButton(c, "返回第一页").Disabled(s.Cursor == "" || s.Loading).OnClick(func() { s.Cursor = ""; a.loadGraphSubmission(s) })
					quietButton(c, "下一页材料").Disabled(s.Next == "" || s.Loading).OnClick(func() { s.Cursor = s.Next; a.loadGraphSubmission(s) })
				})
				if len(s.Chosen) > 0 {
					ui.Text(c, "本次明确选择的版本").FontSize(11).Bold()
					keys := []string{}
					for key := range s.Chosen {
						keys = append(keys, key)
					}
					sort.Strings(keys)
					for _, key := range keys {
						v := s.Chosen[key]
						kind := "知识"
						if v.Origin == "raw" {
							kind = "原始记录"
						}
						ui.Text(c, fmt.Sprintf("%s · %s · v%d", kind, v.Title, v.Revision)).FontSize(11)
					}
					quietButton(c, "清空选择").Disabled(s.Busy).OnClick(func() { s.Chosen = map[string]records.Version{}; s.Confirm = false; s.TokenConfirmed = false })
				}
				if s.Confirm {
					ui.Divider(c)
					ui.Text(c, fmt.Sprintf("确认处理所选 %d 份材料", len(s.Chosen))).Bold()
					ui.Text(c, "将使用 "+graphSubmissionLabel(s.Processing)+"。此操作会调用已启用的处理方式并消耗 Token；准确用量以执行回执为准。").FontSize(11).LineHeight(1.8)
					ui.Checkbox(c.Key("graph-submission-token-confirm"), &s.TokenConfirmed, "确认本次范围并接受 Token 消耗").Disabled(s.Busy)
				}
			}
			ui.Divider(c)
			quietButton(c, "已有会话材料接入（其他来源）").Key("graph-submission-legacy").OnClick(func() {
				m.Legacy = true
				if !m.Loaded {
					a.loadGraphMaterial()
				}
			})
		})
		ui.Row(c).Key("graph-submission-footer").Shrink(0).Wrap().Padding(14, 24).Gap(10).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
			countLabel := fmt.Sprintf("已选择 %d/16", len(s.Chosen))
			if s.Task != nil {
				countLabel = fmt.Sprintf("本次 %d 份材料", s.Task.Materials)
			} else if s.Request != nil {
				countLabel = fmt.Sprintf("本次 %d 份材料", len(s.Request.Sources))
			}
			ui.Text(c, countLabel).FontSize(11)
			ui.Spacer(c)
			if s.Task != nil || s.Request != nil {
				if s.Rejected {
					quietButton(c, "重新读取并选材").Key("graph-submission-rejected-new").Disabled(s.Busy).OnClick(func() { a.newGraphSubmission(s) })
				}
				if s.IntentError {
					quietButton(c, "重试原操作保存与启动").Key("graph-submission-retry-intent").Disabled(s.Busy).OnClick(func() { a.dispatchGraphSubmission(s) })
				}
				quietButton(c, "查询原操作").Key("graph-submission-query").Disabled(s.Busy).OnClick(func() { a.readGraphSubmission(s, false) })
				if s.Task != nil && !graphSubmissionTerminal(s.Task.State) {
					quietButton(c, "取消并等待清理").Key("graph-submission-cancel").Disabled(s.Busy || s.Task.CancelRequested).OnClick(func() { a.readGraphSubmission(s, true) })
				}
				if s.Task != nil && graphSubmissionTerminal(s.Task.State) && s.Task.State != "interrupted" {
					quietButton(c, "重新选材").Key("graph-submission-new").Disabled(s.Busy).OnClick(func() {
						a.newGraphSubmission(s)
					})
				}
			} else if s.Confirm {
				actionButton(c, "确认并开始处理", nil).Key("graph-submission-start").Disabled(!s.TokenConfirmed || s.Busy).OnClick(func() { a.startGraphSubmission(s) })
			} else {
				actionButton(c, "核对所选材料", nil).Key("graph-submission-prepare").Disabled(s.Busy || s.Loading || !s.RecoveryChecked || len(s.Chosen) == 0 || !graphSubmissionBindingValid(s) || !s.Processing.Current.Active || s.Processing.Current.Revision == nil).OnClick(func() { a.prepareGraphSubmission(s) })
			}
		})
	})
}
func graphSubmissionStateLabel(state string) string {
	switch state {
	case "prepared", "queued":
		return "等待处理"
	case "running":
		return "正在处理"
	case "cancelling", "cancel_requested":
		return "正在取消并清理"
	case "cancelled", "canceled":
		return "已取消并完成清理"
	case "completed", "succeeded":
		return "处理完成"
	case "failed":
		return "处理失败"
	case "interrupted":
		return "处理中断，清理或服务状态待核验"
	}
	return "状态待核验"
}

func (a *App) newGraphSubmission(s *GraphSubmissionUI) {
	if s == nil || s.Busy || !(s.Rejected || s.Task != nil && graphSubmissionTerminal(s.Task.State) && s.Task.State != "interrupted") {
		return
	}
	s.Busy = true
	life, dispatch, epoch := a.libraryContext, a.dispatch, a.libraryEpoch
	root, identity, project, prior := s.IntentRoot, s.Identity, s.Project, s.Request
	go func() {
		ctx, cancel := context.WithTimeout(life, 5*time.Second)
		defer cancel()
		_, err := graphIntent(ctx, root, identity, project, "clear", prior)
		if life.Err() != nil {
			return
		}
		dispatch(func() {
			if !a.graphSubmissionCurrent(s, epoch) {
				return
			}
			s.Busy = false
			if err != nil {
				s.Message = "上一操作标识尚未确认清除，原状态保留；未新建任务。"
				return
			}
			s.Task = nil
			s.Rejected = false
			s.Request = nil
			s.Chosen = map[string]records.Version{}
			s.TokenConfirmed = false
			s.Confirm = false
			s.Cursor = ""
			s.Message = "已结束的任务记录保留；请重新明确选择当前材料版本。"
			s.RecoveryChecked = true
			a.loadGraphSubmission(s)
		})
	}()
}

func (a *App) retireGraphSubmissions() {
	for _, s := range a.browsing.Submissions {
		if s.OperationCancel != nil {
			s.OperationCancel()
		}
		if s.WatchCancel != nil {
			s.WatchCancel()
		}
	}
	a.browsing.Submissions = map[string]*GraphSubmissionUI{}
	a.browsing.Material = nil
}
