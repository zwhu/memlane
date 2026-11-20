package simpledesktop

import (
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
