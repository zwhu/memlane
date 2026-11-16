package simpledesktop

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

const (
	knowledgeBatch            = 30
	knowledgeInset    float32 = 48
	knowledgeGap      float32 = 32
	knowledgeBodySize float32 = 16
	knowledgeBodyLine float32 = 24
)

// Only presentation metadata is retained beyond the displayed batch. Bodies
// remain process-local and use the existing shared, four-slot reading scheduler.
type knowledgeFeedState struct {
	Initialized, Detail bool
	Catalog             []Item
	Columns             map[string]int
	Measurements        map[string]knowledgeCardMeasurement
	Copied              string
	CopyUntil           time.Time
	RecallPending       int
	RecallRequest       uint64
	RecallChecked       map[string]bool
	RecallNext          time.Time
}

type knowledgeCardMeasurement struct {
	Body, State   string
	Width, Height float32
}

func (s *knowledgeFeedState) cardHeight(i Item, width float32) float32 {
	if s.Measurements == nil {
		s.Measurements = map[string]knowledgeCardMeasurement{}
	}
	body := knowledgeCardBody(i)
	if m, ok := s.Measurements[i.key()]; ok && m.Body == body && m.State == i.ReadingState && m.Width == width {
		return m.Height
	}
	height := float32(206) // unavailable text and its explicit retry control
	if i.ReadingState != "failed" && i.ReadingState != "unavailable" {
		// Shape with the same font, exact content width, 24-DIP line box
		// and 12-line clamp used by the rendered card, not a font-height ratio.
		text := ui.ShapeText(body, ui.Font{Family: "system-ui", Size: knowledgeBodySize, Weight: 400, LineHeight: knowledgeBodyLine / knowledgeBodySize}, max(float32(1), width-82))
		height = 152 + float32(min(12, max(1, len(text.Lines()))))*knowledgeBodyLine
	}
	s.Measurements[i.key()] = knowledgeCardMeasurement{Body: body, State: i.ReadingState, Width: width, Height: height}
	return height
}

var knowledgeQuote = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor"><path d="M3 13c0-6 3-9 8-10v4c-3 1-4 3-4 5h4v9H3zm12 0c0-6 3-9 8-10v4c-3 1-4 3-4 5h4v9h-8z"/></svg>`))
var knowledgeSource = homeSVG(`<path d="m10 13 4-4M8 16l-2 2a4 4 0 0 1-6-6l5-5a4 4 0 0 1 6 0M16 8l2-2a4 4 0 0 1 6 6l-5 5a4 4 0 0 1-6 0" transform="translate(1 0) scale(.9)"/>`)

func knowledgeCatalog(docs []Document) []Item {
	rows := []Item{}
	seen := map[string]bool{}
	for _, d := range docs {
		if !d.knowledge() {
			continue
		}
		copy := d
		i := Item{ID: d.ID, Dataset: d.Dataset, Kind: "知识", Date: d.date(), Source: d.session(), Document: &copy}
		if !seen[i.key()] {
			rows = append(rows, i)
			seen[i.key()] = true
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		x, y := dateOrder(rows[i].Date), dateOrder(rows[j].Date)
		if x.Equal(y) {
			return rows[i].key() < rows[j].key()
		}
		return x.After(y)
	})
	return rows
}

