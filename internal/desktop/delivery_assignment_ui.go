package desktop

import (
	"context"
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

type DeliveryAssignmentUI struct {
	Root, Selected, Error, ReturnFocus, ProjectReturn string
	Loading, Loaded, ReadAfterList                    bool
	Sequence                                          uint64
	Items                                             []records.AgentDeliveryItem
	Panels                                            map[string]*DeliveryAssignmentPanel
	ListScroll                                        ui.ScrollState
}
type DeliveryAssignmentPanel struct {
	CaptureName                                         string
	CaptureRoute                                        *records.CaptureRoute
	CaptureReceipt                                      *records.CaptureDeliveryReceipt
	Review                                              *records.AgentDeliveryReview
	KnownRejected, AckPending                           bool
	EntryID, Root, SelectedProject, Choice, Error, Info string
	Preview                                             *records.DeliveryAssignmentPreview
	Pending                                             *records.DeliveryAssignmentIntent
	Receipt                                             *records.DeliveryAssignmentReceipt
	Loading, Busy                                       bool
	Sequence                                            uint64
	Cancel                                              context.CancelFunc
	Scroll                                              ui.ScrollState
}

func (a *App) bindDeliveryAssignment() {
	if a.deliveryAssignment.Root != a.library.Root {
		a.retireDeliveryAssignment()
		a.deliveryAssignment.Root = a.library.Root
	}
	if a.deliveryAssignment.Panels == nil {
		a.deliveryAssignment.Panels = map[string]*DeliveryAssignmentPanel{}
	}
}
func (a *App) retireDeliveryAssignment() {
	for _, p := range a.deliveryAssignment.Panels {
		if p.Cancel != nil {
			p.Cancel()
		}
	}
	a.deliveryAssignment = DeliveryAssignmentUI{}
}
func (a *App) assignmentOwner(p *DeliveryAssignmentPanel, epoch uint64) bool {
	return p != nil && a.libraryEpoch == epoch && a.library.Root == p.Root && a.deliveryAssignment.Root == p.Root && a.deliveryAssignment.Panels[p.EntryID] == p
}
func (a *App) openDeliveryAssignment(id string) {
	a.bindDeliveryAssignment()
	a.navigate("待归属提交", "")
	a.deliveryAssignment.Selected = id
	a.deliveryAssignment.ReadAfterList = id != ""
	a.refreshDeliveryAssignments()
}
func (a *App) closeDeliveryAssignment() {
	a.deliveryAssignment.ReturnFocus = a.deliveryAssignment.Selected
	a.openAgentDelivery()
}
func (a *App) deliveryAssignmentProjectReturn(c *ui.Context) {
	s := &a.deliveryAssignment
	if s.ProjectReturn != "" && a.page == "项目" && a.scope == s.ProjectReturn && s.Root == a.library.Root {
		quietButton(c, "返回归属核对").Key("assignment-project-return").OnClick(func() { a.openDeliveryAssignment(s.Selected) })
	}
}
func (a *App) deliveryAssignmentToolbar(c *ui.Context) {
	ui.Text(c, "待归属提交").Bold()
	ui.Spacer(c)

}
func (a *App) refreshDeliveryAssignments() {
	a.bindDeliveryAssignment()
	s := &a.deliveryAssignment
	if s.Loading || a.libraryContext == nil || a.dispatch == nil {
		return
	}
	s.Loading = true
	s.Error = ""
	s.Sequence++
	seq := s.Sequence
	root, life, dispatch, epoch := a.library.Root, a.libraryContext, a.dispatch, a.libraryEpoch
	go func() {
		page, err := readAgentDeliveryPage(life, root)
		dispatch(func() {
			if life.Err() != nil || a.libraryContext != life || a.library.Root != root || a.libraryEpoch != epoch || s.Sequence != seq {
				return
			}
			s.Loading = false
			if s.ReadAfterList {
				s.ReadAfterList = false
				defer a.readDeliveryAssignment(s.Selected)
			}
			if err != nil {
				s.Error = "待归属列表尚未核验，请重新读取；不能据此判断为空。"
				return
			}
			s.Items = nil
			for _, item := range page.Items {
				if !item.HasGrant && item.Attempts == 0 && !item.Status.PersistedConfirmed && (item.Assignment == nil || item.Assignment.State != "saved") {
					s.Items = append(s.Items, item)
				}
			}
			s.Loaded = true
		})
	}()
}
func assignmentProjectChoice(projects []records.Scope, id string) string {
	for _, p := range projects {
		if p.ID == id {
			count := 0
			for _, other := range projects {
				if other.Name == p.Name {
					count++
				}
			}
			if count > 1 {
				return p.Name + " · " + p.Path
			}
			return p.Name
		}
	}
	return "请选择项目"
}
func assignmentChoices(projects []records.Scope) []string {
	result := []string{"请选择项目"}
	seen := map[string]bool{}
	for _, p := range projects {
		label := assignmentProjectChoice(projects, p.ID)
		if !seen[label] {
			result = append(result, label)
			seen[label] = true
		}
	}
	return result
}
func (a *App) chooseDeliveryAssignment(p *DeliveryAssignmentPanel, label string) {
	if p == nil || p.Preview == nil || p.Busy || p.Loading || p.Pending != nil {
		return
	}
	id := ""
	matches := 0
	for _, scope := range p.Preview.Projects {
		if assignmentProjectChoice(p.Preview.Projects, scope.ID) == label {
			id = scope.ID
			matches++
		}
	}
	if matches != 1 {
		id = ""
	}
	p.SelectedProject = id
	p.Choice = label
	p.Receipt = nil
	a.readDeliveryAssignment(p.EntryID)
}
func validAssignmentPreview(v records.DeliveryAssignmentPreview, id, project string) bool {
	return v.EntryID == id && v.Item.Status.ID == id && v.RequestDigest != "" && v.ReviewDigest != "" && manualUUID(v.LibraryID) && v.SelectedProject == project && v.Arguments.Text != "" && (v.Item.Origin == "" || v.Item.Origin == "knowledge" || v.Item.Origin == "raw")
}
func canConfirmAssignment(p *DeliveryAssignmentPanel) bool {
	if p == nil || p.Loading || p.Busy || p.Preview == nil || p.Pending != nil || p.Receipt != nil {
		return false
	}
	v := p.Preview
	return v.CanConfirm && (v.Code == "ready" || v.Code == "") && manualUUID(p.SelectedProject) && v.SelectedProject == p.SelectedProject && v.MatchingProject != nil && v.MatchingProject.ID == p.SelectedProject && v.MatchingProject.Kind == "project" && v.WorkspaceDigest != "" && v.Workspace != "" && !v.Item.HasGrant && v.Item.Attempts == 0 && !v.Item.Status.PersistedConfirmed
}
func assignmentFailure(code string) string {
	switch code {
	case "workspace_mismatch":
		return "该来源对应已登记的项目。可打开匹配项目，或重新核对来源。"
	case "workspace_changed":
		return "来源目录已变化，当前仓库归属尚未核验。原提交保留，不能确认。"
	case "capture_repository_changed":
		return "当前仓库与采集时的身份不同。原始记录保留，不能自动归入当前项目。"
	case "capture_repository_unverified":
		return "采集时未能确认仓库身份。原始记录保留，暂不归入项目或通用知识。"
	case "project_unregistered":
		return "来源仓库尚未登记为项目；请先核对并登记实际仓库，再重新读取。"
	case "workspace_unavailable", "workspace_unverified":
		return "来源工作目录尚未核验；原提交保留，不能确认。"
	case "choose_project", "scope_required", "project_required":
		return "请选择实际来源所属的已登记项目。"
	case "delivery_assignment_ineligible", "delivery_already_attempted":
		return "原提交已有授权或发送记录，不能通过归属操作更换范围。请返回查看原提交状态。"
	case "assignment_saved":
		return "已取得独立用户归属回执；原 Agent 暂存收尾单独核验。"
	case "original_already_saved", "delivery_already_saved":
		return "原 Agent 提交已确认保存，不会另存一条归属记录。"
	case "library_changed", "delivery_library_changed":
		return "记忆库身份已变化，不能将原确认改绑到当前库。"
	default:
		return "来源或归属尚未完成核验，不能确认；原提交仍保留。"
	}
}
func (a *App) deliveryAssignmentLayout(c *ui.Context) {
	a.bindDeliveryAssignment()
	s := &a.deliveryAssignment
	width, _ := c.Size()
	listWidth := float32(316)
	if width < 1040 {
		listWidth = 216
	}
	ui.Row(c).Key("assignment-layout").Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(listWidth).Shrink(0).BorderWidth(0, 1, 0, 0).BorderColor(design.Border).Children(func() {
			ui.Row(c).Padding(20, 16).Gap(8).Shrink(0).Children(func() {
				ui.Text(c, "待归属提交").Bold()
				ui.Spacer(c)
				count := "未核验"
				if s.Loaded && !s.Loading && s.Error == "" {
					count = fmt.Sprintf("本次 %d 条", len(s.Items))
				}
				ui.Text(c, count).FontSize(10).TextColor(design.Muted)
			})
			ui.Scroll(c).Key("assignment-list").TrackScroll(&s.ListScroll).Grow(1).MinHeight(0).Padding(12, 16).Gap(10).Children(func() {
				if s.Loading {
					ui.Text(c, "正在读取本机待归属提交…").FontSize(11)
				}
				if s.Error != "" {
					ui.Text(c, s.Error).FontSize(11).LineHeight(1.6)
				}
				if s.Loaded && !s.Loading && s.Error == "" && len(s.Items) == 0 {
					ui.Text(c, "本次读取没有待归属提交。").FontSize(11)
				}
				for _, item := range s.Items {
					item := item
					title := item.Title
					if title == "" {
						title = "未命名提交"
					}
					ui.Button(c, "").Key("assignment-item-" + item.Status.ID).Justify(ui.Start).Padding(12).BorderWidth(0).Background(func() ui.Color {
						if s.Selected == item.Status.ID {
							return ui.Hex("#edf3f8")
						}
						return ui.Transparent
					}()).Children(func() {
						ui.Column(c).Gap(7).Children(func() {
							ui.Text(c, title).Bold().SingleLine().MinWidth(0)
							ui.Text(c, "提交已到达，项目尚待核验。").FontSize(10).LineHeight(1.5)
						})
					}).OnClick(func() { s.Selected = item.Status.ID; a.readDeliveryAssignment(item.Status.ID) })
				}
			})
			quietButton(c, "重新读取待归属列表").Disabled(s.Loading).OnClick(a.refreshDeliveryAssignments)
		})
		ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
			ui.Row(c).Padding(18, 22).Shrink(0).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
				ui.Text(c, "确认项目归属").FontSize(12)
				ui.Spacer(c)
				quietButton(c, "返回待发送").OnClick(a.closeDeliveryAssignment)
			})
			p := s.Panels[s.Selected]
			if p == nil {
				a.empty(c, "选择一条待归属提交", "核对真实来源和项目，再决定本次保存归属。")
				return
			}
			a.deliveryAssignmentBody(c, p)
			a.deliveryAssignmentFooter(c, p)
		})
	})
}
func (a *App) deliveryAssignmentBody(c *ui.Context, p *DeliveryAssignmentPanel) {
	if _, ok := capturedAssignmentOrigin(p); ok {
		a.capturedAssignmentBody(c, p)
		return
	}
	ui.Scroll(c).Key("assignment-body").TrackScroll(&p.Scroll).Grow(1).MinHeight(0).Padding(24).Gap(16).Children(func() {
		ui.Text(c, "尚未归属的提交").FontSize(11).TextColor(design.Muted)
		ui.Text(c, "先核对仓库，再确认项目").FontSize(design.Heading).Bold()
		if p.Loading {
			ui.Text(c, "正在核验真实来源与已登记仓库…").FontSize(11)
		}
		if p.Error != "" {
			ui.Text(c, p.Error).FontSize(11).LineHeight(1.7)
		}
		if p.Info != "" {
			ui.Text(c, p.Info).FontSize(11).LineHeight(1.7)
		}
		if p.Pending != nil || p.Receipt != nil {
			ui.Text(c, "请重新加载已运行的 Agent 连接；原有本机暂存会保留。").FontSize(11).LineHeight(1.7)
		}
		v := p.Preview
		if v == nil && p.Review != nil {
			r := p.Review
			ui.Text(c, "本机原始提交 · 项目归属尚未核验").FontSize(11).Bold()
			kind := "知识"
			if r.Item.Origin == "raw" {
				kind = "原始记录"
			}
			ui.Text(c, kind+" · "+assignmentAgentLabel(r.Item.Agent)).FontSize(11)
			ui.Text(c, "提交时工作目录："+r.Item.Workspace).Selectable().LineHeight(1.6)
			ui.Text(c, a.conflictReadableText(c, "assignment-local-original-"+p.EntryID, r.Arguments.Text)).Selectable().LineHeight(1.7)
		}
		if v != nil {
			ui.Column(c).Gap(6).Children(func() {
				kind := "知识"
				if v.Item.Origin == "raw" {
					kind = "原始记录"
				}
				ui.Text(c, kind+" · "+assignmentAgentLabel(v.Item.Agent)).FontSize(11).TextColor(design.Muted)
				ui.Text(c, "来源工作目录 · "+v.Workspace).Selectable().FontSize(11).LineHeight(1.7)
				if v.MatchingProject != nil {
					ui.Text(c, "对应「"+v.MatchingProject.Name+"」，同仓库 worktree 共用该项目。").FontSize(11).LineHeight(1.7)
				}
			})
			ui.Text(c, "归属项目").Bold().FontSize(11)
			ui.Select(c.Key("assignment-project"), &p.Choice, assignmentChoices(v.Projects)).Label("归属项目").Disabled(p.Loading || p.Busy || p.Pending != nil || p.Receipt != nil).OnChange(func() { a.chooseDeliveryAssignment(p, p.Choice) })
			if v.Code != "" && v.Code != "ready" {
				ui.Column(c).Padding(14).Gap(10).Border(1, design.Border).Radius(6).Background(ui.Hex("#fffcf7")).Children(func() {
					if v.Code == "workspace_mismatch" {
						ui.Text(c, "所选项目与实际仓库不一致").Bold().FontSize(11)
					}
					ui.Text(c, assignmentFailure(v.Code)).Key("assignment-verification-state").FontSize(11).LineHeight(1.7)
					if v.MatchingProject != nil && v.MatchingProject.ID != p.SelectedProject && v.MatchingProject.Kind == "project" {
						project := *v.MatchingProject
						quietButton(c, "打开匹配的项目").Key("assignment-open-matched").AlignSelf(ui.Start).OnClick(func() { a.deliveryAssignment.ProjectReturn = project.ID; a.navigate("项目", project.ID) })
					}
				})
			}
			ui.Text(c, "原始提交全文").Bold().FontSize(12)
			ui.Text(c, a.conflictReadableText(c, "assignment-original-"+p.EntryID, v.Arguments.Text)).Selectable().LineHeight(1.7)
			quietButton(c, "复制原始提交全文").OnClick(func() { c.WriteClipboard(v.Arguments.Text) })
		}
		if p.Pending != nil {
			ui.Text(c, "原归属确认操作："+p.Pending.OperationID).Selectable().FontSize(10).TextColor(design.Muted)
		}
		if p.Receipt != nil {
			r := p.Receipt
			ui.Text(c, fmt.Sprintf("用户确认归属已保存 · v%d", r.Revision)).Key("assignment-saved-receipt").Bold()
			ui.Text(c, "独立用户归属操作；不表示原 Agent 提交已取得保存回执。").FontSize(11).LineHeight(1.6)
		}
	})
}
func (a *App) deliveryAssignmentFooter(c *ui.Context, p *DeliveryAssignmentPanel) {
	if _, ok := capturedAssignmentOrigin(p); ok {
		a.capturedAssignmentFooter(c, p)
		return
	}
	ui.Column(c).Key("assignment-footer").Shrink(0).Padding(12, 22).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		ui.Text(c, "本次仅确认归属，不增加 Agent 项目授权。").FontSize(10).LineHeight(1.5)
		ui.Row(c).Wrap().Gap(10).Children(func() {
			if p.Receipt != nil {
				if p.AckPending {
					actionButton(c, "重试原归属请求", nil).Disabled(p.Busy || p.Loading || p.KnownRejected).OnClick(func() { a.confirmDeliveryAssignment(p) })
				}
				quietButton(c, "查看已归属记录").OnClick(func() { a.openAssignedRecord(p) })
			} else if p.Pending != nil {
				actionButton(c, "重试原归属请求", nil).Disabled(p.Busy || p.Loading || p.KnownRejected).OnClick(func() { a.confirmDeliveryAssignment(p) })
			} else {
				actionButton(c, "确认项目归属", nil).Disabled(!canConfirmAssignment(p)).OnClick(func() { a.confirmDeliveryAssignment(p) })
			}
			quietButton(c, "重新核验来源").Disabled(p.Loading || p.Busy).OnClick(func() { a.readDeliveryAssignment(p.EntryID) })
		})
	})
}
func (a *App) openAssignedRecord(p *DeliveryAssignmentPanel) {
	if p == nil || p.Receipt == nil {
		return
	}
	r := p.Receipt
	a.navigate("项目", r.Project)
	a.recordKind = "knowledge"
	if p.Preview != nil && p.Preview.Item.Origin == "raw" {
		a.recordKind = "raw"
	}
	a.selectedID = r.Memory
	a.selectedItem = nil
	a.detailMode = "view"
	a.refresh(false)
}

// Called only by the global D04 retained-delivery detail. The daemon preview
// rechecks SQL/source/claim eligibility before this can enable confirmation.
func (a *App) deliveryAssignmentEntry(c *ui.Context, item records.AgentDeliveryItem) {
	if item.HasGrant || item.Attempts != 0 || item.Status.PersistedConfirmed || !item.Status.LocalRetained || item.Assignment != nil && item.Assignment.State == "saved" {
		return
	}
	button := quietButton(c, "核对项目归属").Key("delivery-assignment-entry-" + item.Status.ID).OnClick(func() { a.openDeliveryAssignment(item.Status.ID) })
	if a.deliveryAssignment.Root == a.library.Root && a.deliveryAssignment.ReturnFocus == item.Status.ID {
		button.Focus()
		if button.Focused() {
			a.deliveryAssignment.ReturnFocus = ""
		}
	}
}

func assignmentAgentLabel(agent string) string {
	if agent == "" {
		return "来源 Agent 未记录"
	}
	return sourceDisplayName(agent)
}
