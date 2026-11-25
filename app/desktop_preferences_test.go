package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestDesktopPreferencesDefaultAndAtomicPersistence(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{ConfigFile: filepath.Join(dir, "runtime.json")}

	a := New(cfg, nil)
	if !a.KeepAliveOnQuit() {
		t.Fatal("KeepAliveOnQuit should default to true")
	}

	a.SetKeepAliveOnQuit(false)
	if a.KeepAliveOnQuit() {
		t.Fatal("KeepAliveOnQuit should be false after SetKeepAliveOnQuit(false)")
	}

	prefPath := filepath.Join(dir, "desktop-preferences.json")
	info, err := os.Stat(prefPath)
	if err != nil {
		t.Fatalf("expected %s to exist: %v", prefPath, err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("expected 0600 permissions, got %o", perm)
	}

	// Reload into a fresh App instance eagerly via New().
	reloaded := New(cfg, nil)
	if reloaded.KeepAliveOnQuit() {
		t.Fatal("reloaded App should preserve KeepAliveOnQuit=false")
	}
}

func TestDesktopPreferencesRejectsSymlinkReadAndWrite(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "outside.json")
	if err := os.WriteFile(target, []byte(`{"version":1,"keep_alive_on_quit":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(dir, "desktop-preferences.json")
	if err := os.Symlink(target, linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	cfg := Config{ConfigFile: filepath.Join(dir, "runtime.json")}
	a := New(cfg, nil)
	if !a.KeepAliveOnQuit() {
		t.Fatal("symlink desktop-preferences.json must be ignored and fall back to default true")
	}

	a.SetKeepAliveOnQuit(true)
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"keep_alive_on_quit":false`) {
		t.Fatalf("symlink target should not have been overwritten, got: %s", string(got))
	}
}

func TestDesktopPreferencesToggleOpenAtLoginRollbackOnError(t *testing.T) {
	a := New(Config{}, nil)
	osEnabled := false
	a.ReadOpenAtLoginFn = func() bool { return osEnabled }
	a.SetOpenAtLoginFn = func(enable bool) error {
		return errors.New("SMAppService requires an .app bundle")
	}

	if a.OpenAtLogin() {
		t.Fatal("expected initial OpenAtLogin=false")
	}
	a.ToggleOpenAtLogin(true)
	if a.OpenAtLogin() {
		t.Fatal("OpenAtLogin must NOT fake enabled state when SetOpenAtLoginFn returns an error")
	}
	if a.desktopPrefs.LoginError == "" || !strings.Contains(a.desktopPrefs.LoginError, "SMAppService") {
		t.Fatalf("expected honest LoginError message, got %q", a.desktopPrefs.LoginError)
	}

	// Succeeding clears LoginError and updates state.
	a.SetOpenAtLoginFn = func(enable bool) error {
		osEnabled = enable
		return nil
	}
	a.ToggleOpenAtLogin(true)
	if !a.OpenAtLogin() {
		t.Fatal("expected OpenAtLogin=true after successful enable")
	}
	if a.desktopPrefs.LoginError != "" {
		t.Fatalf("expected LoginError cleared on success, got %q", a.desktopPrefs.LoginError)
	}
}

func TestForceQuitConfirmDialogRejectsEnterAndConfirmsOnClick(t *testing.T) {
	a := settingsFixture()
	var quitCalled bool
	var quitStop bool
	a.QuitAppFn = func(stopServices bool) {
		quitCalled = true
		quitStop = stopServices
	}
	a.openForceQuitConfirm()

	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("彻底停止并退出 Memlane？") {
		t.Fatal("expected force-quit danger dialog to be visible")
	}
	// Danger dialog must NOT confirm on Enter.
	tt.Key(0, ui.KeyEnter)
	if quitCalled {
		t.Fatal("danger force-quit dialog must not trigger on Enter")
	}
	a.desktopPrefs.ForceQuitOpen = true
	tt.Frame()

	// Clicking the explicit danger confirm button stops services and quits.
	if err := tt.Click("彻底停止并退出"); err != nil {
		t.Fatalf("failed to click danger confirm button: %v", err)
	}
	if !quitCalled || !quitStop {
		t.Fatalf("expected QuitApp(true) on explicit confirm click, got called=%v stop=%v", quitCalled, quitStop)
	}
}
