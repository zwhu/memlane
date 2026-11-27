package settings

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func CommonAgentAccess(common AccessProject, agentID string) string {
	return CommonAgentAccessForAgent(common, ProjectPolicy{Access: "maintain"}, AccessAgent{ID: agentID})
}

func CommonAgentAccessForAgent(common AccessProject, defaults ProjectPolicy, agent AccessAgent) string {
	effective := common.Overrides[agent.ID].EffectiveForAgent(defaults, agent.DefaultAccess)
	if effective.Access == "read" || effective.Access == "maintain" {
		return "read"
	}
	return "none"
}

// CommonAccessLabel gives the common-memory menu its permission summary.
func (a *Controller) CommonAccessLabel() string {
	directory := a.access.Directory
	if directory.Common == nil || len(directory.Agents) == 0 {
		return ""
	}
	readable := 0
	for _, agent := range directory.Agents {
		if CommonAgentAccessForAgent(*directory.Common, directory.Defaults, agent) == "read" {
			readable++
		}
	}
	return fmt.Sprintf("Agent 权限 · %d/%d 只读", readable, len(directory.Agents))
}

func (a *Controller) SaveCommonAgentAccess(commonID, agentID, access string) {
	if access != "none" && access != "read" {
		return
	}
	client, ok := a.accessClient()
	key := commonID + "/" + agentID
	if !ok || commonID == "" || agentID == "" || !a.access.Loaded || a.access.Busy || a.access.CheckboxPending[key] != nil || !a.canDispatch() {
		return
	}
	if a.access.CheckboxPending == nil {
		a.access.CheckboxPending = map[string]*ProjectCheckboxPending{}
	}
	pending := &ProjectCheckboxPending{Project: commonID, Agent: agentID, Field: "common_access", Value: access, Revision: a.access.Directory.Revision}
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
		a.dispatch(func() {
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
			a.ApplyAccessDirectory(directory)
		})
	}()
}

