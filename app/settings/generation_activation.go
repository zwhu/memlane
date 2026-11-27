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
				s.MessageScope = "generation"
			} else if validationReceipt.Status == "passed" || validationReceipt.Status == "failed" || validationReceipt.Status == "result_unknown" {
				s.Validation = validationReceipt
				s.Message = ""
				s.MessageScope = ""
				a.finishGenerationOperation(op)
				return
			} else {
				s.Message = "正在验证…"
				s.MessageScope = "generation"
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
	handoffOp := integrationOperation()
	opsDir := a.generationOperationsDir()
	if handoffAvailable && !needsSave {
		op := generationUIOperation{Operation: handoffOp, Kind: "handoffs", Profile: targetRef.ID, Revision: targetRef.Revision}
		if err := a.recordGenerationOperation(op); err != nil {
			s.Error = "无法记录切换操作，尚未发送"
			return
		}
		s.HandoffOperation = handoffOp
	}
	s.Busy = true
	s.Error = ""
	s.Message = "正在切换到 " + label + "…"
	s.MessageScope = "generation"
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var recordedPending []generationUIOperation
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
			if handoffAvailable {
				pending, recErr := recordGenerationOperationAt(opsDir, generationUIOperation{
					Operation: handoffOp,
					Kind:      "handoffs",
					Profile:   targetRef.ID,
					Revision:  targetRef.Revision,
				})
				if recErr != nil {
					a.dispatch(func() {
						s.Busy = false
						s.Message = ""
						s.Error = "无法记录切换操作，尚未发送"
					})
					return
				}
				recordedPending = pending
			}
		}
		var handoffDir GenerationHandoffDirectory
		var switchErr error
		if handoffAvailable {
			switchErr = client.request(ctx, "POST", "/v1/generation/handoffs", map[string]any{
				"profile":           targetRef,
				"expected_revision": revision,
				"operation_id":      handoffOp,
			}, &handoffDir)
		} else {
			var ignored any
			switchErr = client.request(ctx, "PUT", "/v1/generation/next", map[string]any{
				"profile":           targetRef,
				"expected_revision": revision,
				"operation_id":      handoffOp,
			}, &ignored)
		}
		var directory GenerationDirectory
		readErr := client.request(ctx, "GET", "/v1/generation", nil, &directory)
		a.dispatch(func() {
			s.Busy = false
			if len(recordedPending) > 0 {
				s.PendingOperations = recordedPending
			}
			if readErr == nil {
				s.Generation = directory
			}
			if switchErr != nil {
				var rejected *ControlResponseError
				if !handoffAvailable || (errors.As(switchErr, &rejected) && rejected.Status >= 400 && rejected.Status < 500) {
					s.Message = ""
					if a.finishGenerationOperation(handoffOp) {
						s.HandoffOperation = ""
					}
					s.Error = "切换失败：" + ControlUserError(switchErr)
					return
				}
				// Transport or 5xx error: outcome is unknown, enter confirming state and reconcile.
				s.Error = ""
				s.Message = "正在确认使用的模型"
				s.MessageScope = "generation"
				s.HandoffOperation = handoffOp
				s.Handoff = GenerationHandoffDirectory{
					Handoff: &GenerationHandoffReceipt{
						Operation: handoffOp,
						Reference: targetRef,
						Status:    "unknown",
					},
				}
				a.pollGenerationHandoff(handoffOp)
				return
			}
			if handoffAvailable && handoffDir.Handoff != nil {
				switch handoffDir.Handoff.Status {
				case "applied":
					if a.finishGenerationOperation(handoffOp) {
						s.HandoffOperation = ""
					}
				case "not_applied":
					s.Message = ""
					s.Handoff = handoffDir
					if a.finishGenerationOperation(handoffOp) {
						s.HandoffOperation = ""
					}
					s.Error = "切换未生效，仍使用原模型"
					return
				case "unknown", "result_unknown":
					if readErr == nil && directory.Active != nil && *directory.Active == targetRef && directory.ActiveHandoff == nil {
						if a.finishGenerationOperation(handoffOp) {
							s.HandoffOperation = ""
						}
						break
					}
					s.Error = ""
					s.Message = "正在确认使用的模型"
					s.MessageScope = "generation"
					s.HandoffOperation = handoffOp
					s.Handoff = handoffDir
					a.pollGenerationHandoff(handoffOp)
					return
				default:
					s.Error = ""
					s.Message = "正在确认使用的模型"
					s.MessageScope = "generation"
					s.HandoffOperation = handoffOp
					s.Handoff = handoffDir
					a.pollGenerationHandoff(handoffOp)
					return
				}
			}
			if row, found := s.Generation.row(&targetRef); found {
				s.editProfile(targetRef.ID, row)
			}
			s.Handoff = GenerationHandoffDirectory{}
			s.Error = ""
			if handoffAvailable {
				s.Message = ""
				s.MessageScope = ""
			} else {
				s.Message = "已设为下次启动使用"
				s.MessageScope = "generation"
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
			s.MessageScope = "generation"
		})
	}()
}
