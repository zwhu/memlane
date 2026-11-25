package settings

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/zwhu/memlane/internal/bootstrap"
	"github.com/zwhu/memlane/internal/controlplane"
)

func (r *onboardingGenerationRun) save(id string, profile GenerationProfile, key string) error {
	op := generationUIOperation{Kind: "save", Operation: integrationOperation(), Profile: id}
	payload := map[string]any{"profile": profile}
	if key != "" {
		payload["api_key"] = key
	}
	var receipt struct {
		Result struct {
			GenerationRef
			Digest string `json:"digest"`
		} `json:"result"`
	}
	if err := r.mutate(op, http.MethodPut, "/v1/generation/profiles/"+url.PathEscape(id), payload, &receipt); err != nil {
		return err
	}
	if err := r.refresh(); err != nil {
		return err
	}
	row, found := r.directory.row(&receipt.Result.GenerationRef)
	if !found || receipt.Result.ID != id || row.Digest == "" || row.Digest != receipt.Result.Digest {
		return errors.New("保存回执与配置不匹配")
	}
	r.ref = &receipt.Result.GenerationRef
	return controlplane.FinishUIOperation(r.operations, op.Operation)
}

func (r *onboardingGenerationRun) mutate(op generationUIOperation, method, path string, payload map[string]any, out any) error {
	if _, err := recordGenerationOperationAt(r.operations, op); err != nil {
		return errors.New("无法记录配置操作，未发送请求")
	}
	payload["operation_id"], payload["expected_revision"] = op.Operation, r.directory.Revision
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := r.client.request(ctx, method, path, payload, out); err != nil {
		var rejected *ControlResponseError
		if errors.As(err, &rejected) && rejected.Status >= 400 && rejected.Status < 500 &&
			rejected.Phase != "post_mutation" && rejected.Code != "operation_id_conflict" {
			if clearErr := controlplane.FinishUIOperation(r.operations, op.Operation); clearErr != nil {
				return clearErr
			}
		}
		return err
	}
	return nil
}

