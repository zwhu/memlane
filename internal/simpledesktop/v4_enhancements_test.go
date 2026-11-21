package simpledesktop

import (
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestParseInlineMarkdownSupportsCodeBoldItalicStrikeAndLinks(t *testing.T) {
	applyAppearance(true)
	src := previewMarkdownSource("## `engine/foo.py` 修复 **重要** *提示* ~~旧方案~~ [文档](https://example.com) 与 __init__")
	if src != "`engine/foo.py` 修复 **重要** *提示* ~~旧方案~~ [文档](https://example.com) 与 __init__" {
		t.Fatalf("unexpected normalized preview: %q", src)
	}
	spans := parseInlineMarkdown(src, fsBody, tok.Text1)
	var hasCode, hasBold, hasItalic, hasStrike, hasLink, hasDunder bool
	for _, sp := range spans {
		switch {
		case sp.Text == "engine/foo.py" && sp.Font == "SF Mono" && sp.Background == tok.Fill2 && sp.Radius == 3:
			hasCode = true
		case sp.Text == "重要" && sp.Weight == 600:
			hasBold = true
		case sp.Text == "提示" && sp.Italic:
			hasItalic = true
		case sp.Text == "旧方案" && sp.Strikethrough:
			hasStrike = true
		case sp.Text == "文档" && sp.Color == tok.Accent:
			hasLink = true
		case sp.Text == " 与 __init__":
			hasDunder = true
		}
	}
	if !hasCode || !hasBold || !hasItalic || !hasStrike || !hasLink || !hasDunder {
		t.Fatalf("missing expected inline markdown spans: code=%v bold=%v italic=%v strike=%v link=%v dunder=%v spans=%+v",
			hasCode, hasBold, hasItalic, hasStrike, hasLink, hasDunder, spans)
	}
}

func TestAppearanceTogglePersistsAndUpdatesPaletteLive(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "client-state.json")
	a := New(Config{ClientStateFile: stateFile}, nil)
	var applied []string
	a.ApplyAppearanceSource = func(mode string) { applied = append(applied, mode) }

	// Default preference is system.
	if got := a.AppearancePreference(); got != appearanceSystem {
		t.Fatalf("default appearance=%q, want system", got)
	}

	// Render in light mode, click sidebar toggle -> switches to dark immediately.
	tt := ui.NewTester(a.View, 1200, 820)
	clickProjectUI(t, tt, "外观模式")
	if got := a.AppearancePreference(); got != appearanceDark {
		t.Fatalf("after sidebar click appearance=%q, want dark", got)
	}
	if !tok.Dark {
		t.Fatal("expected palette to switch to dark immediately")
	}
	if homeTheme.Inverse != darkPalette.TooltipBg || homeTheme.InverseText != darkPalette.TooltipText {
		t.Fatalf("dark tooltip theme mismatch: inverse=%v text=%v", homeTheme.Inverse, homeTheme.InverseText)
	}

	// Verify persistence on a fresh App instance.
	reloaded := New(Config{ClientStateFile: stateFile}, nil)
	if got := reloaded.AppearancePreference(); got != appearanceDark {
		t.Fatalf("persisted appearance=%q, want dark", got)
	}

	// Settings page Segmented switches back to light and system.
	a.page = "设置"
	a.access.SettingsSection = "记忆提炼"
	tt.Frame()
	if !tt.HasText("外观") || !tt.HasText("跟随系统") || !tt.HasText("浅色") || !tt.HasText("深色") {
		t.Fatal("settings page missing appearance segmented control")
	}
	clickProjectUI(t, tt, "浅色")
	if got := a.AppearancePreference(); got != appearanceLight || tok.Dark {
		t.Fatalf("expected light appearance after settings click, got mode=%q dark=%v", got, tok.Dark)
	}
	if len(applied) < 2 || applied[len(applied)-1] != appearanceLight {
		t.Fatalf("expected ApplyAppearanceSource callback, got %v", applied)
	}
}
