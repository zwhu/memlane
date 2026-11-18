package simpledesktop

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/egoist/mygo/ui"
)

func (s *integrationState) generationDeleteBlock(id string) string {
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

func (a *App) generationDeleteModal(c *ui.Context) {
	s := &a.integrations
	if s.DeleteProfile == nil {
		return
	}
	ref := *s.DeleteProfile
	modal := ui.Modal(c.Key("delete-generation-"+ref.ID), &s.DeleteOpen, func() {
		homeText(c, "删除模型配置？", homeHeading).FontSize(18)
		homeText(c, generationLabel(s.Generation, &ref), homeBody).FontSize(13).MaxLines(3)
		if s.DeleteError != "" {
			homeText(c, s.DeleteError, homeMeta).MaxLines(3)
		}
		ui.Row(c).FillWidth().Justify(ui.End).Gap(12).Children(func() {
			textButton(c, "取消", func() { s.DeleteProfile = nil; s.DeleteOpen = false }).Disabled(s.Busy)
			ui.Button(c, "确认删除").Label("确认删除模型：" + ref.ID).Disabled(s.Busy || s.generationDeleteBlock(ref.ID) != "").OnClick(a.deleteGeneration)
		})
	}).Width(460).Padding(24).Gap(18)
	if modal.Dismissed() || !s.DeleteOpen {
		s.DeleteProfile = nil
	}
}

type generationDeletionReceipt struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

func (a *App) deleteGeneration() {
	s := &a.integrations
	client, ok := a.accessClient()
	if !ok || s.Busy || s.DeleteProfile == nil || a.Update == nil {
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
		a.Update(func() {
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

func (a *App) applyGenerationDeletion(o generationUIOperation) {
	s := &a.integrations
	delete(s.Generation.Profiles, o.Profile)
	if s.ProfileID == o.Profile {
		s.Editor, s.APIKey = false, ""
		s.KeyEpoch++
	}
	s.DeleteProfile, s.DeleteError = nil, ""
	s.DeleteOpen = false
	a.finishGenerationOperation(o.Operation)
}

func (a *App) reviewGenerationDelete(o generationUIOperation) {
	s := &a.integrations
	client, ok := a.accessClient()
	if !ok || s.Busy || a.Update == nil {
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
		a.Update(func() {
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