func (a *Controller) commonDatasetSettings(c *ui.Context) {
	tok := a.tokens()
	directory := a.access.Directory
	ui.Text(c, "通用记忆数据组").Font(kitui.FontUI).FontSize(fsBody).FontWeight(600).TextColor(tok.Text1)
	if !a.access.Loaded {
		ui.Text(c, "正在读取通用记忆权限配置…").Font(kitui.FontUI).FontSize(fsCaption).TextColor(tok.Text3)
	} else if directory.Common != nil {
		ui.Text(c, directory.Common.SyncLabel(directory.Agents...)).Font(kitui.FontUI).FontSize(fsCaption).TextColor(tok.Text2)
	} else {
		ui.Text(c, "尚未准备通用记忆数据组，准备后可按 Agent 开启只读权限。").Font(kitui.FontUI).FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.5)
	}
	kitui.TextButton(c, tok, "准备／核对通用记忆", nil).Disabled(!a.access.Loaded || a.access.Busy).OnClick(func() {
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
		ui.Text(c, "Agent 访问权限").Font(kitui.FontUI).FontSize(fsFootnote).FontWeight(600).TextColor(tok.Text1)
		ui.Text(c, "新接入的 Agent 默认不可访问；可按需开启只读权限（通用记忆不支持 Agent 写入）。").Font(kitui.FontUI).FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.45)
		if len(directory.Agents) == 0 {
			ui.Text(c, "尚未连接任何 Agent").Font(kitui.FontUI).FontSize(fsCaption).TextColor(tok.Text3)
			return
		}
		renderAgentRows := func() {
			for _, ag := range directory.Agents {
				agent := ag
				current := CommonAgentAccessForAgent(common, directory.Defaults, agent)
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
				agentUnconfirmed := common.AgentPolicyUnconfirmed(agent.ID)
				ui.Column(c.Key("common-access-row-" + key)).FillWidth().Gap(4).Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(sp2).Children(func() {
						agentLogo(tok, c, accessAgentIcon(agent), 18)
						ui.Column(c).Grow(1).MinWidth(0).Gap(0).Children(func() {
							ui.Text(c, AgentDisplayName(agent.Name)).Font(kitui.FontUI).FontSize(fsFootnote).FontWeight(500).TextColor(tok.Text1).SingleLine().MaxLines(1)
							switch {
							case saving:
								ui.Row(c).AlignItems(ui.Center).Gap(4).Children(func() {
									kitui.Spinner(c, tok)
									ui.Text(c, "保存中…").Font(kitui.FontUI).FontSize(fsCaption).TextColor(tok.Text3).SingleLine()
								})
							case savedRecently:
								ui.Text(c, "已保存").Font(kitui.FontUI).FontSize(fsCaption).TextColor(tok.OK).SingleLine()
							case pending != nil:
								ui.Text(c, "待核对").Font(kitui.FontUI).FontSize(fsCaption).TextColor(tok.Danger).SingleLine()
							case agentUnconfirmed:
								ui.Text(c, "权限未确认").Font(kitui.FontUI).FontSize(fsCaption).TextColor(tok.Danger).SingleLine()
							}
						})
						seg := kitui.Segmented(c, tok, "common-access-seg-"+key, AgentDisplayName(agent.Name)+" 通用记忆访问权限", &selected, []string{"不可访问", "只读"}, func() {
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
							a.SaveCommonAgentAccess(common.ID, agent.ID, next)
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
							ui.Text(c, errText).Font(kitui.FontUI).FontSize(fsCaption).TextColor(tok.Danger).Grow(1).MinWidth(0).MaxLines(2)
							kitui.SmallButton(c, tok, "重新核对", kitui.IconRefresh, a.RefreshProjectDirectory).Disabled(a.access.Loading)
						})
					}
				})
			}
		}
		if len(directory.Agents) > 6 {
			ui.Scroll(c.Key("common-access-scroll")).FillWidth().MaxHeight(260).TrackScroll(&a.commonAccessScroll).Gap(sp2).Children(renderAgentRows)
		} else {
			ui.Column(c).FillWidth().Gap(sp2).Children(renderAgentRows)
		}
		if a.access.Message != "" && len(a.access.CheckboxPending) == 0 {
			ui.Text(c, a.access.Message).Font(kitui.FontUI).FontSize(fsCaption).TextColor(tok.Text3).LineHeight(1.45)
		}
	})
}

func accessTierDesc(access string) string {
	switch access {
	case "maintain":
		return "读取、写入并维护该项目记忆"
	case "read":
		return "仅允许检索与读取已有记忆，不可写入"
	default:
		return "禁止访问此项目的记忆与原始记录"
	}
}

func (a *Controller) agentPolicyEditor(c *ui.Context, p AccessProject, agent AccessAgent) {
	tok := a.tokens()
	actual := p.EffectivePolicy(a.access.Directory.Defaults, agent)
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
	ui.Row(c.Key("agent-policy-" + key)).AlignItems(ui.Center).Gap(sp2).Children(func() {
		v4TriAccessSelect(tok, c, "project-access-"+key, AgentDisplayName(agent.Name), v4TriAccessState{
			Value:    displayVal,
			Saving:   saving,
			Error:    errText,
			Disabled: a.access.Busy || pending != nil,
		}, func(nextValue string) {
			a.saveProjectCheckbox(p.ID, agent.ID, "access", nextValue)
		})
		if pending != nil && !a.access.Busy {
			kitui.SmallButton(c, tok, "重新核对", kitui.IconRefresh, a.RefreshProjectDirectory).Disabled(a.access.Loading).Tooltip(a.access.Message)
		}
	})
}

func AgentDisplayName(name string) string {
	for _, prefix := range []string{"本机 ", "本地 "} {
		name = strings.TrimPrefix(name, prefix)
	}
	switch name {
	case "TraeX":
		return "TRAE CLI"
	case "TRAE IDE":
		return "TRAE CN"
	}
	return name
}