func (a *App) loadKnowledgeFeed(s *pane) {
	a.syncReadingIdentity()
	p, ok := a.current()
	if !ok || a.Update == nil {
		return
	}
	a.cancelTitles(s)
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	s.cancel = cancel
	s.request++
	request, epoch := s.request, a.epoch
	s.Knowledge.Initialized, s.Loading, s.Error = true, true, ""
	s.Knowledge.RecallRequest++
	s.Knowledge.RecallPending = 0
	s.Knowledge.RecallChecked = nil
	client := a.Client
	go func() {
		defer cancel()
		var docs []Document
		var err error
		if p.Dataset == "" {
			err = fmt.Errorf("项目正在登记，内容归属尚未确认")
		} else {
			docs, err = client.overviewDocuments(ctx, p)
		}
		a.Update(func() {
			if epoch != a.epoch || request != s.request || !a.syncReadingIdentity() {
				return
			}
			s.Loading = false
			if err != nil {
				s.Error = err.Error()
				if readingUnavailable(err) {
					a.InvalidateReading(p.Dataset)
					s.Knowledge.Catalog = nil
				}
				return
			}
			rows := knowledgeCatalog(docs)
			// Retain stable order while reading below the top; new entries join
			// the tail rather than shifting every existing card under the pointer.
			if s.ListScroll.Y > 0 {
				byKey := map[string]Item{}
				for _, i := range rows {
					byKey[i.key()] = i
				}
				ordered := make([]Item, 0, len(rows))
				for _, old := range s.Knowledge.Catalog {
					if i, ok := byKey[old.key()]; ok {
						ordered = append(ordered, i)
						delete(byKey, old.key())
					}
				}
				for _, i := range rows {
					if _, ok := byKey[i.key()]; ok {
						ordered = append(ordered, i)
					}
				}
				rows = ordered
			}
			old := map[string]Item{}
			for _, i := range s.Items {
				old[i.key()] = i
			}
			count := min(max(knowledgeBatch, len(s.Items)), len(rows))
			s.Knowledge.Catalog = rows
			s.Items = append([]Item(nil), rows[:count]...)
			for n, i := range s.Items {
				if before, ok := old[i.key()]; ok && before.BodyLoaded && keyForReading("", i, "") == keyForReading("", before, "") && i.Document != nil && (i.Document.Updated != "" || i.Document.ContentHash != "") {
					s.Items[n].Body, s.Items[n].BodyLoaded, s.Items[n].ReadingState = before.Body, true, "ready"
				}
			}
			s.More, s.Loaded = count < len(rows), true
			a.hydrateKnowledgeCards(s)
		})
	}()
}

func (a *App) appendKnowledgeBatch(s *pane) {
	if s.Loading || s.Error != "" || !s.More {
		return
	}
	end := min(len(s.Items)+knowledgeBatch, len(s.Knowledge.Catalog))
	if end <= len(s.Items) {
		s.More = false
		return
	}
	s.Items = append(s.Items, s.Knowledge.Catalog[len(s.Items):end]...)
	s.More = end < len(s.Knowledge.Catalog)
	a.hydrateKnowledgeCards(s)
}

func (a *App) hydrateKnowledgeCards(s *pane) {
	if a.Update == nil {
		return
	}
	if s.titleCtx == nil || s.titleCtx.Err() != nil {
		s.titleCtx, s.titleCancel = context.WithCancel(context.Background())
	}
	ctx, epoch, request, generation := s.titleCtx, a.epoch, s.titleRequest, a.readingGeneration(s)
	client := a.Client
	for n, row := range s.Items {
		if row.BodyLoaded || row.ReadingState == "pending" || row.ReadingState == "failed" || row.ReadingState == "unavailable" {
			continue
		}
		i, revision := row, a.readingRevisions[row.key()]
		s.Items[n].ReadingState = "pending"
		go func() {
			result, err := a.titles.read(ctx, client, i, generation, false, true)
			a.Update(func() {
				if ctx.Err() != nil || epoch != a.epoch || request != s.titleRequest || revision != a.readingRevisions[i.key()] || !a.syncReadingIdentity() {
					return
				}
				if err != nil {
					a.handleReadingFailure(i, err)
				}
				for n, row := range s.Items {
					if row.key() == i.key() {
						if err != nil {
							s.Items[n].ReadingState = "failed"
							if readingUnavailable(err) {
								s.Items[n].ReadingState = "unavailable"
							}
							return
						}
						s.Items[n].Body, s.Items[n].BodyLoaded, s.Items[n].ReadingState = result.Body, true, "ready"
						return
					}
				}
			})
		}()
	}
}

func knowledgeCardBody(i Item) string {
	if !i.BodyLoaded {
		return ""
	}
	return strings.TrimSpace(itemPresentation(i).VisibleBody)
}

