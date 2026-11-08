package desktop

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/codegraph"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

type CodeUI struct {
	Cancel                                                         context.CancelFunc
	Client                                                         serviceipc.CodeClient
	States                                                         []records.CodeState
	Selected, Project, ProjectLabel, Path, Text, Mode, Node, Error string
	Manage, Busy, Loading                                          bool
	Sequence                                                       int64
	Result                                                         *records.CodeResult
	Jobs                                                           []records.CodeJob
}

func (a *App) codeSelected() *records.CodeState {
	for i := range a.code.States {
		if a.code.States[i].ID == a.code.Selected {
			return &a.code.States[i]
		}
	}
	return nil
}
func codeState(s string) string {
	if t := map[string]string{"disabled": "未开启", "queued": "等待扫描", "indexing": "正在建图", "ready": "就绪", "stale": "来源已变化", "failed": "扫描失败", "interrupted": "已中断", "cancelled": "已取消"}[s]; t != "" {
		return t
	}
	return s
}
func (a *App) refreshCode() {
	if a.page != "代码图谱" || a.dispatch == nil || a.code.Loading {
		return
	}
	a.code.Loading = true
	a.code.Sequence++
	seq := a.code.Sequence
	selected := a.code.Selected
	go func() {
		ctx, cancel := context.WithTimeout(a.libraryContext, 10*time.Second)
		defer cancel()
		states, e := a.code.Client.List(ctx)
		var jobs []records.CodeJob
		if selected != "" && e == nil {
			jobs, e = a.code.Client.Jobs(ctx, selected)
		}
		a.dispatch(func() {
			if seq != a.code.Sequence {
				return
			}
			a.code.Loading = false
			if e != nil {
				a.code.Error = e.Error()
				return
			}
			a.code.States = states
			a.code.Jobs = jobs
			if c := a.codeSelected(); c != nil && a.code.Result != nil && (c.Generation != a.code.Result.State.Generation || c.Snapshot != a.code.Result.State.Snapshot || !c.Enabled) {
				a.code.Result = nil
				a.code.Node = ""
			}
			if c := a.codeSelected(); c != nil && a.code.Result != nil && c.Manifest != nil && c.ObservedDigest != "" && c.ObservedDigest != c.Manifest.Digest {
				a.code.Result = nil
				a.code.Node = ""
				a.code.Error = "代码来源已变化，等待最新扫描后重新查询。"
			}
			if c := a.codeSelected(); c != nil && a.code.Result != nil && (c.State == "stale" || c.State == "failed" || c.State == "interrupted") {
				a.code.Result = nil
				a.code.Node = ""
				a.code.Error = "代码来源已过期或暂时无法核验，请重新扫描后查询。"
			}
		})
	}()
}
func (a *App) codeAction(kind string) {
	if a.code.Busy || a.dispatch == nil {
		return
	}
	r := records.CodeAction{OperationID: uuid.NewString(), Kind: kind, Project: a.code.Project, Path: a.code.Path}
	if c := a.codeSelected(); c != nil {
		r.ID = c.ID
		r.ExpectedGeneration = c.Generation
		if kind == "enable" {
			r.Project = c.Project
			r.Path = c.Worktree
		}
	}
	a.code.Busy = true
	a.code.Sequence++
	a.code.Loading = false
	a.code.Result = nil
	a.code.Node = ""
	a.code.Error = ""
	seq, selected := a.code.Sequence, a.code.Selected
	go func() {
		ctx, cancel := context.WithTimeout(a.libraryContext, 15*time.Second)
		defer cancel()
		out, e := a.code.Client.Action(ctx, r)
		a.dispatch(func() {
			a.code.Busy = false
			if seq != a.code.Sequence || selected != a.code.Selected {
				a.refreshCode()
				return
			}
			if e != nil {
				a.code.Error = e.Error()
				return
			}
			a.code.Selected = out.ID
			a.code.Manage = true
			a.refreshCode()
		})
	}()
}
func (a *App) queryCode(mode, node string) {
	if mapped := map[string]string{"函数与符号": "symbols", "文件": "files", "调用": "calls", "依赖": "dependencies"}[mode]; mapped != "" {
		mode = mapped
	}
	c := a.codeSelected()
	if c == nil || !c.Enabled || a.code.Busy || a.dispatch == nil {
		return
	}
	a.code.Busy = true
	a.code.Error = ""
	a.code.Sequence++
	a.code.Loading = false
	seq := a.code.Sequence
	id := c.ID
	a.code.Mode = map[string]string{"symbols": "函数与符号", "files": "文件", "calls": "调用", "dependencies": "依赖", "impact": "调用"}[mode]
	q := records.CodeQuery{ID: id, Mode: mode, Text: a.code.Text, Node: node, Limit: 30, Hops: 3}
	ctx, cancel := context.WithTimeout(a.libraryContext, 120*time.Second)
	a.code.Cancel = cancel
	go func() {
		defer cancel()
		result, e := a.code.Client.Query(ctx, q)
		a.dispatch(func() {
			if seq != a.code.Sequence || a.code.Selected != id {
				return
			}
			a.code.Busy = false
			a.code.Cancel = nil
			if e != nil {
				a.code.Error = e.Error()
				a.code.Result = nil
				a.refreshCode()
				return
			}
			a.code.Result = &result
			a.code.Manage = false
			a.code.Node = ""
			if len(result.Graph.Nodes) > 0 {
				a.code.Node = result.Graph.Nodes[0].ID
			}
		})
	}()
}
func (a *App) newCode() {
	if a.code.Cancel != nil {
		a.code.Cancel()
		a.code.Cancel = nil
		a.code.Busy = false
	}
	a.code.Sequence++
	a.code.Loading = false
	a.code.Selected = ""
	a.code.Node = ""
	a.code.Result = nil
	a.code.Error = ""
	a.code.Manage = true
	if a.scope != "" {
		a.code.Project = a.scope
	}
	a.navigate("代码图谱", "")
}
func (a *App) codeList(c *ui.Context) {
	for _, state := range a.code.States {
		current := state
		fill := ui.Transparent
		if current.ID == a.code.Selected {
			fill = design.ListSelected
		}
		ui.Button(c, "").Key("code-worktree-" + current.ID).Padding(10).Background(fill).BorderWidth(0).Justify(ui.Start).Children(func() {
			ui.Column(c).Gap(5).Children(func() {
				ui.Text(c, filepath.Base(current.Worktree)).FontWeight(600)
				ui.Text(c, codeState(current.State)).FontSize(design.Small).TextColor(design.Muted)
			})
		}).OnClick(func() {
			if a.code.Cancel != nil {
				a.code.Cancel()
				a.code.Cancel = nil
				a.code.Busy = false
			}
			a.code.Sequence++
			a.code.Loading = false
			a.code.Selected = current.ID
			a.code.Manage = true
			a.code.Result = nil
			a.code.Node = ""
			a.code.Error = ""
			a.refreshCode()
		})
	}
	if a.code.Result != nil {
		for _, n := range a.code.Result.Graph.Nodes {
			node := n
			fill := ui.Transparent
			if node.ID == a.code.Node {
				fill = design.ListSelected
			}
			ui.Button(c, "").Key("code-node-" + node.ID).Background(fill).BorderWidth(0).Padding(10).Justify(ui.Start).Children(func() {
				ui.Column(c).Gap(5).Children(func() {
					ui.Text(c, node.Name).FontWeight(600)
					ui.Text(c, fmt.Sprintf("%s · %s:%d", node.Language, node.File, node.Line)).FontSize(design.Small).TextColor(design.Muted)
				})
			}).OnClick(func() { a.code.Node = node.ID; a.code.Manage = false })
		}
	}
	if len(a.code.States) == 0 {
		a.empty(c, "尚未开启代码图谱", "选择一个已登记项目，明确开启后才扫描。")
	}
}
func (a *App) codeDetail(c *ui.Context) {
	ui.Scroll(c).Grow(1).Padding(24).Gap(14).Children(func() {
		if a.code.Error != "" {
			ui.Text(c, a.code.Error).FontSize(design.Small).TextColor(design.Muted)
		}
		selected := a.codeSelected()
		if a.code.Manage || selected == nil {
			ui.Text(c, "项目代码图谱").FontSize(design.Title).Bold()
			ui.Text(c, "只读 TypeScript/Python 静态代码；原文件保持原位，索引写入 Memlane 管理目录。明确开启后才扫描。").FontSize(design.Small).LineHeight(1.6).TextColor(design.Muted)
			if selected == nil {
				options := []string{}
				for _, p := range a.projects {
					options = append(options, p.Name+" · "+p.ID[:8])
				}
				ui.Select(c.Key("code-project"), &a.code.ProjectLabel, options).Label("项目").Disabled(a.code.Busy).OnChange(func() {
					for _, p := range a.projects {
						if p.Name+" · "+p.ID[:8] == a.code.ProjectLabel {
							a.code.Project = p.ID
							a.code.Path = p.Path
						}
					}
				})
				ui.TextInput(c.Key("code-path"), &a.code.Path).Label("工作区路径").Placeholder("项目或 worktree 的绝对路径").Disabled(a.code.Busy)
				actionButton(c.Key("code-enable"), "开启代码图谱", icons["项目"]).Disabled(a.code.Busy || a.code.Project == "" || a.code.Path == "").OnClick(func() { a.codeAction("enable") })
				return
			}
			ui.Text(c, selected.Worktree).Selectable().FontSize(design.Small)
			ui.Text(c, codeState(selected.State)).FontWeight(600)
			if selected.Error != "" {
				ui.Text(c, selected.Error).FontSize(design.Small).TextColor(design.Muted)
			}
			ui.Row(c).Gap(8).Children(func() {
				if selected.Enabled {
					quietButton(c.Key("code-refresh"), "重新扫描").Disabled(a.code.Busy).OnClick(func() { a.codeAction("refresh") })
					quietButton(c.Key("code-disable"), "停用扫描").Disabled(a.code.Busy).OnClick(func() { a.codeAction("disable") })
				} else {
					quietButton(c.Key("code-enable"), "重新开启").Disabled(a.code.Busy).OnClick(func() { a.codeAction("enable") })
				}
			})
			if selected.Manifest != nil {
				ui.Text(c, fmt.Sprintf("最近成功快照 · %d 个文件", len(selected.Manifest.Files))).FontSize(design.Small).TextColor(design.Muted)
				ui.Text(c, selected.Manifest.Commit).Selectable().FontSize(design.Small)
			}
			if selected.Enabled && selected.Snapshot != "" {
				a.codeSearchForm(c)
			}
			for _, j := range a.code.Jobs {
				ui.Text(c, fmt.Sprintf("%s · %s", time.UnixMilli(j.CreatedMS).Local().Format("01-02 15:04"), j.State)).FontSize(design.Small).TextColor(design.Muted)
			}
			return
		}
		a.codeSearchForm(c)
		if a.code.Result == nil {
			a.empty(c, "", "执行查询以查看当前快照。")
			return
		}
		var node *codegraph.Node
		for i := range a.code.Result.Graph.Nodes {
			if a.code.Result.Graph.Nodes[i].ID == a.code.Node {
				node = &a.code.Result.Graph.Nodes[i]
				break
			}
		}
		if node == nil {
			return
		}
		ui.Text(c, node.Name).FontSize(design.Title).Bold().Selectable()
		ui.Text(c, fmt.Sprintf("%s · %s", node.Language, node.SymbolKind)).FontSize(design.Small).TextColor(design.Muted)
		ui.Text(c, fmt.Sprintf("%s:%d", node.File, node.Line)).Selectable()
		if node.Unresolved {
			ui.Text(c, "外部目标尚未解析，不代表完整运行时调用。").FontSize(design.Small).TextColor(design.Muted)
		}
		ui.Text(c, "来源 SHA · "+node.FileSHA).FontSize(design.Small).Selectable()
		ui.Text(c, "快照 · "+a.code.Result.State.Snapshot).FontSize(design.Small).Selectable()
		if a.code.Result.Graph.Truncated {
			ui.Text(c, "结果达到查询上限；缩小关键词继续查询。").FontSize(design.Small).TextColor(design.Muted)
		}
		ui.Row(c).Gap(8).Children(func() {
			quietButton(c.Key("code-calls"), "查看调用").Disabled(a.code.Busy).OnClick(func() { a.queryCode("calls", node.ID) })
			quietButton(c.Key("code-impact"), "可能影响范围").Disabled(a.code.Busy).OnClick(func() { a.queryCode("impact", node.ID) })
			quietButton(c.Key("code-manage"), "管理扫描").OnClick(func() { a.code.Manage = true })
		})
		ui.Text(c, "影响范围是静态候选，需要结合项目测试判断。").FontSize(design.Small).TextColor(design.Muted)
		labels := map[string]string{}
		for _, n := range a.code.Result.Graph.Nodes {
			labels[n.ID] = n.Name
		}
		for _, e := range a.code.Result.Graph.Edges {
			ui.Text(c, labels[e.Source]+" → "+e.Kind+" → "+labels[e.Target]).FontSize(design.Small).Selectable()
		}
	})
}
func (a *App) codeSearchForm(c *ui.Context) {
	ui.TextInput(c.Key("code-query"), &a.code.Text).Label("代码查询").Placeholder("函数、文件或依赖关键词").Disabled(a.code.Busy)
	ui.Select(c.Key("code-query-mode"), &a.code.Mode, []string{"函数与符号", "文件", "调用", "依赖"}).Label("查询类型").Disabled(a.code.Busy)
	quietButton(c.Key("code-search"), "查询当前代码").Disabled(a.code.Busy).OnClick(func() { a.queryCode(a.code.Mode, "") })
}
