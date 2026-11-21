package simpledesktop

import (
	"fmt"
	"time"
)

func formatNoticeLocalClock(raw string, now time.Time) string {
	t, ok := officialTime(raw)
	if !ok {
		return ""
	}
	local := t.In(now.Location())
	if local.Format("2006-01-02") == now.Format("2006-01-02") {
		return local.Format("15:04")
	}
	return local.Format("01/02 15:04")
}

// A projection of observed facts only. It never changes permissions, archives,
// queues or write outcomes, and does not infer delivery from a running job.
type messageNotice struct {
	Key, Title, Source, Detail, Followup, Tag, Action string
	NeedsUser                                         bool
	Session                                           *queuedSession
	Processing                                        *processingNotice
}

func (n messageNotice) dotTone() string {
	if n.NeedsUser {
		return "warn"
	}
	switch n.Tag {
	case "系统异常", "读取异常", "提炼异常", "采集异常", "采集受阻":
		return "fail"
	case "需要授权", "需要补交", "等待恢复", "状态未确认", "已暂停":
		return "warn"
	case "处理中", "正在检查", "正在核对", "等待重试", "自动重试":
		return "run"
	default:
		return ""
	}
}

func (n messageNotice) badgeTone() string {
	if n.NeedsUser {
		return "warn"
	}
	switch n.Tag {
	case "系统异常", "读取异常", "提炼异常", "采集异常", "采集受阻":
		return "danger"
	case "需要授权", "需要补交", "等待恢复", "状态未确认", "已暂停":
		return "warn"
	case "处理中", "正在检查", "正在核对", "等待重试", "自动重试", "待采集", "等待核对":
		return "accent"
	default:
		return ""
	}
}

func (n messageNotice) diagCode(q *projectQueueState) string {
	if n.Session != nil && n.Session.View != nil {
		if n.Session.View.ReadCode != "" {
			return n.Session.View.ReadCode
		}
		if check := q.pendingCheck(*n.Session); check.Reason != "" {
			return check.Reason
		}
		if n.Session.View.Status.Blocked != nil && n.Session.View.Status.Blocked.Reason != "" {
			return n.Session.View.Status.Blocked.Reason
		}
	}
	if n.Processing != nil {
		if n.Processing.Code != "" {
			return n.Processing.Code
		}
		if n.Processing.Status > 0 {
			return fmt.Sprintf("HTTP %d", n.Processing.Status)
		}
	}
	if q.Backlog != nil && q.Backlog.Reason != "" && n.Key == "backlog" {
		return q.Backlog.Reason
	}
	return ""
}

