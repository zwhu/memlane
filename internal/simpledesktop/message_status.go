package simpledesktop

import "fmt"

// A projection of observed facts only. It never changes permissions, archives,
// queues or write outcomes, and does not infer delivery from a running job.
type messageNotice struct {
	Key, Title, Source, Detail, Followup, Tag, Action string
	NeedsUser                                         bool
	Session                                           *queuedSession
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
	for _, failure := range q.ProcessingNotices {
		detail := fmt.Sprintf("最近一次提炼调用返回 HTTP %d，尚不能据此确认记忆已生成。", failure.Status)
		switch failure.Code {
		case "codex_schema_unsupported":
			detail = "提炼请求的结构化输出格式不被 Codex 接受。"
		case "codex_quota_exceeded":
			detail = "Codex 可用额度不足，提炼调用未完成。"
		case "codex_authentication_failed":
			detail = "Codex 登录认证失败，提炼调用未完成。"
		case "codex_context_exceeded":
			detail = "本次提炼内容超出 Codex 的上下文限制。"
		}
		source := "记忆处理服务"
		if failure.Backend == "codex" {
			source = "Codex · 记忆提炼"
		}
		add(messageNotice{Key: "processing/" + failure.Operation, Title: "记忆提炼调用失败", Source: source,
			Detail: detail, Followup: "这是近期调用异常记录，不代表原始消息未保存；后续整理结果需另行核对。", Tag: "整理异常", Action: "refresh"})
	}
	for index, row := range q.Sessions {
		r := row
		v := r.View
		if v == nil {
			continue
		}
		source := agentDisplayName(r.Agent.Name) + " · 会话 " + shortSessionLabel(r.Binding.HostSessionKey)
		key := r.Agent.ID + "/" + r.Binding.HostSessionKey
		if r.Agent.ID == "" || r.Binding.HostSessionKey == "" {
			key = fmt.Sprintf("unidentified/%d", index)
		}
		if !v.Loaded || v.ReadError != "" {
			detail := "未能读取这次会话的保存状态，不能据此判断消息是否已保存。"
			if v.ReadCode == "official_session_unconfirmed" {
				detail = "本机会话与服务端会话尚未对应，暂时无法读取保存状态。"
			}
			add(messageNotice{Key: key + "/read", Title: "会话保存状态读取失败", Source: source, Detail: detail, Followup: "系统会定期重新读取；本次不补采或重发消息。", Tag: "读取异常", Action: "refresh", Session: &r})
			continue
		}
		s := v.Status // Legacy dismissal must never turn an unknown write into success.
		if s.Pending == 0 && v.Message != "" {
			add(messageNotice{Key: key + "/receipt", Title: "上次消息操作结果", Source: source, Detail: v.Message, Tag: "操作记录"})
		}
		safe := max(0, s.Pending-s.Unknown)
		if safe > 0 {
			n := messageNotice{Key: key + "/pending", Title: fmt.Sprintf("%d 条消息等待保存", safe), Source: source, Tag: "待保存", Session: &r, Action: "details"}
			access := p.Overrides[r.Agent.ID].effective(a.access.Directory.Defaults).Access
			denied := access == "read" || access == "none"
			previouslyDenied := s.Blocked != nil && (s.Blocked.Reason == "write_denied" || s.Blocked.Reason == "project_write_denied" || s.Blocked.Reason == "official_http_403")
			switch {
			case denied:
				n.NeedsUser, n.Tag, n.Action = true, "需要授权", "permissions"
				n.Detail = "这个 Agent 没有当前项目的写入权限，消息尚未保存。"
				n.Followup = "如需保存，请检查权限设置；权限恢复后，原来被拒绝的消息仍需明确选择补交。"
			case previouslyDenied && access == "maintain" && s.Unknown == 0:
				n.NeedsUser, n.Tag, n.Action = true, "需要补交", "replay"
				n.Detail = "这些消息曾因没有写入权限被暂停，目前权限已允许写入。"
				n.Followup = "请先查看本机消息，确认后补交；系统不会自动补交原来被拒绝的消息。"
			case s.Blocked != nil:
				n.Tag, n.Detail, n.Followup = "保存受阻", "本机保存队列已被暂停，当前不能继续发送这些消息。", "原消息保留；暂停原因见详情，系统不会绕过限制重新发送。"
			case q.Backlog.active():
				n.Detail, n.Followup = "消息仍在本机队列，尚未完成保存。", "后台保存任务正在运行，切换页面不会停止。"
			case q.AutomaticSave:
				n.Detail, n.Followup = "消息仍在本机队列，尚未完成保存。", "自动保存已开启；当前尚未读到正在执行的保存任务。"
			default:
				n.Detail, n.Followup = "消息仍在本机队列，尚未完成保存。", "当前没有正在运行的保存任务。"
			}
			add(n)
		}
		retryable := min(max(0, s.RetryableUnknown), max(0, s.Unknown))
		if retryable > 0 {
			n := messageNotice{Key: key + "/retry", Title: fmt.Sprintf("%d 条消息等待核对保存结果", retryable), Source: source, Detail: "尚未收到保存结果，原消息保留在本机。", Followup: "当前没有正在执行的保存任务，消息仍待核对。", Tag: "等待核对", Action: "details", Session: &r}
			if p.Overrides[r.Agent.ID].effective(a.access.Directory.Defaults).Access != "maintain" {
				n.Tag, n.Followup = "等待授权", "当前没有写入权限，系统不会重试发送。"
			} else if (s.Blocked != nil && s.Blocked.Reason == "capture_request_rejected") || (q.Backlog != nil && q.Backlog.Reason == "capture_request_rejected") {
				n.Tag, n.Followup = "保存异常", "服务拒绝了这次保存请求，原消息保留；系统已停止重试。"
			} else if q.Backlog != nil && (q.Backlog.Status == "paused" || q.Backlog.Status == "stopped" || q.Backlog.Status == "interrupted" || q.Backlog.Status == "stopping") {
				n.Tag, n.Followup = "等待恢复", "保存任务已暂停；恢复后才会按原编号核对并重试。"
			} else if q.BacklogError != "" {
				n.Tag, n.Followup = "状态未确认", "暂时无法读取保存任务状态，尚不能确认是否会重试。"
			} else if q.pendingCheck(r).Status == "checking" {
				n.Tag, n.Followup = "正在核对", "正在查询原消息的保存结果。"
			} else if q.AutomaticSave || q.Backlog.active() {
				n.Tag, n.Followup = "等待重试", "后台将按原编号核对并重试，避免重复保存。"
			}
			add(n)
		}
		if legacyUnknown := s.Unknown - retryable; legacyUnknown > 0 {
			check := q.pendingCheck(r)
			n := messageNotice{Key: key + "/unknown", Title: fmt.Sprintf("%d 条消息未收到保存结果", legacyUnknown), Source: source, Detail: "发送过程没有留下可确认的保存结果；具体原因尚未确定。", Followup: "原消息保留在本机，暂不重复发送。", Tag: "系统异常", Action: "details", Session: &r}
			switch check.Status {
			case "checking":
				n.Tag, n.Followup = "正在检查", "系统正在查询保存结果，不会重复发送。"
			case "unconfirmed":
				if check.Reason == "original_receipt_unavailable" {
					n.Detail = "这批消息缺少可查询的唯一保存凭证，系统无法核实这次保存。"
					n.Followup = "这是 Memlane 的保存机制缺陷，目前无法自动恢复。原消息保留，不重复发送，也不需要你判断是否保存。"
				} else {
					n.Detail = "系统未能读取服务端保存结果；目前无法确定是否已保存。"
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
			add(messageNotice{Key: "review-failed", Title: "部分会话暂未保存", Tag: "读取异常", Detail: fmt.Sprintf("%d 个会话的保存状态未能核对，已单独保留。", j.ReviewFailed), Followup: "其他会话可继续保存；失败会话不会被当成已保存。"})
		}
		paused := j.Status == "paused" || j.Status == "stopped" || j.Status == "interrupted"
		if paused {
			n := messageNotice{Key: "backlog", Title: "后台保存已暂停", Tag: "系统异常", Detail: backlogReason(j.Reason), Followup: "此状态不代表所有消息都未保存；具体消息见上方会话。"}
			switch j.Reason {
			case "user_stopped":
				n.Title, n.Tag = "积压消息保存已暂停", "已暂停"
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
				n.Detail = "系统尚未确认权限是否已生效，后台保存暂缓。"
				n.Followup = "这是权限状态核对问题，不表示你需要重新授权。"
				n.Action = "refresh"
			case "capture_retry_pending":
				n.Title, n.Tag = "消息保存等待重试", "自动重试"
				n.Detail, n.Followup = "尚未收到保存结果，系统将按原编号自动核对并重试。", "原消息保留在本机，不会重复保存。"
				if !j.Automatic {
					n.Title, n.Tag = "消息保存已暂停", "等待恢复"
					n.Detail, n.Followup = "手动保存任务尚未收到结果，原消息保留。", "恢复后按原编号核对并重试，不会重复保存。"
					if queueRecoverableCount(q) > 0 {
						n.NeedsUser, n.Action = true, "resume"
					}
				}
			case "capture_request_rejected":
				n.Title, n.Tag = "保存请求被拒绝", "系统异常"
				n.Detail, n.Followup = "服务拒绝了这次保存请求，系统已停止重试。", "原消息保留在本机。"
			case "backlog_installation_changed", "backlog_binding_changed":
				n.Detail = "Agent 接入配置或会话所属项目发生变化，原保存任务已暂停。"
				n.Followup = "当前不能安全沿用旧任务发送；需要先核对连接与原消息归属。"
			}
			add(n)
		}
		if j.ProcessingFailed > 0 {
			add(messageNotice{Key: "processing-failed", Title: "部分消息的记忆整理未确认完成", Tag: "整理异常", Detail: fmt.Sprintf("%d 个会话的整理提交失败或结果未确认。", j.ProcessingFailed), Followup: "这不表示原消息保存失败；整理结果未确认前不会重复提交。"})
		}
	}
	if q.BacklogError != "" {
		add(messageNotice{Key: "backlog-read", Title: "后台保存状态读取失败", Tag: "读取异常", Detail: "暂时无法读取后台保存任务；不能据此判断消息是否保存。", Followup: "系统会定期重新读取，不会重新启动同一个保存请求。", Action: "refresh"})
	}
	if q.DocumentError != "" {
		add(messageNotice{Key: "documents", Title: "记忆整理状态读取失败", Tag: "读取异常", Detail: "暂时无法获取已保存文档的整理进度。", Followup: "这不表示原消息丢失；系统会定期重新读取。", Action: "refresh"})
	}
	if q.KnowledgeReadError != "" {
		add(messageNotice{Key: "knowledge", Title: "记忆阅读状态读取失败", Tag: "读取异常", Detail: "暂时无法核对记忆的已读状态，未读数量可能不完整。", Action: "refresh"})
	}
	if q.ActivityError != "" {
		add(messageNotice{Key: "activity", Title: "近期处理记录读取失败", Tag: "读取异常", Detail: "暂时无法获取近期记忆整理记录；这不表示消息保存失败。", Action: "refresh"})
	}
	return
}
func queueSafeCount(q *projectQueueState) int {
	n := 0
	for _, r := range q.Sessions {
		if r.View != nil && r.View.Loaded && r.View.ReadError == "" {
			n += max(0, r.View.Status.Pending-r.View.Status.Unknown)
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
		n += max(0, s.Pending-s.Unknown) + min(max(0, s.RetryableUnknown), max(0, s.Unknown), max(0, s.Pending))
	}
	return n
}
