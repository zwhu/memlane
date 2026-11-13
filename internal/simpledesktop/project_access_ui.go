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
	Directory                                     AccessDirectory
	Loaded, Loading, Busy                         bool
	Request                                       uint64
	Next                                          time.Time
	Message                                       string
	DirectoryError                                string
	Signature                                     string
	Defaults                                      ProjectPolicy
	DefaultsDirty                                 bool
	DefaultsRevision                              int64
	OpenProject, SettingsSection                  string
	CreateName, Workspace, CreateMode, LinkChoice string
	Linkable                                      []LinkableDataset
	PendingExpanded                               bool
	CheckboxPending                               map[string]*projectCheckboxPending
	Buffers                                       map[string]*pendingBufferView
}

func (a *App) accessClient() (ProjectControlClient, bool) {
	if a.Config.ProjectControl == nil {
		return ProjectControlClient{}, false
	}
	return ProjectControlClient{Config: *a.Config.ProjectControl}, true
}

func (a *App) RefreshProjectDirectory() {
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
	// Keep existing order and code associations. A newly discovered project is
	// appended, so an unchanged catalog never moves a reading pane's index.
	projects := append([]Project(nil), a.Config.Projects...)
	for _, record := range directory.Projects {
		p := Project{ID: record.ID, Name: record.Name, Dataset: record.Dataset, CodeEnabled: record.CodeEnabled, CodeDataset: record.CodeDataset}
		if len(record.Aliases) > 0 {
			p.Path = record.Aliases[0]
		}
		found := false
		for idx, old := range projects {
			if old.ID == p.ID || (p.Dataset != "" && old.Dataset == p.Dataset) {
				if !record.CodeConfigured {
					p.CodeDataset, p.CodeEnabled = old.CodeDataset, old.CodeEnabled
				}
				projects[idx] = p
				found = true
				break
			}
		}
		if !found {
			projects = append(projects, p)
		}
	}
	a.Config.Projects = projects
	if directory.Common != nil && directory.Common.Dataset != "" {
		common := Project{ID: directory.Common.ID, Name: "通用知识", Dataset: directory.Common.Dataset}
		found := false
		for idx, old := range a.Config.Common {
			if old.Dataset == common.Dataset {
				a.Config.Common[idx] = common
				found = true
				break
			}
		}
		if !found {
			a.Config.Common = append(a.Config.Common, common)
		}
	}
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
		found := false
		for _, current := range after.Projects {
			if old.ID != current.ID {
				continue
			}
			found = true
			old.Name, current.Name = "", ""
			old.Aliases, current.Aliases = nil, nil
			if encode(old) != encode(current) {
				return true
			}
			break
		}
		if !found {
			return true
		}
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
		return "可写并维护知识（含删除）"
	}
	return "未知"
}
func accessValue(label string) string {
	switch label {
	case "不可访问":
		return "none"
	case "只读":
		return "read"
	case "可写并维护知识（含删除）":
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
	ui.Button(c, "重新读取默认规则").Disabled(a.access.Loading || a.access.Busy).OnClick(a.RefreshProjectDirectory)
	if !a.access.Loaded {
		ui.Text(c, "正在读取项目规则…").FontSize(12)
		return
	}
	directory := a.access.Directory
	ui.Text(c, "新项目自动接入；具体项目可逐项覆盖。可写并维护包含官方删除能力，不授予分享权限。").FontSize(12).LineHeight(1.7)
	access, capture, process := accessLabel(a.access.Defaults.Access), booleanLabel(a.access.Defaults.Capture), booleanLabel(a.access.Defaults.AutoProcess)
	policySettingRow(c, "Agent 默认访问权限", "可写并维护包含读取、写入和删除；不授予分享权限。", accessLabel(directory.Defaults.Access), false, func() {
		ui.Select(c.Key("default-access"), &access, []string{"不可访问", "只读", "可写并维护知识（含删除）"}).Width(260).Disabled(a.access.Busy).OnChange(func() { a.access.Defaults.Access = accessValue(access); a.access.DefaultsDirty = true })
	})
	policySettingRow(c, "默认自动采集", "保存 Agent 后续的新会话材料；不补采关闭期间的内容。", booleanLabel(directory.Defaults.Capture), false, func() {
		ui.Select(c.Key("default-capture"), &capture, []string{"开启", "关闭"}).Width(180).Label("默认自动采集").Disabled(a.access.Busy).OnChange(func() { a.access.Defaults.Capture = capture == "开启"; a.access.DefaultsDirty = true })
	})
	policySettingRow(c, "默认自动处理", "收到新材料后自动整理知识；关闭不取消已经运行的任务。", booleanLabel(directory.Defaults.AutoProcess), false, func() {
		ui.Select(c.Key("default-process"), &process, []string{"开启", "关闭"}).Width(180).Label("默认自动处理").Disabled(a.access.Busy).OnChange(func() { a.access.Defaults.AutoProcess = process == "开启"; a.access.DefaultsDirty = true })
	})
	ui.Text(c, fmt.Sprintf("保存将更新 %d 个项目中的 %d 个继承字段；明确覆盖的字段保持。关闭自动处理只停止后续触发，已运行任务保留真实状态。", len(directory.Projects), inheritedFields(directory))).FontSize(11).TextColor(muted).LineHeight(1.7)
	ui.Button(c, "保存默认规则").Disabled(a.access.Busy || !a.access.DefaultsDirty).OnClick(func() {
		policy := a.access.Defaults
		draftRevision := a.access.DefaultsRevision
		a.mutateAccess(func(ctx context.Context, client ProjectControlClient, revision int64) (AccessDirectory, error) {
			return client.defaults(ctx, draftRevision, policy)
		}, func() { a.access.DefaultsDirty = false })
	})
	if a.access.DefaultsDirty {
		ui.Button(c, "重新载入默认规则").Disabled(a.access.Busy).OnClick(func() {
			a.access.Defaults = a.access.Directory.Defaults
			a.access.DefaultsDirty = false
			a.access.DefaultsRevision = a.access.Directory.Revision
		})
	}
}

func (a *App) commonDatasetSettings(c *ui.Context) {
	directory := a.access.Directory
	ui.Text(c, "通用知识数据组").FontSize(17).Bold()
	if directory.Common != nil {
		ui.Text(c, directory.Common.syncLabel(directory.Agents...)).FontSize(11).TextColor(muted)
	}
	ui.Button(c, "准备／核对通用知识").Disabled(a.access.Busy).OnClick(func() {
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
}

func (a *App) agentPolicyEditor(c *ui.Context, p AccessProject, agent AccessAgent) {
	actual := p.Overrides[agent.ID].effective(a.access.Directory.Defaults)
	read, maintain := actual.Access != "none" && actual.Access != "", actual.Access == "maintain"
	key := p.ID + "/" + agent.ID
	pending := a.access.CheckboxPending[key]
	if pending != nil {
		switch pending.Field {
		case "access":
			read = pending.Value != "none"
			maintain = pending.Value == "maintain"

		}
	}
	locked := a.access.Busy || pending != nil
	ui.Column(c.Key("agent-policy-" + key)).Gap(20).Children(func() {
		ui.Column(c).Gap(14).Children(func() {
			ui.Checkbox(c.Key("project-read-"+key), &read, "允许读取知识").Disabled(locked).OnChange(func() {
				value := "none"
				if read {
					value = "read"
				}
				a.saveProjectCheckbox(p.ID, agent.ID, "access", value)
			})
			ui.Checkbox(c.Key("project-write-"+key), &maintain, "允许写入和删除知识").Disabled(locked || !read).OnChange(func() {
				value := "read"
				if maintain {
					value = "maintain"
				}
				a.saveProjectCheckbox(p.ID, agent.ID, "access", value)
			})
		})
		if pending != nil {
			homeText(c, "修改结果待确认", homeMeta)
			commonButton(c, "核对修改结果", false, a.RefreshProjectDirectory).Disabled(a.access.Loading || a.access.Busy)
		}
	})
}

func (a *App) projectRegistration(c *ui.Context) {
	ui.Text(c, "新建／关联项目").FontSize(17).Bold()
	if a.access.CreateMode == "" {
		a.access.CreateMode = "新建项目"
	}
	ui.Select(c, &a.access.CreateMode, []string{"新建项目", "关联已有项目", "关联已有官方数据组"}).Width(280).Disabled(a.access.Busy)
	ui.TextInput(c, &a.access.CreateName).Placeholder("项目显示名称").Height(34).Label("项目显示名称").Disabled(a.access.Busy)
	ui.TextInput(c, &a.access.Workspace).Placeholder("选择或输入本机仓库路径").Height(34).Label("项目仓库路径").Disabled(a.access.Busy)
	choices := []string{}
	if a.access.CreateMode == "关联已有项目" {
		for _, p := range a.access.Directory.Projects {
			choices = append(choices, p.Name+" · "+strings.Join(p.Aliases, ", "))
		}
	}
	if a.access.CreateMode == "关联已有官方数据组" {
		ui.Button(c, "读取可关联数据组").Disabled(a.access.Busy).OnClick(func() {
			client, ok := a.accessClient()
			if !ok {
				return
			}
			a.access.Busy = true
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				rows, err := client.linkableDatasets(ctx)
				a.Update(func() {
					a.access.Busy = false
					if err != nil {
						a.access.Message = err.Error()
					} else {
						a.access.Linkable = rows
					}
				})
			}()
		})
		for _, p := range a.access.Linkable {
			choices = append(choices, p.Name)
		}
	}
	if len(choices) > 0 {
		ui.Select(c, &a.access.LinkChoice, choices).Width(560).Disabled(a.access.Busy)
	}
	ui.Text(c, "同仓库自动复用；同名不同路径分开。关联已有数据组先保留已有权限事实，不自动放宽。").FontSize(11).TextColor(muted)
	ui.Button(c, "保存项目关联").Disabled(a.access.Busy).OnClick(func() {
		workspace, name, mode, choice := strings.TrimSpace(a.access.Workspace), strings.TrimSpace(a.access.CreateName), a.access.CreateMode, a.access.LinkChoice
		if workspace == "" {
			a.access.Message = "请提供本机仓库路径"
			return
		}
		identifier := ""
		if mode == "关联已有项目" {
			for idx, label := range choices {
				if label == choice {
					identifier = a.access.Directory.Projects[idx].ID
				}
			}
		}
		if mode == "关联已有官方数据组" {
			for idx, label := range choices {
				if label == choice {
					identifier = a.access.Linkable[idx].ID
				}
			}
		}
		if mode != "新建项目" && identifier == "" {
			a.access.Message = "请选择要关联的项目或数据组"
			return
		}
		a.mutateAccess(func(ctx context.Context, client ProjectControlClient, revision int64) (AccessDirectory, error) {
			var err error
			if mode == "关联已有项目" {
				_, err = client.alias(ctx, revision, identifier, workspace)
			} else {
				_, err = client.create(ctx, revision, name, workspace, identifier)
			}
			if err != nil {
				return AccessDirectory{}, err
			}
			return client.directory(ctx)
		})
	})
}
