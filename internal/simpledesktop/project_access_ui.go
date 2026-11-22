package simpledesktop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type projectAccessState struct {
	Directory                    AccessDirectory
	Loaded, Loading, Busy        bool
	Request                      uint64
	Next                         time.Time
	Message                      string
	DirectoryError               string
	Signature                    string
	Defaults                     ProjectPolicy
	DefaultsDirty                bool
	DefaultsRevision             int64
	OpenProject, SettingsSection string
	RevealQueueProject           string
	PermissionSaved              map[string]time.Time
	Queues                       map[string]*projectQueueState
	CheckboxPending              map[string]*projectCheckboxPending
	Buffers                      map[string]*pendingBufferView
	SidebarQueueIndex            int
	QueueReadSlots               chan struct{}
}

func (a *App) accessClient() (ProjectControlClient, bool) {
	if a.Config.ProjectControl == nil || strings.TrimSpace(a.Config.ProjectControl.URL) == "" {
		return ProjectControlClient{}, false
	}
	return ProjectControlClient{Config: *a.Config.ProjectControl}, true
}

func (a *App) RefreshProjectDirectory() {
	if a.Config.GenerationSetupOnly {
		return
	}
	client, ok := a.accessClient()
	if !ok || a.access.Loading || a.access.Busy || a.Update == nil {
		return
	}
	a.access.Loading = true
	a.access.Next = time.Now().Add(15 * time.Second)
	a.access.Request++
	request := a.access.Request
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		directory, err := client.directory(ctx)
		a.Update(func() {
			if request != a.access.Request {
				return
			}
			a.access.Loading = false
			if err != nil {
				a.access.DirectoryError = err.Error()
				return
			}
			a.access.DirectoryError = ""
			a.applyAccessDirectory(directory)
		})
	}()
}

func (a *App) applyAccessDirectory(directory AccessDirectory) {
	if a.access.Loaded && directory.Revision < a.access.Directory.Revision {
		return
	}
	if directory.CodexConnection == nil {
		directory.CodexConnection = a.access.Directory.CodexConnection
	}
	data, _ := json.Marshal(directory)
	signature := fmt.Sprintf("%x", sha256.Sum256(data))
	changed := !a.access.Loaded || accessPolicyChanged(a.access.Directory, directory)
	a.confirmProjectCheckboxes(directory)
	a.access.Directory = directory
	a.access.Signature = signature
	a.access.Loaded = true
	if !a.access.DefaultsDirty {
		a.access.Defaults = directory.Defaults
		a.access.DefaultsRevision = directory.Revision
	}
	// The registry is authoritative. Keep presentation order by stable ID only.
	oldProjects, oldCommon := a.Config.Projects, a.Config.Common
	registered := make(map[string]Project, len(directory.Projects))
	for _, record := range directory.Projects {
		p := Project{ID: record.ID, Name: record.Name, Dataset: record.Dataset, CodeEnabled: record.CodeEnabled, CodeDataset: record.CodeDataset}
		if len(record.Aliases) > 0 {
			p.Path = record.Aliases[0]
		}
		registered[p.ID] = p
	}
	projects := make([]Project, 0, len(registered))
	for _, old := range oldProjects {
		if p, ok := registered[old.ID]; ok {
			projects = append(projects, p)
			delete(registered, old.ID)
		}
	}
	for _, record := range directory.Projects {
		if p, ok := registered[record.ID]; ok {
			projects = append(projects, p)
			delete(registered, record.ID)
		}
	}
	a.Config.Projects = projects
	a.Config.Common = nil
	if directory.Common != nil && directory.Common.Dataset != "" {
		common := Project{ID: directory.Common.ID, Name: "通用记忆", Dataset: directory.Common.Dataset}
		a.Config.Common = []Project{common}
	}
	a.reconcileProjectReading(oldProjects, oldCommon)
	a.Client.Config = a.Config
	if changed {
		a.SetReadingPolicyRevision(signature)
	}
}

