package desktop

import (
	"context"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"time"
)

type conversationReadCandidate struct {
	Previous   records.CogneeBinding
	Connection records.Connection
}
type conversationReadReview struct {
	Candidate conversationReadCandidate
	Intent    records.CogneeReadReauthorization
	Busy      bool
	Message   string
	Cancel    context.CancelFunc
}

func conversationReadCandidateFor(previous records.CogneeBinding, connections []records.Connection) *conversationReadCandidate {
	agent := "codex"
	if previous.MaterialSubmission {
		agent = "pi"
	}
	for _, c := range connections {
		if c.ID == previous.ConnectionID && c.Agent == agent && c.Enabled && c.Generation > previous.Generation {
			return &conversationReadCandidate{Previous: previous, Connection: c}
		}
	}
	return nil
}

func (a *App) openConversationReadReview() {
	s := &a.conversations
	if !a.conversationPageVisible() || s.ReadCandidate == nil || s.ReadReview != nil {
		return
	}
	candidate := *s.ReadCandidate
	if candidate.Previous.Project != s.Project {
		return
	}
	s.ReadReview = &conversationReadReview{Candidate: candidate, Intent: records.CogneeReadReauthorization{Previous: candidate.Previous, Generation: candidate.Connection.Generation, Operation: uuid.NewString()}}
}

func (a *App) confirmConversationReadReview() {
	s := &a.conversations
	r := s.ReadReview
	if !a.conversationPageVisible() || r == nil || r.Busy || a.libraryContext == nil {
		return
	}
	r.Busy = true
	r.Message = "正在核对当前项目与官方数据集的访问权限…"
	epoch, project, client, life, dispatch := a.libraryEpoch, s.Project, a.library, a.libraryContext, a.dispatch
	ctx, cancel := context.WithTimeout(life, 20*time.Second)
	r.Cancel = cancel
	q := r.Intent
	go func() {
		defer cancel()
		binding, err := client.ReauthorizeCogneeRead(ctx, q)
		if life.Err() != nil {
			return
		}
		dispatch(func() {
			s := &a.conversations
			if a.libraryEpoch != epoch || a.library.Root != client.Root || s.Project != project || s.ReadReview != r || !a.conversationPageVisible() {
				return
			}
			r.Busy = false
			r.Cancel = nil
			if err != nil {
				r.Message = "读取授权尚未确认。请核对当前连接后重试。"
				return
			}
			expected := q.Previous
			if !expected.MaterialSubmission {
				expected.Generation = q.Generation
				expected.Operation = q.Operation
				expected.ReadPreviousOperation = q.Previous.Operation
				expected.ReadReviewDigest = records.MergeDigest(q)
			}
			if binding != expected {
				r.Message = "回执与确认的读取范围不一致，请重新核对。"
				return
			}
			if s.Cancel != nil {
				s.Cancel()
			}
			s.Loading = false
			s.Sequence++
			s.Binding = &binding
			s.ReadReview = nil
			s.ReadCandidate = nil
			s.ErrorCode = ""
			a.refreshConversationCatalog(false)
		})
	}()
}

func (a *App) conversationReadReviewView(c *ui.Context) {
	s := &a.conversations
	r := s.ReadReview
	ui.Scroll(c.Key("conversation-read-review")).Grow(1).MinHeight(0).Padding(26, 20).Gap(18).Children(func() {
		ui.Text(c, "确认会话读取范围").FontSize(19).Bold()
		ui.Text(c, "连接授权已变化。确认后恢复读取当前项目已绑定的官方数据集。").FontSize(12).LineHeight(1.8)
		meta(c, "项目", a.scopeLabel(r.Intent.Previous.Project))
		meta(c, "Agent", r.Candidate.Connection.Name)
		meta(c, "服务", r.Intent.Previous.Endpoint)
		meta(c, "数据集", r.Intent.Previous.Dataset)
		message := "读取官方当前保留的问答和工具记录，已有资料与来源继续保留。"
		if r.Intent.Previous.MaterialSubmission {
			message = "仅恢复读取本项目已确认保存的问答和工具记录；不会开启后续采集或读取其他历史会话。"
		}
		ui.Text(c, message).FontSize(11).TextColor(design.Muted).LineHeight(1.8)
		if r.Message != "" {
			ui.Text(c, r.Message).FontSize(11).TextColor(design.Muted).LineHeight(1.8)
		}
	})
	ui.Row(c).Height(48).Shrink(0).Padding(7, 20).Gap(10).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		quietButton(c, "取消").Disabled(r.Busy).OnClick(func() { s.ReadReview = nil })
		ui.Spacer(c)
		firstLaunchPrimary(c, "确认恢复读取").Disabled(r.Busy).OnClick(a.confirmConversationReadReview)
	})
}
