package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zwhu/memlane/kit/aiinstaller/cli"
)

func TestEnvelopePrioritizesBlockersAndEncodesJSON(t *testing.T) {
	e := cli.NewEnvelope("install", "apply", "Use the preview.")
	e.App = cli.AppSummary{Executable: "A & B <CLI>"}
	e.SetOperation("setup-55")
	e.AddAction("read_only", "doctor", "--json")
	e.AddAction("read_only", "doctor", "--json")
	if e.SchemaVersion != 1 || e.OperationID != "setup-55" || len(e.NextActions) != 1 || e.AgentGuidance != "Use the preview." {
		t.Fatalf("unexpected initial response: %+v", e)
	}
	e.Block(cli.ExitHumanRequired, "confirmation_needed")
	e.Busy("installer_active")
	e.Block(cli.ExitPreflightBlocked, "preflight_failed")
	e.Finalize()
	if e.OK || e.Status != "in_progress_or_busy" || e.ExitCode != cli.ExitInProgressOrBusy || e.RetryAfterMS != cli.DefaultRetryMillis {
		t.Fatalf("unexpected response status: %+v", e)
	}
	if len(e.Blockers) != 3 || e.Blockers[2].Status != "preflight_blocked" {
		t.Fatalf("unexpected blockers: %+v", e.Blockers)
	}

	var stdout bytes.Buffer
	if err := cli.EncodeJSON(&stdout, e); err != nil {
		t.Fatal(err)
	}
	raw := stdout.String()
	if !strings.Contains(raw, "\n  \"schema_version\": 1,\n") ||
		!strings.Contains(raw, e.App.Executable) || !strings.HasSuffix(raw, "\n") {
		t.Fatalf("unexpected JSON encoding: %s", raw)
	}
	var decoded cli.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil || decoded.App != e.App || decoded.ExitCode != e.ExitCode {
		t.Fatalf("decoded response: %+v, error: %v", decoded, err)
	}
	statuses := cli.StatusMap()
	statuses[cli.ExitOK] = "modified"
	var success cli.Envelope
	success.Finalize()
	if !success.OK || success.Status != "ok" || cli.StatusName(cli.ExitOK) != "ok" {
		t.Fatalf("status map mutation changed the protocol: %+v", success)
	}
}

func TestSecretGuardUsesCallerEnvironment(t *testing.T) {
	guard := cli.SecretGuard{EnvPrefix: "EXAMPLE", EnvNames: []string{"ANOTHER_CREDENTIAL"}}
	for _, tc := range []struct {
		name string
		args []string
		env  string
		want bool
	}{
		{name: "credentials flag", args: []string{"--vendor_API_KEY=synthetic"}, want: true},
		{name: "bare token flag", args: []string{"--token", "synthetic"}, want: true},
		{name: "caller prefix", env: "EXAMPLE_MANAGEMENT_TOKEN", want: true},
		{name: "caller extra name", env: "ANOTHER_CREDENTIAL", want: true},
		{name: "other prefix", env: "OTHER_OWNER_TOKEN"},
		{name: "proxy env", env: "HTTPS_PROXY"},
		{name: "ordinary flag", args: []string{"--config", "/home/example/settings"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := guard.ContainsSecretInput(tc.args, func(key string) string {
				if key == tc.env {
					return "synthetic"
				}
				return ""
			})
			if got != tc.want {
				t.Errorf("ContainsSecretInput() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSanitizerUsesCallerPolicy(t *testing.T) {
	const home = "/home/example"
	redactors := []func(string) string{func(s string) string {
		return strings.ReplaceAll(s, "vendor-session", "[vendor-redacted]")
	}}
	allowed := []string{"installation complete"}
	sanitize := func(value string) string {
		return cli.SanitizeString(value, home, redactors, "sentinel-secret")
	}
	logLine := func(line string) string {
		return cli.SanitizeLogLine(line, home, allowed, redactors, "sentinel-secret")
	}
	for _, tc := range []struct{ input, want string }{
		{"/home/example/config.json", "~/config.json"},
		{"/home/example/owner.token", "[redacted-private-content]"},
		{"/home/example/.local/state", "~[redacted-local-path]"},
		{"sentinel-secret vendor-session", "[redacted] [vendor-redacted]"},
	} {
		if got := sanitize(tc.input); got != tc.want {
			t.Errorf("SanitizeString(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
	if got := sanitize("https://example.invalid/?session=credential"); strings.Contains(got, "credential") {
		t.Errorf("URL query leaked: %s", got)
	}
	if got := logLine(" installation complete "); got != "installation complete" {
		t.Errorf("allowed log line discarded: %q", got)
	}
	if got := logLine("unknown log line"); got != "[redacted-log-line]" {
		t.Errorf("unexpected log output: %q", got)
	}
}

func TestReadBoundedJSONRejectsUnsafePaths(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "data.json")
	if err := os.WriteFile(valid, []byte(`{"state":"ready"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var state map[string]string
	if err := cli.ReadBoundedJSON(valid, &state); err != nil || state["state"] != "ready" {
		t.Fatalf("ReadBoundedJSON(valid) = %v, state = %v", err, state)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(valid, link); err != nil {
		t.Fatal(err)
	}
	oversize := filepath.Join(dir, "oversize.json")
	if err := os.WriteFile(oversize, make([]byte, (2<<20)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"relative.json", valid + "\x00", dir + "/../data.json", link, oversize, dir} {
		if err := cli.ReadBoundedJSON(path, &state); !errors.Is(err, cli.ErrUnsafePath) {
			t.Errorf("ReadBoundedJSON(%q) = %v, want ErrUnsafePath", path, err)
		}
	}
}

func TestSafeValues(t *testing.T) {
	for _, tc := range []struct {
		name    string
		check   func(string) string
		allowed string
		denied  string
	}{
		{"operation ID", cli.SafeOperationID, "B6-setup", "../setup"},
		{"instance ID", cli.SafeInstanceID, "user_agent", "User-Agent"},
		{"short hash", func(s string) string { return cli.SafeHash(s, true) }, strings.Repeat("A", 16), strings.Repeat("A", 64)},
		{"long hash", func(s string) string { return cli.SafeHash(s, false) }, strings.Repeat("7", 64), strings.Repeat("g", 64)},
		{"known string", func(s string) string { return cli.Known(s, "ready", "stopped") }, "ready", "READY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.check(tc.allowed); got != tc.allowed {
				t.Errorf("allowed value %q changed to %q", tc.allowed, got)
			}
			if got := tc.check(tc.denied); got != "" {
				t.Errorf("denied value %q passed as %q", tc.denied, got)
			}
		})
	}
}

func TestValidVersion(t *testing.T) {
	if !cli.ValidVersion("1.2.3-beta.4+sha.abc") || cli.ValidVersion("1.2.3 private") {
		t.Fatal("version validation accepted an unexpected value")
	}
}
