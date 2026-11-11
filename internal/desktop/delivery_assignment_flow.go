package desktop

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agentbridge"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func (a *App) readDeliveryAssignment(id string) {
	a.bindDeliveryAssignment()
	if id == "" || a.libraryContext == nil || a.dispatch == nil {
		return
	}
	s := &a.deliveryAssignment
	p := s.Panels[id]
	if p == nil {
		p = &DeliveryAssignmentPanel{EntryID: id, Root: a.library.Root, Choice: "请选择项目"}
		s.Panels[id] = p
	}
	if p.Loading || p.Busy {
		return
	}
	p.Loading = true
	p.Error = ""
	p.Sequence++
	sequence := p.Sequence
	// A previously verified preview must not authorize a new choice/confirmation
	// while current source and SQL facts are being read again.
	p.Preview = nil
	root, life, client, dispatch, epoch := a.library.Root, a.libraryContext, a.library, a.dispatch, a.libraryEpoch
	selected := p.SelectedProject
	ctx, cancel := context.WithTimeout(life, 12*time.Second)
	p.Cancel = cancel
	go func() {
		defer cancel()
		pending, err := agentbridge.ReadDeliveryAssignmentIntent(root, id)
		var review records.AgentDeliveryReview
		if err == nil {
			review, err = agentbridge.ReviewDelivery(root, id)
		}
		q := records.DeliveryAssignmentPreviewQuery{EntryID: id, ReviewDigest: review.Item.Status.ReviewDigest, Project: selected}
		if pending != nil {
			q.ReviewDigest = pending.ReviewDigest
			q.Project = pending.Project
		}
		var preview records.DeliveryAssignmentPreview
		if err == nil {
			preview, err = client.DeliveryAssignmentPreview(ctx, q)
		}
		dispatch(func() {
			if life.Err() != nil || a.libraryContext != life || !a.assignmentOwner(p, epoch) || p.Sequence != sequence {
				return
			}
			p.Loading = false
			if review.Item.Status.ID == id && review.Item.Status.ReviewDigest != "" {
				copy := review
				p.Review = &copy
			}
			if pending != nil {
				copy := *pending
				p.Pending = &copy
				p.SelectedProject = copy.Project
			}
			if err != nil {
				p.Error = assignmentReadFailure(err)
				p.KnownRejected = true
				return
			}
			if !validAssignmentPreview(preview, id, q.Project) || pending != nil && (pending.LibraryID != preview.LibraryID || pending.RequestDigest != preview.RequestDigest || pending.ReviewDigest != preview.ReviewDigest || pending.WorkspaceDigest != preview.WorkspaceDigest && (preview.Assignment == nil || assignmentIntentFromReceipt(*preview.Assignment) != *pending)) {
				p.Error = "归属预览与原提交、库身份或工作目录核验不符，不能确认；原请求保留。"
				p.KnownRejected = true
				return
			}
			p.SelectedProject = q.Project
			p.Choice = assignmentProjectChoice(preview.Projects, p.SelectedProject)
			p.Preview = &preview
			p.KnownRejected = p.Pending != nil && !preview.CanConfirm && preview.Assignment == nil
			if r := preview.Assignment; r != nil {
				intent := assignmentIntentFromReceipt(*r)
				if !validAssignmentReceipt(*r, intent, preview) || pending != nil && *pending != intent {
					p.Error = "归属保存回执与原请求不符，不能认定已完成。"
					p.KnownRejected = true
					return
				}
				p.Receipt = r
				p.Pending = &intent
				p.AckPending = true
				p.KnownRejected = false
				p.Info = "归属保存已取得独立回执；原 Agent 提交的本机收尾尚待核验。"
			}
		})
	}()
}
func assignmentIntentFromReceipt(r records.DeliveryAssignmentReceipt) records.DeliveryAssignmentIntent {
	return records.DeliveryAssignmentIntent{OperationID: r.OperationID, LibraryID: r.LibraryID, EntryID: r.EntryID, RequestDigest: r.RequestDigest, ReviewDigest: r.ReviewDigest, Project: r.Project, WorkspaceDigest: r.WorkspaceDigest}
}
func validAssignmentReceipt(r records.DeliveryAssignmentReceipt, q records.DeliveryAssignmentIntent, v records.DeliveryAssignmentPreview) bool {
	return assignmentIntentFromReceipt(r) == q && manualUUID(q.OperationID) && manualUUID(q.LibraryID) && q.LibraryID == v.LibraryID && q.EntryID == v.EntryID && q.RequestDigest == v.RequestDigest && q.ReviewDigest == v.ReviewDigest && manualUUID(q.Project) && (q.Project == v.SelectedProject || v.Assignment != nil && *v.Assignment == r) && q.WorkspaceDigest != "" && (q.WorkspaceDigest == v.WorkspaceDigest || v.Assignment != nil && *v.Assignment == r) && manualUUID(r.Memory) && r.Revision > 0 && r.Source == "user_assignment" && r.State == "saved" && r.CreatedMS > 0 && r.OriginalOperationID == v.Item.OperationID && r.OriginalConnectionID == v.Item.ConnectionID
}
func assignmentReadFailure(err error) string {
	var failure *serviceipc.ServiceError
	if errors.As(err, &failure) {
		return assignmentFailure(failure.Code)
	}
	return "归属或原请求尚未核验；本机内容保留，不能确认。请重新读取来源。"
}
func (a *App) confirmDeliveryAssignment(p *DeliveryAssignmentPanel) {
	if p == nil || p.Loading || p.Busy || a.libraryContext == nil || a.dispatch == nil || p.KnownRejected {
		return
	}
	if p.Pending == nil {
		if !canConfirmAssignment(p) {
			return
		}
		v := p.Preview
		p.Pending = &records.DeliveryAssignmentIntent{OperationID: uuid.NewString(), LibraryID: v.LibraryID, EntryID: v.EntryID, RequestDigest: v.RequestDigest, ReviewDigest: v.ReviewDigest, Project: p.SelectedProject, WorkspaceDigest: v.WorkspaceDigest}
	}
	q := *p.Pending
	p.Busy = true
	p.Error = ""
	root, life, client, dispatch, epoch := p.Root, a.libraryContext, a.library, a.dispatch, a.libraryEpoch
	var proof records.DeliveryAssignmentPreview
	if p.Preview != nil {
		proof = *p.Preview
	}
	ctx, cancel := context.WithTimeout(life, 20*time.Second)
	p.Cancel = cancel
	go func() {
		defer cancel()
		// Journal + claim are one controlled spool operation: it never edits or
		// replays the original Agent payload, scope or operation identifier.
		err := agentbridge.PrepareDeliveryAssignment(root, q)
		prepared := err == nil
		var receipt records.DeliveryAssignmentReceipt
		valid := false
		acknowledged := false
		if err == nil {
			receipt, err = client.ConfirmDeliveryAssignment(ctx, q)
			if err == nil {
				valid = validAssignmentReceipt(receipt, q, proof)
				if !valid {
					err = errors.New("unverified assignment receipt")
				} else {
					err = agentbridge.AckDeliveryAssignment(root, q, receipt)
					acknowledged = err == nil
				}
			}
		}
		dispatch(func() {
			if life.Err() != nil || a.libraryContext != life || !a.assignmentOwner(p, epoch) {
				return
			}
			p.Busy = false
			if valid {
				p.Receipt = &receipt
				p.AckPending = !acknowledged
				p.KnownRejected = false
				if acknowledged {
					p.Info = "用户归属保存已确认；原 Agent 暂存已独立收尾。"
					a.refreshDeliveryAssignments()
				} else {
					p.Error = "归属保存回执已取得，本机收尾尚未确认；可用原请求重试。"
				}
				return
			}
			if err != nil {
				if !prepared {
					p.Error = "原归属意图尚未完成本机保存或认领，尚未发送；原操作和选择保留。"
					return
				}
				p.Error = "归属结果尚未确认；原请求保留，请使用同一请求重试。"
				var failure *serviceipc.ServiceError
				if errors.As(err, &failure) {
					switch failure.Code {
					case "workspace_changed", "workspace_mismatch", "workspace_unavailable", "workspace_unverified", "library_changed", "delivery_library_changed", "scope_denied", "original_already_saved", "delivery_assignment_ineligible":
						p.Error = assignmentFailure(failure.Code)
						p.KnownRejected = true
					}
				}
			}
		})
	}()
}