func (r *onboardingGenerationRun) validate(parent context.Context, progress func(GenerationValidation, time.Time), beginEnabling func() bool) error {
	op := generationUIOperation{Kind: "validations", Operation: integrationOperation(), Profile: r.ref.ID, Revision: r.ref.Revision}
	r.validationStarted = time.Now()
	progress(GenerationValidation{Status: "queued", Phase: "queued"}, r.validationStarted)
	// Keep the submission alive even if Stop is clicked during it so a
	// registered operation can be explicitly canceled using its stable ID.
	var receipt struct {
		Result struct {
			Operation string `json:"operation_id"`
		} `json:"result"`
	}
	err := r.mutate(op, http.MethodPost, "/v1/generation/validations", map[string]any{"profile": r.ref}, &receipt)
	path := "/v1/generation/validations/" + url.PathEscape(op.Operation)
	if parent.Err() != nil {
		return r.cancelValidation(op.Operation, path)
	}
	if err != nil {
		return err
	}
	if receipt.Result.Operation != op.Operation {
		return errors.New("验证登记回执与请求不匹配")
	}
	ctx, cancel := context.WithTimeout(parent, 7*time.Minute)
	defer cancel()
	for {
		requestCtx, done := context.WithTimeout(ctx, 10*time.Second)
		err = r.client.request(requestCtx, http.MethodGet, path, nil, &r.validation)
		done()
		if parent.Err() != nil {
			return r.cancelValidation(op.Operation, path)
		}
		if err != nil {
			return err
		}
		if !generationValidationActive(r.validation) {
			break
		}
		progress(r.validation, r.validationStarted)
		select {
		case <-time.After(time.Second):
		case <-parent.Done():
			return r.cancelValidation(op.Operation, path)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err = r.refresh(); err != nil {
		return err
	}
	row, saved := r.directory.row(r.ref)
	if !saved || r.validation.Reference == nil || *r.validation.Reference != *r.ref || row.Digest != r.validation.Digest {
		return errors.New("验证回执与所选配置不匹配")
	}
	if parent.Err() != nil {
		return r.cancelValidation(op.Operation, path)
	}
	if r.validation.Status != "passed" || !r.validation.Plain || !r.validation.Schema || !r.validation.Quality ||
		r.directory.Validations[op.Operation].Digest != row.Digest || !r.directory.validated(r.ref) {
		if r.validation.Status == "failed" || r.validation.Status == "passed" {
			if err = controlplane.FinishUIOperation(r.operations, op.Operation); err != nil {
				return err
			}
		}
		return errors.New("本次测试请求未通过验证")
	}
	if !beginEnabling() {
		return r.cancelValidation(op.Operation, path)
	}
	return controlplane.FinishUIOperation(r.operations, op.Operation)
}

func (r *onboardingGenerationRun) cancelValidation(operation, path string) error {
	if err := r.refresh(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	var receipt struct {
		Result struct {
			Operation string `json:"operation_id"`
			Exited    bool   `json:"local_process_exited"`
		} `json:"result"`
	}
	payload := map[string]any{"expected_revision": r.directory.Revision, "operation_id": integrationOperation()}
	if err := r.client.request(ctx, http.MethodDelete, path, payload, &receipt); err != nil {
		return err
	}
	if receipt.Result.Operation != operation || !receipt.Result.Exited {
		return errors.New("尚未确认验证进程已退出")
	}
	if err := controlplane.FinishUIOperation(r.operations, operation); err != nil {
		return err
	}
	return context.Canceled
}

func (r *onboardingGenerationRun) selectNext() error {
	op := generationUIOperation{Kind: "next", Operation: integrationOperation(), Profile: r.ref.ID, Revision: r.ref.Revision}
	var receipt struct {
		Result struct {
			Next *GenerationRef `json:"next"`
		} `json:"result"`
	}
	if err := r.mutate(op, http.MethodPut, "/v1/generation/next", map[string]any{"profile": r.ref}, &receipt); err != nil {
		return err
	}
	if err := r.refresh(); err != nil {
		return err
	}
	if receipt.Result.Next == nil || *receipt.Result.Next != *r.ref || r.directory.Next == nil ||
		*r.directory.Next != *r.ref || !r.directory.validated(r.ref) {
		return errors.New("待启用回执与本次验证的配置不匹配")
	}
	return controlplane.FinishUIOperation(r.operations, op.Operation)
}

func (r *onboardingGenerationRun) activate(prepared bootstrap.FirstRuntime,
	activate func(context.Context, bootstrap.FirstRuntime, bootstrap.FirstGenerationReference, string, int64) error,
	checkComplete func(string) (bool, error)) error {
	row, ok := r.directory.row(r.ref)
	if !ok || r.directory.Next == nil || *r.directory.Next != *r.ref || !r.directory.validated(r.ref) {
		return errors.New("当前配置尚无可确认的验证与待启用回执")
	}
	base, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	ctx, end := bootstrap.TrackStartupContext(base)
	defer end()
	ref := bootstrap.FirstGenerationReference{ID: r.ref.ID, Revision: r.ref.Revision}
	if err := activate(ctx, prepared, ref, row.Digest, r.directory.Revision); err != nil {
		if firstActivationRejected(err) {
			r.activationRefreshErr = r.refresh()
			if r.activationRefreshErr != nil && errors.Is(err, bootstrap.ErrFirstActivationRejected) {
				// A late pre-mutation check can reject after the setup service
				// has shut down. Restore normal setup before reading its directory.
				prepared.ActivationFile = ""
				if bootstrap.StartFirstRuntime(ctx, prepared) == nil {
					r.activationRefreshErr = r.refresh()
				}
			}
		}
		return err
	}
	loaded, err := LoadConfig(prepared.ConfigPath)
	if err != nil {
		return err
	}
	complete, err := checkComplete(prepared.ConfigPath)
	if err != nil {
		return err
	}
	if !complete {
		return errors.New("初始化完成回执尚未确认")
	}
	if loaded.ProjectControl == nil {
		return errors.New("无法读取启用后的记忆提炼配置")
	}
	newClient := ProjectControlClient{Config: *loaded.ProjectControl, HTTP: r.client.HTTP}
	active, err := readOnboardingGenerationDirectory(newClient)
	if err != nil {
		return err
	}
	activeRow, found := active.row(active.Active)
	if !found || *active.Active != *r.ref || activeRow.Digest != row.Digest {
		return errors.New("实际启用配置与本次已验证配置不同")
	}
	r.loaded, r.directory = loaded, active
	r.activationClearErr = bootstrap.ClearFirstSetupDeferred(prepared.ConfigPath)
	return nil
}

func firstActivationRejected(err error) bool {
	var response *ControlResponseError
	return errors.Is(err, bootstrap.ErrFirstActivationRejected) ||
		errors.As(err, &response) && response.Status == http.StatusConflict &&
			response.Code == "revision_conflict" && response.Phase == "pre_mutation"
}

func onboardingGenerationFailure(phase onboardingGenerationPhase, validation GenerationValidation, err error) string {
	if errors.Is(err, context.Canceled) && phase == onboardingValidating {
		return "已停止验证，配置已保存。"
	}
	prefix := phase.label() + "未完成"
	if message := generationValidationMessage(validation.ErrorCode); message != "" {
		return prefix + " · " + message
	}
	var response *ControlResponseError
	if errors.As(err, &response) {
		detail := fmt.Sprintf("HTTP %d", response.Status)
		if message := generationValidationMessage(response.Code); message != "" {
			detail += " · " + message
		}
		return prefix + " · " + detail
	}
	return prefix + "，请核对本地服务与原操作回执。"
}
