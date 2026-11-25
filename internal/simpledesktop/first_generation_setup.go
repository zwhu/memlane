package simpledesktop

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zwhu/memlane/internal/bootstrap"
	"github.com/zwhu/memlane/internal/controlplane"
)

func (a *App) firstGenerationEnableBlocker() string {
	s := &a.integrations
	missingProcessingCLI := s.Editor && s.DraftMode == "agent_bridge" && !s.availableProcessingBridge(s.BridgeKind)
	switch {
	case !a.environment.RuntimeStarted || a.environment.staleRuntimeOwner():
		return "记忆服务尚未启动，请先返回上一步检查服务。"
	case a.environment.Busy || a.environment.RestartingStaleDaemon:
		return "正在启用记忆提炼，请等待完成。"
	case generationValidationActive(s.Validation):
		elapsed := generationValidationElapsed(time.Now(), s.ValidationStartedAt, s.Validation)
		if elapsed >= 15*time.Second {
			return fmt.Sprintf("验证响应较慢，已等待 %d 秒。可继续等待或停止验证。", elapsed/time.Second)
		}
		return "正在进行真实验证，验证通过后可继续。"
	case s.Busy:
		return "正在处理配置，请等待操作完成。"
	case s.SaveOperation != "":
		return "保存结果待确认，请核对原操作。"
	case s.Editor && !missingProcessingCLI && !s.cleanSaved():
		return "请先保存当前草稿。"
	case s.Validation.Status == "failed" || s.Validation.Status == "result_unknown":
		return "验证尚未通过，已保存的配置可继续使用。请重试或调整配置。"
	case s.hasPendingGeneration():
		return "有操作结果待确认，请先核对原操作。"
	}

	if missingProcessingCLI {
		if unconfigured := a.detectedUnconfiguredProcessingCLIs(); len(unconfigured) > 0 {
			for _, cli := range unconfigured {
				if s.BridgeKind == cli.Kind {
					return fmt.Sprintf("已检测到 %s，请先在终端完成登录并选择默认模型后重新检测。", cli.DisplayName)
				}
			}
			return "已检测到本机 CLI，请先在终端完成登录并选择默认模型后重新检测。"
		}
		return "请先安装或重新检测本机 CLI，或先进入 Memlane 稍后配置。"
	}
	if s.Editor && !s.Generation.validated(s.Saved) {
		return "草稿已保存，请先进行真实验证。"
	}
	next := s.Generation.Next
	if next == nil || (s.Editor && *next != *s.Saved) {
		return "请先将当前配置设为待启用。"
	}
	if !s.Generation.validated(next) {
		return "待启用配置尚未通过真实验证。"
	}
	if !a.generationCanUse(next) {
		return "待启用配置的本机 CLI 当前不可用，请重新检测。"
	}
	return ""
}

func generationValidationActive(v GenerationValidation) bool {
	return v.Status == "queued" || v.Status == "running"
}

func generationValidationStageLabel(v GenerationValidation) string {
	switch v.Status {
	case "queued":
		return "验证已登记，正在等待执行"
	case "running":
		if stage := generationPhaseLabel(v.Phase); stage != "" {
			return stage
		}
		return "正在验证"
	case "passed":
		return "验证通过"
	case "failed":
		return "验证未通过"
	case "result_unknown":
		return "验证结果未确认"
	}
	return ""
}

func generationPhaseLabel(phase string) string {
	return map[string]string{
		"queued":        "正在等待执行",
		"prepare":       "正在准备验证环境",
		"bridge_start":  "正在启动 Agent 模型代理",
		"process_spawn": "正在启动验证进程",
		"probe":         "正在向模型发送真实验证",
		"cleanup":       "正在清理验证进程",
	}[phase]
}

func generationValidationElapsed(now time.Time, started time.Time, v GenerationValidation) time.Duration {
	if !generationValidationActive(v) {
		if v.ElapsedMs > 0 && v.ElapsedMs < float64(math.MaxInt64)/float64(time.Millisecond) {
			return time.Duration(v.ElapsedMs * float64(time.Millisecond))
		}
		return 0
	}
	if started.IsZero() && v.StartedAt > 0 && v.StartedAt < float64(math.MaxInt64)/float64(time.Second) {
		seconds, fraction := math.Modf(v.StartedAt)
		started = time.Unix(int64(seconds), int64(fraction*float64(time.Second)))
	}
	if started.IsZero() || now.Before(started) {
		return 0
	}
	return now.Sub(started)
}

