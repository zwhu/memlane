package ui

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	mygoui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

func TestThemesAndTypography(t *testing.T) {
	for _, dark := range []bool{false, true} {
		tok := TokensFor(dark)
		if tok.Dark != dark || tok.Heat[0] != tok.Fill2 {
			t.Fatalf("incorrect palette for dark=%v: %+v", dark, tok)
		}
		home, sidebar, selected, list := HomeTheme(tok), SidebarTheme(tok), SidebarSelectedTheme(tok), ListTheme(tok)
		if home.Background != tok.Bg || home.Text != tok.Text1 || home.FontSize != FSBody ||
			sidebar.Accent != tok.Text2 || selected.Accent != tok.Accent || list.Accent != tok.AccentSoft {
			t.Fatalf("incorrect theme colors for dark=%v", dark)
		}
		if md := MarkdownOptions(tok, 0, nil); md.Theme.FontSize != FSReading || md.Theme.Text != tok.Text1 ||
			md.Theme.CodeBlockBackground != tok.Fill1 || md.Theme.LineHeight != 1.8 {
			t.Fatalf("incorrect Markdown theme for dark=%v", dark)
		}
		roles := BuildTypography(tok)
		if roles[RoleHeading].Size != FSHeading || roles[RoleMeta].Color != tok.Text3 || roles[RoleAction].Color != tok.Accent {
			t.Fatalf("incorrect typography for dark=%v: %+v", dark, roles)
		}
		tt := mygoui.NewTester(func(c *mygoui.Context) { Text(c, tok, "Heading", RoleHeading) }, 160, 50)
		if !tt.HasText("Heading") {
			t.Fatal("typography helper did not render text")
		}
	}
	light := LightTokens()
	light.Accent = mygoui.Transparent
	if LightTokens().Accent == light.Accent || DarkTokens().Accent == LightTokens().Accent {
		t.Fatal("palette factories shared mutable colors")
	}
}

func TestPoseTrackerArmsLaunchesAndHonorsReduceMotion(t *testing.T) {
	var tracker PoseTracker
	var run uint64
	tt := mygoui.NewTester(func(c *mygoui.Context) {
		mygoui.Column(c).Children(func() {
			tracker.PageEnter(c, mygoui.Box(c.Key(fmt.Sprintf("page:%d", run))).Size(30, 12), "page", run)
			tracker.StaggerEnter(c, mygoui.Box(c.Key(fmt.Sprintf("row:%d", run))).Size(30, 12), "row", 2, run, true)
			tracker.StaggerEnter(c, mygoui.Box(c.Key(fmt.Sprintf("outside:%d", run))).Size(30, 12), "outside", 8, run, false)
			tracker.ContentFade(c, mygoui.Box(c.Key(fmt.Sprintf("fade:%d", run))).Size(30, 12), "content", run)
		})
	}, 160, 100)

	if len(tracker.armed) != 0 || tt.FramePending() {
		t.Fatal("run zero armed an entrance")
	}
	run = 1
	tt.Frame()
	for _, key := range []string{"page:page", "row:row", "fade:content"} {
		if tracker.armed[key] != 1 || tracker.launched[key] != 1 {
			t.Fatalf("entrance %q did not arm and launch", key)
		}
	}
	if _, ok := tracker.armed["row:outside"]; ok {
		t.Fatal("staggered entrance armed outside the animated range")
	}
	tt.Advance(time.Second)
	if tt.FramePending() {
		t.Fatal("entrance requested frames after settling")
	}
	tt.SetPreferences(mygoui.Preferences{ReduceMotion: true})
	run = 2
	tt.Frame()
	if tracker.armed["page:page"] != 1 || tracker.launched["fade:content"] != 1 || tt.FramePending() {
		t.Fatal("reduced motion armed another entrance")
	}
}

