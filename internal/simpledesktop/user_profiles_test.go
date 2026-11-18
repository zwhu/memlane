package simpledesktop

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func profileUIFixture(t *testing.T) *App {
	a := overviewFixture(t)
	a.page = "用户画像"
	a.access.Next = time.Now().Add(time.Hour)
	a.overviewRefreshAt = time.Now().Add(time.Hour)
	a.userProfiles = userProfilesState{Loaded: true, Next: time.Now().Add(time.Hour), Selected: "codex", Directory: userProfilesDirectory{
		Prompt: "请导出你自己已经保存的关于我的记忆。尽可能保留我的原话；未知内容写未知。",
		Profiles: []userProfile{
			{ID: "codex", Kind: "codex", Name: "Codex", Status: "ready", Method: "automatic", Updated: 1794830400, Document: &userProfileDocument{Dataset: "synthetic", ID: "one"}},
			{ID: "manual:grok", Kind: "manual", Name: "Grok", Status: "manual"},
		},
	}}
	a.userProfiles.Body = "# 用户画像\n\n## 沟通与写作偏好\n\n用户原话：先给结论，再说明依据。\n\n## 依据与未知\n\n仅依据此 AI 的现有记忆；职业未知。"
	a.userProfiles.BodyKey = "codex/synthetic/one"
	return a
}

func TestUserProfileReadManualImportAndDraftContinuity(t *testing.T) {
	for _, wh := range [][2]int{{1200, 820}, {1440, 900}} {
		t.Run(fmt.Sprint(wh), func(t *testing.T) {
			a := profileUIFixture(t)
			tt := ui.NewTester(a.View, wh[0], wh[1])
			fixtureImage(t, tt, fmt.Sprintf("profiles-%dx%d-read", wh[0], wh[1]))
			if !tt.HasText("Codex 对你的了解") || !tt.HasText("用户原话：先给结论，再说明依据。") {
				t.Fatal("saved profile not presented")
			}
			clickProjectUI(t, tt, "Grok用户画像")
			if a.userProfiles.Body != "" || tt.HasText("用户原话：先给结论，再说明依据。") {
				t.Fatal("previous source body leaked into new source")
			}
			clickProjectUI(t, tt, "导入此来源画像")
			if !a.userProfiles.Open {
				t.Fatal("import action did not open dialog")
			}
			fixtureImage(t, tt, fmt.Sprintf("profiles-%dx%d-import", wh[0], wh[1]))
			if !tt.HasText("复制提示词") {
				t.Fatal("manual import prompt action missing")
			}
			clickProjectUI(t, tt, "复制提示词")
			if tt.Clipboard() != a.userProfiles.Directory.Prompt {
				t.Fatal("wrong manual prompt copied")
			}
			footer, ok := tt.Find("保存画像")
			if !ok || footer.Y+footer.H > float32(wh[1]) {
				t.Fatal("save action below viewport")
			}
			clickProjectUI(t, tt, "用户画像文档")
			tt.Type("# Grok 对我的了解\n\n保留原话。")
			clickProjectUI(t, tt, "取消")
			clickProjectUI(t, tt, "导入此来源画像")
			if !strings.Contains(a.userProfiles.Draft, "保留原话") {
				t.Fatal("draft lost when reopening import")
			}
		})
	}
}

func TestUserProfileBusyAndUnknownNeverOfferAnotherAutomaticRun(t *testing.T) {
	a := profileUIFixture(t)
	a.userProfiles.Directory.Profiles[0].CanGenerate = true
	a.userProfiles.Directory.Profiles[0].Status = "unknown"
	a.userProfiles.Directory.Profiles[0].SaveUnknown = true
	tt := ui.NewTester(a.View, 1200, 820)
	if tt.HasText("自动生成") || tt.HasText("重新生成") || !tt.HasText("核对保存结果") {
		t.Fatal("unknown operation can be silently replayed")
	}
}

func TestUserProfileLateReadCannotReplaceAnotherSource(t *testing.T) {
	a := profileUIFixture(t)
	a.userProfiles.Body, a.userProfiles.BodyKey = "", ""
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"body":"OLD SOURCE MUST NOT APPEAR","document":{"dataset_id":"synthetic","data_id":"one"}}`)
	}))
	defer server.Close()
	key := filepath.Join(t.TempDir(), "manager-token")
	if err := os.WriteFile(key, []byte(strings.Repeat("m", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	a.Config.ProjectControl = &ProjectControlConfig{URL: server.URL, ManagementTokenFile: key}
	updates := make(chan func(), 1)
	a.Update = func(fn func()) { updates <- fn }
	a.readUserProfile()
	<-started
	a.selectUserProfile("manual:grok")
	close(release)
	select {
	case update := <-updates:
		update()
	case <-time.After(3 * time.Second):
		t.Fatal("document callback missing")
	}
	if a.userProfiles.Body != "" || a.userProfiles.Selected != "manual:grok" {
		t.Fatal("late read crossed source boundaries")
	}
}

func TestUserProfileExplicitImportEntryAfterFailure(t *testing.T) {
	a := profileUIFixture(t)
	a.userProfiles.Body, a.userProfiles.BodyKey = "", ""
	a.userProfiles.Directory.Profiles[0].Status = "failed"
	a.userProfiles.Directory.Profiles[0].CanGenerate = true
	a.userProfiles.Directory.Profiles[0].ErrorDetail = "Error loading config.toml: invalid transport"
	exit := 1
	a.userProfiles.Directory.Profiles[0].ExitCode = &exit
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("自动导入") || !tt.HasText("手动导入") || tt.HasText("自动生成") {
		t.Fatal("explicit attempt or paste fallback missing")
	}
	if !tt.HasText("导入失败：Error loading config.toml: invalid transport（退出码 1）") {
		t.Fatal("actual CLI error missing")
	}
	for _, label := range []string{"上次导入未完成", "复制提示词", "来源独立保留 · 不自动合并为通用记忆", "将它记住的背景、偏好与协作约定，独立保存在这里。"} {
		if tt.HasText(label) {
			t.Fatal("retired explanatory content remains", label)
		}
	}
	if a.userProfiles.Busy {
		t.Fatal("view started a profile operation")
	}
	a.userProfiles.Directory.Profiles[0].Status = "unknown"
	tt = ui.NewTester(a.View, 1200, 820)
	if tt.HasText("尝试导入") {
		t.Fatal("unknown result offered another attempt")
	}
}