func (a *App) messageNotices(q *projectQueueState, p AccessProject) (needs, info []messageNotice) {
	add := func(n messageNotice) {
		if n.NeedsUser {
			needs = append(needs, n)
		} else {
			info = append(info, n)
		}
	}
	if q.ProcessingReadError != "" {
		add(messageNotice{Key: "processing-read", Title: q.ProcessingReadError, Detail: "暂时不能确认最近的模型调用状态。", Tag: "读取异常", Action: "refresh"})
	}
	now := time.Now()
	for _, failure := range q.ProcessingNotices {
		f := failure
		detail := fmt.Sprintf("最近一次提炼调用返回 HTTP %d，尚不能据此确认记忆已生成。", f.Status)
		switch f.Code {
		case "codex_schema_unsupported":
			detail = "提炼请求的结构化输出格式不被 Codex 接受。"
		case "codex_schema_failed":
			detail = "Codex 返回内容未能通过结构化格式校验，提炼调用未完成。"
		case "codex_quota_exceeded":
			detail = "Codex 可用额度不足，提炼调用未完成。"
		case "codex_authentication_failed", "codex_authentication_required":
			detail = "Codex 登录认证失败，提炼调用未完成。"
		case "codex_context_exceeded":
			detail = "本次提炼内容超出 Codex 的上下文限制。"
		case "codex_connection_failed":
			detail = fmt.Sprintf("连接 Codex 上游模型服务失败（HTTP %d），常见于网络波动或代理中断。", f.Status)
		case "codex_stream_disconnected":
			detail = fmt.Sprintf("与 Codex 上游模型服务的连接中途断开（HTTP %d）。", f.Status)
		case "codex_timeout":
			detail = fmt.Sprintf("Codex 模型响应超时（HTTP %d）。", f.Status)
		case "codex_rate_limited":
			detail = fmt.Sprintf("触发 Codex 请求频率限制（HTTP %d），提炼调用未完成。", f.Status)
		case "codex_busy":
			detail = fmt.Sprintf("Codex 代理当前队列繁忙（HTTP %d），请稍后刷新查看重试结果。", f.Status)
		case "codex_upstream_error":
			detail = fmt.Sprintf("Codex 上游服务返回异常（HTTP %d），尚不能据此确认记忆已生成。", f.Status)
		case "pi_authentication_required", "pi_command_credential_unsupported":
			detail = "Pi 登录认证失败或凭证配置不受支持，提炼调用未完成。"
		case "pi_provider_configuration_invalid":
			detail = "Pi 所选 Provider 或模型配置无效，提炼调用未完成。"
		case "pi_provider_schema_unsupported", "pi_schema_failed", "pi_native_schema_unconfirmed":
			detail = "Pi 模型未通过结构化输出格式约束或不支持当前 Schema。"
		case "pi_timeout":
			detail = fmt.Sprintf("Pi 模型响应超时（HTTP %d）。", f.Status)
		case "pi_model_failed", "pi_prompt_rejected", "pi_rpc_rejected", "pi_tool_rejected",
			"pi_tool_or_retry_rejected", "pi_transport_closed_or_limit", "pi_transport_invalid",
			"pi_model_or_state_changed", "pi_unexpected_startup_event", "pi_interactive_request_rejected",
			"pi_retry_or_terminal_invalid", "pi_terminal_incomplete", "pi_event_unrecognized", "pi_output_limit":
			detail = fmt.Sprintf("Pi 记忆提炼调用异常（%s，HTTP %d），尚不能据此确认记忆已生成。", f.Code, f.Status)
		case "antigravity_authentication_required", "antigravity_auth_home_layout_unsupported":
			detail = "Antigravity 登录认证失败或凭据目录布局不受支持，提炼调用未完成。"
		case "antigravity_account_location_unavailable":
			detail = "Antigravity 账号所在地区或网络出口不可用，提炼调用未完成。"
		case "antigravity_schema_not_bound", "antigravity_schema_failed", "antigravity_structured_output_missing":
			detail = "Antigravity 模型未能按结构化输出契约返回有效结果。"
		case "antigravity_timeout":
			detail = fmt.Sprintf("Antigravity 模型响应超时（HTTP %d）。", f.Status)
		case "antigravity_model_failed", "antigravity_process_failed", "antigravity_tool_rejected",
			"antigravity_tools_not_isolated", "antigravity_transport_closed_or_limit", "antigravity_transport_invalid",
			"antigravity_initialization_unconfirmed", "antigravity_duplicate_initialization",
			"antigravity_conversation_unconfirmed", "antigravity_conversation_changed",
			"antigravity_model_or_context_changed", "antigravity_terminal_invalid", "antigravity_cancelled":
			detail = fmt.Sprintf("Antigravity 记忆提炼调用异常（%s，HTTP %d），尚不能据此确认记忆已生成。", f.Code, f.Status)
		case "traex_authentication_failed", "traex_authentication_required":
			detail = "TraeX 登录认证失败，提炼调用未完成。"
		case "traex_quota_exceeded":
			detail = "TraeX 可用额度不足，提炼调用未完成。"
		case "traex_rate_limited", "traex_busy":
			detail = fmt.Sprintf("触发 TraeX 请求频率限制或并发队列繁忙（HTTP %d）。", f.Status)
		case "traex_schema_unsupported", "traex_schema_failed":
			detail = "TraeX 未通过结构化输出格式约束或不支持当前 Schema。"
		case "traex_context_exceeded":
			detail = "本次提炼内容超出 TraeX 的上下文限制。"
		case "traex_timeout":
			detail = fmt.Sprintf("TraeX 模型响应超时（HTTP %d）。", f.Status)
		case "traex_model_failed", "traex_request_invalid", "traex_connection_failed",
			"traex_stream_disconnected", "traex_upstream_error", "traex_upstream_cancelled":
			detail = fmt.Sprintf("TraeX 记忆提炼调用异常（%s，HTTP %d），尚不能据此确认记忆已生成。", f.Code, f.Status)
		}
		source := "记忆提炼服务"
		switch f.Backend {
		case "codex":
			source = "Codex · 记忆提炼"
		case "pi":
			source = "Pi · 记忆提炼"
		case "traex":
			source = "TraeX · 记忆提炼"
		case "antigravity":
			source = "Antigravity · 记忆提炼"
		}
		if atLabel := formatNoticeLocalClock(f.At, now); atLabel != "" {
			source += " · " + atLabel
		}
		followup := "这是近期调用异常记录，不代表原始消息未采集；后续提炼结果需另行核对。"
		if recLabel := formatNoticeLocalClock(f.RecoveredAt, now); recLabel != "" {
			followup = fmt.Sprintf("后续调用已在 %s 恢复正常；但本次失败对应的提炼结果仍需另行核对，不代表原始消息未采集。", recLabel)
		}
		add(messageNotice{Key: "processing/" + f.Operation, Title: "记忆提炼调用失败", Source: source,
			Detail: detail, Followup: followup, Tag: "提炼异常", Action: "refresh", Processing: &f})
	}
	for index, row := range q.Sessions {
		r := row
		v := r.View
		if v == nil {
			continue
		}
		source := agentDisplayName(r.Agent.Name) + " · 会话 " + shortSessionLabel(r.Binding.HostSessionKey)
		key := noticeSessionKey(r, index)
		if !v.Loaded || v.ReadError != "" {
			detail := "未能读取这次会话的采集状态，不能据此判断消息是否已采集。"
			if v.ReadCode == "official_session_unconfirmed" {
				detail = "本机会话与服务端会话尚未对应，暂时无法读取采集状态。"
			}
			add(messageNotice{Key: key + "/read", Title: "会话采集状态读取失败", Source: source, Detail: detail, Followup: "系统会定期重新读取；本次不补采或重发消息。", Tag: "读取异常", Action: "refresh", Session: &r})
			if v.Status.Pending > v.Status.Unknown && v.Status.Blocked != nil {
				add(messageNotice{Key: key + "/pending", Title: "上次读取的消息采集受阻", Source: source, Detail: "上次读取时存在未发送的受阻消息；本次读取失败，尚不能确认是否恢复。", Tag: "采集受阻", Action: "refresh", Session: &r})
			}
			if v.Status.Unknown == 0 {
				continue
			}
		}
		s := v.Status // Legacy dismissal must never turn an unknown write into success.
		if s.Pending == 0 && v.Message != "" {
			add(messageNotice{Key: key + "/receipt", Title: "上次消息操作结果", Source: source, Detail: v.Message, Tag: "操作记录"})
		}
		filtered := q.filteredPending(r)
		held := q.heldPending(r)
		if held.Rejected > 0 {
			add(messageNotice{Key: key + "/rejected", Title: fmt.Sprintf("%d 条消息采集受阻", held.Rejected), Source: source, Tag: "采集异常", Action: "details", Session: &r,
				Detail: "这些记录未通过采集检查或被服务拒绝，原消息已单独保留。", Followup: "不会自动重复发送这些记录；其他符合规则的消息可继续采集。"})
		}
		if filtered > 0 {
			add(messageNotice{Key: key + "/filtered", Title: fmt.Sprintf("%d 条消息因采集规则未采集", filtered), Source: source,
				Detail: "这些工具记录不符合当前采集规则，原消息仍保留在本机。", Followup: "其他符合规则的消息可继续采集；不会绕过采集规则发送这些记录。", Tag: "已保留", Action: "details", Session: &r})
		}
		safe := max(0, s.Pending-s.Unknown-filtered-held.RejectedUnsent-held.DeferredUnsent)
		if !v.Loaded || v.ReadError != "" {
			safe = 0 // Retained outcomes can be acknowledged, never used to send.
		}
		if safe > 0 {
			n := messageNotice{Key: key + "/pending", Title: fmt.Sprintf("%d 条消息等待采集", safe), Source: source, Tag: "待采集", Session: &r, Action: "details"}
			access := p.Overrides[r.Agent.ID].effective(a.access.Directory.Defaults).Access
			denied := access == "read" || access == "none"
			previouslyDenied := s.Blocked != nil && (s.Blocked.Reason == "write_denied" || s.Blocked.Reason == "project_write_denied" || s.Blocked.Reason == "official_http_403")
			switch {
			case denied:
				n.NeedsUser, n.Tag, n.Action = true, "需要授权", "permissions"
				n.Detail = "这个 Agent 没有当前项目的写入权限，消息尚未采集。"
				n.Followup = "如需采集，请检查权限设置；权限恢复后，原来被拒绝的消息仍需明确选择补交。"
			case previouslyDenied && access == "maintain" && s.Unknown == 0:
				n.NeedsUser, n.Tag, n.Action = true, "需要补交", "replay"
				n.Detail = "这些消息曾因没有写入权限被暂停，目前权限已允许写入。"
				n.Followup = "请先查看本机消息，确认后补交；系统不会自动补交原来被拒绝的消息。"
			case s.Blocked != nil:
				n.Tag, n.Detail, n.Followup = "采集受阻", "本机采集队列已被暂停，当前不能继续发送这些消息。", "原消息保留；暂停原因见详情，系统不会绕过限制重新发送。"
			case q.Backlog.active():
				n.Detail, n.Followup = "消息仍在本机队列，尚未完成采集。", "后台采集任务正在运行，切换页面不会停止。"
			case q.AutomaticSave:
				n.Detail, n.Followup = "消息仍在本机队列，尚未完成采集。", "自动采集已开启；当前尚未读到正在执行的采集任务。"
			default:
				n.Detail, n.Followup = "消息仍在本机队列，尚未完成采集。", "当前没有正在运行的采集任务。"
				if access == "maintain" && q.BacklogLoaded && !q.BacklogLoading && q.BacklogError == "" && (q.Backlog == nil || q.Backlog.Status == "completed" || q.Backlog.Status == "watching") {
					n.NeedsUser, n.Action = true, "save"
					n.Followup = "开始项目采集任务，补采明确未发送的消息；无编号的未知消息不会重发。"
				}
			}
			add(n)
		}
		retryable := min(max(0, s.RetryableUnknown), max(0, s.Unknown))
		if retryable > 0 {
			n := messageNotice{Key: key + "/retry", Title: fmt.Sprintf("%d 条消息等待核对采集结果", retryable), Source: source, Detail: "尚未收到采集结果，原消息保留在本机。", Followup: "当前没有正在执行的采集任务，消息仍待核对。", Tag: "等待核对", Action: "details", Session: &r}
			if p.Overrides[r.Agent.ID].effective(a.access.Directory.Defaults).Access != "maintain" {
				n.Tag, n.Followup = "等待授权", "当前没有写入权限，系统不会重试发送。"
			} else if (s.Blocked != nil && s.Blocked.Reason == "capture_request_rejected") || (q.Backlog != nil && q.Backlog.Reason == "capture_request_rejected") {
				n.Tag, n.Followup = "采集异常", "服务拒绝了这次采集请求，原消息保留；系统已停止重试。"
			} else if q.Backlog != nil && (q.Backlog.Status == "paused" || q.Backlog.Status == "stopped" || q.Backlog.Status == "interrupted" || q.Backlog.Status == "stopping") {
				n.Tag, n.Followup = "等待恢复", "采集任务已暂停；恢复后才会按原编号核对并重试。"
			} else if q.BacklogError != "" {
				n.Tag, n.Followup = "状态未确认", "暂时无法读取采集任务状态，尚不能确认是否会重试。"
			} else if q.pendingCheck(r).Status == "checking" {
				n.Tag, n.Followup = "正在核对", "正在查询原消息的采集结果。"
			} else if q.AutomaticSave || q.Backlog.active() {
				n.Tag, n.Followup = "等待重试", "后台将按原编号核对并重试，避免重复采集。"
			}
			add(n)
		}
		if legacyUnknown := s.Unknown - retryable - held.RejectedUnknown; legacyUnknown > 0 {
			check := q.pendingCheck(r)
			n := messageNotice{Key: key + "/unknown", Title: fmt.Sprintf("%d 条消息未收到采集结果", legacyUnknown), Source: source, Detail: "发送过程没有留下可确认的采集结果；具体原因尚未确定。", Followup: "原消息保留在本机，暂不重复发送。", Tag: "系统异常", Action: "details", Session: &r}
			switch check.Status {
			case "checking":
				n.Tag, n.Followup = "正在检查", "系统正在查询采集结果，不会重复发送。"
			case "unconfirmed":
				if check.Reason == "original_receipt_unavailable" {
					n.Detail = "这批消息缺少可查询的唯一采集凭证，系统无法核实这次采集。"
					n.Followup = "这是 Memlane 的采集机制缺陷，目前无法自动恢复。原消息保留，不重复发送，也不需要你判断是否采集。"
				} else {
					n.Detail = "系统未能读取服务端采集结果；目前无法确定是否已采集。"
					n.Followup = "系统会定期重新检查；原消息保留，不重复发送。"
				}
			default:
				n.Followup = "尚未读到检查结果；原消息保留，暂不重复发送。"
			}
			add(n)
		}
	}
	if j := q.Backlog; j != nil {
		if j.ReviewFailed > 0 {
			add(messageNotice{Key: "review-failed", Title: "部分会话暂未采集", Tag: "读取异常", Detail: fmt.Sprintf("%d 个会话的采集状态未能核对，已单独保留。", j.ReviewFailed), Followup: "其他会话可继续采集；失败会话不会被当成已采集。"})
		}
		paused := j.Status == "paused" || j.Status == "stopped" || j.Status == "interrupted"
		if paused {
			n := messageNotice{Key: "backlog", Title: "后台采集已暂停", Tag: "系统异常", Detail: backlogReason(j.Reason), Followup: "此状态不代表所有消息都未采集；具体消息见上方会话。"}
			switch j.Reason {
			case "user_stopped":
				n.Title, n.Tag = "积压消息采集已暂停", "已暂停"
				n.Detail = "暂停原因：任务留有停止标记，触发来源未记录。"
				if queueRecoverableCount(q) > 0 {
					n.NeedsUser, n.Action = true, "resume"
					n.Followup = "恢复后补存明确未发送的消息，并按原编号核对可恢复消息；无编号未知消息不会重发。"
				} else {
					n.Followup = "当前没有明确等待补存的消息。"
				}
			case "write_denied", "backlog_write_denied":
				n.NeedsUser, n.Action, n.Tag = true, "permissions", "需要授权"
			case "policy_unconfirmed":
				n.Detail = "系统尚未确认权限是否已生效，后台采集暂缓。"
				n.Followup = "这是权限状态核对问题，不表示你需要重新授权。"
				n.Action = "refresh"
			case "capture_retry_pending":
				n.Title, n.Tag = "消息采集等待重试", "自动重试"
				n.Detail, n.Followup = "尚未收到采集结果，系统将按原编号自动核对并重试。", "原消息保留在本机，不会重复采集。"
				if !j.Automatic {
					n.Title, n.Tag = "消息采集已暂停", "等待恢复"
					n.Detail, n.Followup = "手动采集任务尚未收到结果，原消息保留。", "恢复后按原编号核对并重试，不会重复采集。"
					if queueRecoverableCount(q) > 0 {
						n.NeedsUser, n.Action = true, "resume"
					}
				}
			case "capture_request_rejected":
				n.Title, n.Tag = "采集请求被拒绝", "系统异常"
				n.Detail, n.Followup = "服务拒绝了这次采集请求，系统已停止重试。", "原消息保留在本机。"
			case "backlog_installation_changed", "backlog_binding_changed":
				n.Detail = "Agent 接入配置或会话所属项目发生变化，原采集任务已暂停。"
				n.Followup = "当前不能安全沿用旧任务发送；需要先核对连接与原消息归属。"
				n.Action = "refresh"
			default:
				n.Action = "refresh"
			}
			add(n)
		}
		if j.ProcessingFailed > 0 {
			add(messageNotice{Key: "processing-failed", Title: "部分消息的记忆提炼未确认完成", Tag: "提炼异常", Detail: fmt.Sprintf("%d 个会话的提炼提交失败或结果未确认。", j.ProcessingFailed), Followup: "这不表示原消息采集失败；提炼结果未确认前不会重复提交。", Action: "refresh"})
		}
		if j.ProcessingPartial > 0 {
			add(messageNotice{Key: "processing-partial", Title: "部分材料已提交记忆提炼", Tag: "等待接续",
				Detail:   fmt.Sprintf("%d 个会话已有部分材料提交提炼，仍有消息尚未完成同步。", j.ProcessingPartial),
				Followup: "剩余消息采集后，系统会按已有提炼进度继续；提交成功不代表提炼已完成。", Action: "refresh"})
		}
		if j.ProcessingActive > 0 {
			add(messageNotice{Key: "processing-active", Title: "正在提交记忆提炼", Tag: "处理中",
				Detail: fmt.Sprintf("%d 个会话正在提交，尚未取得提交结果。", j.ProcessingActive), Action: "refresh"})
		}
	}
	if q.BacklogError != "" {
		add(messageNotice{Key: "backlog-read", Title: "后台采集状态读取失败", Tag: "读取异常", Detail: "暂时无法读取后台采集任务；不能据此判断消息是否采集。", Followup: "系统会定期重新读取，不会重新启动同一个采集请求。", Action: "refresh"})
	}
	if q.DocumentError != "" {
		add(messageNotice{Key: "documents", Title: "记忆提炼状态读取失败", Tag: "读取异常", Detail: "暂时无法获取已采集文档的提炼进度。", Followup: "这不表示原消息丢失；系统会定期重新读取。", Action: "refresh"})
	}
	if q.KnowledgeReadError != "" {
		add(messageNotice{Key: "knowledge", Title: "记忆阅读状态读取失败", Tag: "读取异常", Detail: "暂时无法核对记忆的已读状态，未读数量可能不完整。", Action: "refresh"})
	}
	if q.ActivityError != "" {
		add(messageNotice{Key: "activity", Title: "近期提炼记录读取失败", Tag: "读取异常", Detail: "暂时无法获取近期记忆提炼记录；这不表示消息采集失败。", Action: "refresh"})
	}
	return
}
func queueSafeCount(q *projectQueueState) int {
	n := 0
	for _, r := range q.Sessions {
		if r.View != nil && r.View.Loaded && r.View.ReadError == "" {
			held := q.heldPending(r)
			n += max(0, r.View.Status.Pending-r.View.Status.Unknown-q.filteredPending(r)-held.RejectedUnsent-held.DeferredUnsent)
		}
	}
	return n
}

// Only confirmed-unsent or numbered recoverable entries can be resumed.
func queueRecoverableCount(q *projectQueueState) int {
	n := 0
	for _, row := range q.Sessions {
		if row.View == nil || !row.View.Loaded || row.View.ReadError != "" {
			continue
		}
		s := row.View.Status
		if s.Blocked != nil && s.Blocked.Reason == "capture_request_rejected" {
			continue
		}
		held := q.heldPending(row)
		n += max(0, s.Pending-s.Unknown-q.filteredPending(row)-held.RejectedUnsent-held.DeferredUnsent) + min(max(0, s.RetryableUnknown), max(0, s.Unknown-held.RejectedUnknown), max(0, s.Pending))
	}
	return n
}
