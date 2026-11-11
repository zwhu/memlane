package desktop

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func (a *App) captureRepositoryDiscovery(c *ui.Context) {
	s := &a.homeActivity.Delivery
	if s.Error != "" || !s.Loaded {
		return
	}
	if s.CaptureDeferred == nil {
		s.CaptureDeferred = map[string]bool{}
	}
	for _, review := range s.CaptureDiscoveries {
		origin, ok := records.ReadCaptureOrigin(review.Arguments)
		if !ok {
			continue
		}
		key := records.MergeDigest([]any{origin.Consent, origin.Repository})
		if s.CaptureDeferred[key] {
			continue
		}
		ui.Column(c).Key("capture-repository-discovery").Padding(14).Gap(8).Border(1, design.Border).Radius(6).Children(func() {
			ui.Text(c, "发现待归属仓库").Bold()
			path := origin.Repository.GitRoot
			if path == "" {
				path = review.Item.Workspace
			}
			ui.Text(c, path).Selectable().FontSize(11)
			ui.Text(c, "原始记录已在本机保留，是否添加或关联项目？").FontSize(11)
			ui.Row(c).Gap(10).Children(func() {
				quietButton(c, "添加项目").Key("capture-discovery-add").OnClick(func() { a.openDeliveryAssignment(review.Item.Status.ID) })
				quietButton(c, "暂缓").Key("capture-discovery-defer").OnClick(func() { s.CaptureDeferred[key] = true })
			})
		})
		return
	}
}

func capturedAssignmentOrigin(p *DeliveryAssignmentPanel) (records.CaptureOrigin, bool) {
	if p == nil {
		return records.CaptureOrigin{}, false
	}
	if p.Preview != nil {
		return records.ReadCaptureOrigin(p.Preview.Arguments)
	}
	if p.Review != nil {
		return records.ReadCaptureOrigin(p.Review.Arguments)
	}
	return records.CaptureOrigin{}, false
}

func (a *App) capturedAssignmentBody(c *ui.Context, p *DeliveryAssignmentPanel) {
	origin, _ := capturedAssignmentOrigin(p)
	ui.Scroll(c).Key("capture-repository-body").TrackScroll(&p.Scroll).Grow(1).MinHeight(0).Padding(24).Gap(16).Children(func() {
		ui.Text(c, "确认仓库原始记录归属").FontSize(design.Heading).Bold()
		ui.Text(c, "原始记录已在本机保留；暂缓不会删除材料。").FontSize(11).LineHeight(1.7)
		ui.Text(c, "仓库位置："+origin.Repository.GitRoot).Selectable().FontSize(11).LineHeight(1.6)
		if origin.Repository.State != "known" {
			ui.Text(c, "采集时仓库身份未知，暂不能归入项目。").FontSize(11)
		}
		if p.Preview != nil && p.Preview.Code == "capture_session_unverified" {
			ui.Text(c, "此材料的正常用户会话来源尚未核实，保留待核对，暂不能自动归属。").FontSize(11).LineHeight(1.6)
		}
		if p.Preview != nil && p.Preview.MatchingProject != nil {
			ui.Text(c, "复用项目：「"+p.Preview.MatchingProject.Name+"」").Bold()
		} else {
			if p.CaptureName == "" {
				p.CaptureName = filepath.Base(origin.Repository.GitRoot)
			}
			ui.TextInput(c.Key("capture-project-name"), &p.CaptureName).Label("项目名称").Disabled(p.Busy || p.CaptureRoute != nil)
		}
		ui.Text(c, "确认后，此仓库在当前采集授权下已保留的材料，以及后续新增对话，将归入该项目。只保存原始记录，不自动整理，也不增加知识读取权限。").FontSize(11).LineHeight(1.7)
		if p.Loading {
			ui.Text(c, "正在核对仓库与项目…").FontSize(11)
		}
		if p.Error != "" {
			ui.Text(c, p.Error).FontSize(11).LineHeight(1.7)
		}
		if p.Info != "" {
			ui.Text(c, p.Info).FontSize(11).LineHeight(1.7)
		}
		text := ""
		if p.Review != nil {
			text = p.Review.Arguments.Text
		}
		if p.Preview != nil {
			text = p.Preview.Arguments.Text
		}
		ui.Text(c, "原始记录").Bold()
		ui.Text(c, a.conflictReadableText(c, "capture-original-"+p.EntryID, text)).Selectable().LineHeight(1.7)
	})
}

func canConfirmCapturedRepository(p *DeliveryAssignmentPanel) bool {
	origin, ok := capturedAssignmentOrigin(p)
	if !ok || origin.Repository.State != "known" || p.Preview == nil || p.Loading || p.Busy || p.Pending != nil {
		return false
	}
	v := p.Preview
	if v.Item.HasGrant || v.Item.Attempts != 0 || v.Item.Status.PersistedConfirmed {
		return false
	}
	switch v.Code {
	case "", "ready", "choose_project", "project_unregistered":
	default:
		return false
	}
	return manualUUID(v.LibraryID) && v.EntryID == p.EntryID && v.Item.Status.ID == p.EntryID && v.RequestDigest != "" && v.ReviewDigest != "" && (v.MatchingProject != nil || strings.TrimSpace(p.CaptureName) != "")
}