func accessPolicyChanged(before, after AccessDirectory) bool {
	encode := func(value any) string { b, _ := json.Marshal(value); return string(b) }
	agentIDs := func(agents []AccessAgent) map[string]bool {
		ids := map[string]bool{}
		for _, agent := range agents {
			ids[agent.ID] = true
		}
		return ids
	}
	if before.Defaults != after.Defaults || encode(agentIDs(before.Agents)) != encode(agentIDs(after.Agents)) {
		return true
	}
	for _, old := range before.Projects {
		for _, current := range after.Projects {
			if old.ID != current.ID {
				continue
			}
			old.Name, current.Name = "", ""
			old.Aliases, current.Aliases = nil, nil
			old.ToolGraph, current.ToolGraph = ToolGraphSettings{}, ToolGraphSettings{}
			if encode(old) != encode(current) {
				return true
			}
			break
		}
		// Removed associations are withdrawn by reconcileProjectReading; they
		// must not invalidate unrelated, still-authorized reading panes.
	}
	return before.Common != nil && encode(before.Common) != encode(after.Common)
}

func (a *App) mutateAccess(operation func(context.Context, ProjectControlClient, int64) (AccessDirectory, error), onSuccess ...func()) {
	client, ok := a.accessClient()
	if !ok || !a.access.Loaded || a.access.Busy {
		return
	}
	a.access.Busy = true
	a.access.Loading = false
	a.access.Message = "正在保存并核对实际权限…"
	a.access.Request++
	request, revision := a.access.Request, a.access.Directory.Revision
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 125*time.Second)
		defer cancel()
		directory, err := operation(ctx, client, revision)
		a.Update(func() {
			if request != a.access.Request {
				return
			}
			a.access.Busy = false
			if err != nil {
				a.access.Message = err.Error()
				return
			}
			for _, fn := range onSuccess {
				fn()
			}
			a.applyAccessDirectory(directory)
			a.access.Message = "设置已保存；生效状态以官方核对结果为准"
		})
	}()
}

func accessLabel(access string) string {
	switch access {
	case "none":
		return "不可访问"
	case "read":
		return "只读"
	case "maintain":
		return "可写并维护记忆（含删除）"
	}
	return "未知"
}
func accessValue(label string) string {
	switch label {
	case "不可访问":
		return "none"
	case "只读":
		return "read"
	case "可写并维护记忆（含删除）":
		return "maintain"
	}
	return ""
}
func booleanLabel(value bool) string {
	if value {
		return "开启"
	}
	return "关闭"
}
func inheritedFields(directory AccessDirectory) int {
	count := 0
	for _, p := range directory.Projects {
		for _, agent := range directory.Agents {
			o := p.Overrides[agent.ID]
			if o.Access == nil {
				count++
			}
			if o.Capture == nil {
				count++
			}
			if o.AutoProcess == nil {
				count++
			}
		}
	}
	return count
}