func TestAnimatedWidgetsAndHeatmapWave(t *testing.T) {
	tok := DarkTokens()
	widgets := map[string]func(*mygoui.Context){
		"spinner":  func(c *mygoui.Context) { Spinner(c, tok) },
		"skeleton": func(c *mygoui.Context) { SkeletonBar(c, tok, 120, 12, 3) },
		"stage":    func(c *mygoui.Context) { StageBlocks(c, tok) },
		"empty":    func(c *mygoui.Context) { EmptyState(c, tok, "empty", "Empty", "Hint") },
	}
	for name, widget := range widgets {
		t.Run(name, func(t *testing.T) {
			tt := mygoui.NewTester(widget, 400, 220)
			if !tt.FramePending() {
				t.Fatal("visible animation did not request a frame")
			}
			tt.SetPreferences(mygoui.Preferences{ReduceMotion: true})
			before := tt.Image()
			tt.Advance(150 * time.Millisecond)
			if tt.FramePending() || !bytes.Equal(before.Pix, tt.Image().Pix) {
				t.Fatal("reduced motion did not stabilize the widget")
			}
		})
	}

	var start time.Time
	var elapsed float64
	var playing bool
	const duration = 500 * time.Millisecond
	tt := mygoui.NewTester(func(c *mygoui.Context) {
		mygoui.Box(c).Size(20, 20).Draw(func(p *mygoui.Painter, r mygoui.Rect) {
			elapsed, playing = HeatmapWaveElapsed(c, p, start, duration)
		})
	}, 80, 80)
	if playing || elapsed != duration.Seconds() {
		t.Fatal("zero start time should show the settled wave")
	}
	start = tt.Now()
	tt.Frame()
	tt.Advance(100 * time.Millisecond)
	if !playing || math.Abs(elapsed-0.1) > 1e-6 || !tt.FramePending() {
		t.Fatalf("wave did not advance: playing=%v elapsed=%v", playing, elapsed)
	}
	tt.SetPreferences(mygoui.Preferences{ReduceMotion: true})
	if playing || elapsed != duration.Seconds() || tt.FramePending() {
		t.Fatal("reduced motion did not settle the wave")
	}
	if BouncyScale(0) != 0 || BouncyScale(1) != 1 || BouncyScale(0.21) <= 0 || SmoothPoseProgress(1) != 1 {
		t.Fatal("spring curves lost their endpoints or motion")
	}
}

func TestButtonsTabsAndSelect(t *testing.T) {
	tok := LightTokens()
	var on bool
	var tab, segment, clicks, switches, tabChanges, segmentChanges int
	value := "one"
	loading := true
	tt := mygoui.NewTester(func(c *mygoui.Context) {
		mygoui.Column(c).AlignItems(mygoui.Start).Gap(4).Children(func() {
			click := func() { clicks++ }
			Switch(c, tok, "toggle", &on, "Enable", func() { switches++ })
			Select(c, tok, "picker", "Choose", value, []SelectOption{{"one", "First option"}, {"two", "Second option"}},
				false, false, func(next string) { value = next })
			OutlineButton(c, tok, "Outline action", IconPlus, click)
			PrimaryButton(c, tok, "Primary action", click)
			SmallButton(c, tok, "Small action", nil, click)
			DangerButton(c, tok, "Danger action", click)
			GhostButton(c, tok, "Ghost action", nil, click)
			TextButton(c, tok, "Text action ›", click)
			UnderlineTabs(c, tok, "tabs", "Tabs", &tab, []string{"First tab", "Second tab"}, func() { tabChanges++ })
			Segmented(c, tok, "segments", "Segments", &segment, []string{"First segment", "Second segment"}, func() { segmentChanges++ })
			CountBadge(c, tok, FormatBadgeCount(120), true)
			RefreshActionLoading(c, tok, "Refresh", loading, click)
		})
	}, 600, 650)
	tt.SetPreferences(mygoui.Preferences{ReduceMotion: true})
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	for _, label := range []string{"Outline action", "Primary action", "Small action", "Danger action", "Ghost action", "Text action ›"} {
		click(label)
	}
	click("Refresh")
	if clicks != 6 {
		t.Fatalf("button or loading refresh activation changed: clicks=%d", clicks)
	}
	loading = false
	tt.Frame()
	click("Refresh")
	click("Enable")
	click("Second tab")
	click("Second segment")
	click("Choose")
	click("Second option")
	if clicks != 7 || !on || switches != 1 || tab != 1 || segment != 1 || tabChanges != 1 || segmentChanges != 1 || value != "two" {
		t.Fatalf("incorrect control state: clicks=%d on=%v switches=%d tab=%d/%d segment=%d/%d value=%q",
			clicks, on, switches, tab, tabChanges, segment, segmentChanges, value)
	}
	if !tt.HasText("99+") {
		t.Fatal("count badge did not render the capped count")
	}
}