func (a *App) knowledgePage(c *ui.Context, s *pane) {
	a.projectNavigation(c)
	if !s.Knowledge.Initialized && !s.Knowledge.Detail {
		a.loadKnowledgeFeed(s)
	}
	// Entry after a tab/source return cancels old hydration. Restart only
	// genuinely unread cards, not failed requests on every frame.
	if s.titleCtx != nil && s.titleCtx.Err() != nil {
		for n := range s.Items {
			if s.Items[n].ReadingState == "pending" {
				s.Items[n].ReadingState = ""
			}
		}
		a.hydrateKnowledgeCards(s)
	}
	a.loadKnowledgeCardRecalls(s)
	if s.Knowledge.Columns == nil {
		s.Knowledge.Columns = map[string]int{}
	}
	width, _ := c.Size()
	cardWidth := max(float32(200), (width-homeSidebarWidth-2*knowledgeInset-2*knowledgeGap)/3)
	columns := [3][]Item{}
	heights := [3]float32{}
	ready := true
	for _, i := range s.Items {
		if !i.BodyLoaded && i.ReadingState != "failed" && i.ReadingState != "unavailable" {
			ready = false
		}
	}
	for _, i := range s.Items {
		// Do not permanently assign columns using loading-placeholder heights.
		// Existing cards stay mounted while the next batch finishes reading.
		if _, placed := s.Knowledge.Columns[i.key()]; !placed && !ready {
			continue
		}
		height := s.Knowledge.cardHeight(i, cardWidth)
		col, exists := s.Knowledge.Columns[i.key()]
		if !exists {
			col = 0
			for n := 1; n < 3; n++ {
				if heights[n] < heights[col] {
					col = n
				}
			}
			s.Knowledge.Columns[i.key()] = col
		}
		columns[col] = append(columns[col], i)
		heights[col] += height + knowledgeGap
	}
	// Wait for hydration before appending, so small loading placeholders
	// cannot make us consume every batch without a real scroll boundary.
	if !s.Knowledge.Detail && s.More && s.Loaded && ready && s.ListScroll.MaxY > 0 && s.ListScroll.Y >= s.ListScroll.MaxY-160 {
		a.appendKnowledgeBatch(s)
	}
	ui.Scroll(c.Key("knowledge-feed-"+a.routeKey())).Label("记忆瀑布流").Grow(1).MinHeight(0).TrackScroll(&s.ListScroll).Padding(32, knowledgeInset, 48, knowledgeInset).Gap(32).Children(func() {
		if s.Error != "" {
			homeText(c, "读取未完成 · "+s.Error, homeMeta)
			projectLink(c, "重试读取记忆", func() { a.loadKnowledgeFeed(s) })
		}
		if !s.Loaded {
			homeText(c, "正在读取记忆…", homeMeta)
			return
		}
		if len(s.Items) == 0 {
			homeText(c, "此项目暂无记忆", homeBody)
			return
		}
		ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(knowledgeGap).Children(func() {
			for _, items := range columns {
				ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(knowledgeGap).Children(func() {
					for _, i := range items {
						a.knowledgeCard(c, s, i)
					}
				})
			}
		})
		if !ready {
			homeText(c, "正在读取记忆…", homeMeta)
		}
	})
}