func (a *App) accessSettings(c *ui.Context) {
	if a.Config.ProjectControl == nil {
		ui.Text(c, "项目管理服务尚未启用").FontSize(13).TextColor(muted)
		return
	}
	ui.Text(c, "项目默认规则").FontSize(17).Bold()
	if a.access.Message != "" {
		ui.Text(c, a.access.Message).FontSize(11).TextColor(muted)
	}
	if a.access.DirectoryError != "" {
		ui.Text(c, a.access.DirectoryError).FontSize(11).TextColor(muted)
	}
	refreshAction(c, "重新读取默认规则", a.RefreshProjectDirectory).Disabled(a.access.Loading || a.access.Busy)
	if !a.access.Loaded {
		ui.Text(c, "正在读取项目规则…").FontSize(12)
		return
	}
	directory := a.access.Directory
	ui.Text(c, "新项目自动接入；具体项目可逐项覆盖。可写并维护包含官方删除能力，不授予分享权限。").FontSize(12).LineHeight(1.7)
	access, capture, process := accessLabel(a.access.Defaults.Access), booleanLabel(a.access.Defaults.Capture), booleanLabel(a.access.Defaults.AutoProcess)
	policySettingRow(c, "Agent 默认访问权限", "可写并维护包含读取、写入和删除；不授予分享权限。", accessLabel(directory.Defaults.Access), false, func() {
		ui.Select(c.Key("default-access"), &access, []string{"不可访问", "只读", "可写并维护记忆（含删除）"}).Width(260).Disabled(a.access.Busy).OnChange(func() { a.access.Defaults.Access = accessValue(access); a.access.DefaultsDirty = true })
	})
	policySettingRow(c, "默认自动采集", "采集 Agent 后续的新会话材料；不补采关闭期间的内容。", booleanLabel(directory.Defaults.Capture), false, func() {
		ui.Select(c.Key("default-capture"), &capture, []string{"开启", "关闭"}).Width(180).Label("默认自动采集").Disabled(a.access.Busy).OnChange(func() { a.access.Defaults.Capture = capture == "开启"; a.access.DefaultsDirty = true })
	})
	policySettingRow(c, "默认自动提炼", "收到新材料后自动提炼记忆；关闭不取消已经运行的任务。", booleanLabel(directory.Defaults.AutoProcess), false, func() {
		ui.Select(c.Key("default-process"), &process, []string{"开启", "关闭"}).Width(180).Label("默认自动提炼").Disabled(a.access.Busy).OnChange(func() { a.access.Defaults.AutoProcess = process == "开启"; a.access.DefaultsDirty = true })
	})
	ui.Text(c, fmt.Sprintf("保存将更新 %d 个项目中的 %d 个继承字段；明确覆盖的字段保持。关闭自动提炼只停止后续触发，已运行任务保留真实状态。", len(directory.Projects), inheritedFields(directory))).FontSize(11).TextColor(muted).LineHeight(1.7)
	ui.Button(c, "保存默认规则").Disabled(a.access.Busy || !a.access.DefaultsDirty).OnClick(func() {
		policy := a.access.Defaults
		draftRevision := a.access.DefaultsRevision
		a.mutateAccess(func(ctx context.Context, client ProjectControlClient, revision int64) (AccessDirectory, error) {
			return client.defaults(ctx, draftRevision, policy)
		}, func() { a.access.DefaultsDirty = false })
	})
	if a.access.DefaultsDirty {
		textButton(c, "重新载入默认规则", nil).Disabled(a.access.Busy).OnClick(func() {
			a.access.Defaults = a.access.Directory.Defaults
			a.access.DefaultsDirty = false
			a.access.DefaultsRevision = a.access.Directory.Revision
		})
	}
}

func commonAgentAccess(common AccessProject, agentID string) string {
	if ov, ok := common.Overrides[agentID]; ok && ov.Access != nil && *ov.Access == "none" {
		return "none"
	}
	return "read"
}

func commonAccessButtonLabel(directory AccessDirectory) string {
	if directory.Common == nil || len(directory.Agents) == 0 {
		return ""
	}
	readable := 0
	for _, agent := range directory.Agents {
		if commonAgentAccess(*directory.Common, agent.ID) == "read" {
			readable++
		}
	}
	return fmt.Sprintf("Agent 权限 · %d/%d 只读", readable, len(directory.Agents))
}

