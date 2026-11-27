package settings

import (
	"context"
	"errors"
	"net/url"
	"time"
)

func (a *Controller) pollGenerationValidation(operation string) {
	a.pollGenerationValidationWithTiming(operation, 2*time.Second, 7*time.Minute)
}

func (a *Controller) pollGenerationValidationWithTiming(operation string, interval, budget time.Duration) {
	client, ok := a.accessClient()
	if !ok || operation == "" {
		return
	}
	if a.integrations.ValidationCancel != nil {
		a.integrations.ValidationCancel()
	}
	// The local provider has a 360s backend budget. Observe its terminal receipt
	// beyond that budget without cancelling or resubmitting the model operation.
	parent, stop := context.WithTimeout(context.Background(), budget)
	a.integrations.ValidationCancel = stop
	go func() {
		defer stop()
		defer func() {
			if errors.Is(parent.Err(), context.DeadlineExceeded) {
				a.dispatch(func() {
					s := &a.integrations
					if s.ValidationOperation == operation && (s.Validation.Status == "running" || s.Validation.Status == "queued") {
						s.Error = "等待验证回执超时，请核对原验证；不会重新发起验证。"
					}
				})
			}
		}()
		for {
			ctx, cancel := context.WithTimeout(parent, 10*time.Second)
			var receipt GenerationValidation
			err := client.request(ctx, "GET", "/v1/generation/validations/"+url.PathEscape(operation), nil, &receipt)
			cancel()
			if parent.Err() != nil {
				return
			}
			terminal := err != nil || (receipt.Status != "running" && receipt.Status != "queued")
			a.dispatch(func() {
				if a.integrations.ValidationOperation != operation {
					return
				}
				if terminal {
					a.integrations.Message = ""
				}
				if err != nil {
					var rejected *ControlResponseError
					if errors.As(err, &rejected) && rejected.Status == 404 {
						a.integrations.Validation = GenerationValidation{Status: "result_unknown", ErrorCode: "validation_not_recorded"}
						a.integrations.Error = "未找到该次验证记录，可能尚未成功提交，可点击「重新验证」重新发起。"
						if a.initialSetup && a.onboardingPage() == onboardingProcess &&
							rejected.Code == "generation_validation_not_found" && a.finishGenerationOperation(operation) {
							a.onboardingGeneration.phase = onboardingValidating
							a.onboardingGeneration.failure = "服务端未登记这次验证，可以重新验证或修改配置。"
						}
					} else {
						a.integrations.Error = "验证回执暂时不可用，请核对原验证"
					}
				} else {
					a.integrations.Validation = receipt
					if receipt.Status == "passed" || receipt.Status == "failed" {
						a.finishGenerationOperation(operation)
					}
				}
				if terminal {
					a.RefreshIntegrations()
				}
			})
			if terminal {
				return
			}
			select {
			case <-time.After(interval):
			case <-parent.Done():
				return
			}
		}
	}()
}

func (a *Controller) cancelGenerationValidation() {
	client, ok := a.accessClient()
	s := &a.integrations
	if !ok || s.Busy || s.ValidationOperation == "" {
		return
	}
	s.Busy = true
	operation := s.ValidationOperation
	revision := s.Generation.Revision
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		var receipt any
		err := client.request(ctx, "DELETE", "/v1/generation/validations/"+url.PathEscape(operation), map[string]any{"expected_revision": revision, "operation_id": integrationOperation()}, &receipt)
		a.dispatch(func() {
			s.Busy = false
			if err != nil {
				s.Error = "停止验证的结果未确认，请核对原验证"
			} else {
				s.Message = "已请求停止验证；远端请求结果仍以回执为准"
				s.MessageScope = "generation"
			}
			a.pollGenerationValidation(operation)
		})
	}()
}
