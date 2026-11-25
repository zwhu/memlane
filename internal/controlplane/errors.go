package controlplane

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

type ControlError struct {
	Status      int    `json:"status"`
	Code        string `json:"code,omitempty"`
	Phase       string `json:"phase,omitempty"`
	SafeMessage string `json:"safe_message"`
}

func (e *ControlError) Error() string { return e.SafeMessage }

func (e *ControlError) IsConflict() bool { return e.Status == http.StatusConflict }

func (e *ControlError) IsPreMutationRejected() bool {
	return e.IsConflict() && e.Phase == "pre_mutation"
}

func (e *ControlError) IsAuthDenied() bool {
	return e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden
}

func (e *ControlError) IsCode(status int, code string) bool {
	return e.Status == status && e.Code == code
}

var errorIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,79}$`)

func decodeControlError(status int, body io.Reader, token string) *ControlError {
	var payload struct {
		Error string `json:"error"`
		Code  string `json:"code"`
		Phase string `json:"phase"`
	}
	data, err := io.ReadAll(io.LimitReader(body, 4<<10))
	if err != nil || json.Unmarshal(data, &payload) != nil {
		return newControlError(status, "", "")
	}

	safeIdentifier := func(value string) string {
		if !errorIdentifier.MatchString(value) || (token != "" && strings.Contains(value, token)) {
			return ""
		}
		return value
	}
	code := safeIdentifier(payload.Code)
	if code == "" {
		code = safeIdentifier(payload.Error)
	}
	return newControlError(status, code, safeIdentifier(payload.Phase))
}

func newControlError(status int, code, phase string) *ControlError {
	e := &ControlError{Status: status, Code: code, Phase: phase}
	switch code {
	case "control_credential_unavailable":
		e.SafeMessage = "项目管理连接凭据不可用"
	case "project_unconfirmed":
		e.SafeMessage = "项目尚未确认，无法读取内容"
	case "document_path_denied":
		e.SafeMessage = "内容路径受限，无法读取"
	case "catalog_index_rebuilding":
		e.SafeMessage = "索引正在构建中，请稍候刷新"
	case "reading_contract_mismatch":
		e.SafeMessage = "当前版本与本地后台的读取协议不一致，请重启后台服务"
	case "invalid_cursor":
		e.SafeMessage = "列表分页游标已失效，请刷新列表"
	case "revision_conflict", "reviewed_preview_required":
		e.SafeMessage = "接入配置已变化或需要重新确认，请刷新后核对"
	default:
		switch {
		case e.IsPreMutationRejected():
			e.SafeMessage = "操作在写入前被拒绝，请刷新后核对"
		case e.IsConflict():
			e.SafeMessage = "设置已变化或存在冲突，请刷新核对原操作"
		case e.IsAuthDenied():
			e.SafeMessage = "项目管理服务拒绝访问，请核对连接凭据和权限"
		case status == http.StatusBadRequest:
			e.SafeMessage = "项目管理服务拒绝了无效请求"
		case status == http.StatusNotFound:
			e.SafeMessage = "项目管理服务未找到所请求的资源"
		case status == http.StatusServiceUnavailable:
			e.SafeMessage = "项目管理服务暂时不可用，请核对原操作"
		default:
			e.SafeMessage = fmt.Sprintf("项目管理请求未确认（HTTP %d），请核对原操作", status)
		}
	}
	return e
}
