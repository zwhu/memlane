package desktop

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// Exercise the input and navigation boundary before introducing a real service:
// missing components must never become a successful save or lose a draft on navigation.
func TestShellWithoutEngine(t *testing.T) {
	app := New("0.0.1", nil)
	tt := ui.NewTester(app.View, 1200, 780)
	if !tt.HasText("服务未连接") || tt.HasText("已保存") {
		t.Fatal("shell misrepresents the disconnected service")
	}
	writeScreenshot(t, tt, "home.png")
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	click("新增记忆")
	click("记忆内容")
	tt.Compose("项目", 2)
	tt.Type("项目经验：保留 request_id")
	if app.draft != "项目经验：保留 request_id" {
		t.Fatalf("text composition failed: %q", app.draft)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Command("copy")
	if tt.Clipboard() != app.draft {
		t.Fatalf("copy lost Unicode text: %q", tt.Clipboard())
	}
	click("设置")
	click("全部记忆")
	if app.draft != "项目经验：保留 request_id" || !tt.HasText("记忆服务尚未就绪，暂不能保存。") {
		t.Fatal("navigation lost the draft or implied it was saved")
	}
	writeScreenshot(t, tt, "draft.png")
	if err := tt.Click("保存"); err == nil && tt.HasText("已保存") {
		t.Fatal("disabled save fabricated success")
	}
	click("设置")
	click("放大")
	if app.zoom <= 1 {
		t.Fatal("zoom did not change")
	}
	tt.SetSize(980, 620)
	if !tt.HasText("界面缩放") {
		t.Fatal("settings lost at minimum size")
	}
}

func writeScreenshot(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	dir := os.Getenv("MEMLANE_SCREENSHOT_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, tt.Image()); err != nil {
		t.Fatal(err)
	}
}