func (a *App) saveCommonAgentAccess(commonID, agentID, access string) {
	if access != "none" && access != "read" {
		return
	}
	client, ok := a.accessClient()
	key := commonID + "/" + agentID
	if !ok || commonID == "" || agentID == "" || !a.access.Loaded || a.access.Busy || a.access.CheckboxPending[key] != nil || a.Update == nil {
		return
	}
	if a.access.CheckboxPending == nil {
		a.access.CheckboxPending = map[string]*projectCheckboxPending{}
	}
	pending := &projectCheckboxPending{Project: commonID, Agent: agentID, Field: "common_access", Value: access, Revision: a.access.Directory.Revision}
	a.access.CheckboxPending[key] = pending
	a.access.Busy = true
	a.access.Loading = false
	a.access.Request++
	request := a.access.Request
	a.access.Message = ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 125*time.Second)
		defer cancel()
		directory, err := client.overrides(ctx, pending.Revision, commonID, agentID, map[string]any{"access": access})
		a.Update(func() {
			if request != a.access.Request {
				return
			}
			a.access.Busy = false
			if err != nil {
				// Unknown writes stay locked until explicitly verified via directory refresh;
				// definitive 400/403/404 rejections mutated nothing and release the lock.
				a.access.Message = "未能确认修改，请核对后再操作：" + err.Error()
				if status, ok := httpStatusCode(err); ok {
					switch status {
					case 409:
						delete(a.access.CheckboxPending, key)
						a.RefreshProjectDirectory()
					case 400, 403, 404:
						delete(a.access.CheckboxPending, key)
					}
				} else if strings.Contains(err.Error(), "设置已变化") {
					delete(a.access.CheckboxPending, key)
					a.RefreshProjectDirectory()
				}
				return
			}
			a.applyAccessDirectory(directory)
		})
	}()
}

func (a *App) commonDatasetSettings(c *ui.Context) {
	directory := a.access.Directory
	ui.Text(c, "通用记忆数据组").Font("system-ui").FontSize(fsBody).FontWeight(600).TextColor(tok.Text1)
	if !a.access.Loaded {
		ui.Text(c, "正在读取通用记忆权限配置…").Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3)
	} else if directory.Common != nil {
		ui.Text(c, directory.Common.syncLabel(directory.Agents...)).Font("system-ui").FontSize(fsCaption).TextColor(tok.Text2)
	} else {
		ui.Text(c, "尚未准备通用记忆数据组，准备后可按 Agent 开启只读权限。").Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.5)
	}
	textButton(c, "准备／核对通用记忆", nil).Disabled(!a.access.Loaded || a.access.Busy).OnClick(func() {
		a.mutateAccess(func(ctx context.Context, client ProjectControlClient, revision int64) (AccessDirectory, error) {
			var result AccessDirectory
			path := "/v1/common"
			if directory.Common != nil {
				path += "/reconcile"
			}
			err := client.request(ctx, "POST", path, map[string]any{"expected_revision": revision}, &result)
			return result, err
		})
	})
	if !a.access.Loaded || directory.Common == nil {
		return
	}
	common := *directory.Common
	ui.Column(c).FillWidth().Padding(sp3, 0, 0, 0).Gap(sp2).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
		ui.Text(c, "Agent 访问权限").Font("system-ui").FontSize(fsFootnote).FontWeight(600).TextColor(tok.Text1)
		ui.Text(c, "新接入的 Agent 默认不可访问；可按需开启只读权限（通用记忆不支持 Agent 写入）。").Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.45)
		if len(directory.Agents) == 0 {
			ui.Text(c, "尚未连接任何 Agent").Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3)
			return
		}
		renderAgentRows := func() {
			for _, ag := range directory.Agents {
				agent := ag
				current := commonAgentAccess(common, agent.ID)
				key := common.ID + "/" + agent.ID
				pending := a.access.CheckboxPending[key]
				saving := pending != nil && a.access.Busy
				savedRecently := pending == nil && time.Now().Before(a.access.PermissionSaved[key])
				displayVal := current
				if saving {
					if pv, ok := pending.Value.(string); ok {
						displayVal = pv
					}
				}
				selected := 0
				if displayVal == "read" {
					selected = 1
				}
				disabled := a.access.Busy || pending != nil
				agentUnconfirmed := common.agentPolicyUnconfirmed(agent.ID)
				ui.Column(c.Key("common-access-row-"+key)).FillWidth().Gap(4).Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
						agentLogo(c, accessAgentIcon(agent), 18)
						ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
							ui.Text(c, agentDisplayName(agent.Name)).Font("system-ui").FontSize(fsFootnote).FontWeight(500).TextColor(tok.Text1).SingleLine().MaxLines(1)
							switch {
							case saving:
								ui.Row(c).AlignItems(ui.Center).Gap(4).Children(func() {
									v4Spinner(c)
									ui.Text(c, "保存中…").Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3).SingleLine()
								})
							case savedRecently:
								ui.Text(c, "已保存").Font("system-ui").FontSize(fsCaption).TextColor(tok.OK).SingleLine()
							case pending != nil:
								ui.Text(c, "待核对").Font("system-ui").FontSize(fsCaption).TextColor(tok.Danger).SingleLine()
							case agentUnconfirmed:
								ui.Text(c, "权限未确认").Font("system-ui").FontSize(fsCaption).TextColor(tok.Danger).SingleLine()
							}
						})
						seg := v4Segmented(c, "common-access-seg-"+key, agentDisplayName(agent.Name)+" 通用记忆访问权限", &selected, []string{"不可访问", "只读"}, func() {
							if disabled {
								return
							}
							next := "none"
							if selected == 1 {
								next = "read"
							}
							if next == current {
								return
							}
							a.saveCommonAgentAccess(common.ID, agent.ID, next)
						})
						if disabled {
							seg.Disabled(true).Opacity(0.55)
						}
					})
					if pending != nil && !a.access.Busy {
						errText := "尚未确认保存，已保留原权限"
						if a.access.Message != "" {
							errText = "尚未确认保存：" + a.access.Message
						}
						ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
							ui.Text(c, errText).Font("system-ui").FontSize(fsCaption).TextColor(tok.Danger).Grow(1).MinWidth(0).MaxLines(2)
							v4SmallButton(c, "重新核对", v4IconRefresh, a.RefreshProjectDirectory).Disabled(a.access.Loading)
						})
					}
				})
			}
		}
		if len(directory.Agents) > 6 {
			ui.Scroll(c.Key("common-access-scroll")).FillWidth().MaxHeight(260).TrackScroll(&a.commonPage.AccessScroll).Gap(sp2).Children(renderAgentRows)
		} else {
			ui.Column(c).FillWidth().Gap(sp2).Children(renderAgentRows)
		}
		if a.access.Message != "" && len(a.access.CheckboxPending) == 0 {
			ui.Text(c, a.access.Message).Font("system-ui").FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.45)
		}
	})
}

