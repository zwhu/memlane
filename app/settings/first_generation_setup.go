package settings

import (
	"fmt"
	"math"
	"strings"
	"time"
)

func (a *Controller) firstGenerationEnableBlocker() string {
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

func generationValidationCanFinish(v GenerationValidation) bool {
	switch v.Status {
	case "passed", "failed":
		return true
	case "result_unknown":
		return v.ErrorCode != "" && v.ErrorCode != "validation_cleanup_unconfirmed"
	default:
		return false
	}
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

func (s *IntegrationState) canRetryGenerationValidation() bool {
	return (s.Validation.Status == "failed" || s.Validation.Status == "result_unknown") &&
		!s.Busy && s.cleanSaved() && (s.DraftMode != "agent_bridge" || s.availableProcessingBridge(s.BridgeKind))
}

func (a *Controller) retryGenerationValidation() {
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
