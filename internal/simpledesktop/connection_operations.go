package simpledesktop

import (
	"context"
	"net/url"
	"time"
)

// Reopening an application only reads the original receipt. An absent receipt
// cannot prove that a timed-out installation was never submitted.
func (a *App) reviewConnectionOperation(o generationUIOperation) {
	s := &a.integrations
	client, ok := a.accessClient()
	if !ok || s.Busy || a.Update == nil || !o.connection() || !o.valid() {
		return
	}
	s.Busy = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var receipt struct {
			Recorded bool   `json:"recorded"`
			Status   string `json:"status"`
			Outcome  string `json:"outcome"`
			Result   struct {
				Connection AgentConnection `json:"connection"`
			} `json:"result"`
		}
		err := client.request(ctx, "GET", "/v1/connections/operations/"+url.PathEscape(o.Operation), nil, &receipt)
		a.Update(func() {
			s.Busy = false
			if err != nil {
				s.Error = "无法读取 Agent 原操作回执；未重新提交"
				return
			}
			if !receipt.Recorded || receipt.Status != "complete" || receipt.Result.Connection.ID != o.Profile {
				s.Message = "Agent 原操作结果仍未确认；保留原编号，未重新提交。可核对该 Agent 的安装状态。"
				return
			}
			if a.finishGenerationOperation(o.Operation) {
				s.Message = "Agent 原操作回执已核对；未重新安装"
				if receipt.Outcome == "rejected" {
					s.Message = "原接入操作已确认拒绝，未重新安装；处理卡片错误后可重新准备或启用。"
				}
				a.RefreshIntegrations()
			}
		})
	}()
}
