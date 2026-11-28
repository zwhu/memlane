package app

import (
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/settings"
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
			id:       "antigravity-1234567890abcdef",
			identity: "Agy 会话 · antigrav…90abcdef",
			agent:    "Agy",
			icon:     agyIcon,
			notCodex: true,
		},
		{
			id:       "antigravity-short",
			identity: "Agy 会话 · antigravity-short",
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
			id:       "traex-1234567890abcdef",
			identity: "TRAE CLI 会话 · traex-12…90abcdef",
			agent:    "TRAE CLI",
			icon:     agentBrandIcon("traex"),
			notCodex: true,
		},
		{
			id:       "trae_ide-1234567890abcdef",
			identity: "TRAE CN 会话 · trae_ide…90abcdef",
			agent:    "TRAE CN",
			icon:     agentBrandIcon("trae_ide"),
			notCodex: true,
		},
		{
			id:       "pi-1234567890abcdef",
			identity: "pi 会话 · pi-12345…90abcdef",
			agent:    "pi",
			icon:     agentBrandIcon("pi"),
			notCodex: true,
		},
		{
			id:       "opencode-1234567890abcdef",
			identity: "opencode 会话 · opencode…90abcdef",
			agent:    "opencode",
			icon:     agentBrandIcon("opencode"),
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
	if got := agentBrandIcon("traex"); got != traeIcon {
		t.Fatal("agentBrandIcon(\"traex\") did not match agentBrandIcon(\"traex\")")
	}
	for _, name := range []string{"TRAE CLI", "TraeX", "TRAE"} {
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

func TestTraeCNIconAcceptsHistoricalNames(t *testing.T) {
	icon := agentBrandIcon("trae_ide")
	for _, name := range []string{"TRAE CN", "traecn", "TRAE IDE", "本机 TRAE IDE"} {
		if agentBrandIconFor("", name) != icon {
			t.Errorf("display name %q did not resolve to TRAE CN", name)
		}
	}
	for _, key := range []string{"trae_ide", "TRAE CN", "traecn", "TRAE IDE"} {
		if agentBrandIcon(key) != icon {
			t.Errorf("host key %q did not resolve to TRAE CN", key)
		}
	}
	if settings.AgentDisplayName("本机 TRAE IDE") != "TRAE CN" {
		t.Fatal("historical registered name did not use the current display name")
	}
}

func TestBareUUIDSessionAgentIDResolution(t *testing.T) {
	const bareUUID = "01a2201c-ff97-7430-8035-dd1c06201848"
	const shortUUID = "01a2201c…06201848"

	cases := []struct {
		agentID  string
		wantName string
		wantIcon *ui.SVG
		wantID   string
	}{
		{
			agentID:  "traex-7859e85d2cf88d393366e77e",
			wantName: "TRAE CLI",
			wantIcon: agentBrandIcon("traex"),
			wantID:   "TRAE CLI 会话 · " + shortUUID,
		},
		{
			agentID:  "codex-2564787a11223344",
			wantName: "Codex",
			wantIcon: agentBrandIcon("codex"),
			wantID:   "Codex 会话 · " + shortUUID,
		},
		{
			agentID:  "trae_ide-99887766",
			wantName: "TRAE CN",
			wantIcon: agentBrandIcon("trae_ide"),
			wantID:   "TRAE CN 会话 · " + shortUUID,
		},
		{
			agentID:  "antigravity-11223344",
			wantName: "Agy",
			wantIcon: agentBrandIcon("antigravity"),
			wantID:   "Agy 会话 · " + shortUUID,
		},
		{
			agentID:  "agy-11223344",
			wantName: "Agy",
			wantIcon: agentBrandIcon("antigravity"),
			wantID:   "Agy 会话 · " + shortUUID,
		},
		{
			agentID:  "pi-55667788",
			wantName: "pi",
			wantIcon: agentBrandIcon("pi"),
			wantID:   "pi 会话 · " + shortUUID,
		},
		{
			agentID:  "claude_code-aabbccdd",
			wantName: "Claude Code",
			wantIcon: agentBrandIcon("claude_code"),
			wantID:   "Claude Code 会话 · " + shortUUID,
		},
		{
			agentID:  "opencode-eeff0011",
			wantName: "opencode",
			wantIcon: agentBrandIcon("opencode"),
			wantID:   "opencode 会话 · " + shortUUID,
		},
		{
			agentID:  "cursor-12345678",
			wantName: "Cursor",
			wantIcon: agentBrandIcon("cursor"),
			wantID:   "Cursor 会话 · " + shortUUID,
		},
		{
			agentID:  "gemini-abcdef12",
			wantName: "Gemini",
			wantIcon: agentBrandIcon("gemini"),
			wantID:   "Gemini 会话 · " + shortUUID,
		},
	}

	for _, tc := range cases {
		if tc.wantIcon == nil || tc.wantIcon == homeDocument {
			t.Fatalf("expected real brand icon for %q", tc.agentID)
		}
		sessItem := Item{
			ID:      bareUUID,
			Kind:    "会话",
			Title:   bareUUID,
			Session: &Session{ID: bareUUID, Agent: tc.agentID},
		}
		docItem := Item{
			ID:          "doc-" + tc.agentID,
			Kind:        "知识",
			Source:      bareUUID,
			SourceAgent: tc.agentID,
			Document: &Document{
				ID: "doc-" + tc.agentID,
				Metadata: map[string]any{
					"node_set":   []any{"session_learnings", "session_learnings:" + bareUUID},
					"session_id": bareUUID,
				},
			},
		}
		for label, item := range map[string]Item{"session": sessItem, "knowledge": docItem} {
			if got := recentAgentIcon(item); got != tc.wantIcon {
				t.Errorf("%s (%q): recentAgentIcon returned generic/wrong icon", label, tc.agentID)
			}
			if got := sourceAgentName(item); got != tc.wantName {
				t.Errorf("%s (%q): sourceAgentName = %q, want %q", label, tc.agentID, got, tc.wantName)
			}
			if got := sessionSourceAgentDisplay(item); got != tc.wantName {
				t.Errorf("%s (%q): sessionSourceAgentDisplay = %q, want %q", label, tc.agentID, got, tc.wantName)
			}
		}
		if got := itemSessionIdentity(sessItem); got != tc.wantID {
			t.Errorf("%q: itemSessionIdentity = %q, want %q", tc.agentID, got, tc.wantID)
		}
		if got := sessionFallbackTitle(sessItem); got != tc.wantID {
			t.Errorf("%q: sessionFallbackTitle = %q, want %q", tc.agentID, got, tc.wantID)
		}
		if got := sessionReadableParentTitle(sessItem); got != tc.wantID {
			t.Errorf("%q: sessionReadableParentTitle = %q, want %q", tc.agentID, got, tc.wantID)
		}
		if got, _ := searchItemPrimaryText(sessItem, ""); got != tc.wantID {
			t.Errorf("%q: searchItemPrimaryText = %q, want %q", tc.agentID, got, tc.wantID)
		}
		if got := sessionIdentityWithAgent(docItem.Source, itemSourceAgentID(docItem)); got != tc.wantID {
			t.Errorf("%q: knowledge source identity = %q, want %q", tc.agentID, got, tc.wantID)
		}
	}

	// Non-brand prefixes like "pixel-123" must NOT falsely match "pi".
	unknown := Item{
		ID:      bareUUID,
		Kind:    "会话",
		Title:   bareUUID,
		Session: &Session{ID: bareUUID, Agent: "pixel-123"},
	}
	if got := recentAgentIcon(unknown); got != homeDocument {
		t.Fatal("unrecognized agent_id pixel-123 must fall back to homeDocument")
	}
	if got := sourceAgentName(unknown); got != "Agent 未记录" {
		t.Fatalf("sourceAgentName(pixel-123) = %q, want Agent 未记录", got)
	}
	if got := sessionSourceAgentDisplay(unknown); got != "pixel-123" {
		t.Fatalf("sessionSourceAgentDisplay(pixel-123) = %q, want pixel-123", got)
	}
	if got := itemSessionIdentity(unknown); got != "Agent 会话 · "+shortUUID {
		t.Fatalf("itemSessionIdentity(pixel-123) = %q", got)
	}
}