func (a *App) capturedAssignmentFooter(c *ui.Context, p *DeliveryAssignmentPanel) {
	ui.Column(c).Key("capture-repository-footer").Shrink(0).Padding(12, 22).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		ui.Row(c).Gap(10).Children(func() {
			label := "添加项目并关联原始记录"
			if p.Preview != nil && p.Preview.MatchingProject != nil {
				label = "确认关联原始记录"
			}
			if p.CaptureRoute != nil {
				label = "核对保存结果"
			}
			if p.CaptureReceipt == nil {
				actionButton(c, label, nil).Disabled(!canConfirmCapturedRepository(p)).OnClick(func() { a.confirmCapturedRepository(p) })
			} else {
				quietButton(c, "查看项目原始记录").OnClick(func() { a.navigate("项目", p.CaptureReceipt.Project); a.recordKind = "raw"; a.refresh(false) })
			}
			quietButton(c, "暂缓").Disabled(p.Busy).OnClick(func() { a.closeDeliveryAssignment() })
			quietButton(c, "重新核对").Disabled(p.Busy || p.Loading).OnClick(func() { a.readDeliveryAssignment(p.EntryID) })
		})
		ui.Text(c, "暂缓后可从总览 → 待归属原始记录继续处理。").FontSize(10).TextColor(design.Muted)
	})
}

func (a *App) confirmCapturedRepository(p *DeliveryAssignmentPanel) {
	if !canConfirmCapturedRepository(p) || a.libraryContext == nil || a.dispatch == nil {
		return
	}
	origin, _ := capturedAssignmentOrigin(p)
	preview := *p.Preview
	name := strings.TrimSpace(p.CaptureName)
	prior := p.CaptureRoute
	p.Busy = true
	p.Error = ""
	p.Info = ""
	root, life, client, dispatch, epoch := a.library.Root, a.libraryContext, a.library, a.dispatch, a.libraryEpoch
	ctx, cancel := context.WithTimeout(life, 15*time.Second)
	p.Cancel = cancel
	go func() {
		defer cancel()
		var route records.CaptureRoute
		var receipt records.CaptureDeliveryReceipt
		var err error
		project := ""
		if prior != nil {
			route = *prior
			project = prior.Project
		} else {
			var inspection records.ProjectInspection
			inspection, err = client.InspectProject(ctx, origin.Repository.GitRoot)
			if err == nil && inspection.Identity != origin.Repository.RegistrationIdentity() {
				err = errors.New("repository changed since capture")
			}
			if err == nil && inspection.Existing != nil {
				project = inspection.Existing.ID
			}
			if err == nil && project == "" {
				var created records.Scope
				created, err = client.RegisterProject(ctx, records.ScopeDraft{Name: name, Path: origin.Repository.GitRoot, ExpectedRepository: origin.Repository.RegistrationIdentity()})
				project = created.ID
			}
			if err == nil {
				route, err = client.ConfirmCaptureRoute(ctx, records.CaptureRouteConfirmation{OperationID: uuid.NewString(), LibraryID: preview.LibraryID, ConnectionID: preview.Item.ConnectionID, EntryID: p.EntryID, ReviewDigest: preview.ReviewDigest, Project: project})
			}
		}
		if err == nil && (!manualUUID(route.OperationID) || route.LibraryID != preview.LibraryID || route.ConnectionID != preview.Item.ConnectionID || route.ConsentID != origin.Consent || !route.Repository.SameRepository(origin.Repository) || !manualUUID(route.Project) || route.Project != project) {
			err = errors.New("capture route receipt mismatch")
			route = records.CaptureRoute{}
		}
		if err == nil {
			receipt, err = client.DeliverCapturedRaw(ctx, records.CaptureDeliveryQuery{EntryID: p.EntryID, ReviewDigest: preview.ReviewDigest})
		}
		dispatch(func() {
			if life.Err() != nil || a.libraryContext != life || a.library.Root != root || !a.assignmentOwner(p, epoch) {
				return
			}
			p.Busy = false
			if route.OperationID != "" {
				copy := route
				p.CaptureRoute = &copy
			}
			if err != nil {
				p.Error = "未取得完整保存回执，原始记录仍保留；请重新核对。项目可能已建立，可复用，不能据此重复原文。"
				if p.CaptureRoute != nil {
					p.Info = "仓库归属规则已保存，原文等待后台补送。"
				}
				return
			}
			if !receipt.Persisted || receipt.Revision != 1 || receipt.RouteID != route.OperationID || receipt.EntryID != p.EntryID || receipt.LibraryID != preview.LibraryID || receipt.Project != route.Project || receipt.ConnectionID != preview.Item.ConnectionID || receipt.OperationID != preview.Item.OperationID || receipt.RequestDigest != preview.RequestDigest || receipt.ReviewDigest != preview.ReviewDigest || !manualUUID(receipt.Memory) {
				p.Error = "保存回执与当前材料不符，不能认定已完成。"
				return
			}
			copy := receipt
			p.CaptureReceipt = &copy
			p.Info = "此条原始记录已保存；同一采集授权下、同仓库的其余材料由后台继续补送，不重复保存。"
			if receipt.AckPending {
				p.Info = "原文已保存，本机发送回执仍待核对；不会重复创建原文。"
			}
			a.refresh(false)
		})
	}()
}
