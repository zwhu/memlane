package desktop

import (
	"errors"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

// Review-time consent belongs to one immutable proposal in one task. It is
// never inferred from the earlier selection of allowed destination projects.
func (a *App) organizationMoveAuthorized(t records.OrganizationTask, e records.OrganizationEntry) bool {
	return e.Item.Action == "move" && a.organization.MoveAuthorizations[e.ID] == t.ID+":"+records.MergeDigest(e.Item)
}

func (a *App) organizationMoveDestinationCurrent(t records.OrganizationTask, e records.OrganizationEntry) bool {
	for _, frozen := range t.Snapshot.Destinations {
		if frozen.ID == e.Item.Destination && frozen.Kind == "project" {
			for _, current := range a.scopes {
				if current.ID == frozen.ID && current.Kind == "project" && current.Path == frozen.Path {
					return true
				}
			}
		}
	}
	return false
}

func (a *App) organizationMoveConsent(c *ui.Context, t records.OrganizationTask, e records.OrganizationEntry) {
	accepted := a.organizationMoveAuthorized(t, e)
	frozen := a.organization.Busy || e.State != "review_pending" || a.organization.Decisions[e.ID+"true"].Receipt != "" || a.organization.Decisions[e.ID+"false"].Receipt != ""
	field := ui.Checkbox(c.Key("organization-move-consent-"+e.ID), &accepted, "明确授权来源与目标项目的范围变化").Disabled(frozen || !a.organizationSourcesCurrent(t, e)).OnChange(func() {
		if a.organization.MoveAuthorizations == nil {
			a.organization.MoveAuthorizations = map[string]string{}
		}
		if accepted {
			a.organization.MoveAuthorizations[e.ID] = t.ID + ":" + records.MergeDigest(e.Item)
		} else {
			delete(a.organization.MoveAuthorizations, e.ID)
		}
		a.persistMaterialPreview(nil)
	})
	a.trackMaterialFocus(field, "organization-move-consent-"+e.ID, frozen)
}

// Only an authoritative "not committed" response may retire a pre-contract
// legacy move request. Lost responses retain their original receipt/payload.
func (a *App) retireUnconfirmedLegacyMove(err error) bool {
	var remote *serviceipc.ServiceError
	if !errors.As(err, &remote) || remote.Code != "move_scope_confirmation_required" || a.organization.Task == nil {
		return false
	}
	e := a.selectedOrganizationEntry()
	if e == nil || e.Item.Action != "move" {
		return false
	}
	key := e.ID + "true"
	d, ok := a.organization.Decisions[key]
	if !ok || !d.Confirm || d.MoveScopeAccepted || d.Task != a.organization.Task.ID || d.Item != e.ID || d.Digest != records.MergeDigest(e.Item) {
		return false
	}
	delete(a.organization.Decisions, key)
	delete(a.organization.MoveAuthorizations, e.ID)
	return true
}

func (a *App) organizationSourcePermissionDenied(id string) bool {
	switch a.organization.SourceErrorCodes[id] {
	case "scope_denied", "permission_denied", "connection_revoked", "authorization_required", "cognee_authorization_required":
		return true
	}
	return false
}

func (a *App) organizationReviewSourceState(t records.OrganizationTask, e records.OrganizationEntry) string {
	if a.organization.Loading {
		return "loading"
	}
	for _, ref := range e.Item.Sources {
		if a.organizationSourcePermissionDenied(ref.Memory) {
			return "permission"
		}
	}
	if a.organization.SourcesReadFailed {
		return "unavailable"
	}
	for _, ref := range e.Item.Sources {
		if a.organization.SourceErrors[ref.Memory] != "" {
			return "unavailable"
		}
	}
	if a.organizationSourcesCurrent(t, e) && e.State != "expired" {
		return "current"
	}
	for _, ref := range e.Item.Sources {
		if _, ok := a.organization.Sources[ref.Memory]; !ok {
			return "unread"
		}
	}
	return "changed"
}

func (a *App) organizationReviewSourceNotice(c *ui.Context, t records.OrganizationTask, e records.OrganizationEntry) {
	state := a.organizationReviewSourceState(t, e)
	ui.Column(c).Key("organization-review-stale").Gap(8).Padding(12).Border(1, design.Border).Radius(6).Background(design.Surface).Children(func() {
		switch state {
		case "permission":
			ui.Text(c, "来源访问权限不足或已撤销").Bold().FontSize(12)
			ui.Text(c, "不能继续使用旧确认或查看原来源正文。请先核对项目授权，再重新选择相关材料。").FontSize(11).LineHeight(1.6)
		case "unavailable", "unread":
			ui.Text(c, "当前来源尚未读取成功").Bold().FontSize(12)
			ui.Text(c, "暂时不能判断建议是否仍适用；已有建议保留，不能提交新的确认。").FontSize(11).LineHeight(1.6)
			quietButton(c, "重试核对来源").Disabled(a.organization.Busy || a.organization.Loading).OnClick(func() { a.refreshOrganization(false, false) })
		case "loading":
			ui.Text(c, "正在核对来源版本与权限…").FontSize(11)
		default:
			ui.Text(c, "来源版本或范围已变化").Bold().FontSize(12)
			ui.Text(c, "该建议已过期，不能继续使用旧确认。原始建议与历史保留。").FontSize(11).LineHeight(1.6)
		}
		quietButton(c, "重新整理相关材料").Key("organization-review-reselect").Disabled(a.organization.Busy || a.organization.Loading || a.organization.Decisions[e.ID+"true"].Receipt != "" || a.organization.Decisions[e.ID+"false"].Receipt != "").OnClick(func() { a.reselectOrganizationMaterials(t, e) })
	})
}

func (a *App) reselectOrganizationMaterials(t records.OrganizationTask, e records.OrganizationEntry) {
	s := &a.organization
	if s.Busy || s.Loading || s.Task == nil || s.Task.ID != t.ID || s.Selected != t.ID || s.Decisions[e.ID+"true"].Receipt != "" || s.Decisions[e.ID+"false"].Receipt != "" {
		return
	}
	chosen := []MemoryItem{}
	for _, ref := range e.Item.Sources {
		v, ok := s.Sources[ref.Memory]
		if ok && s.SourceErrors[ref.Memory] == "" && !a.organizationSourcePermissionDenied(ref.Memory) && v.Dataset == t.Project && v.State == "active" && v.Revision > 0 && (v.Derived == nil || v.Derived.State != "stale") {
			chosen = append(chosen, v)
		}
	}
	a.beginOrganizationFor(t.Project, chosen)
}
