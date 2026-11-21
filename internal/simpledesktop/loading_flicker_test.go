package simpledesktop

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestDesktopPagesSuppressTransientLoadingAndRefreshFlicker(t *testing.T) {
	for _, appearance := range []string{"light", "dark"} {
		t.Run(appearance, func(t *testing.T) {
			// 1. 项目 -> 记忆：首次加载与静默刷新不出现“正在读取…”/“已载入 0 项”。
			a := projectUIFixture()
			setTestAppearance(t, a, appearance)
			s := a.state()
			s.Loaded = false
			s.Loading = true
			s.Items = nil
			s.Selected = nil
			tt := ui.NewTester(a.View, 1200, 820)
			for _, unwanted := range []string{"正在读取…", "已载入 0 项", "更新中"} {
				if tt.HasText(unwanted) {
					t.Fatalf("[%s] initial knowledge load flashed %q: %v", appearance, unwanted, tt.Texts())
				}
			}

			s.Loaded = true
			s.Loading = true
			s.Items = []Item{{ID: "pending-k", Dataset: "ds", Kind: "知识"}}
			tt.Frame()
			for _, unwanted := range []string{"正在读取…", "更新中", "正在刷新"} {
				if tt.HasText(unwanted) {
					t.Fatalf("[%s] knowledge refresh flashed %q: %v", appearance, unwanted, tt.Texts())
				}
			}

			// 2. 项目 -> 原始记录：切回与刷新无“更新中”/“正在读取…”，空态不闪烁消失。
			a.kind = "文档"
			raw := a.state()
			raw.Loaded = true
			raw.Loading = true
			raw.Reading = true
			d := lessonDocument()
			raw.Items = []Item{{ID: d.ID, Dataset: "ds", Kind: "文档", Title: "main.go", Body: "body", BodyLoaded: true, Document: &d}}
			raw.Selected = &raw.Items[0]
			tt.Frame()
			for _, unwanted := range []string{"更新中", "正在读取…", "已载入 1 条 · 更新中"} {
				if tt.HasText(unwanted) {
					t.Fatalf("[%s] raw records refresh flashed %q: %v", appearance, unwanted, tt.Texts())
				}
			}
			raw.Items = nil
			raw.Selected = nil
			tt.Frame()
			if !tt.HasText("此项目暂无文档") {
				t.Fatalf("[%s] empty raw records disappeared during background refresh: %v", appearance, tt.Texts())
			}

			// 3. 项目 -> 图谱：a.graphLoading=true 时工具栏无“正在读取…”。
			a.page = "图谱"
			a.graphMode = "实体关系"
			a.graphLoading = true
			tt.Frame()
			if tt.HasText("正在读取…") || !tt.HasText("刷新图谱") {
				t.Fatalf("[%s] graph toolbar flashed loading text or hid action: %v", appearance, tt.Texts())
			}

			// 4. 项目 -> 管理：q.Loading 与未载入消息状态不闪“更新中…”/“正在读取消息状态…”。
			sa := settingsFixture()
			setTestAppearance(t, sa, appearance)
			sa.kind = "管理"
			sa.access.OpenProject = "p"
			p := sa.access.Directory.Projects[0]
			q := sa.projectQueueStateFor(p)
			q.Next = time.Now().Add(time.Hour)
			q.Loaded = false
			q.Loading = true
			q.BacklogLoading = true
			stt := ui.NewTester(sa.View, 1200, 820)
			for _, unwanted := range []string{"更新中…", "正在读取消息状态…", "0 条记录", "0 份文档"} {
				if stt.HasText(unwanted) {
					t.Fatalf("[%s] unloaded project queue flashed %q: %v", appearance, unwanted, stt.Texts())
				}
			}

			q.Loaded = true
			q.Loading = true
			stt.Frame()
			if stt.HasText("更新中…") {
				t.Fatalf("[%s] loaded project queue flashed 更新中…: %v", appearance, stt.Texts())
			}

			// 管理队列原文查看器：读取尚未就绪时不显示纯文字“正在读取原文…”。
			q.Reader = &pane{
				Reading:  true,
				Selected: &Item{ID: "doc-raw", Dataset: "ds", Kind: "文档", Title: "session.md", BodyLoaded: false},
			}
			stt.Frame()
			if stt.HasText("正在读取原文…") {
				t.Fatalf("[%s] queued doc reader flashed 正在读取原文…: %v", appearance, stt.Texts())
			}
			q.Reader = nil

			// 5. 全局设置 -> Agent 与模型 / 服务：按钮不抖动为“检测中…”，未载入不用“正在读取连接状态…”。
			sa.page = "设置"
			sa.settingsOrigin = nil
			sa.access.SettingsSection = "Agent 与模型"
			sa.integrations.Loaded = false
			sa.integrations.Loading = true
			sa.integrations.Error = ""
			stt.Frame()
			if !stt.HasText("重新检测") || stt.HasText("检测中…") || stt.HasText("正在读取连接状态…") {
				t.Fatalf("[%s] agent settings flashed loading text: %v", appearance, stt.Texts())
			}

			sa.access.SettingsSection = "服务"
			sa.monitor.Polling = true
			sa.monitor.LastCheckedAt = time.Now()
			stt.Frame()
			if !stt.HasText("重新检查") || stt.HasText("检测中…") {
				t.Fatalf("[%s] service card check button flashed 检测中…: %v", appearance, stt.Texts())
			}

			// 6. 用户画像：切到已生成来源但正文读取中时，不闪出“尚未从 … 导入画像”。
			pa := profileUIFixture(t)
			setTestAppearance(t, pa, appearance)
			pa.userProfiles.Body = ""
			pa.userProfiles.Reading = true
			ptt := ui.NewTester(pa.View, 1200, 820)
			if ptt.HasText("尚未从 Codex 导入画像") {
				t.Fatalf("[%s] user profile flashed empty import CTA while body was loading: %v", appearance, ptt.Texts())
			}
		})
	}
}
