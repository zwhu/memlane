package settings

import (
	"context"
	"errors"
	"net/url"
	"time"
)

func (a *Controller) generationAction(action string) {
	client, ok := a.accessClient()
	s := &a.integrations
	if !ok || s.Busy || !s.cleanSaved() || (action != "validations" && action != "next") {
		return
	}
	if s.DraftMode == "agent_bridge" && !s.availableProcessingBridge(s.BridgeKind) {
		return
	}
	if action == "next" && !a.generationCanUse(s.Saved) {
		return
	}
	ref := *s.Saved
	revision := s.Generation.Revision
	op := integrationOperation()
	if err := a.recordGenerationOperation(generationUIOperation{Operation: op, Kind: action, Profile: ref.ID, Revision: ref.Revision}); err != nil {
		s.Error = "无法保存操作回执位置，本次请求尚未发送"
		return
	}
	s.Busy = true
	s.Error = ""
	if action == "validations" {
		s.ValidationOperation = op
		s.Validation = GenerationValidation{Status: "queued", Phase: "queued"}
		s.ValidationStartedAt = time.Now()
	}
	if action == "next" {
		s.NextOperation = op
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		method := "POST"
		if action == "next" {
			method = "PUT"
		}
		var result any
		err := client.request(ctx, method, "/v1/generation/"+action, map[string]any{"expected_revision": revision, "operation_id": op, "profile": ref}, &result)
		var directory GenerationDirectory
		readErr := client.request(ctx, "GET", "/v1/generation", nil, &directory)
		a.dispatch(func() {
			s.Busy = false
			if readErr == nil {
				s.Generation = directory
			}
			if err != nil {
				s.Error = err.Error()
				var rejected *ControlResponseError
				if action == "next" && errors.As(err, &rejected) && rejected.Status >= 400 && rejected.Status < 500 {
					if !a.finishGenerationOperation(op) {
						return
					}
					s.NextOperation = ""
				}
			} else if action == "next" {
				if !a.finishGenerationOperation(op) {
					return
				}
				s.NextOperation = ""
				s.Message = "已设为下次启动使用；当前任务保持原配置"
			} else {
				s.Message = "正在验证，会使用所选 Provider 的额度"
			}
			if action == "validations" {
				var rejected *ControlResponseError
				if errors.As(err, &rejected) && rejected.Status >= 400 && rejected.Status < 500 {
					if !a.finishGenerationOperation(op) {
						return
					}
					s.Validation = GenerationValidation{Status: "failed"}
					return
				}
				a.pollGenerationValidation(op)
			}
		})
	}()
}

func (a *Controller) reviewGenerationNext() {
	client, ok := a.accessClient()
	s := &a.integrations
	if !ok || s.Busy || s.NextOperation == "" {
		return
	}
	s.Busy = true
	op := s.NextOperation
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var receipt struct {
			Recorded bool `json:"recorded"`
			Result   struct {
				Next *GenerationRef `json:"next"`
			} `json:"result"`
		}
		var directory GenerationDirectory
		err := client.request(ctx, "GET", "/v1/generation/operations/"+url.PathEscape(op), nil, &receipt)
		if err == nil && receipt.Recorded {
			err = client.request(ctx, "GET", "/v1/generation", nil, &directory)
		}
		a.dispatch(func() {
			s.Busy = false
			if err != nil || !receipt.Recorded || receipt.Result.Next == nil {
				s.Error = "待应用设置尚无确认回执，不会重复提交"
				return
			}
			s.Generation = directory
			if !a.finishGenerationOperation(op) {
				return
			}
			s.NextOperation = ""
			s.Error = ""
			s.Message = "原待应用设置已核对"
		})
	}()
}