func generationValidationSlowNotice(elapsed time.Duration, v GenerationValidation) string {
	if !generationValidationActive(v) || elapsed < 15*time.Second {
		return ""
	}
	return fmt.Sprintf("响应较慢（已等待 %d 秒）：模型或网络仍在处理中，无需重复点击。可继续等待或点击「停止验证」。", elapsed/time.Second)
}

func generationValidationDiagnosticLine(v GenerationValidation) string {
	var parts []string
	phase := v.DiagnosticStage
	if phase == "" {
		phase = v.FailedPhase
	}
	if stage := generationPhaseLabel(phase); stage != "" {
		parts = append(parts, "失败阶段："+strings.TrimPrefix(stage, "正在"))
	}
	if v.HTTPStatus >= 100 && v.HTTPStatus <= 599 {
		parts = append(parts, fmt.Sprintf("HTTP %d", v.HTTPStatus))
	}
	if label := map[string]string{
		"gaierror":       "DNS 解析失败",
		"ConnectTimeout": "连接超时", "ReadTimeout": "读取超时", "WriteTimeout": "写入超时", "PoolTimeout": "等待连接超时",
		"TimeoutError": "等待超时", "ConnectError": "连接失败", "ConnectionRefusedError": "连接被拒绝",
		"ConnectionError": "连接异常", "ConnectionResetError": "连接已重置", "ConnectionAbortedError": "连接中止",
		"BrokenPipeError": "连接已关闭", "ProxyError": "代理连接失败", "OSError": "系统连接异常",
		"ReadError": "读取连接失败", "WriteError": "写入连接失败", "RemoteProtocolError": "远端协议异常",
	}[v.NetworkError]; label != "" {
		parts = append(parts, label)
	}
	if label := map[string]string{
		"dns_resolution_failed": "DNS 解析失败", "connection_refused": "连接被拒绝", "connection_error": "连接异常",
		"probe_output_too_large": "验证回执超过大小上限", "probe_output_invalid_json": "验证回执无法解析",
		"probe_output_invalid_schema": "验证回执格式无效", "ollama_native_schema_unverified": "Ollama 结构化能力回执缺失或不匹配",
	}[v.DiagnosticCode]; label != "" && !containsGenerationDiagnostic(parts, label) {
		parts = append(parts, label)
	}
	if label := map[string]string{
		"pi_timeout": "Pi 模型请求超时", "pi_model_failed": "Pi 模型请求失败", "pi_authentication_required": "Pi 需要登录",
		"codex_timeout": "Codex 模型请求超时", "codex_connection_failed": "Codex 连接失败", "codex_model_failed": "Codex 模型请求失败",
		"antigravity_timeout": "Antigravity 模型请求超时", "antigravity_model_failed": "Antigravity 模型请求失败",
		"traex_timeout": "TraeX 模型请求超时", "traex_connection_failed": "TraeX 连接失败", "traex_model_failed": "TraeX 模型请求失败",
	}[v.BridgeErrorCode]; label != "" {
		parts = append(parts, label)
	}
	if v.ProcessExitCode != nil && *v.ProcessExitCode != 0 {
		parts = append(parts, fmt.Sprintf("验证进程退出码 %d", *v.ProcessExitCode))
	}
	if elapsed := generationValidationElapsed(time.Time{}, time.Time{}, v); elapsed > 0 {
		parts = append(parts, fmt.Sprintf("耗时 %.1f 秒", elapsed.Seconds()))
	}
	return strings.Join(parts, " · ")
}

func containsGenerationDiagnostic(parts []string, label string) bool {
	for _, part := range parts {
		if part == label {
			return true
		}
	}
	return false
}

func generationValidationNextStep(v GenerationValidation) string {
	if v.Status != "failed" && v.Status != "result_unknown" {
		return ""
	}
	advice := "请核对原验证，或调整模型配置后重新验证。"
	switch {
	case v.ErrorCode == "validation_cleanup_unconfirmed" || v.ErrorCode == "validation_owner_interrupted":
		advice = "请先核对原验证和服务状态，再重新验证。"
	case v.ErrorCode == "generation_bridge_changed_revalidate":
		advice = "请核对 Agent 的当前模型和权限，再重新验证。"
	case v.HTTPStatus == 401 || v.HTTPStatus == 403 || v.ErrorCode == "authentication_failed" || v.ErrorCode == "permission_denied":
		advice = "请检查凭据和模型权限，再重新验证。"
	case v.HTTPStatus == 402 || v.HTTPStatus == 429 || v.ErrorCode == "rate_or_quota_limit" || v.ErrorCode == "payment_required":
		advice = "请检查模型额度和请求限制，稍后重新验证。"
	case v.ErrorCode == "dns_resolution_failed" || v.NetworkError == "gaierror" || v.DiagnosticCode == "dns_resolution_failed":
		advice = "请检查 API 地址与 DNS，再重新验证。"
	case v.HTTPStatus >= 500 && v.HTTPStatus <= 599:
		advice = "请检查模型服务状态，恢复后重新验证。"
	case v.ErrorCode == "validation_timeout" || v.ErrorCode == "provider_timeout":
		advice = "请核对原验证，或在模型服务恢复响应后重新验证。"
	case v.ErrorCode == "provider_connection_unconfirmed":
		advice = "请检查 API 地址、网络和代理设置，再重新验证。"
	}
	return "已保留当前保存的配置，无需重新填写。" + advice
}

