package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
)

const (
	appearanceSystem = "system"
	appearanceLight  = "light"
	appearanceDark   = "dark"
)

var appearanceModes = []string{appearanceSystem, appearanceLight, appearanceDark}

var (
	v4IconSun  = v4IconSVG(`<circle cx="10" cy="10" r="3.5"/><path d="M10 2.5v2M10 15.5v2M2.5 10h2M15.5 10h2M4.7 4.7l1.4 1.4M13.9 13.9l1.4 1.4M4.7 15.3l1.4-1.4M13.9 6.1l1.4-1.4"/>`)
	v4IconMoon = v4IconSVG(`<path d="M15.75 11.75A6.25 6.25 0 0 1 8.25 4.25a6.25 6.25 0 1 0 7.5 7.5z"/>`)
)

type appearancePreferenceFile struct {
	Version    int    `json:"version"`
	Appearance string `json:"appearance"`
}

type appearanceState struct {
	mode   string
	path   string
	loaded bool
}

func normalizeAppearanceMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case appearanceLight:
		return appearanceLight
	case appearanceDark:
		return appearanceDark
	default:
		return appearanceSystem
	}
}

func appearanceModeIndex(mode string) int {
	switch normalizeAppearanceMode(mode) {
	case appearanceLight:
		return 1
	case appearanceDark:
		return 2
	default:
		return 0
	}
}

func (a *App) appearancePreferencePath() (string, error) {
	base := a.Config.ClientStateFile
	if base == "" {
		base = a.Config.ConfigFile
	}
	if base == "" {
		return "", fmt.Errorf("外观设置存储位置不可用")
	}
	return filepath.Join(filepath.Dir(base), "appearance-preference.json"), nil
}

func readAppearancePreference(path string) string {
	if path == "" {
		return appearanceSystem
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return appearanceSystem
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return appearanceSystem
	}
	var payload appearancePreferenceFile
	if json.Unmarshal(b, &payload) != nil || payload.Version != 1 {
		return appearanceSystem
	}
	return normalizeAppearanceMode(payload.Appearance)
}

// AppearancePreference returns the saved in-app appearance preference:
// "system", "light", or "dark".
func (a *App) AppearancePreference() string {
	path, _ := a.appearancePreferencePath()
	if !a.appearance.loaded || a.appearance.path != path {
		a.appearance = appearanceState{
			mode:   readAppearancePreference(path),
			path:   path,
			loaded: true,
		}
	}
	return normalizeAppearanceMode(a.appearance.mode)
}

// setAppearancePreference persists the user's choice and applies it immediately.
func (a *App) setAppearancePreference(mode string) {
	mode = normalizeAppearanceMode(mode)
	path, err := a.appearancePreferencePath()
	a.appearance = appearanceState{mode: mode, path: path, loaded: true}
	if err == nil && path != "" {
		if b, marshalErr := json.Marshal(appearancePreferenceFile{Version: 1, Appearance: mode}); marshalErr == nil {
			_ = writePrivateJSONFile(path, b)
		}
	}
	if a.ApplyAppearanceSource != nil {
		a.ApplyAppearanceSource(mode)
	}
}

// effectiveDark resolves the palette dark flag: explicit light/dark overrides
// take effect immediately; "system" follows the window theme.
func (a *App) effectiveDark(windowDark bool) bool {
	switch a.AppearancePreference() {
	case appearanceLight:
		return false
	case appearanceDark:
		return true
	default:
		return windowDark
	}
}

func (a *App) sidebarAppearanceButton(c *ui.Context) {
	mode := a.AppearancePreference()
	icon := v4IconMoon
	nextMode := appearanceDark
	tip := "切换到深色模式（右键可选跟随系统）"
	if tok.Dark {
		icon = v4IconSun
		nextMode = appearanceLight
		tip = "切换到浅色模式（右键可选跟随系统）"
	}
	btn := ui.ButtonBase(c.Key("sidebar-appearance-toggle")).Role(ui.RoleButton).Label("外观模式").
		Cursor(ui.CursorPointer).Size(26, 26).Radius(radiusSmall).BorderWidth(0).
		AlignItems(ui.Center).Justify(ui.Center).Shrink(0).Tooltip(tip)
	btn.Background(homeHoverColor(btn, "app-toggle-bg", ui.Transparent, tok.Hover))
	v4ButtonPress(btn, 0.92)
	icoColor := tok.Text3
	if btn.Hovered() {
		icoColor = tok.Text1
	}
	btn.Children(func() {
		ui.Icon(c, icon).FontSize(15).TextColor(icoColor)
	}).OnClick(func() {
		a.setAppearancePreference(nextMode)
	}).ContextMenu(func(m *ui.Menu) {
		if m.Item("跟随系统").Checked(mode == appearanceSystem).Chosen() {
			a.setAppearancePreference(appearanceSystem)
		}
		if m.Item("浅色").Checked(mode == appearanceLight).Chosen() {
			a.setAppearancePreference(appearanceLight)
		}
		if m.Item("深色").Checked(mode == appearanceDark).Chosen() {
			a.setAppearancePreference(appearanceDark)
		}
	})
}