func TestRecallChipAndHoverCopy(t *testing.T) {
	tok := DarkTokens()
	count := 0
	row := func(c *mygoui.Context) {
		mygoui.Column(c).AlignItems(mygoui.Start).Children(func() {
			RecallChip(c, tok, "Recalls", "Recall count", count)
			line := mygoui.Row(c)
			line.Children(func() {
				mygoui.Text(c, "Original")
				HoverCopyAction(c, tok, line, IconCopy24, "Copy", func() { c.WriteClipboard("original value") })
			})
		})
	}
	tt := mygoui.NewTester(row, 300, 120)
	plain, ok := tt.Find("Recalls")
	if !ok {
		t.Fatal("zero-count recall label missing")
	}
	count = 3
	tt.Frame()
	pill, ok := tt.Find("Recalls")
	if !ok || pill.X <= plain.X {
		t.Fatalf("positive count did not add the leading icon and inset: plain=%v pill=%v", plain, pill)
	}

	text, _ := tt.Find("Original")
	copyButton, ok := tt.Find("Copy")
	if !ok || copyButton.W != 22 || copyButton.H != 22 {
		t.Fatalf("copy button lost its hit target: %v", copyButton)
	}
	hidden := tt.Image()
	tt.Move(text.X+2, text.Y+2)
	tt.Frame()
	if shown, _ := tt.Find("Original"); shown != text || bytes.Equal(hidden.Pix, tt.Image().Pix) {
		t.Fatal("hover did not reveal the copy icon at fixed geometry")
	}
	if err := tt.Click("Copy"); err != nil || tt.Clipboard() != "original value" {
		t.Fatalf("copy action failed: %v, clipboard=%q", err, tt.Clipboard())
	}
}

func TestDialogAndGroupedRow(t *testing.T) {
	tok := DarkTokens()
	open, danger := true, false
	confirmations, clicks := 0, 0
	tt := mygoui.NewTester(func(c *mygoui.Context) {
		Dialog(c, tok, &open, DialogOptions{
			Key: "confirm", Title: "Confirm changes", ConfirmText: "Apply",
			Danger: danger, HideCancel: danger, OnConfirm: func() { confirmations++; open = false },
			Body: func() {
				Group(c, tok, "Settings", "One row", nil, func() {
					Row(c, tok, RowOptions{Title: "Editable", Desc: "Description", Clickable: true, OnClick: func() { clicks++ }})
				})
			},
		})
	}, 800, 600)
	for _, text := range []string{"Confirm changes", "Settings", "One row", "Description"} {
		if !tt.HasText(text) {
			t.Fatalf("dialog or group missing %q", text)
		}
	}
	if err := tt.Click("Editable"); err != nil || clicks != 1 {
		t.Fatalf("group row did not activate: %v, clicks=%d", err, clicks)
	}
	if err := tt.Click("Apply"); err != nil || confirmations != 1 || open {
		t.Fatalf("normal confirm failed: %v, confirmations=%d open=%v", err, confirmations, open)
	}
	open, danger = true, true
	tt.Frame()
	tt.Key(0, mygoui.KeyEnter)
	if confirmations != 1 || !open {
		t.Fatal("Enter activated danger confirm")
	}
	if err := tt.Click("Apply"); err != nil || confirmations != 2 || open {
		t.Fatalf("explicit danger confirm failed: %v, confirmations=%d open=%v", err, confirmations, open)
	}
}

func TestInlineMarkdownUsesExplicitAppearance(t *testing.T) {
	const raw = "# Intro\n> `snake_case` [link](https://example.org) ~~old~~"
	preview := PreviewMarkdownSource(raw)
	if preview != "Intro `snake_case` [link](https://example.org) ~~old~~" {
		t.Fatalf("unexpected preview: %q", preview)
	}
	for _, tok := range []*Tokens{LightTokens(), DarkTokens()} {
		spans := ParseInlineMarkdown(preview, FSBody, tok.Text1, tok.Fill2, tok.Accent)
		if len(spans) != 6 || spans[1].Text != "snake_case" || spans[1].Color != tok.Text1 || spans[1].Background != tok.Fill2 ||
			spans[3].Text != "link" || spans[3].Color != tok.Accent || !spans[5].Strikethrough {
			t.Fatalf("incorrect spans with dark=%v: %+v", tok.Dark, spans)
		}
		tt := mygoui.NewTester(func(c *mygoui.Context) {
			InlineMarkdown(c, tok, raw, FSBody, 400, 1.4, tok.Text1)
		}, 480, 60)
		if !tt.HasText("snake_case") || !tt.HasText("link") {
			t.Fatalf("inline preview did not render with dark=%v: %v", tok.Dark, tt.Texts())
		}
	}
}

