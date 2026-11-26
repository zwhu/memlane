package gateway

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"unicode"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/hooks"
)

type CaptureFilterEventView = domain.CaptureFilterEvent

var filterSessionID = regexp.MustCompile(`^[A-Za-z0-9:_-]{1,160}$`)

func safeFilterSessionID(id string) string {
	if filterSessionID.MatchString(id) && !hooks.EvaluateCaptureFilter(hooks.CaptureFilterInput{Content: id}).Blocked {
		return id
	}
	return ""
}

func (s *Server) filterCapture(w http.ResponseWriter, ctx context.Context, req captureRequest, agent domain.Agent, project domain.Project, content any) bool {
	host := req.Host
	if host == "" {
		host = agent.Kind
	}
	result := hooks.EvaluateCaptureFilter(hooks.CaptureFilterInput{Host: host, Kind: string(req.Kind), Content: content})
	if !result.Blocked {
		return false
	}
	event, err := s.cfg.Store.RecordCaptureFilterEvent(ctx, domain.CaptureFilterEvent{
		ProjectID: project.ID, AgentID: agent.ID, HostKind: result.HostKind, SessionID: safeFilterSessionID(req.SessionID),
		ToolName: result.ToolName, Rule: result.Rule, Category: result.Category, Source: result.Source,
		Reason: result.Reason, NextAction: result.NextAction,
	})
	if err != nil {
		writeDomainError(w, err)
		return true
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "accepted": false, "filtered": true, "status": "filtered",
		"project_id": project.ID, "dataset_id": project.DatasetID, "filter_id": event.ID,
		"filter_rule": event.Rule, "filter_category": event.Category, "filter_source": event.Source,
		"filter_reason": event.Reason, "next_action": event.NextAction, "occurred_at": event.OccurredAt,
	})
	return true
}

func (s *Server) listFilteredCaptures(w http.ResponseWriter, r *http.Request) {
	ctx, id := r.Context(), r.PathValue("id")
	if _, err := s.cfg.Store.GetProject(ctx, id); err != nil {
		writeDomainError(w, err)
		return
	}
	items, err := s.cfg.Store.ListCaptureFilterEvents(ctx, id, parseLimit(r.URL.Query().Get("limit"), 20, 200))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	count, err := s.cfg.Store.CountCaptureFilterEvents(ctx, id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "filtered_count": count})
}

func describeInboxFailure(rawErr string) (code, summary, nextAction string) {
	errText := strings.ToLower(rawErr)
	switch {
	case strings.Contains(errText, "timeout"), strings.Contains(errText, "deadline"), strings.Contains(errText, "超时"):
		code, summary, nextAction = "engine_timeout", "提炼服务连接超时", "请确认提炼服务可以连接，然后一键重试。"
	case strings.Contains(errText, "connection refused"), strings.Contains(errText, "engine_unavailable"), strings.Contains(errText, "unavailable"), strings.Contains(errText, "503"):
		code, summary, nextAction = "engine_unavailable", "提炼服务暂时无法连接", "请检查服务状态，恢复后再重试。"
	case strings.Contains(errText, "unauthorized"), strings.Contains(errText, "forbidden"), strings.Contains(errText, "authentication"), strings.Contains(errText, "401"), strings.Contains(errText, "403"):
		code, summary, nextAction = "authorization_failed", "提炼服务拒绝了请求", "请检查模型凭据和访问权限，更新后再重试。"
	case strings.Contains(errText, "rate limit"), strings.Contains(errText, "rate_limit"), strings.Contains(errText, "429"):
		code, summary, nextAction = "rate_limited", "模型服务请求过于频繁", "请稍后重试。"
	case strings.Contains(errText, "digest") && strings.Contains(errText, "mismatch"):
		code, summary, nextAction = "digest_mismatch", "提炼服务的配置与当前模型设置不一致", "请在模型设置中检查当前配置，应用后再重试。"
	case strings.Contains(errText, "capture_paused"):
		return "capture_paused", "项目自动采集已暂停", "开启自动采集后，新会话将继续采集。"
	case strings.Contains(errText, "capture_filtered"):
		return "capture_filtered", "记录因采集规则未采集", "请查看规则命中原因，无需手动重试。"
	default:
		code, summary, nextAction = "processing_failed", "采集记录暂未处理完成", "请点击「一键重试」，或展开技术详情排查。"
	}
	if plainFailureMessage(rawErr) {
		summary = rawErr
	}
	return code, summary, nextAction
}

func plainFailureMessage(raw string) bool {
	hasChinese := false
	for _, r := range raw {
		if unicode.Is(unicode.Han, r) {
			hasChinese = true
		} else if !strings.ContainsRune(" ，。；、！？（）", r) {
			return false
		}
	}
	return hasChinese
}
