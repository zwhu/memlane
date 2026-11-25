package settings

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func (s *IntegrationState) generationDeleteBlock(id string) string {
	if s.Generation.Active != nil && s.Generation.Active.ID == id {
		return "当前正在使用，切换模型后再删除"
	}
	if s.Generation.Next != nil && s.Generation.Next.ID == id {
		return "请先改选下次启动使用的模型"
	}
	if s.pendingGenerationProfile(id) {
		return "请先核对这个配置的未完成操作"
	}
	for _, validation := range s.Generation.Validations {
		if validation.Status != "queued" && validation.Status != "running" && validation.Status != "result_unknown" {
			continue
		}
		matches := validation.Reference != nil && validation.Reference.ID == id
		for _, row := range s.Generation.Profiles[id] {
			matches = matches || (validation.Digest != "" && validation.Digest == row.Digest)
		}
		if matches {
			return "请先停止或核对这个模型的验证"
		}
	}
	return ""
}

func (a *Controller) generationDeleteModal(c *ui.Context) {
	tok := a.tokens()
	s := &a.integrations
	if s.DeleteProfile == nil {
		return
	}
	ref := *s.DeleteProfile
	blockReason := s.generationDeleteBlock(ref.ID)
	kitui.Dialog(c, tok, &s.DeleteOpen, kitui.DialogOptions{
		Key:             "delete-generation-" + ref.ID,
		Title:           "删除模型配置？",
		Width:           kitui.DialogSmall,
		Danger:          true,
		CancelText:      "取消",
		ConfirmText:     "确认删除",
		ConfirmLabel:    "确认删除模型：" + ref.ID,
		Hint:            "此操作不可撤销（删除确认不绑定回车键）",
		CancelDisabled:  s.Busy,
		ConfirmDisabled: s.Busy || blockReason != "",
		OnCancel:        func() { s.DeleteProfile = nil; s.DeleteOpen = false },
		OnConfirm:       a.deleteGeneration,
		Body: func() {
			kitui.Text(c, tok, generationLabel(s.Generation, &ref), kitui.RoleBody).FontSize(fsBody).TextColor(tok.Text1).MaxLines(3)
			kitui.Text(c, tok, "已保存在 Cognee 中的历史记忆与原始记录不受影响。", kitui.RoleMeta).LineHeight(1.5)
			if s.DeleteError != "" {
				kitui.Text(c, tok, s.DeleteError, kitui.RoleMeta).TextColor(tok.Danger).MaxLines(3)
			}
		},
	})
	if !s.DeleteOpen {
		s.DeleteProfile = nil
	}
}

type generationDeletionReceipt struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

func (a *Controller) deleteGeneration() {
	s := &a.integrations
	client, ok := a.accessClient()
	if !ok || s.Busy || s.DeleteProfile == nil || !a.canDispatch() {
		return
	}
	ref := *s.DeleteProfile
	pending, err := a.loadGenerationOperations()
	if err != nil {
		s.DeleteError = "无法读取未完成操作，请稍后再试"
		return
	}
	s.PendingOperations = pending
	if reason := s.generationDeleteBlock(ref.ID); reason != "" {
		s.DeleteError = reason
		return
	}
	op := generationUIOperation{Operation: integrationOperation(), Kind: "delete", Profile: ref.ID, Revision: ref.Revision}
	if err := a.recordGenerationOperation(op); err != nil {
		s.DeleteError = "无法记录删除操作，尚未发送请求"
		return
	}
	s.Busy, s.DeleteError = true, ""
	revision := s.Generation.Revision
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var reply struct {
			Result generationDeletionReceipt `json:"result"`
		}
		err := client.request(ctx, "POST", "/v1/generation/profiles/"+url.PathEscape(ref.ID)+"/delete",
			map[string]any{"expected_revision": revision, "operation_id": op.Operation, "profile_revision": ref.Revision}, &reply)
		var directory GenerationDirectory
		readErr := client.request(ctx, "GET", "/v1/generation", nil, &directory)
		a.dispatch(func() {
			s.Busy = false
			if readErr == nil {
				s.Generation = directory
			}
			var rejection *ControlResponseError
			if errors.As(err, &rejection) && (rejection.Status == 400 || rejection.Status == 401 || rejection.Status == 403 || rejection.Status == 404 || rejection.Status == 409) {
				a.finishGenerationOperation(op.Operation)
				s.DeleteError = "配置已变化或仍在使用，请重新读取后再删除。"
				return
			}
			if err != nil || reply.Result.ID != ref.ID || !reply.Result.Deleted {
				s.DeleteError = "删除结果暂时无法确认，请核对原操作。"
				return
			}
			a.applyGenerationDeletion(op)
		})
	}()
}

func (a *Controller) applyGenerationDeletion(o generationUIOperation) {
	s := &a.integrations
	delete(s.Generation.Profiles, o.Profile)
	delete(s.GenerationDrafts, o.Profile)
	for mode, id := range s.GenerationModeSelection {
		if id == o.Profile {
			delete(s.GenerationModeSelection, mode)
		}
	}
	if s.ProfileID == o.Profile {
		s.Editor, s.APIKey = false, ""
		s.KeyEpoch++
	}
	s.DeleteProfile, s.DeleteError = nil, ""
	s.DeleteOpen = false
	a.finishGenerationOperation(o.Operation)
}

func (a *Controller) reviewGenerationDelete(o generationUIOperation) {
	s := &a.integrations
	client, ok := a.accessClient()
	if !ok || s.Busy || !a.canDispatch() {
		return
	}
	s.Busy = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var receipt struct {
			Recorded bool                      `json:"recorded"`
			Result   generationDeletionReceipt `json:"result"`
		}
		err := client.request(ctx, "GET", "/v1/generation/operations/"+url.PathEscape(o.Operation), nil, &receipt)
		a.dispatch(func() {
			s.Busy = false
			if err != nil || !receipt.Recorded || !receipt.Result.Deleted || receipt.Result.ID != o.Profile {
				s.Error = "删除结果仍未确认，保留原操作；未重新发送。"
				return
			}
			a.applyGenerationDeletion(o)
			s.Message = "模型配置已删除"
		})
	}()
}
