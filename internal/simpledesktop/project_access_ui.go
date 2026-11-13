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

type overrideDraft struct {
	Access, Capture, Process string
	Revision                 int64
}

type projectAccessState struct {
	Directory                                     AccessDirectory
	Loaded, Loading, Busy                         bool
	Request                                       uint64
	Next                                          time.Time
	Message                                       string
	Signature                                     string
	Defaults                                      ProjectPolicy
	DefaultsDirty                                 bool
	DefaultsRevision                              int64
	Overrides                                     map[string]*overrideDraft
	OpenProject                                   string
	CreateName, Workspace, CreateMode, LinkChoice string
	Linkable                                      []LinkableDataset
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
				a.access.Message = err.Error()
				return
			}
			a.applyAccessDirectory(directory)
		})
	}()
}

func (a *App) applyAccessDirectory(directory AccessDirectory) {
	if a.access.Loaded && directory.Revision < a.access.Directory.Revision {
		return
	}
	data, _ := json.Marshal(directory)
	signature := fmt.Sprintf("%x", sha256.Sum256(data))
	changed := !a.access.Loaded || accessPolicyChanged(a.access.Directory, directory)
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
			a.access.Message = "设置已保存；各项目实际生效状态见下方"
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
	ui.Button(c, "刷新项目与权限状态").Disabled(a.access.Loading || a.access.Busy).OnClick(a.RefreshProjectDirectory)
	if !a.access.Loaded {
		ui.Text(c, "正在读取项目规则…").FontSize(12)
		return
	}
	directory := a.access.Directory
	ui.Text(c, "新项目自动接入；具体项目可逐项覆盖。可写并维护包含官方删除能力，不授予分享权限。").FontSize(12).LineHeight(1.7)
	access, capture, process := accessLabel(a.access.Defaults.Access), booleanLabel(a.access.Defaults.Capture), booleanLabel(a.access.Defaults.AutoProcess)
	ui.Row(c).Gap(12).Children(func() {
		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, "默认访问").FontSize(10).TextColor(muted)
			ui.Select(c.Key("default-access"), &access, []string{"不可访问", "只读", "可写并维护知识（含删除）"}).Width(260).Disabled(a.access.Busy).OnChange(func() { a.access.Defaults.Access = accessValue(access); a.access.DefaultsDirty = true })
		})
		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, "自动采集").FontSize(10).TextColor(muted)
			ui.Select(c.Key("default-capture"), &capture, []string{"开启", "关闭"}).Width(130).Label("默认自动采集").Disabled(a.access.Busy).OnChange(func() { a.access.Defaults.Capture = capture == "开启"; a.access.DefaultsDirty = true })
		})
		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, "自动处理").FontSize(10).TextColor(muted)
			ui.Select(c.Key("default-process"), &process, []string{"开启", "关闭"}).Width(130).Label("默认自动处理").Disabled(a.access.Busy).OnChange(func() { a.access.Defaults.AutoProcess = process == "开启"; a.access.DefaultsDirty = true })
		})
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
	ui.Text(c, "项目设置").FontSize(17).Bold()
	if len(directory.Agents) == 0 {
		ui.Text(c, "尚无受管 Agent；接入后按默认规则计算权限。").FontSize(12).TextColor(muted)
	}
	for _, p := range directory.Projects {
		project := p
		label := project.Name
		labels := projectLabels(a.Config.Projects)
		for i, row := range a.Config.Projects {
			if row.ID == project.ID {
				label = labels[i]
				break
			}
		}
		ui.Column(c.Key("project-policy-"+p.ID)).Border(1, border).Radius(8).Padding(16).Gap(10).Children(func() {
			ui.Row(c).Gap(12).Children(func() {
				ui.Text(c, label).FontSize(14).Bold()
				ui.Spacer(c)
				ui.Text(c, project.syncLabel(directory.Agents...)).FontSize(11).TextColor(muted)
				button(c, "设置", func() {
					if a.access.OpenProject == project.ID {
						a.access.OpenProject = ""
					} else {
						a.access.OpenProject = project.ID
					}
				})
			})
			if a.access.OpenProject != project.ID {
				return
			}
			for _, agent := range directory.Agents {
				a.agentPolicyEditor(c, project, agent)
			}
			ui.Button(c, "重新核对权限").Disabled(a.access.Busy).OnClick(func() {
				a.mutateAccess(func(ctx context.Context, client ProjectControlClient, revision int64) (AccessDirectory, error) {
					return client.reconcile(ctx, revision, project.ID)
				})
			})
		})
	}
	a.projectRegistration(c)
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
	if a.access.Overrides == nil {
		a.access.Overrides = map[string]*overrideDraft{}
	}
	key := p.ID + "/" + agent.ID
	draft := a.access.Overrides[key]
	if draft == nil {
		draft = &overrideDraft{Access: "继承默认", Capture: "继承默认", Process: "继承默认", Revision: a.access.Directory.Revision}
		o := p.Overrides[agent.ID]
		if o.Access != nil {
			draft.Access = accessLabel(*o.Access)
		}
		if o.Capture != nil {
			draft.Capture = booleanLabel(*o.Capture)
		}
		if o.AutoProcess != nil {
			draft.Process = booleanLabel(*o.AutoProcess)
		}
		a.access.Overrides[key] = draft
	}
	ui.Column(c.Key("agent-policy-" + key)).Gap(8).Children(func() {
		ui.Text(c, agent.Name).FontSize(12).Bold()
		actual := p.Overrides[agent.ID].effective(a.access.Directory.Defaults)
		ui.Text(c, "当前规则："+accessLabel(actual.Access)+" · 自动采集"+booleanLabel(actual.Capture)+" · 自动处理"+booleanLabel(actual.AutoProcess)).FontSize(11).TextColor(muted)
		ui.Row(c).Gap(8).Children(func() {
			ui.Column(c).Gap(4).Children(func() {
				ui.Text(c, "访问规则").FontSize(10).TextColor(muted)
				ui.Select(c.Key("override-access-"+key), &draft.Access, []string{"继承默认", "不可访问", "只读", "可写并维护知识（含删除）"}).Width(260).Disabled(a.access.Busy)
			})
			ui.Column(c).Gap(4).Children(func() {
				ui.Text(c, "自动采集").FontSize(10).TextColor(muted)
				ui.Select(c.Key("override-capture-"+key), &draft.Capture, []string{"继承默认", "开启", "关闭"}).Width(130).Label("项目自动采集").Disabled(a.access.Busy)
			})
			ui.Column(c).Gap(4).Children(func() {
				ui.Text(c, "自动处理").FontSize(10).TextColor(muted)
				ui.Select(c.Key("override-process-"+key), &draft.Process, []string{"继承默认", "开启", "关闭"}).Width(130).Label("项目自动处理").Disabled(a.access.Busy)
			})
		})
		ui.Row(c).Gap(10).Children(func() {
			ui.Button(c, "保存项目规则").Disabled(a.access.Busy).OnClick(func() {
				fields := map[string]any{"access": nil, "capture": nil, "auto_process": nil}
				if draft.Access != "继承默认" {
					fields["access"] = accessValue(draft.Access)
				}
				if draft.Capture != "继承默认" {
					fields["capture"] = draft.Capture == "开启"
				}
				if draft.Process != "继承默认" {
					fields["auto_process"] = draft.Process == "开启"
				}
				a.mutateAccess(func(ctx context.Context, client ProjectControlClient, revision int64) (AccessDirectory, error) {
					return client.overrides(ctx, draft.Revision, p.ID, agent.ID, fields)
				}, func() { delete(a.access.Overrides, key) })
			})
			ui.Button(c, "恢复全部默认").Disabled(a.access.Busy).OnClick(func() {
				a.mutateAccess(func(ctx context.Context, client ProjectControlClient, revision int64) (AccessDirectory, error) {
					return client.overrides(ctx, revision, p.ID, agent.ID, map[string]any{"access": nil, "capture": nil, "auto_process": nil})
				}, func() { delete(a.access.Overrides, key) })
			})
			ui.Button(c, "重新载入项目规则").Disabled(a.access.Busy).OnClick(func() { delete(a.access.Overrides, key) })
		})
		a.pendingMaterialsEditor(c, p, agent)
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