func (s *integrationState) canRetryGenerationValidation() bool {
	return (s.Validation.Status == "failed" || s.Validation.Status == "result_unknown") &&
		!s.Busy && s.cleanSaved() && (s.DraftMode != "agent_bridge" || s.availableProcessingBridge(s.BridgeKind))
}

func (a *App) retryGenerationValidation() {
	s := &a.integrations
	if _, ok := a.accessClient(); !ok || !s.canRetryGenerationValidation() {
		return
	}
	var cleared *generationUIOperation
	if s.Validation.Status == "result_unknown" && s.ValidationOperation != "" {
		for _, pending := range s.PendingOperations {
			if pending.Kind == "validations" && pending.Operation == s.ValidationOperation && pending.Profile == s.Saved.ID {
				copyPending := pending
				if !a.finishGenerationOperation(pending.Operation) {
					return
				}
				cleared = &copyPending
				break
			}
		}
	}
	beforeOp := s.ValidationOperation
	a.generationAction("validations")
	if cleared != nil && !s.Busy && s.ValidationOperation == beforeOp {
		_ = a.recordGenerationOperation(*cleared)
	}
}

type onboardingGenerationPhase uint8

const (
	onboardingGenerationIdle onboardingGenerationPhase = iota
	onboardingSaving
	onboardingValidating
	onboardingSelecting
	onboardingActivating
)

func (p onboardingGenerationPhase) label() string {
	return [...]string{"", "保存配置", "发送测试请求", "设为待启用", "启用记忆提炼"}[p]
}

type onboardingGenerationState struct {
	phase                              onboardingGenerationPhase
	validation                         GenerationValidation
	validationStarted                  time.Time
	validationElapsed                  time.Duration
	failure                            string
	stopValidation                     context.CancelFunc
	activationRef                      *GenerationRef
	activationDirectory                GenerationDirectory
	advanced, inputFocused, focusModel bool
	stopping                           bool
	shownPhase                         onboardingGenerationPhase
	shownFailure                       string
	shownSlow                          bool
}

type onboardingGenerationRun struct {
	client               ProjectControlClient
	directory            GenerationDirectory
	operations           string
	phase                onboardingGenerationPhase
	ref                  *GenerationRef
	validation           GenerationValidation
	validationStarted    time.Time
	loaded               Config
	activationClearErr   error
	activationRefreshErr error
}