func (a *App) knowledgeCard(c *ui.Context, s *pane, i Item) {
	card := ui.Column(c.Key("knowledge-card-"+i.key())).Label("记忆卡片").FillWidth().MinWidth(0).Padding(32, 40).Gap(16).Radius(24).Border(1, homeBorder).Background(ui.Transparent)
	card.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Icon(c, knowledgeQuote).FontSize(24).TextColor(ui.Hex("#a5b2bf"))
			ui.Spacer(c)
			if a.itemIsUnread(i) {
				homeText(c, "未读", homeMeta)
			}
		})
		body := knowledgeCardBody(i)
		if i.ReadingState == "failed" || i.ReadingState == "unavailable" {
			homeText(c, "记忆暂不可读", homeMeta)
			projectLink(c, "重试读取正文", func() {
				for n, row := range s.Items {
					if row.key() == i.key() {
						s.Items[n].ReadingState = ""
					}
				}
				a.hydrateKnowledgeCards(s)
			})
		} else if !i.BodyLoaded {
			homeText(c, "正在读取…", homeMeta).MinHeight(48)
		} else {
			ui.ButtonBase(c).Label("阅读记忆：" + body).Tooltip("阅读完整记忆").FillWidth().MinWidth(0).Padding(0).Background(ui.Transparent).BorderWidth(0).Cursor(ui.CursorPointer).Children(func() {
				ui.Text(c, body).Font("system-ui").FontSize(knowledgeBodySize).FontWeight(400).FixedLineHeight(knowledgeBodyLine).TextColor(homeInk).FillWidth().MinWidth(0).MaxLines(12)
			}).OnClick(func() { s.Knowledge.Detail = true; a.selectItem(s, i) })
		}
		ui.Row(c).FillWidth().MinWidth(0).Margin(8, 0, 0, 0).AlignItems(ui.Center).Gap(8).Children(func() {
			date := i.Date
			if i.Document != nil {
				if day, ok := i.Document.knowledgeDay(time.Local); ok {
					date = day
				} else {
					date = ""
				}
			}
			if date == "" {
				date = "来源日期未知"
			} else if t, ok := officialTime(date); ok {
				date = t.In(time.Local).Format("2006-01-02")
			}
			homeText(c, date, homeMeta).MinWidth(0).SingleLine()
			ui.Spacer(c)
			if s.Knowledge.Copied == i.key() && time.Now().Before(s.Knowledge.CopyUntil) {
				homeText(c, "已复制", homeMeta)
			} else {
				label, tip := knowledgeCardRecall(i)
				homeText(c, label, homeMeta).SingleLine().Tooltip(tip)
			}
			hoverCopyAction(c, card, "复制记忆", func() {
				c.WriteClipboard(body)
				s.Knowledge.Copied = i.key()
				s.Knowledge.CopyUntil = time.Now().Add(3 * time.Second)
			}).Disabled(!i.BodyLoaded || i.ReadingState == "unavailable")
			smallIconAction(c, "查看来源", knowledgeSource, func() { s.Selected = &i; a.source(s) }).Disabled(!i.BodyLoaded || (i.Source == "" && i.SourceDocument == "") || s.Reading)
		})
	})
}

// The SDK modal owns focus trapping, Escape, outside dismissal and restoration.
// Keep the feed mounted below it so closing never reconstructs its scroll state.
func (a *App) knowledgeDialog(c *ui.Context) {
	if a.page != "知识" || a.kind != "知识" {
		return
	}
	s := a.state()
	if !s.Knowledge.Detail {
		return
	}
	w, h := c.Size()
	ui.Modal(c.Key("knowledge-detail-modal"), &s.Knowledge.Detail, func() {
		ui.Row(c).FillWidth().Height(56).Shrink(0).Padding(0, 32).AlignItems(ui.Center).Children(func() {
			homeText(c, "记忆详情", homeHeading).FontSize(16)
			ui.Spacer(c)
			smallIconAction(c, "关闭记忆详情", readingClose, func() { a.closeKnowledgeDetail(s) })
		})
		a.projectDetail(c, s)
	}).Width(min(float32(880), w-96)).Height(min(float32(720), h-96)).Padding(0).Gap(0).Radius(16).Label("记忆详情弹框").Transition(projectOverlayMotion())
	if !s.Knowledge.Detail {
		a.closeKnowledgeDetail(s)
	}
}

func (a *App) closeKnowledgeDetail(s *pane) {
	s.Knowledge.Detail = false
	a.projectUI.InfoOpen = false
	s.History = nil
	if s.HomeAnchor != nil {
		s.HomeAnchor = nil
		a.loadKnowledgeFeed(s)
	}
}
