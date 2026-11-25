package settings

import (
	"context"
	"strings"
	"time"
)

type ProjectCheckboxPending struct {
	Project, Agent, Field string
	Value                 any
	Revision              int64
}

func (a *Controller) saveProjectCheckbox(project, agent, field string, value any) {
	client, ok := a.accessClient()
	key := project + "/" + agent
	if !ok || field != "access" || !a.access.Loaded || a.access.Busy || a.access.CheckboxPending[key] != nil || !a.canDispatch() {
		return
	}
	if a.access.CheckboxPending == nil {
		a.access.CheckboxPending = map[string]*ProjectCheckboxPending{}
	}
	pending := &ProjectCheckboxPending{Project: project, Agent: agent, Field: field, Value: value, Revision: a.access.Directory.Revision}
	a.access.CheckboxPending[key] = pending
	a.access.Busy = true
	a.access.Loading = false
	a.access.Request++
	request := a.access.Request
	a.access.Message = ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 125*time.Second)
		defer cancel()
		directory, err := client.overrides(ctx, pending.Revision, project, agent, map[string]any{"access": value})
		a.dispatch(func() {
			if request != a.access.Request {
				return
			}
			a.access.Busy = false
			if err != nil {
				// Unknown writes stay locked. Catalog verification never resends them.
				a.access.Message = "未能确认修改，请核对后再操作：" + err.Error()
				if strings.Contains(err.Error(), "设置已变化") {
					delete(a.access.CheckboxPending, key)
					a.RefreshProjectDirectory()
				}
				return
			}
			a.ApplyAccessDirectory(directory)
		})
	}()
}

func (a *Controller) confirmProjectCheckboxes(directory AccessDirectory) {
	for key, pending := range a.access.CheckboxPending {
		if directory.Revision <= pending.Revision {
			continue
		}
		if pending.Field == "common_access" {
			if directory.Common == nil || directory.Common.ID != pending.Project || !directory.Common.PermissionsReady(directory.Agents...) {
				continue
			}
			actual := CommonAgentAccess(*directory.Common, pending.Agent)
			delete(a.access.CheckboxPending, key)
			if actual != pending.Value {
				a.access.Message = "设置已由其他操作更新，已显示最新值。"
			} else if len(a.access.CheckboxPending) == 0 {
				a.access.Message = ""
				if a.access.PermissionSaved == nil {
					a.access.PermissionSaved = map[string]time.Time{}
				}
				a.access.PermissionSaved[key] = time.Now().Add(2 * time.Second)
			}
			continue
		}
		for _, p := range directory.Projects {
			if p.ID != pending.Project || !p.PermissionsReady(directory.Agents...) {
				continue
			}
			value := p.Overrides[pending.Agent].Effective(directory.Defaults).Access
			delete(a.access.CheckboxPending, key)
			if value != pending.Value {
				a.access.Message = "设置已由其他操作更新，已显示最新值。"
			} else if len(a.access.CheckboxPending) == 0 {
				a.access.Message = ""
				if a.access.PermissionSaved == nil {
					a.access.PermissionSaved = map[string]time.Time{}
				}
				a.access.PermissionSaved[key] = time.Now().Add(2 * time.Second)
			}
		}
	}
}
