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
		timeout := 20 * time.Second
		if action == "validations" {
			timeout = 180 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		method := "POST"
		if action == "next" {
			method = "PUT"
		}
		var validationReceipt GenerationValidation
		var target any
		if action == "validations" {
			target = &validationReceipt
		} else {
			var result any
			target = &result
		}
		err := client.request(ctx, method, "/v1/generation/"+action, map[string]any{"expected_revision": revision, "operation_id": op, "profile": ref}, target)
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
				s.Message = "已设为下次启动使用"
			} else if validationReceipt.Status == "passed" || validationReceipt.Status == "failed" || validationReceipt.Status == "result_unknown" {
				s.Validation = validationReceipt
				s.Message = ""
				a.finishGenerationOperation(op)
				return
			} else {
				s.Message = "正在验证…"
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

// switchAgentBridge directly activates a locally detected Agent CLI in regular
// Settings without pre-validation, automatically saving updated CLI defaults first
// when needed so activeProfileGate never hits a stale model mismatch.
func (a *Controller) switchAgentBridge() {
	client, ok := a.accessClient()
	s := &a.integrations
	if !ok || s.Busy || s.DraftMode != "agent_bridge" || !s.availableProcessingBridge(s.BridgeKind) {
		return
	}
	draft := s.draftProfile()
	if draft.Model == "" {
		s.Error = "尚未读取到本机 CLI 的默认模型，请确认已登录后重试。"
		return
	}
	id := s.ProfileID
	if id == "" {
		id = "agent-" + draft.BridgeKind
	}
	var targetRef GenerationRef
	needsSave := true
	if s.Saved != nil {
		if row, found := s.Generation.row(s.Saved); found &&
			row.Profile.Mode == "agent_bridge" &&
			row.Profile.BridgeKind == draft.BridgeKind &&
			row.Profile.Model == draft.Model &&
			row.Profile.BridgeProvider == draft.BridgeProvider {
			targetRef = *s.Saved
			needsSave = false
		}
	}
	revision := s.Generation.Revision
	handoffAvailable := s.Generation.HandoffAvailable
	label := generationBridgeLabel(draft.BridgeKind)
	s.Busy = true
	s.Error = ""
	s.Message = "正在切换到 " + label + "…"
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if needsSave {
			var saveResp struct {
				Result GenerationRef `json:"result"`
			}
			err := client.request(ctx, "PUT", "/v1/generation/profiles/"+url.PathEscape(id), map[string]any{
				"expected_revision": revision,
				"operation_id":      integrationOperation(),
				"profile":           draft,
			}, &saveResp)
			if err != nil {
				a.dispatch(func() {
					s.Busy = false
					s.Message = ""
					s.Error = "保存 CLI 配置失败：" + ControlUserError(err)
				})
				return
			}
			targetRef = saveResp.Result
		}
		var handoffDir GenerationHandoffDirectory
		var switchErr error
		if handoffAvailable {
			switchErr = client.request(ctx, "POST", "/v1/generation/handoffs", map[string]any{
				"profile":           targetRef,
				"expected_revision": revision,
				"operation_id":      integrationOperation(),
			}, &handoffDir)
		} else {
			var ignored any
			switchErr = client.request(ctx, "PUT", "/v1/generation/next", map[string]any{
				"profile":           targetRef,
				"expected_revision": revision,
				"operation_id":      integrationOperation(),
			}, &ignored)
		}
		var directory GenerationDirectory
		readErr := client.request(ctx, "GET", "/v1/generation", nil, &directory)
		a.dispatch(func() {
			s.Busy = false
			if readErr == nil {
				s.Generation = directory
			}
			if switchErr != nil {
				s.Message = ""
				s.Error = "切换失败：" + ControlUserError(switchErr)
				return
			}
			if handoffAvailable && handoffDir.Handoff != nil && handoffDir.Handoff.Status != "applied" {
				s.Message = ""
				s.Handoff = handoffDir
				s.Error = "切换未生效，仍使用原模型"
				return
			}
			if row, found := s.Generation.row(&targetRef); found {
				s.editProfile(targetRef.ID, row)
			}
			s.Handoff = GenerationHandoffDirectory{}
			s.Error = ""
			if handoffAvailable {
				s.Message = "已切换到 " + label
			} else {
				s.Message = "已设为下次启动使用"
			}
			a.refreshServiceMonitor()
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
