package desktop

import (
	"context"
	"errors"
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"time"
)

// A single current-detail event page; it never owns navigation or source-reader
// state. Root+memory+current revision bind asynchronous replies to their pane.
type UsageEventsUI struct {
	Root, Memory, Dataset, Channel, Next         string
	Revision, Sequence                           int64
	CoverageStartedMS, RetentionDays, SnapshotMS int64
	Loading, Loaded                              bool
	RetryMore, TargetChanged                     bool
	Error                                        string
	Items                                        []usageEventFact
	Expanded                                     map[string]bool
}
type usageEventFact struct {
	ID, Memory, Dataset, Kind, Channel, Connection, Agent, Exposure string
	Revision, Generation, ObservedMS                                int64
}

func (a *App) bindUsageEvents(v MemoryItem) {
	s := &a.usageEvents
	if s.Root != a.library.Root || s.Memory != v.ID || s.Dataset != v.Dataset || s.Revision != v.Revision {
		sequence := s.Sequence + 1
		*s = UsageEventsUI{Root: a.library.Root, Memory: v.ID, Dataset: v.Dataset, Revision: v.Revision, Sequence: sequence, Channel: "daily", Expanded: map[string]bool{}}
	}
	if !s.Loaded && !s.Loading && s.Error == "" && a.libraryContext != nil {
		a.refreshUsageEvents(false)
	}
}
func (a *App) refreshUsageEvents(more bool) {
	s := &a.usageEvents
	if s.Loading || s.Memory == "" || a.libraryContext == nil {
		return
	}
	if more && s.Next == "" {
		return
	}
	s.Sequence++
	sequence := s.Sequence
	s.Loading = true
	s.Error = ""
	s.TargetChanged = false
	s.RetryMore = more
	query := records.UsageEventsQuery{Memory: s.Memory, Dataset: s.Dataset, ExpectedRevision: s.Revision, Channel: s.Channel, Limit: 20}
	if more {
		query.Cursor = s.Next
	}
	library, ctxBase := a.library, a.libraryContext
	go func() {
		ctx, cancel := context.WithTimeout(ctxBase, 8*time.Second)
		defer cancel()
		page, err := library.UsageEvents(ctx, query)
		if ctxBase.Err() != nil {
			return
		}
		a.dispatch(func() {
			if s.Sequence != sequence || a.library.Root != library.Root || s.Memory != query.Memory || s.Dataset != query.Dataset || s.Revision != query.ExpectedRevision || s.Channel != query.Channel {
				return
			}
			s.Loading = false
			if err != nil {
				s.Error = "使用明细读取未完成；未将失败当作零条。"
				var remote *serviceipc.ServiceError
				if errors.As(err, &remote) && (remote.Code == "invalid_cursor" || remote.Code == "version_changed") {
					s.RetryMore = false
					s.TargetChanged = remote.Code == "version_changed"
					s.Error = "知识或分页快照已变化，请刷新使用明细；先前事实仍保留。"
				}
				return
			}
			if page.Memory != query.Memory || page.Dataset != query.Dataset || page.CurrentRevision != query.ExpectedRevision || page.Channel != query.Channel {
				s.Error = "使用明细目标已变化，请刷新后重试。"
				return
			}
			if more && s.SnapshotMS > 0 && s.SnapshotMS != page.SnapshotMS {
				s.Error = "使用明细分页快照已变化，请刷新后重试。"
				return
			}
			items := []usageEventFact{}
			for _, event := range page.Events {
				if event.Memory != query.Memory || event.Dataset != query.Dataset || event.Channel != query.Channel || event.ID == "" || event.Revision < 1 || ((event.Channel == "daily" || event.Channel == "verification") && (event.ConnectionID == "" || event.Generation < 1)) {
					s.Error = "使用明细回执与当前目标不符，请刷新后重试。"
					return
				}
				items = append(items, usageEventFact{ID: event.ID, Memory: event.Memory, Dataset: event.Dataset, Kind: event.Kind, Channel: event.Channel, Connection: event.ConnectionID, Generation: event.Generation, Agent: event.Agent, Revision: event.Revision, ObservedMS: event.ObservedMS, Exposure: event.Exposure})
			}
			if more {
				s.Items = mergeUsageEventFacts(s.Items, items)
			} else {
				s.Items = items
				s.Expanded = map[string]bool{}
			}
			s.Loaded = true
			s.Next = page.NextCursor
			s.CoverageStartedMS = page.CoverageStartedMS
			s.RetentionDays = int64(page.RetentionDays)
			s.SnapshotMS = page.SnapshotMS
			if !page.HasMore {
				s.Next = ""
			}
		})
	}()
}
func mergeUsageEventFacts(current, next []usageEventFact) []usageEventFact {
	seen := map[string]bool{}
	out := make([]usageEventFact, 0, len(current)+len(next))
	for _, page := range [][]usageEventFact{current, next} {
		for _, event := range page {
			key := fmt.Sprint(event.ID, "/", event.Revision)
			if !seen[key] {
				seen[key] = true
				out = append(out, event)
			}
		}
	}
	return out
}
func usageEventAction(kind string) string {
	switch kind {
	case "return":
		return "检索返回"
	case "read":
		return "成功读取"
	case "adopt":
		return "报告采用"
	case "browse":
		return "App 浏览"
	default:
		return "其他观测事实"
	}
}
func usageEventChannel(channel string) string {
	switch channel {
	case "daily":
		return "日常 Agent"
	case "app":
		return "App"
	case "verification":
		return "连接验证"
	case "background":
		return "后台处理"
	default:
		return "来源未知"
	}
}
func (a *App) usageEventsPanel(c *ui.Context, v MemoryItem) {
	a.bindUsageEvents(v)
	s := &a.usageEvents
	ui.Column(c).Key("usage-events").Gap(8).Padding(10, 0, 0, 0).Children(func() {
		ui.Text(c, "实际观测明细").FontSize(12).Bold()
		ui.Row(c).Gap(5).Children(func() {
			for _, channel := range []string{"daily", "app", "verification"} {
				button := quietButton(c, usageEventChannel(channel)).Key("usage-channel-" + channel)
				if s.Channel == channel {
					button.Disabled(true)
				}
				button.OnClick(func() {
					if s.Channel == channel {
						return
					}
					s.Sequence++
					s.Channel = channel
					s.Items = nil
					s.Next = ""
					s.Loaded = false
					s.Loading = false
					s.Error = ""
					s.Expanded = map[string]bool{}
					a.refreshUsageEvents(false)
				})
			}
		})
		ui.Text(c, "每条事实保留当时的知识版本与连接；不会把旧版本事件改成当前正文。").FontSize(design.Small).TextColor(design.Muted)
		if s.Error != "" {
			ui.Text(c, s.Error).FontSize(design.Small).TextColor(design.Muted)
			quietButton(c, "重试使用明细").Disabled(s.Loading).OnClick(func() { a.refreshUsageEvents(s.RetryMore) })
			if s.TargetChanged {
				quietButton(c, "刷新当前知识").Disabled(s.Loading).OnClick(func() { a.refresh(false) })
			}
		}
		if s.Loaded {
			ui.Text(c, usageEventsCoverageText(*s)).FontSize(design.Small).TextColor(design.Muted)
		}
		if s.Loading {
			ui.Text(c, "正在读取使用明细…").FontSize(design.Small).TextColor(design.Muted)
		}
		if s.Loaded && len(s.Items) == 0 && s.Error == "" && !s.Loading {
			ui.Text(c, usageEventEmptyText(s.Channel, s.CoverageStartedMS)).FontSize(design.Small).TextColor(design.Muted)
		}
		for _, event := range s.Items {
			ui.Column(c).Key("usage-event-"+event.ID+fmt.Sprint(event.Revision)).Gap(5).Padding(8).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
				ui.Text(c, fmt.Sprintf("%s · v%d", usageEventAction(event.Kind), event.Revision)).FontSize(12).Bold()
				date := "时间未知"
				if event.ObservedMS > 0 {
					date = time.UnixMilli(event.ObservedMS).Local().Format("2006-01-02 15:04:05")
				}
				actor := usageEventChannel(event.Channel)
				if event.Agent != "" {
					actor += " · " + sourceDisplayName(event.Agent)
				}
				ui.Text(c, date+" · "+actor).FontSize(design.Small).TextColor(design.Muted)
				expanded := s.Expanded[event.ID]
				label := "查看事实回执"
				if expanded {
					label = "收起事实回执"
				}
				quietButton(c, label).Key("usage-receipt-" + event.ID).OnClick(func() {
					if s.Expanded == nil {
						s.Expanded = map[string]bool{}
					}
					s.Expanded[event.ID] = !s.Expanded[event.ID]
				})
				if expanded {
					for _, line := range usageEventReceiptLines(event) {
						ui.Text(c, line).Selectable().FontSize(design.Small).TextColor(design.Muted)
					}
				}
			})
		}
		if s.Loaded {
			ui.Text(c, fmt.Sprintf("已显示 %d 条实际观测", len(s.Items))).FontSize(design.Small).TextColor(design.Muted)
		}
		ui.Row(c).Gap(8).Children(func() {
			quietButton(c, "刷新使用明细").Disabled(s.Loading).OnClick(func() { a.refreshUsageEvents(false) })
			if s.Next != "" {
				quietButton(c, "更多使用明细").Disabled(s.Loading).OnClick(func() { a.refreshUsageEvents(true) })
			}
		})
	})
}
func usageEventReceiptLines(event usageEventFact) []string {
	lines := []string{"事件：" + event.ID, "知识：" + event.Memory + fmt.Sprintf(" · v%d", event.Revision), "范围：" + event.Dataset, "来源：" + usageEventChannel(event.Channel)}
	if event.Connection != "" {
		lines = append(lines, "连接："+event.Connection+fmt.Sprintf(" · 代次%d", event.Generation))
	}
	if event.Exposure != "" {
		lines = append(lines, "采用依据回执："+event.Exposure)
	}
	return lines
}

func usageEventEmptyText(channel string, coverage int64) string {
	if coverage <= 0 {
		return usageEventChannel(channel) + " · 覆盖起点未知，尚无可确认的明细。"
	}
	return usageEventChannel(channel) + " · 覆盖保留期内暂无观测事件。"
}

func usageEventsCoverageText(s UsageEventsUI) string {
	coverage := "明细覆盖起点未知"
	if s.CoverageStartedMS > 0 {
		coverage = "明细采集自 " + time.UnixMilli(s.CoverageStartedMS).Local().Format("2006-01-02 15:04") + "；此前未知"
	}
	if s.RetentionDays > 0 {
		coverage += fmt.Sprintf(" · 保留%d天", s.RetentionDays)
	}
	if s.SnapshotMS > 0 {
		coverage += " · 快照 " + time.UnixMilli(s.SnapshotMS).Local().Format("15:04:05")
	}
	return coverage
}
