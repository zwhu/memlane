package desktop

import (
	"context"
	"errors"
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/agentbridge"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"time"
)

// Local retained transmissions are not HomeActivities or saved knowledge.
// This pane deliberately does not alter counts, selected knowledge or sources.
type AgentDeliveryUI struct {
	CaptureDiscoveries                                      []records.AgentDeliveryReview
	CaptureDeferred                                         map[string]bool
	Technical                                               bool
	Root, Selected, Error, DetailError, Info                string
	Sequence, DetailSequence, RetrySequence                 uint64
	Full, Loaded, Loading, DetailLoading, Confirm, Retrying bool
	Items                                                   []records.AgentDeliveryItem
	Review                                                  *records.AgentDeliveryReview
	Cancel                                                  context.CancelFunc
	CheckedAt                                               time.Time
	ListScroll, DetailScroll                                ui.ScrollState
}

func deliveryLabel(i records.AgentDeliveryItem, active bool) string {
	if active {
		return "正在补发"
	}
	switch i.Status.State {
	case "pending_unverified":
		return "授权未核验"
	case "sending":
		return "上次结果未知"
	case "waiting":
		if i.Attempts > 1 && i.Status.Code != "" {
			return "补发失败 · 结果未知"
		}
		return "等待补发"
	case "blocked":
		return "补发已停止"
	case "confirmed":
		return "已确认保存"
	}
	return "状态未知"
}
func deliveryMayRetry(i records.AgentDeliveryItem) bool {
	return i.HasGrant && i.Status.LocalRetained && (i.Status.State == "waiting" || i.Status.State == "sending") && i.Status.ReviewDigest != ""
}
func (a *App) bindAgentDelivery() {
	s := &a.homeActivity.Delivery
	if s.Root != a.library.Root {
		if s.Cancel != nil {
			s.Cancel()
		}
		seq := s.Sequence + 1
		*s = AgentDeliveryUI{Root: a.library.Root, Sequence: seq}
	}
}