func TestChunkCacheBoundsAndInvalidation(t *testing.T) {
	cache := NewChunkCacheWithLimits(4, 10)
	loads := 0
	load := func(dataset, id, text string) []string {
		return cache.GetOrSplit(dataset, id, "body", MarkdownContentKey(text), func() []string {
			loads++
			return []string{text}
		})
	}
	load("a", "old", "1111")
	load("a", "recent", "2222")
	load("a", "old", "1111")
	load("b", "new", "3333")
	if loads != 3 || cache.Clock() != 4 || cache.Len() != 2 || cache.TotalBytes() != 8 {
		t.Fatalf("unexpected cache accounting: loads=%d clock=%d len=%d bytes=%d", loads, cache.Clock(), cache.Len(), cache.TotalBytes())
	}
	load("a", "old", "1111")
	if loads != 3 {
		t.Fatal("LRU eviction dropped the recently reused entry")
	}
	load("a", "recent", "2222")
	if loads != 4 {
		t.Fatal("stale entry survived eviction")
	}
	if got := load("a", "old", "too-large-for-cache"); got[0] != "too-large-for-cache" || cache.TotalBytes() > cache.MaxBytes() {
		t.Fatalf("oversize entry did not bypass the cache: %v", got)
	}
	load("b", "new", "3333")
	cache.InvalidateObject("a", "recent")
	if cache.HasDataset("a") || !cache.HasDataset("b") {
		t.Fatal("object invalidation affected the wrong cache entries")
	}
	cache.Invalidate("b")
	if cache.Len() != 0 || cache.TotalBytes() != 0 {
		t.Fatal("dataset invalidation left cached bytes")
	}
	if PartsContentKey([]byte("ab"), []byte("c")) == PartsContentKey([]byte("a"), []byte("bc")) {
		t.Fatal("content key lost part boundaries")
	}
}

func TestVirtualizedMarkdownAndTrace(t *testing.T) {
	const table = "| Name | Value |\n| --- | --- |\n| a | b |"
	src := "```go\n" + strings.Repeat("var sample = 1\n", 80) + "```\n\n" + table + "\n\n" + strings.Repeat("界", 3000)
	chunks := SplitMarkdownChunks(src)
	if len(chunks) < 4 || !IsFenceSplitBoundary(chunks[0], chunks[1]) {
		t.Fatalf("fenced content was not split into renderable chunks: %d chunks", len(chunks))
	}
	foundTable := false
	for _, chunk := range chunks {
		if !utf8.ValidString(chunk) || strings.Count(chunk, "```")%2 != 0 {
			t.Fatal("chunk has broken UTF-8 or unbalanced fences")
		}
		foundTable = foundTable || strings.Contains(chunk, table)
	}
	if !foundTable {
		t.Fatal("Markdown table was split across chunks")
	}
	cache := NewChunkCache()
	trace := cache.GetOrSplitJSONTrace("docs", "trace", "body", []byte(`{"input":1}`), []byte(`{"output":2}`), "trace error")
	if len(trace) != 1 || !strings.Contains(trace[0], "```json\n{\"output\":2}\n```\n\ntrace error") {
		t.Fatalf("unexpected trace chunks: %q", trace)
	}
	if traceChunks := SplitJSONTraceChunks([]byte(strings.Repeat("界", 3000)), nil, "tail"); len(traceChunks) < 3 || traceChunks[len(traceChunks)-1] != "tail" {
		t.Fatalf("long trace lost its chunked payload or error: %d chunks", len(traceChunks))
	}

	var lists map[string]*mygoui.ListState
	var scrolls map[string]*mygoui.ScrollState
	list, scroll := ListStateFor(&lists, "doc"), ScrollStateFor(&scrolls, "doc")
	if list != ListStateFor(&lists, "doc") || scroll != ScrollStateFor(&scrolls, "doc") {
		t.Fatal("scroll/list state was recreated for the same key")
	}
	opts := VirtualizedMarkdownOptions{
		Cache: cache, Dataset: "docs", ID: "doc", Slot: "body", MaxWidth: 500,
		Markdown: MarkdownOptions(DarkTokens(), 15, func(c *mygoui.Context, block *markdown.CodeBlockView) {
			mygoui.Text(c, "Injected code renderer")
		}),
	}
	tt := mygoui.NewTester(func(c *mygoui.Context) {
		RenderVirtualizedMarkdownList(c, "doc", list, scroll, src, opts)
	}, 650, 480)
	if !tt.HasText("Injected code renderer") || cache.Len() != 2 || !cache.HasDataset("docs") {
		t.Fatalf("injected cache or Markdown options were not used: %v, cache len=%d", tt.Texts(), cache.Len())
	}
	clock := cache.Clock()
	tt.Frame()
	if cache.Clock() <= clock || cache.Len() != 2 {
		t.Fatal("rendering another frame did not reuse the cached chunks")
	}
}
