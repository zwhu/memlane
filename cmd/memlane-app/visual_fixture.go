//go:build e2e

package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/zwhu/memlane/internal/desktop"
)

func configureVisualFixture(app *desktop.App) {
	if !visualFixtureEnabled() {
		return
	}
	if state := os.Getenv("MEMLANE_MVP_VISUAL_STATE"); state == "conversation-live" || state == "conversation-plugin-live" {
		return // Read only the explicitly marked isolated service, no sample rows.
	}
	today := time.Now()
	projects := []desktop.ProjectItem{{ID: "visual-project-a", Name: "记忆桌面端"}, {ID: "visual-project-b", Name: "设计系统"}}
	titles := []string{"项目记忆按仓库隔离", "接口异常保留 requestId", "组件优先复用已有设计规范", "接入外部接口先验证最小请求", "统一使用 pnpm 管理依赖", "提交 pnpm 锁文件", "保持紧凑的文字层级"}
	texts := []string{
		"同一个代码仓库对应一个项目，多个 Agent 共享这个项目的记忆。\n\n默认检索当前项目、全局个人偏好和全局经验；其他项目需要明确指定。",
		"保留请求标识与错误码，方便追踪排查过程。",
		"新页面优先使用项目现有的组件和交互约定。",
		"先确认权限和返回格式，再进行完整封装。",
		"统一包管理工具，减少环境差异。",
		"锁文件和依赖变更一起提交。",
		"正文清楚，标题克制，减少装饰。",
	}
	items := []desktop.MemoryItem{}
	for i, title := range titles {
		items = append(items, desktop.MemoryItem{ID: fmt.Sprintf("visual-memory-%d", i), Title: title, Text: texts[i], Original: texts[i], Source: "合成视觉夹具", ScopeName: projects[i%2].Name, Category: "项目约定", State: "active", IndexState: "indexed", Operation: "edit", Revision: 2, CreatedAt: today.Add(-time.Duration(i) * time.Hour)})
	}
	history := []desktop.MemoryHistory{{Revision: 2, Text: texts[0], Operation: "edit", CreatedAt: today}, {Revision: 1, Text: "一个项目对应一个代码仓库，不同 Agent 用同一份项目记忆。", Operation: "save", CreatedAt: today.Add(-time.Hour)}}
	activity := map[string]int{}
	for i := 0; i < 365; i++ {
		if i%7 != 0 && i%3 == 0 {
			activity[today.AddDate(0, 0, -i).Format("2006-01-02")] = i%8 + 1
		}
	}
	app.SetVisualFixture(items, projects, history, activity)
	app.SetMVPVisualFixture(os.Getenv("MEMLANE_MVP_VISUAL_STATE"))
	app.SetSettingsVisualFixture(os.Getenv("MEMLANE_SETTINGS_VISUAL_STATE"))
}

func visualFixtureEnabled() bool {
	if os.Getenv("MEMLANE_VISUAL_FIXTURE") == "1" {
		return true
	}
	for _, arg := range os.Args {
		if arg == "--p1-visual-fixture" {
			return true
		}
	}
	return false
}

func isolatedVisualFixtureEnabled() bool {
	return visualFixtureEnabled() && (os.Getenv("MEMLANE_SETTINGS_VISUAL_STATE") != "" || strings.HasPrefix(os.Getenv("MEMLANE_MVP_VISUAL_STATE"), "conversation"))
}
