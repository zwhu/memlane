package hooks

import (
	"strings"
	"testing"
)

func TestCaptureFilterPatchTargetsAndSecrets(t *testing.T) {
	const token = "sk-abcdefghijklmnopqrstuvwxyz0123456789"
	for _, tc := range []struct {
		name, tool, patch, rule, category string
	}{
		{"ordinary properties", "APPLY_PATCH", "*** Begin Patch\n*** Update File: foo.go\n@@\n+value := item.key\n-obj.key\n*** End Patch", "", ""},
		{"native hunk with header-shaped body", "apply_patch", "*** Begin Patch\n*** Update File: foo.go\n@@\n+++ b/private.key\n*** End Patch", "", ""},
		{"unified hunk with header-shaped body", "apply_patch", "--- a/foo.go\n+++ b/foo.go\n@@ -1 +1 @@\n--- a/private.key\n+++ b/private.key", "", ""},
		{"sensitive update", "apply_patch", "*** Update File: certs/server.key\n+body stays local", "*.key", "sensitive_path"},
		{"sensitive addition", "Edit", "*** Add File: tls/dev.pem\n+body stays local", "*.pem", "sensitive_path"},
		{"sensitive deletion", "PATCH", "*** Delete File: /private/.netrc", ".netrc", "sensitive_path"},
		{"sensitive rename", "edit_patch", "*** Update File: foo.go\n*** Move to: certs/server.key", "*.key", "sensitive_path"},
		{"unified addition", "edit", "--- /dev/null\n+++ b/.env.local\n@@ -0,0 +1 @@\n+body stays local", ".env.*", "sensitive_path"},
		{"single unified header", "patch", "+++ b/.env.local", ".env.*", "sensitive_path"},
		{"placeholder", "apply_patch", "*** Update File: foo.go\n+Authorization: Bearer <token>\n+Authorization: Bearer REDACTED", "", ""},
		{"real private key", "apply_patch", "*** Update File: foo.go\n+-----BEGIN PRIVATE KEY-----", "private_key", "secret_value"},
		{"real API token", "apply_patch", "*** Update File: foo.go\n+" + token, "api_token", "secret_value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := EvaluateCaptureFilter(CaptureFilterInput{Host: "traex", Kind: "capture_trace", Content: map[string]any{
				"origin_function": tc.tool, "method_params": map[string]any{"command": tc.patch},
			}})
			if got.Blocked != (tc.rule != "") || got.Rule != tc.rule || got.Category != tc.category {
				t.Fatalf("unexpected decision: %+v", got)
			}
			if tc.category == "sensitive_path" && (got.Source != "apply_patch" ||
				!strings.Contains(got.Reason, "TRAE CLI 补丁路径命中 `"+tc.rule+"`") ||
				!strings.Contains(got.Reason, "规则命中本身不足以确认补丁包含密钥")) {
				t.Fatalf("unexpected reason: %+v", got)
			}
			for _, private := range []string{"certs/server.key", "tls/dev.pem", ".env.local", "/private/.netrc", "body stays local", "-----BEGIN PRIVATE KEY-----", token} {
				if strings.Contains(got.Reason+got.NextAction, private) {
					t.Fatal("private content leaked in the filter result")
				}
			}
		})
	}
}

func TestCaptureFilterShellArgumentsAndStructuredCredentials(t *testing.T) {
	for command, rule := range map[string]string{
		"grep item.key foo.go": "", "rg --glob '*.go' obj.key": "", "rg -e 'obj.key' foo.go": "",
		"grep 'obj.key' 'certs/server.key'": "*.key", "cat certs/server.key": "*.key",
		"cat 'a path/server.key'": "*.key", "cat server.key": "*.key", "cat .env": ".env",
		"cat ~/.ssh/id_rsa": "id_rsa", "openssl req -out tls.key": "*.key",
		"grep foo src.go; cat > private.key": "*.key", "node -e 'item.key'": "",
	} {
		got := EvaluateCaptureFilter(CaptureFilterInput{Kind: "capture_trace", Content: map[string]any{"cmd": command}})
		if got.Rule != rule || got.Blocked && got.Source != "shell_command" {
			t.Errorf("%q: %+v", command, got)
		}
	}
	for _, tc := range []struct {
		kind, rule, source string
		content            any
	}{
		{"capture_trace", "*.p12", "tool_path", map[string]any{"tool_name": "read", "paths": []any{"src.go", "certs/private.p12"}}},
		{"capture_qa", "bearer_token", "content", map[string]any{"context": map[string]any{"Authorization": "Bearer x1Y2z3"}}},
		{"capture_qa", "api_token", "content", map[string]any{"answer": "ghp_abcdefghijklmnopqrstuvwxyz"}},
		{"document", "api_token", "content", "AKIA12345678901ABCDE"},
		{"capture_qa", "", "", map[string]any{"question": "obj.key", "answer": "Authorization: Bearer REDACTED"}},
	} {
		got := EvaluateCaptureFilter(CaptureFilterInput{Kind: tc.kind, Content: tc.content})
		if got.Rule != tc.rule || got.Source != tc.source {
			t.Errorf("%v: %+v", tc, got)
		}
	}
}