func (a *App) runOnboardingGeneration() {
	_, blocked, _ := a.onboardingProcessAction()
	client, accessible := a.accessClient()
	if blocked || !accessible || !a.initialSetup || a.onboardingPage() != onboardingProcess {
		return
	}
	s, g := &a.integrations, &a.onboardingGeneration
	config, prepared := a.Config, a.environment.RuntimePreparation
	activate := a.ActivateFirstRuntimeFn
	if activate == nil {
		activate = bootstrap.ActivateFirstRuntime
	}
	checkComplete := a.FirstInitCompleteFn
	if checkComplete == nil {
		checkComplete = bootstrap.FirstInitializationComplete
	}
	update := a.Update
	run := onboardingGenerationRun{client: client, directory: s.Generation, operations: a.generationOperationsDir()}
	profile, id, key := s.draftProfile(), s.ProfileID, s.APIKey
	if profile.Provider == "ollama" {
		key = ""
	}
	if g.phase == onboardingActivating {
		run.phase, run.ref, run.directory = onboardingActivating, g.activationRef, g.activationDirectory
	} else if g.phase > onboardingSaving && s.cleanSaved() {
		ref := *s.Saved
		run.ref = &ref
	}
	s.Busy = true
	s.Request++
	s.Loading = false
	g.phase, g.failure, g.validation, g.stopping = onboardingSaving, "", GenerationValidation{}, false
	if run.phase == onboardingActivating {
		g.phase = onboardingActivating
	} else {
		g.validationElapsed = 0
	}
	validationCtx, cancel := context.WithCancel(context.Background())
	g.stopValidation = cancel
	go func() {
		defer cancel()
		firstProgress := true
		err := run.execute(validationCtx, id, profile, key, func(validation GenerationValidation, started time.Time) {
			if firstProgress {
				firstProgress = false
				directory := run.directory
				row, ok := directory.row(run.ref)
				update(func() {
					s.Generation = directory
					if ok {
						s.editProfile(id, row)
					}
				})
			}
			update(func() {
				g.phase, g.validation, g.validationStarted = onboardingValidating, validation, started
			})
		}, func() bool {
			// Hand off ownership from validation to enabling on the UI thread.
			// A Stop action already dispatched by that thread must win.
			advance := make(chan bool, 1)
			update(func() {
				allowed := validationCtx.Err() == nil
				if allowed {
					g.phase = onboardingSelecting
					g.stopValidation = nil
					g.validationElapsed = time.Since(g.validationStarted)
				}
				advance <- allowed
			})
			select {
			case allowed := <-advance:
				return allowed
			case <-validationCtx.Done():
				return false
			}
		})
		if err == nil {
			run.phase = onboardingActivating
			update(func() { g.phase = onboardingActivating })
			err = run.activate(prepared, activate, checkComplete)
			if firstActivationRejected(err) {
				run.phase = onboardingSelecting
			}
		} else if run.phase != onboardingActivating {
			// A rejected mutation or a terminal failed validation can have
			// advanced the shared directory revision.
			_ = run.refresh()
		}
		pending, pendingErr := loadGenerationOperationsFromDir(run.operations)
		update(func() {
			s.Busy, g.stopping, g.stopValidation = false, false, nil
			if pendingErr == nil {
				s.PendingOperations = pending
			}
			s.Generation = run.directory
			if run.activationRefreshErr != nil {
				s.GenerationError = "启用未写入，暂时无法读取最新提炼配置，请重新检测。"
			}
			if row, found := run.directory.row(run.ref); found && id == run.ref.ID {
				s.editProfile(id, row)
			}
			g.phase, g.validation = run.phase, run.validation
			if !run.validationStarted.IsZero() {
				g.validationElapsed = generationValidationElapsed(time.Time{}, time.Time{}, run.validation)
				if g.validationElapsed == 0 {
					g.validationElapsed = time.Since(run.validationStarted)
				}
			}
			if err != nil {
				g.failure = onboardingGenerationFailure(run.phase, run.validation, err)
				if run.phase == onboardingActivating {
					g.activationRef, g.activationDirectory = run.ref, run.directory
				} else if firstActivationRejected(err) {
					g.activationRef, g.activationDirectory = nil, GenerationDirectory{}
					g.failure = "启用在写入前被拒绝，配置已保留。可以修改配置、重新验证或稍后配置。"
				}
				return
			}
			g.activationRef, g.activationDirectory = nil, GenerationDirectory{}
			loaded := run.loaded
			if loaded.ConfigFile == "" {
				loaded.ConfigFile = config.ConfigFile
			}
			a.acceptFirstRuntimeActivation(loaded, prepared, run.activationClearErr, false)
			a.showOnboardingDone(generationLabel(s.Generation, run.ref))
		})
	}()
}

func (a *App) stopOnboardingValidation() {
	g := &a.onboardingGeneration
	if g.phase == onboardingValidating && !g.stopping && g.stopValidation != nil {
		g.stopping = true
		g.stopValidation()
	}
}

func (r *onboardingGenerationRun) refresh() error {
	directory, err := readOnboardingGenerationDirectory(r.client)
	if err == nil {
		r.directory = directory
	}
	return err
}

func readOnboardingGenerationDirectory(client ProjectControlClient) (GenerationDirectory, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var directory GenerationDirectory
	err := client.request(ctx, http.MethodGet, "/v1/generation", nil, &directory)
	return directory, err
}

func (r *onboardingGenerationRun) execute(validationCtx context.Context, id string, profile GenerationProfile, key string,
	progress func(GenerationValidation, time.Time), beginEnabling func() bool) error {
	// Only activation-uncertain recovery reuses a previously approved Next.
	// Revalidating it would replace the revision in the original activation receipt.
	if r.phase == onboardingActivating {
		return nil
	}
	r.phase = onboardingSaving
	if r.ref == nil {
		if err := r.save(id, profile, key); err != nil {
			return err
		}
	}
	r.phase = onboardingValidating
	if err := r.validate(validationCtx, progress, beginEnabling); err != nil {
		return err
	}
	r.phase = onboardingSelecting
	return r.selectNext()
}

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
