package simpledesktop

import (
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSessionIdentityAndRecentAgentIcon(t *testing.T) {
	codexIcon := agentBrandIcon("codex")
	agyIcon := agentBrandIcon("antigravity")
	geminiIcon := agentBrandIcon("gemini")
	if agyIcon == codexIcon || agyIcon == homeDocument {
		t.Fatal("Agy icon must not be Codex or fallback document icon")
	}
	if geminiIcon == codexIcon || geminiIcon == homeDocument {
		t.Fatal("Gemini icon must not be Codex or fallback document icon")
	}

	cases := []struct {
		id       string
		identity string
		agent    string
		icon     *ui.SVG
		notCodex bool
	}{
		{
			id:       "codex_antigravity-1234567890abcdef",
			identity: "Agy 会话 · antigrav…90abcdef",
			agent:    "Agy",
			icon:     agyIcon,
			notCodex: true,
		},
		{
			id:       "codex_antigravity-short",
			identity: "Agy 会话 · antigravity-short",
			agent:    "Agy",
			icon:     agyIcon,
			notCodex: true,
		},
		{
			id:       "codex_agy-1234567890abcdef",
			identity: "Agy 会话 · agy-1234…90abcdef",
			agent:    "Agy",
			icon:     agyIcon,
			notCodex: true,
		},
		{
			id:       "antigravity-1234567890abcdef",
			identity: "Agy 会话 · antigrav…90abcdef",
			agent:    "Agy",
			icon:     agyIcon,
			notCodex: true,
		},
		{
			id:       "antigravity-abc_def",
			identity: "Agy 会话 · antigrav…-abc_def",
			agent:    "Agy",
			icon:     agyIcon,
			notCodex: true,
		},
		{
			id:       "antigravity_1234567890abcdef",
			identity: "Agy 会话 · 1234567890abcdef",
			agent:    "Agy",
			icon:     agyIcon,
			notCodex: true,
		},
		{
			id:       "agy_1234567890abcdef",
			identity: "Agy 会话 · 1234567890abcdef",
			agent:    "Agy",
			icon:     agyIcon,
			notCodex: true,
		},
		{
			id:       "agy-1234567890abcdef",
			identity: "Agy 会话 · agy-1234…90abcdef",
			agent:    "Agy",
			icon:     agyIcon,
			notCodex: true,
		},
		{
			id:       "codex_gemini-1234567890abcdef",
			identity: "Gemini 会话 · gemini-1…90abcdef",
			agent:    "Gemini",
			icon:     geminiIcon,
			notCodex: true,
		},
		{
			id:       "gemini-1234567890abcdef",
			identity: "Gemini 会话 · gemini-1…90abcdef",
			agent:    "Gemini",
			icon:     geminiIcon,
			notCodex: true,
		},
		{
			id:       "gemini_1234567890abcdef",
			identity: "Gemini 会话 · 1234567890abcdef",
			agent:    "Gemini",
			icon:     geminiIcon,
			notCodex: true,
		},
		{
			id:       "codex_traex-1234567890abcdef",
			identity: "TraeX 会话 · traex-12…90abcdef",
			agent:    "TraeX",
			icon:     agentBrandIcon("traex"),
			notCodex: true,
		},
		{
			id:       "codex_pi-1234567890abcdef",
			identity: "pi 会话 · pi-12345…90abcdef",
			agent:    "pi",
			icon:     agentBrandIcon("pi"),
			notCodex: true,
		},
		{
			id:       "codex_1234567890abcdef",
			identity: "Codex 会话 · 1234567890abcdef",
			agent:    "Codex",
			icon:     codexIcon,
		},
		{
			id:       "claude_123456",
			identity: "Claude Code 会话 · 123456",
			agent:    "Claude Code",
			icon:     agentBrandIcon("claude_code"),
			notCodex: true,
		},
		{
			id:       "plain-session-id",
			identity: "Agent 会话 · plain-session-id",
			agent:    "Agent 未记录",
			icon:     homeDocument,
			notCodex: true,
		},
	}

	for _, tc := range cases {
		if got := sessionIdentity(tc.id); got != tc.identity {
			t.Errorf("sessionIdentity(%q) = %q, want %q", tc.id, got, tc.identity)
		}

		sessionItem := Item{ID: tc.id, Session: &Session{ID: tc.id}}
		docItem := Item{
			ID: "doc-" + tc.id,
			Document: &Document{
				Metadata: map[string]any{
					"node_set":   []any{"session_learnings", "session_learnings:" + tc.id},
					"session_id": tc.id,
				},
			},
		}
		for label, item := range map[string]Item{"session": sessionItem, "document": docItem} {
			if got := sourceAgentName(item); got != tc.agent {
				t.Errorf("%s sourceAgentName(%q) = %q, want %q", label, tc.id, got, tc.agent)
			}
			gotIcon := recentAgentIcon(item)
			if gotIcon != tc.icon {
				t.Errorf("%s recentAgentIcon(%q) mismatch", label, tc.id)
			}
			if tc.notCodex && gotIcon == codexIcon {
				t.Errorf("%s recentAgentIcon(%q) unexpectedly resolved to Codex icon", label, tc.id)
			}
		}
	}
}

func TestAgentBrandIconAgyAndGeminiAliases(t *testing.T) {
	agyIcon := agentBrandIcon("antigravity")
	geminiIcon := agentBrandIcon("gemini")
	codexIcon := agentBrandIcon("codex")

	for _, kind := range []string{"agy", "Agy", "antigravity", "ANTIGRAVITY"} {
		if got := agentBrandIcon(kind); got != agyIcon || got == codexIcon {
			t.Errorf("agentBrandIcon(%q) did not resolve to antigravity icon", kind)
		}
		if got := agentBrandIconFor(kind, ""); got != agyIcon || got == codexIcon {
			t.Errorf("agentBrandIconFor(%q, \"\") did not resolve to antigravity icon", kind)
		}
	}
	for _, name := range []string{"Agy", "本机 Agy", "Antigravity", "Gemini App"} {
		if got := agentBrandIconFor("", name); got != agyIcon || got == codexIcon {
			t.Errorf("agentBrandIconFor(\"\", %q) did not resolve to antigravity icon", name)
		}
	}
	for _, name := range []string{"Gemini", "本机 Gemini", "Gemini CLI"} {
		if got := agentBrandIconFor("", name); got != geminiIcon || got == codexIcon {
			t.Errorf("agentBrandIconFor(\"\", %q) did not resolve to gemini icon", name)
		}
	}
	if got := agentBrandIconFor("gemini", ""); got != geminiIcon || got == codexIcon {
		t.Error("agentBrandIconFor(\"gemini\", \"\") did not resolve to gemini icon")
	}
}

func TestTraeXIconUsesMonochromeCurrentColorInLightAndDarkAppearances(t *testing.T) {
	traeIcon := agentBrandIcon("traex")
	codexIcon := agentBrandIcon("codex")
	if traeIcon == nil || traeIcon == homeDocument || traeIcon == codexIcon {
		t.Fatalf("unexpected TraeX icon: %v", traeIcon)
	}
	if got := agentOriginalIcon("traex"); got != traeIcon {
		t.Fatal("agentOriginalIcon(\"traex\") did not match agentBrandIcon(\"traex\")")
	}
	for _, name := range []string{"TraeX", "TRAE"} {
		if got := agentBrandIconFor("", name); got != traeIcon {
			t.Fatalf("agentBrandIconFor(\"\", %q) did not resolve to TraeX icon", name)
		}
	}

	t.Cleanup(func() { applyAppearance(false) })

	absDiff := func(a, b uint8) int {
		if a > b {
			return int(a - b)
		}
		return int(b - a)
	}
	closeColor := func(got color.RGBA, want ui.Color) bool {
		return absDiff(got.R, want.R) <= 20 && absDiff(got.G, want.G) <= 20 && absDiff(got.B, want.B) <= 20
	}

	for _, tc := range []struct {
		name string
		dark bool
	}{
		{name: "light", dark: false},
		{name: "dark", dark: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			applyAppearance(tc.dark)
			tt := ui.NewTester(func(c *ui.Context) {
				applyAppearance(tc.dark)
				ui.Themed(c, homeTheme).Fill().Background(tok.Bg).
					AlignItems(ui.Center).Justify(ui.Center).
					Children(func() {
						agentLogo(c, traeIcon, 24)
					})
			}, 48, 48)
			tt.SetDark(tc.dark)
			tt.Frame()

			img := tt.Image()
			b := img.Bounds()
			fgCount := 0
			minX, minY := b.Max.X, b.Max.Y
			maxX, maxY := b.Min.X-1, b.Min.Y-1
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					p := img.RGBAAt(x, y)
					if int(p.G)-int(p.R) > 25 && int(p.G)-int(p.B) > 25 {
						t.Fatalf("found bright green pixel at (%d,%d): %+v", x, y, p)
					}
					if closeColor(p, tok.Text1) {
						fgCount++
						if x < minX {
							minX = x
						}
						if x > maxX {
							maxX = x
						}
						if y < minY {
							minY = y
						}
						if y > maxY {
							maxY = y
						}
					}
				}
			}
			if fgCount == 0 {
				t.Fatalf("expected foreground pixels matching tok.Text1 (%+v), found none", tok.Text1)
			}
			innerBgCount := 0
			for y := minY; y <= maxY; y++ {
				for x := minX; x <= maxX; x++ {
					if closeColor(img.RGBAAt(x, y), tok.Bg) {
						innerBgCount++
					}
				}
			}
			if innerBgCount == 0 {
				t.Fatalf("expected inner background cutout pixels matching tok.Bg (%+v) within icon bbox (%d,%d)-(%d,%d)", tok.Bg, minX, minY, maxX, maxY)
			}
		})
	}
}