func accessTierDesc(access string) string {
	switch access {
	case "maintain":
		return "读取、写入并维护该项目记忆（含删除）"
	case "read":
		return "仅允许检索与读取已有记忆，不可写入"
	default:
		return "禁止访问此项目的记忆与原始记录"
	}
}

func (a *App) agentPolicyEditor(c *ui.Context, p AccessProject, agent AccessAgent) {
	actual := p.Overrides[agent.ID].effective(a.access.Directory.Defaults)
	currentVal := actual.Access
	if currentVal != "read" && currentVal != "maintain" {
		currentVal = "none"
	}
	key := p.ID + "/" + agent.ID
	pending := a.access.CheckboxPending[key]
	saving := pending != nil && a.access.Busy
	errText := ""
	displayVal := currentVal
	if saving {
		if pv, ok := pending.Value.(string); ok {
			displayVal = pv
		}
	} else if pending != nil {
		// Unconfirmed / failed write rolls back display to server-confirmed value with danger text.
		errText = "尚未确认保存，已保留原权限"
		if a.access.Message != "" {
			errText = "尚未确认保存：" + a.access.Message
		}
	}
	ui.Row(c.Key("agent-policy-"+key)).AlignItems(ui.Center).Gap(sp2).Children(func() {
		v4TriAccessSelect(c, "project-access-"+key, agentDisplayName(agent.Name), v4TriAccessState{
			Value:    displayVal,
			Saving:   saving,
			Error:    errText,
			Disabled: a.access.Busy || pending != nil,
		}, func(nextValue string) {
			a.saveProjectCheckbox(p.ID, agent.ID, "access", nextValue)
		})
		if pending != nil && !a.access.Busy {
			v4SmallButton(c, "重新核对", v4IconRefresh, a.RefreshProjectDirectory).Disabled(a.access.Loading).Tooltip(a.access.Message)
		}
	})
}