// Background capture and GUI reads share the same durable queue lock. A busy
// lock is transient, not an unreadable queue. Bound waiting to this view's
// lifetime and one second; preserve every other error without retrying it.
func readDeliveryQueue[T any](ctx context.Context, read func() (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	for {
		page, err := read()
		var remote *serviceipc.ServiceError
		busy := err != nil && (err.Error() == "delivery_busy" || errors.As(err, &remote) && remote.Code == "delivery_busy")
		if !busy {
			return page, err
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return page, ctx.Err()
		case <-timer.C:
		}
	}
}

func readAgentDeliveryPage(ctx context.Context, root string) (records.AgentDeliveryPage, error) {
	return readDeliveryQueue(ctx, func() (records.AgentDeliveryPage, error) { return agentbridge.ListDeliveries(root) })
}

func (a *App) refreshAgentDeliveries() {
	a.bindAgentDelivery()
	s := &a.homeActivity.Delivery
	if s.Loading || s.Retrying || s.Root == "" || a.dispatch == nil || a.libraryContext == nil {
		return
	}
	s.Sequence++
	seq := s.Sequence
	s.Loading = true
	s.Confirm = false
	s.Error = ""
	root, owner, epoch := s.Root, a.libraryContext, a.libraryEpoch
	captureRoot := a.agents.Root
	dispatch := a.dispatch
	go func() {
		page, err := readAgentDeliveryPage(owner, root)
		var discoveries []records.AgentDeliveryReview
		if err == nil {
			for _, item := range page.Items {
				if len(discoveries) >= 64 {
					break
				}
				if item.HasGrant || item.Status.PersistedConfirmed || item.Assignment != nil || item.Origin != "raw" {
					continue
				}
				review, readErr := readDeliveryQueue(owner, func() (records.AgentDeliveryReview, error) { return agentbridge.ReviewDelivery(root, item.Status.ID) })
				if readErr == nil {
					if _, ok := records.ReadCaptureOrigin(review.Arguments); ok {
						// Advisory display only; the daemon owns the lock and
						// repeats this proof before routing or saving anything.
						i, proofErr := agents.LoadInstallation(captureRoot, item.ConnectionID)
						if proofErr != nil || agents.ValidateCodexCaptureSource(captureRoot, i, review.Arguments) != nil {
							continue
						}
						discoveries = append(discoveries, review)
					}
				}
			}
		}
		dispatch(func() {
			if owner.Err() != nil || a.libraryContext != owner || a.libraryEpoch != epoch || s.Root != root || a.library.Root != root || s.Sequence != seq {
				return
			}
			s.Loading = false
			s.CheckedAt = time.Now()
			if err != nil {
				s.Error = "本机待发送记录暂时无法读取，请重试。"
				return
			}
			s.Items = page.Items
			s.CaptureDiscoveries = discoveries
			s.Loaded = true
			found := false
			for _, item := range s.Items {
				if item.Status.ID == s.Selected {
					found = true
					if s.Review != nil && (s.Review.Item.Status.State != item.Status.State || s.Review.Item.Status.ReviewDigest != item.Status.ReviewDigest || s.Review.Item.Status.Code != item.Status.Code) {
						a.selectAgentDelivery(s.Selected)
					}
				}
			}
			if !found {
				s.Selected = ""
				s.Review = nil
				s.Confirm = false
			}
			if s.Full && s.Selected == "" && len(s.Items) > 0 {
				a.selectAgentDelivery(s.Items[0].Status.ID)
			}
		})
	}()
}
func (a *App) selectAgentDelivery(id string) {
	s := &a.homeActivity.Delivery
	if s.Retrying {
		return
	}
	if s.Selected != id {
		s.Technical = false
		s.DetailScroll.Y = 0
	}
	s.Selected = id
	s.Review = nil
	s.DetailError = ""
	s.Info = ""
	s.Confirm = false
	s.DetailSequence++
	seq := s.DetailSequence
	s.DetailLoading = true
	root, owner, epoch := s.Root, a.libraryContext, a.libraryEpoch
	dispatch := a.dispatch
	if a.dispatch == nil || owner == nil {
		s.DetailLoading = false
		return
	}
	go func() {
		review, err := readDeliveryQueue(owner, func() (records.AgentDeliveryReview, error) { return agentbridge.ReviewDelivery(root, id) })
		dispatch(func() {
			if owner.Err() != nil || a.libraryContext != owner || a.libraryEpoch != epoch || s.Root != root || a.library.Root != root || s.DetailSequence != seq || s.Selected != id {
				return
			}
			s.DetailLoading = false
			if err != nil {
				s.DetailError = "原始暂存内容未能核验；不能补发。"
				return
			}
			if review.Item.Status.ID != id || review.Item.Status.ReviewDigest == "" {
				s.DetailError = "本机暂存的核对信息不符，请重新读取。"
				return
			}
			s.Review = &review
		})
	}()
}
func (a *App) beginAgentDeliveryRetry() {
	s := &a.homeActivity.Delivery
	if !s.Confirm || s.Retrying || s.Loading || s.DetailLoading || s.Error != "" || s.DetailError != "" || s.Review == nil || !deliveryMayRetry(s.Review.Item) || s.Root != a.library.Root || a.dispatch == nil || a.libraryContext == nil {
		return
	}
	review := *s.Review
	s.Confirm = false
	s.Retrying = true
	s.Info = "正在补发同一条原始提交，等待保存确认。"
	s.RetrySequence++
	seq := s.RetrySequence
	root, owner, epoch := s.Root, a.libraryContext, a.libraryEpoch
	dispatch := a.dispatch
	ctx, cancel := context.WithTimeout(owner, 120*time.Second)
	s.Cancel = cancel
	go func() {
		defer cancel()
		out := agentbridge.RetryDelivery(ctx, root, review.Item.Status.ID, review.Item.Status.ReviewDigest, true)
		current, readErr := agentbridge.ReviewDelivery(root, review.Item.Status.ID)
		dispatch(func() {
			if owner.Err() != nil || a.libraryContext != owner || a.libraryEpoch != epoch || a.library.Root != root || s.Root != root || s.RetrySequence != seq {
				return
			}
			s.Retrying = false
			s.Cancel = nil
			if readErr == nil && s.Selected == current.Item.Status.ID {
				s.Review = &current
			}
			if revision := confirmedDeliveryRevision(out, current, readErr, review.Item.Status.ID, review.Item.Status.ReviewDigest); revision > 0 {
				if readErr != nil && s.Review != nil && out.Delivery != nil {
					s.Review.Item.Status = *out.Delivery
				}
				s.Info = fmt.Sprintf("已确认保存 v%d；索引状态单独读取。", revision)
			} else {
				s.Info = "本机暂存仍保留；结果尚未确认，请核对后明确重试。"
				if out.Error != nil {
					switch out.Error.Code {
					case "delivery_library_changed", "delivery_authorization_changed", "disconnected", "reload_required", "restore_reconnect_required", "connection_not_found", "scope_denied", "scope_required", "workspace_mismatch", "authorization_expired":
						s.Info = "原记忆库或授权已变化，补发已停止；不会改绑范围或另建一条提交。"
					case "delivery_unverified":
						s.Info = "原库身份未核验；这条暂存不能直接补发。"
					case "delivery_review_required", "delivery_operation_conflict":
						s.Info = "原始提交的核对信息已变化，请重新读取后确认。"
					}
				}
			}
			a.refreshAgentDeliveries()
		})
	}()
}
func (a *App) openAgentDelivery() {
	a.navigate("总览", "")
	a.bindAgentDelivery()
	a.homeActivity.Full = true
	a.homeActivity.Review.Full = false
	a.homeActivity.Delivery.Full = true
	a.refreshAgentDeliveries()
}
func (a *App) agentDeliveryEntry(c *ui.Context) {
	a.bindAgentDelivery()
	s := &a.homeActivity.Delivery
	c.After(5 * time.Second)
	if !s.Loading && !s.Retrying && s.Error == "" && (!s.Loaded || time.Since(s.CheckedAt) > 5*time.Second) {
		a.refreshAgentDeliveries()
	}
	a.captureRepositoryDiscovery(c)
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		label := "待发送"
		if s.Loaded && s.Error == "" {
			n := 0
			for _, i := range s.Items {
				if !i.Status.PersistedConfirmed && (i.Assignment == nil || i.Assignment.State != "saved") {
					n++
				}
			}
			label = fmt.Sprintf("待发送 %d", n)
		}
		quietButton(c, label).Key("agent-delivery-entry").OnClick(a.openAgentDelivery)
		if len(s.CaptureDiscoveries) > 0 {
			quietButton(c, "待归属原始记录").Key("capture-unassigned-entry").OnClick(func() { a.openDeliveryAssignment("") })
		}
		if s.Error != "" {
			ui.Text(c, "本机暂存状态未知").FontSize(10).TextColor(design.Muted)
		}
	})
}
func (a *App) deliveryScopeName(i records.AgentDeliveryItem) string {
	for _, scope := range a.scopes {
		if scope.ID == i.Scope {
			if !i.HasGrant {
				return scope.Name + "（授权未核验）"
			}
			return scope.Name
		}
	}
	if i.Scope != "" {
		return "原始指定范围（名称未核验）"
	}
	return "原始指定范围未核验"
}
func (a *App) agentDeliveryFull(c *ui.Context) {
	a.bindAgentDelivery()
	s := &a.homeActivity.Delivery
	if !s.Loaded && !s.Loading && s.Error == "" {
		a.refreshAgentDeliveries()
	}
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		width := float32(316)
		w, _ := c.Size()
		if w < 1000 {
			width = 240
		}
		ui.Scroll(c).Key("agent-delivery-list").TrackScroll(&s.ListScroll).Width(width).Shrink(0).MinHeight(0).Padding(20, 16).Gap(12).BorderWidth(0, 1, 0, 0).BorderColor(design.Border).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "待发送").Bold()
				ui.Spacer(c)
				quietButton(c, "刷新").Disabled(s.Loading || s.Retrying).OnClick(a.refreshAgentDeliveries)
			})
			quietButton(c, "返回最近动态").OnClick(func() {
				s.Full = false
				a.homeActivity.Full = true
				if !a.homeActivity.Loaded {
					a.refreshHomeActivity(false)
				}
			})
			ui.Divider(c)
			if s.Loading {
				ui.Text(c, "正在读取本机暂存").FontSize(design.Small)
			}
			if s.Error != "" {
				ui.Text(c, s.Error).FontSize(design.Small)
			}
			if s.Loaded && !s.Loading && s.Error == "" && len(s.Items) == 0 {
				ui.Text(c, "本机没有待发送记录").FontSize(design.Small)
			}
			for _, item := range s.Items {
				i := item
				label := i.Title
				if label == "" {
					label = "未命名提交"
				}
				ui.Button(c, "").Key("delivery-" + i.Status.ID).Disabled(s.Retrying).Padding(12).BorderWidth(0).Background(func() ui.Color {
					if s.Selected == i.Status.ID {
						return ui.Hex("#edf3f8")
					}
					return ui.Transparent
				}()).Children(func() {
					ui.Column(c).Gap(5).Children(func() {
						ui.Text(c, label).Bold().SingleLine().MinWidth(0)
						ui.Text(c, deliveryLabel(i, s.Retrying && s.Selected == i.Status.ID)).FontSize(design.Small).TextColor(design.Muted)
						kind := "知识"
						if i.Origin == "raw" {
							kind = "原始记录"
						}
						actor := sourceDisplayName(i.Agent)
						if actor == "" {
							actor = "Agent"
						}
						ui.Text(c, actor+" · "+kind+" · "+a.deliveryScopeName(i)).FontSize(10).TextColor(design.Muted).SingleLine().MinWidth(0)
					})
				}).OnClick(func() { a.selectAgentDelivery(i.Status.ID) })
			}
			ui.Text(c, "本机暂存与已保存知识分别记录；不会计入知识数量。").FontSize(10).TextColor(design.Muted)
		})
		ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
			ui.Row(c).Height(44).Shrink(0).Padding(12, 20).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() { ui.Text(c, "传输状态").Bold() })
			ui.Scroll(c).Key("agent-delivery-detail").TrackScroll(&s.DetailScroll).Grow(1).MinHeight(0).Padding(24).Gap(16).Children(func() { a.agentDeliveryDetail(c) })
			ui.Row(c).Shrink(0).Padding(12, 20).Gap(12).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
				if s.Retrying {
					quietButton(c, "取消本次等待").OnClick(func() {
						if s.Cancel != nil {
							s.Cancel()
						}
						s.Info = "已停止本次等待；发送结果可能未知，本机原始提交仍保留。"
					})
				} else if s.Review != nil && s.Error == "" && !s.Loading && !s.DetailLoading && deliveryMayRetry(s.Review.Item) {
					if s.Confirm {
						quietButton(c, "取消").OnClick(func() { s.Confirm = false })
						ui.Button(c, "确认补发").Key("delivery-confirm-retry").OnClick(a.beginAgentDeliveryRetry)
					} else {
						ui.Button(c, "重试补发").Key("delivery-review-retry").OnClick(func() { s.Confirm = true })
					}
				}
				if s.Review != nil && !s.Retrying {
					a.deliveryAssignmentEntry(c, s.Review.Item)
				}
				ui.Spacer(c)
				quietButton(c, "重新读取暂存").Disabled(s.Retrying || s.Selected == "").OnClick(func() { a.selectAgentDelivery(s.Selected) })
			})
		})
	})
}
func (a *App) agentDeliveryDetail(c *ui.Context) {
	s := &a.homeActivity.Delivery
	if s.DetailLoading {
		ui.Text(c, "正在核对原始暂存")
		return
	}
	if s.DetailError != "" {
		ui.Text(c, s.DetailError)
		return
	}
	if s.Review == nil {
		ui.Text(c, "选择一条本机待发送记录").TextColor(design.Muted)
		return
	}
	r := s.Review
	i := r.Item
	ui.Text(c, deliveryLabel(i, s.Retrying)).FontSize(design.Heading).Bold()
	description := "原始提交内容已保留在本机，尚未确认保存。"
	if !i.HasGrant {
		description = "原库身份与授权尚未核验。这条暂存不会直接补发，也不会自动改绑到新库。"
	}
	if i.Status.State == "blocked" {
		description = "原记忆库或当前授权已变化，补发已停止。原内容仍保留，不会改绑范围或另建一条提交。"
	}
	if i.Status.PersistedConfirmed {
		description = fmt.Sprintf("已确认保存 v%d。这里显示原始提交，当前知识版本与索引状态单独读取。", i.Status.SavedRevision)
	}
	ui.Text(c, description).FontSize(design.Small).TextColor(design.Muted)
	if s.Info != "" {
		ui.Text(c, s.Info).FontSize(design.Small)
	}
	if s.Confirm {
		ui.Text(c, "将补发下方同一条原始提交，保留原内容和范围；重试不另建重复知识。服务会重新检查记忆库和连接授权。").FontSize(design.Small).Bold()
	}
	ui.Row(c).Wrap().Gap(8).Children(func() {
		for _, step := range []string{"Agent 提交 · 同一条原始提交", "本机暂存 · 原内容已保留", "发送 Memlane · " + deliveryLabel(i, s.Retrying), "保存与索引 · 分别确认"} {
			ui.Text(c, step).FontSize(10).Padding(8).Background(ui.Hex("#f4f7fa"))
		}
	})
	ui.Text(c, "当前事实").Bold()
	ui.Divider(c)
	version := "尚未确认保存"
	if i.Status.PersistedConfirmed {
		version = fmt.Sprintf("原始保存 v%d；当前版本另行读取", i.Status.SavedRevision)
	}
	meta(c, "版本", version)
	meta(c, "范围", a.deliveryScopeName(i))
	meta(c, "类型", func() string {
		if i.Origin == "raw" {
			return "原始记录"
		}
		return "知识"
	}())
	meta(c, "工作目录", i.Workspace)
	meta(c, "索引", "尚未读取索引状态")
	toggle := "查看投递详情"
	if s.Technical {
		toggle = "收起投递详情"
	}
	quietButton(c, toggle).OnClick(func() { s.Technical = !s.Technical })
	if s.Technical {
		meta(c, "提交标识", i.OperationID)
		meta(c, "范围标识", i.Scope)
		meta(c, "原请求摘要", i.Status.RequestDigest)
		meta(c, "核对摘要", i.Status.ReviewDigest)
		meta(c, "连接", fmt.Sprintf("%s · 代次 %d", i.ConnectionID, i.Generation))
		library := i.LibraryID
		if library == "" {
			library = "未取得可信原库身份"
		}
		meta(c, "原库", library)
		if i.Status.Code != "" {
			meta(c, "最近状态", i.Status.Code)
		}
	}
	if i.UpdatedMS > 0 {
		meta(c, "记录时间", time.UnixMilli(i.UpdatedMS).In(time.Local).Format("2006-01-02 15:04:05"))
	}
	ui.Text(c, "原始提交 · 本机暂存").Bold()
	ui.Text(c, r.Arguments.Text).Selectable().FontSize(design.Body)
	if r.Arguments.Source != nil && r.Arguments.Source.Fragment != "" {
		meta(c, "来源片段", r.Arguments.Source.Fragment)
	}
	if r.Arguments.Source != nil {
		for _, ref := range r.Arguments.Source.References {
			meta(c, "来源引用", ref)
		}
	}
}

func (a *App) retireAgentDelivery() {
	s := &a.homeActivity.Delivery
	if s.Cancel != nil {
		s.Cancel()
	}
	*s = AgentDeliveryUI{Sequence: s.Sequence + 1, DetailSequence: s.DetailSequence + 1, RetrySequence: s.RetrySequence + 1}
}

func confirmedDeliveryRevision(out agentbridge.Result, current records.AgentDeliveryReview, readErr error, id, digest string) int64 {
	if readErr == nil && current.Item.Status.ID == id && current.Item.Status.ReviewDigest == digest && current.Item.Status.PersistedConfirmed && current.Item.Status.SavedRevision > 0 {
		return current.Item.Status.SavedRevision
	}
	if out.Delivery != nil && out.Delivery.ID == id && out.Delivery.ReviewDigest == digest && out.Delivery.PersistedConfirmed && out.Delivery.SavedRevision > 0 {
		return out.Delivery.SavedRevision
	}
	return 0
}
