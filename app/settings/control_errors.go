package settings

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/zwhu/memlane/app/api"
	"github.com/zwhu/memlane/internal/controlplane"
)

type ControlResponseError struct {
	Status  int
	Code    string
	Phase   string
	Message string
}

func (e *ControlResponseError) Error() string { return e.Message }

var controlErrorIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,79}$`)

func parseControlError(body []byte, token string) (code, phase string) {
	var payload struct {
		Error string `json:"error"`
		Code  string `json:"code"`
		Phase string `json:"phase"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return "", ""
	}
	for _, candidate := range []string{payload.Code, payload.Error} {
		if controlErrorIdentifier.MatchString(candidate) && (token == "" || !strings.Contains(candidate, token)) {
			code = candidate
			break
		}
	}
	if payload.Phase == "pre_mutation" || payload.Phase == "post_mutation" {
		phase = payload.Phase
	}
	return code, phase
}

func formatControlConflictMessage(code, phase string) string {
	if !controlErrorIdentifier.MatchString(code) {
		return "设置已变化或存在冲突，请刷新核对原操作"
	}
	var message string
	switch code {
	case "revision_conflict":
		message = "设置已被其他窗口或命令行更新，请刷新后再试"
	case "reviewed_preview_required":
		message = "接入预览已过期，请重新预览确认后再继续"
	case "host_configuration_changed":
		message = "宿主配置文件已变化，请刷新后核对"
		if phase == "post_mutation" {
			message = "写入后检测到宿主配置文件变化，需人工核对"
		}
	case "codex_configuration_changed":
		message = "Codex 配置已变动，请刷新后核对"
	case "trae_ide_hook_changed_requires_review":
		message = "TRAE CN Hooks 配置与记录不一致，请使用「一键修复 TRAE Hooks」或核对连接"
	case "trae_ide_unsupported_hook_events":
		message = "TRAE CN Hooks 包含旧版不兼容事件，请点击「一键修复 TRAE Hooks」"
	case "connection_operation_ambiguous_requires_review":
		message = "该次历史连接操作存在写入风险，需人工核对后处理"
	case "host_executable_changed":
		message = "Agent 应用版本已更新，请点击「核对连接」确认"
	case "project_binding_conflict", "project_workspace_conflict":
		message = "该项目目录已关联到其他项目，请检查目录后重试"
	case "project_already_exists":
		message = "同名项目已存在，请更换名称后重试"
	case "dataset_already_linked":
		message = "该记忆库已关联到其他项目，请刷新后核对"
	default:
		message = "操作存在冲突，请刷新核对原操作"
		switch phase {
		case "pre_mutation":
			message = "操作在写入前已拦截，请刷新核对原操作"
		case "post_mutation":
			message = "写入后状态待核对，请人工核对原操作"
		}
	}
	return message + "（" + code + "）"
}

func apiStatus(err error, codes ...int) bool {
	var e *api.APIError
	return errors.As(err, &e) && slices.Contains(codes, e.Status)
}

func IsControlAuthDenied(err error) bool {
	if apiStatus(err, http.StatusUnauthorized, http.StatusForbidden) {
		return true
	}
	var ce *ControlResponseError
	return errors.As(err, &ce) && (ce.Status == http.StatusUnauthorized || ce.Status == http.StatusForbidden)
}

func IsControlErrorCode(err error, status int, code string) bool {
	var ce *ControlResponseError
	if errors.As(err, &ce) && ce.Status == status && ce.Code == code {
		return true
	}
	var shared *controlplane.ControlError
	return errors.As(err, &shared) && shared.Status == status && shared.Code == code
}

func ControlUserError(err error) string {
	if err == nil {
		return ""
	}
	var ce *ControlResponseError
	if errors.As(err, &ce) {
		if ce.Status == http.StatusConflict {
			return formatControlConflictMessage(ce.Code, ce.Phase)
		}
		switch ce.Code {
		case "project_unconfirmed":
			return "项目尚未确认，无法读取内容"
		case "document_path_denied":
			return "内容路径受限，无法读取"
		case "catalog_index_rebuilding":
			return "索引正在构建中，请稍候刷新"
		case "reading_contract_mismatch":
			return "桌面端与本地后台的读取协议不一致，请用当前版本重启后台服务"
		case "invalid_cursor":
			return "列表分页游标已失效，请刷新列表"
		}
	}
	var shared *controlplane.ControlError
	if errors.As(err, &shared) && shared.IsConflict() {
		return formatControlConflictMessage(shared.Code, shared.Phase)
	}
	return err.Error()
}
