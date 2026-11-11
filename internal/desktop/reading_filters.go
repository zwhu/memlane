package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"sort"
	"time"
)

type ReadingFilterUI struct {
	Scroll                        ui.ScrollState
	Open                          bool
	Source, Scope, Category, Time string
	SinceMS                       int64
}

func (f ReadingFilterUI) active() bool {
	return f.Source != "" || f.Scope != "" || f.Category != "" || f.SinceMS > 0
}
func (a *App) readingFiltersChanged() {
	a.nextCursor = ""
	if a.page == "搜索" && a.searchQuery != "" {
		a.applySearchKind()
		return
	}
	a.items = nil
	a.refresh(false)
}
func (a *App) readingFilterControls(c *ui.Context) {
	f := &a.readingFilters
	count := 0
	for _, on := range []bool{f.Source != "", f.Scope != "", f.Category != "", f.SinceMS > 0} {
		if on {
			count++
		}
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		label := "筛选"
		if count > 0 {
			label = fmt.Sprintf("筛选 · %d", count)
		}
		quietButton(c, label).Key("reading-filters-toggle").OnClick(func() { f.Open = !f.Open })
		if a.page != "搜索" || a.searchQuery == "" {
			ui.Select(c.Key("memory-sort"), &a.listSort, []string{"最近更新", "常用", "重要优先"}).Grow(1).MinWidth(0).Tooltip(usageSortExplanation(a.listSort)).OnChange(a.readingFiltersChanged)
		}
	})
	if !f.Open {
		return
	}
	_, windowHeight := c.Size()
	panelHeight := float32(280)
	if windowHeight < 720 {
		panelHeight = 200
	}
	ui.Scroll(c.Key("reading-filters-panel")).TrackScroll(&f.Scroll).MaxHeight(panelHeight).Shrink(0).Padding(10).Gap(8).Border(1, design.Border).Radius(6).Background(design.Surface).Children(func() {
		if a.page == "搜索" && a.searchQuery != "" {
			ui.Text(c, "筛选本次搜索结果（最多 30 条）").FontSize(10).TextColor(design.Muted)
		}
		scopeIDs := []string{""}
		scopeLabels := []string{"当前范围"}
		currentScope := scopeLabels[0]
		if a.page == "搜索" && a.scope == "" {
			scopeLabels[0] = "不限范围"
			currentScope = scopeLabels[0]
			for _, s := range a.scopes {
				scopeIDs = append(scopeIDs, s.ID)
				short := s.ID
				if len(short) > 8 {
					short = short[:8]
				}
				scopeLabels = append(scopeLabels, scopeDisplayName(s)+" · "+short)
			}
		}
		for i, id := range scopeIDs {
			if f.Scope == id {
				currentScope = scopeLabels[i]
			}
		}
		ui.Text(c, "项目或通用知识范围").FontSize(10).TextColor(design.Muted)
		ui.Select(c.Key("reading-filter-scope"), &currentScope, scopeLabels).Disabled(len(scopeIDs) == 1).OnChange(func() {
			for i, label := range scopeLabels {
				if label == currentScope {
					f.Scope = scopeIDs[i]
					a.readingFiltersChanged()
					break
				}
			}
		})
		available := a.readingSources
		ranked := a.page == "搜索" && a.searchQuery != ""
		if ranked {
			available = nil
			for _, item := range a.searchHits {
				available = append(available, memoryRawSource(item))
			}
		} else if a.readingSourcesRoute != a.page+":"+a.scope {
			available = nil
		}
		sources, labels := readingSourceOptions(available, f.Source)
		sourceLabel := labels[0]
		for i, source := range sources {
			if source == f.Source {
				sourceLabel = labels[i]
				break
			}
		}

		ui.Text(c, "来源").FontSize(10).TextColor(design.Muted)
		ui.Select(c.Key("reading-filter-source"), &sourceLabel, labels).OnChange(func() {
			for i, label := range labels {
				if label == sourceLabel {
					f.Source = sources[i]
					a.readingFiltersChanged()
					break
				}
			}
		})
		if a.readingSourcesTruncated && !ranked && a.readingSourcesRoute == a.page+":"+a.scope {
			ui.Text(c, "来源目录仅列出前 1000 种；可先缩小范围。").FontSize(10).TextColor(design.Muted)
		}
		categoryLabels := []string{"全部类别", "项目约定", "个人偏好", "通用经验"}
		categoryIDs := []string{"", "project", "preference", "experience"}
		category := categoryLabels[0]
		for i, id := range categoryIDs {
			if f.Category == id {
				category = categoryLabels[i]
			}
		}
		ui.Text(c, "类别").FontSize(10).TextColor(design.Muted)
		ui.Select(c.Key("reading-filter-category"), &category, categoryLabels).OnChange(func() {
			for i, label := range categoryLabels {
				if label == category {
					f.Category = categoryIDs[i]
					a.readingFiltersChanged()
					break
				}
			}
		})
		if f.Time == "" {
			f.Time = "不限时间"
		}
		ui.Text(c, "当前版本保存时间").FontSize(10).TextColor(design.Muted)
		ui.Select(c.Key("reading-filter-time"), &f.Time, []string{"不限时间", "今天", "最近 7 天", "最近 30 天"}).OnChange(func() {
			now := time.Now()
			days := 0
			switch f.Time {
			case "今天":
				days = 1
			case "最近 7 天":
				days = 7
			case "最近 30 天":
				days = 30
			}
			f.SinceMS = 0
			if days > 0 {
				f.SinceMS = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -days+1).UnixMilli()
			}
			a.readingFiltersChanged()
		})
		quietButton(c, "清空条件").Key("reading-filter-clear").OnClick(func() { *f = ReadingFilterUI{Open: true}; a.readingFiltersChanged() })
	})
}

func (a *App) matchesReadingFilters(item MemoryItem) bool {
	f := a.readingFilters
	if f.Scope != "" && item.Dataset != f.Scope {
		return false
	}
	if f.Source != "" && memoryRawSource(item) != f.Source {
		return false
	}
	if f.Category != "" {
		kind := "project"
		for _, s := range a.scopes {
			if s.ID == item.Dataset {
				kind = s.Kind
				break
			}
		}
		if kind != f.Category {
			return false
		}
	}
	return f.SinceMS == 0 || !item.UpdatedAt.IsZero() && item.UpdatedAt.UnixMilli() >= f.SinceMS
}

// Display terminology can evolve independently of the persisted source key.
// Keep source identity exact, including legacy and renamed records side by side.
func memoryRawSource(item MemoryItem) string {
	if item.RawSource != "" {
		return item.RawSource
	}
	return item.Source
}
func readingSourceOptions(available []string, selected string) ([]string, []string) {
	seen := map[string]bool{}
	for _, source := range available {
		if source != "" {
			seen[source] = true
		}
	}
	if selected != "" {
		seen[selected] = true
	}
	keys := []string{}
	for source := range seen {
		keys = append(keys, source)
	}
	sort.Strings(keys)
	counts := map[string]int{}
	for _, source := range keys {
		counts[sourceDisplayName(source)]++
	}
	sources, labels := []string{""}, []string{"全部来源"}
	used := map[string]bool{"全部来源": true}
	for _, source := range keys {
		display := sourceDisplayName(source)
		label := display
		if source != display && counts[display] > 1 {
			label += " · 旧记录"
		}
		base := label
		for n := 2; used[label]; n++ {
			label = fmt.Sprintf("%s · 来源%d", base, n)
		}
		used[label] = true
		sources = append(sources, source)
		labels = append(labels, label)
	}
	return sources, labels
}
