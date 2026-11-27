package app

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestCaptureVisualCompareScreenshots renders 1200x820 @2x PNG snapshots across
// key views in both light and dark appearances when MEMLANE_CAPTURE_UI_DIR is set.
// Normal `go test` runs skip this immediately with zero overhead.
func TestCaptureVisualCompareScreenshots(t *testing.T) {
	outDir := os.Getenv("MEMLANE_CAPTURE_UI_DIR")
	if outDir == "" {
		t.Skip("set MEMLANE_CAPTURE_UI_DIR to capture visual comparison screenshots")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", outDir, err)
	}

	save := func(name string, tt *ui.Tester, dark bool) {
		t.Helper()
		tt.SetScale(2)
		tt.SetDark(dark)
		tt.SetPreferences(ui.Preferences{ReduceMotion: true})
		tt.Advance(time.Second)
		tt.Frame()
		tt.Advance(time.Second)
		tt.Frame()
		img := tt.Image()
		suffix := "light"
		if dark {
			suffix = "dark"
		}
		path := filepath.Join(outDir, name+"-"+suffix+".png")
		f, err := os.Create(path)
		if err != nil {
			t.Fatalf("create %s: %v", path, err)
		}
		if err := png.Encode(f, img); err != nil {
			_ = f.Close()
			t.Fatalf("encode %s: %v", path, err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("close %s: %v", path, err)
		}
	}

	for _, dark := range []bool{false, true} {
		// 1. Overview (populated with memories, heatmap dates, and activity)
		ov := overviewSelectedAFixture(t)
		ov.overviewState.Loaded = true
		ov.overviewState.Loading = false
		ov.overviewState.RawLoaded = true
		ov.overviewState.ActivityLoaded = true
		ov.overviewState.ScopeKey = ov.overviewScope()
		ov.overviewState.Snapshot.Raw = 42
		// Add realistic historical memory dates so the heatmap shows multi-level activity
		// while keeping overviewSelectedAFixture's rich recent titles at the top.
		now := time.Now()
		for _, spec := range []struct {
			daysAgo int
			count   int
		}{
			{2, 2}, {4, 4}, {7, 2}, {9, 3}, {12, 1},
			{15, 5}, {18, 2}, {21, 3}, {26, 1}, {33, 4}, {41, 2}, {55, 3},
			{68, 2}, {82, 4}, {97, 1}, {114, 3}, {132, 2}, {158, 5}, {180, 2},
		} {
			at := now.AddDate(0, 0, -spec.daysAgo)
			for n := 0; n < spec.count; n++ {
				id := "hm-" + at.Format("20060102") + "-" + string(rune('a'+n))
				d := Document{ID: id, Dataset: "0", Name: "历史记忆 " + at.Format("01-02"), Created: at.Format(time.RFC3339)}
				ov.overviewState.Snapshot.Knowledge = append(ov.overviewState.Snapshot.Knowledge, Item{
					ID: d.ID, Dataset: d.Dataset, Kind: "知识", Title: d.Name, Date: d.Created, Document: &d,
				})
			}
		}
		ov.SetAppearancePreferenceForTest(dark)
		ttOv := ui.NewTester(ov.View, 1200, 820)
		save("overview", ttOv, dark)

		// 2. Project 3-column reading (populated list + markdown reader)
		pr := projectUIFixture()
		pr.SetAppearancePreferenceForTest(dark)
		pr.page = "知识"
		pr.state().Items = append(pr.state().Items,
			Item{
				ID: "k2", Dataset: "ds", Kind: "知识",
				Title:      "Hook 接入统一走 Go 子命令 `memlane hook`，长尾宿主走标准 `/mcp`",
				Date:       "2026-11-26T10:12:00Z",
				BodyLoaded: true,
				Body:       "宿主侧彻底与 Cognee 官方插件解耦：核心宿主走 `memlane hook`，`pi` 保留极薄无状态扩展，长尾宿主走 `/mcp`。",
				Document:   &Document{ID: "k2", Dataset: "ds", Name: "方案 C 宿主接入", Created: "2026-11-26T10:12:00Z"},
			},
			Item{
				ID: "k3", Dataset: "ds", Kind: "知识",
				Title:      "严格禁止清空 `build/` 目录，清理前先核对进程与配置依赖",
				Date:       "2026-11-24T09:30:00Z",
				BodyLoaded: true,
				Body:       "`scripts/disk-space.py` 依赖 `build/disk-cleanup/` 中的保留清单与清理回执。",
				Document:   &Document{ID: "k3", Dataset: "ds", Name: "目录保护红线", Created: "2026-11-24T09:30:00Z"},
			},
		)
		ttPr := ui.NewTester(pr.View, 1200, 820)
		save("project", ttPr, dark)

		// 3. Common knowledge 3-column
		cm := commonUIFixture()
		cm.SetAppearancePreferenceForTest(dark)
		ttCm := ui.NewTester(cm.View, 1200, 820)
		save("common", ttCm, dark)

		// 4. Settings (通用 + 服务)
		st := settingsFixture()
		st.SetAppearancePreferenceForTest(dark)
		st.desktopPrefs.Loaded = true
		st.desktopPrefs.KeepAliveOnQuit = true
		st.page = "设置"
		st.access.SettingsSection = "通用"
		ttSt := ui.NewTester(st.View, 1200, 820)
		save("settings-general", ttSt, dark)

		st.access.SettingsSection = "服务"
		ttSt.Frame()
		save("settings-service", ttSt, dark)

		// 5. Command Palette (⌘K) with populated search hits
		sr := projectUIFixture()
		sr.SetAppearancePreferenceForTest(dark)
		sr.OpenSearch()
		sr.searchUI.Query = "Cognee"
		sr.searchUI.Rows = []Item{
			{
				ID: "s1", Dataset: "ds", Kind: "知识片段",
				Title:      "Cognee 1.6.7 采用零侵入薄壳，禁止修改上游源码与绕过 SHA-256 校验",
				Body:       "Cognee 1.6.7 采用零侵入薄壳，禁止修改上游源码与绕过 SHA-256 校验",
				Preview:    "Cognee 1.6.7 采用零侵入薄壳，禁止修改上游源码与绕过 SHA-256 校验",
				Date:       "2026-11-26T10:00:00Z",
				BodyLoaded: true,
			},
			{
				ID: "s2", Dataset: "ds", Kind: "知识片段",
				Title:      "Hybrid 检索并联向量切片与 1-hop 实体图谱，单库单通道上限 10",
				Body:       "Hybrid 检索并联向量切片与 1-hop 实体图谱，单库单通道上限 10",
				Preview:    "Hybrid 检索并联向量切片与 1-hop 实体图谱，单库单通道上限 10",
				Date:       "2026-11-27T14:20:00Z",
				BodyLoaded: true,
			},
			{
				ID: "s3", Dataset: "ds", Kind: "原始材料片段",
				Title:      "讨论 Cognee 相对传统 RAG 的优势与图谱命中边界",
				Body:       "讨论 Cognee 相对传统 RAG 的优势与图谱命中边界",
				Preview:    "讨论 Cognee 相对传统 RAG 的优势与图谱命中边界",
				Date:       "2026-11-27T22:08:00Z",
				BodyLoaded: true,
			},
		}
		sr.searchUI.Loaded = true
		sr.searchUI.Loading = false
		sr.searchUI.Submitted = sr.searchSignature()
		ttSr := ui.NewTester(sr.View, 1200, 820)
		save("search", ttSr, dark)

		// 6. Tray flyout
		tr := settingsFixture()
		tr.SetAppearancePreferenceForTest(dark)
		tr.desktopPrefs.Loaded = true
		tr.desktopPrefs.KeepAliveOnQuit = true
		tr.monitor.LastCheckedAt = time.Date(2026, 11, 28, 10, 30, 0, 0, time.Local)
		ttTr := ui.NewTester(tr.TrayFlyoutView, TrayFlyoutWidth, tr.TrayFlyoutHeight())
		save("tray", ttTr, dark)
	}
}

func (a *App) SetAppearancePreferenceForTest(dark bool) {
	if dark {
		a.setAppearancePreference(appearanceDark)
	} else {
		a.setAppearancePreference(appearanceLight)
	}
	a.desktopPrefs.Loaded = true
}
