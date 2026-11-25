package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

const smokePath = "/usr/bin:/bin:/usr/sbin:/sbin"

type smokeJSON map[string]any

func (body smokeJSON) blocker(t *testing.T, want string) {
	t.Helper()
	blockers, _ := body["blockers"].([]any)
	for _, item := range blockers {
		blocker, _ := item.(map[string]any)
		if blocker["code"] == want {
			return
		}
	}
	t.Fatalf("missing blocker %q", want)
}

func (body smokeJSON) field(t *testing.T, key string, want any) {
	t.Helper()
	if got := body[key]; got != want {
		t.Fatalf("unexpected %s: want %v", key, want)
	}
}

func (body smokeJSON) object(t *testing.T, key string) smokeJSON {
	t.Helper()
	value, ok := body[key].(map[string]any)
	if !ok {
		t.Fatalf("%s object missing", key)
	}
	return value
}

func (body smokeJSON) hexField(t *testing.T, key string, length int) {
	t.Helper()
	value, _ := body[key].(string)
	if !regexp.MustCompile(fmt.Sprintf("^[0-9a-fA-F]{%d}$", length)).MatchString(value) {
		t.Fatalf("%s must be %d hexadecimal characters", key, length)
	}
}

type releaseSmoke struct {
	exe  string
	home string
}

func (s releaseSmoke) run(t *testing.T, timeout time.Duration, code int, env []string, input string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.exe, args...)
	cmd.Dir = s.home
	cmd.Env = append([]string{"HOME=" + s.home, "TMPDIR=" + s.home, "PATH=" + smokePath}, env...)
	cmd.Stdin = strings.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	start := time.Now()
	out, err := cmd.Output()
	if ctx.Err() != nil || time.Since(start) >= timeout {
		t.Fatalf("%s exceeded %s", args[0], timeout)
	}
	actual := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("launch %s: %v", args[0], err)
		}
		actual = exit.ExitCode()
	}
	if actual != code || stderr.Len() != 0 {
		t.Fatalf("%s: exit=%d, want %d, stderr length=%d", args[0], actual, code, stderr.Len())
	}
	return string(out)
}

func (s releaseSmoke) json(t *testing.T, code int, env []string, args ...string) smokeJSON {
	t.Helper()
	return s.jsonWithin(t, 15*time.Second, code, env, args...)
}

func (s releaseSmoke) jsonWithin(t *testing.T, timeout time.Duration, code int, env []string, args ...string) smokeJSON {
	t.Helper()
	raw := s.run(t, timeout, code, env, "", args...)
	var body smokeJSON
	if err := json.Unmarshal([]byte(raw), &body); err != nil || body == nil {
		t.Fatalf("%s must return a single valid JSON object", args[0])
	}
	checkSmokePrivacy(t, raw, body, s.home)
	return body
}

const smokeGuidance = "Execute read-only checks first. Show preview and ask user confirmation before any requires=explicit_confirmation action. Never read or print tokens, API keys, .local/ paths, hooks.json raw contents, or memory data; never run xattr or edit host config files directly. On exit 20 follow next_actions after retry_after_ms (poll doctor --operation-id while active; retry install after confirmation on lock contention). Stop after 3 retries (4th failure). On exit 11 hand human_steps to user."

var smokeSecretPrefix = regexp.MustCompile(`(?i)(^|[^A-Za-z0-9_])sk-`)

func checkSmokePrivacy(t *testing.T, raw string, body smokeJSON, home string) {
	t.Helper()
	if body["agent_guidance"] == smokeGuidance {
		encoded, err := json.Marshal(smokeGuidance)
		require(t, err)
		raw = strings.Replace(raw, string(encoded), `""`, 1)
		delete(body, "agent_guidance")
		defer func() { body["agent_guidance"] = smokeGuidance }()
	}
	decoded, err := json.Marshal(body)
	require(t, err)
	markers := []string{"Bearer ", "X-Api-Key", "management.token", "owner.token", ".local/", home, os.Getenv("HOME")}
	if user := os.Getenv("USER"); user != "" {
		markers = append(markers, "/Users/"+user)
	}
	for _, output := range []string{raw, string(decoded)} {
		if smokeSecretPrefix.MatchString(output) {
			t.Fatal("JSON leaked a secret key prefix")
		}
		for _, marker := range markers {
			if marker != "" && strings.Contains(strings.ToLower(output), strings.ToLower(marker)) {
				t.Fatal("JSON leaked a private value or path")
			}
		}
	}
}

func smokeExecutable(t *testing.T, repo, scratch string) string {
	t.Helper()
	if path := os.Getenv("MEMLANE_RELEASE_SMOKE_BIN"); path != "" {
		exe, err := filepath.Abs(path)
		require(t, err)
		return exe
	}
	exe := filepath.Join(scratch, "memlane")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	goBin := "go"
	if root := runtime.GOROOT(); root != "" {
		goBin = filepath.Join(root, "bin", "go")
	}
	cmd := exec.CommandContext(ctx, goBin, "build", "-mod=readonly", "-o", exe, "./cmd/memlane")
	cmd.Dir = repo
	cmd.Env = []string{"HOME=" + scratch, "TMPDIR=" + scratch, "PATH=" + smokePath, "CGO_ENABLED=0", "GOENV=off"}
	for _, key := range []string{"GOROOT", "GOPATH", "GOMODCACHE", "GOCACHE", "GOFLAGS", "GOTOOLCHAIN", "GOPROXY", "GOSUMDB"} {
		if val, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+val)
		}
	}
	if os.Getenv("GOCACHE") == "" {
		cmd.Env = append(cmd.Env, "GOCACHE="+filepath.Join(scratch, "go-build"))
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build release smoke executable: %v\n%s", err, out)
	}
	return exe
}

func TestReleaseContractSmoke(t *testing.T) {
	repo, err := filepath.Abs("../..")
	require(t, err)
	scratch, err := filepath.EvalSymlinks(t.TempDir())
	require(t, err)
	exe := smokeExecutable(t, repo, scratch)
	scenario := func(t *testing.T) releaseSmoke {
		t.Helper()
		home, err := os.MkdirTemp(scratch, "home-")
		require(t, err)
		return releaseSmoke{exe: exe, home: home}
	}

	t.Run("version", func(t *testing.T) {
		body := scenario(t).json(t, 0, nil, "version", "--json")
		body.field(t, "schema_version", float64(1))
		body.field(t, "exit_code", float64(0))
		body.object(t, "capabilities")
		app := body.object(t, "app")
		want := os.Getenv("MEMLANE_RELEASE_SMOKE_VERSION")
		if want == "" {
			var manifest struct{ Version string }
			raw, err := os.ReadFile(filepath.Join(repo, "mygo.json"))
			require(t, err)
			require(t, json.Unmarshal(raw, &manifest))
			want = manifest.Version
			if want == "" {
				t.Fatal("manifest version missing")
			}
		}
		app.field(t, "version", want)
		app.hexField(t, "bundled_runtime_hash", 16)
	})

	t.Run("cold-doctor", func(t *testing.T) {
		for _, args := range [][]string{{"doctor", "--json"}, {"setup", "doctor", "--json"}} {
			body := scenario(t).jsonWithin(t, 5*time.Second, 10, nil, args...)
			body.field(t, "status", "action_required")
			body.field(t, "ok", false)
		}
	})

	t.Run("secret-input", func(t *testing.T) {
		s := scenario(t)
		s.json(t, 50, nil, "doctor", "--api-key", "sk-smoke-secret", "--json").blocker(t, "secret_input_forbidden")
		s.json(t, 50, []string{"MEMLANE_TOKEN=smoke-forbidden"}, "doctor", "--json").blocker(t, "secret_input_forbidden")
	})

	t.Run("external-config", func(t *testing.T) {
		s := scenario(t)
		cfg := filepath.Join(s.home, "runtime.json")
		raw := []byte(`{"official_data":"external-store"}`)
		require(t, os.WriteFile(cfg, raw, 0600))
		s.json(t, 50, nil, "install", "--apply", "--plan-digest", strings.Repeat("a", 64),
			"--config", cfg, "--component-root", filepath.Join(s.home, "components"), "--json").
			blocker(t, "non_first_install_config_read_only")
		after, err := os.ReadFile(cfg)
		require(t, err)
		if sha256.Sum256(raw) != sha256.Sum256(after) {
			t.Fatal("external runtime config changed")
		}
	})

	t.Run("install-plan", func(t *testing.T) {
		s := scenario(t)
		body := s.json(t, 10, nil, "install", "--plan", "--json")
		components := body.object(t, "components")
		components.hexField(t, "plan_digest", 64)
		body.object(t, "artifacts").field(t, "plan_digest", components["plan_digest"])
		s.json(t, 30, nil, "install", "--apply", "--plan-digest", strings.Repeat("0", 64), "--json").
			blocker(t, "plan_digest_mismatch")
	})

	t.Run("first-store-resources", func(t *testing.T) {
		python := filepath.Join(repo, ".local/cognee-python/bin/python")
		if info, err := os.Stat(python); err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
			python, err = exec.LookPath("python3")
			require(t, err)
			python, err = filepath.Abs(python)
			require(t, err)
		}
		scenario(t).run(t, 35*time.Second, 0, nil, "", "--check-first-store-resources", python)
	})

	t.Run("dev-and-uninstall", func(t *testing.T) {
		s := scenario(t)
		cfg := filepath.Join(s.home, "runtime.json")
		raw, err := json.Marshal(map[string]string{"store_root": s.home, "gateway_url": "http://127.0.0.1:1"})
		require(t, err)
		require(t, os.WriteFile(cfg, raw, 0600))
		s.json(t, 0, nil, "dev", "status", "--config", cfg, "--json").field(t, "status", "stopped")
		s.json(t, 0, nil, "dev", "recover", "--config", cfg, "--json").field(t, "status", "already_released")
		s.json(t, 0, nil, "setup", "uninstall", "--host", "codex", "--dry-run", "--config", cfg, "--json").
			field(t, "applied", false)
	})

	t.Run("hooks", func(t *testing.T) {
		s := scenario(t)
		out := s.run(t, 10*time.Second, 0, []string{"MEMLANE_PROCESSING_SESSION=1"}, "invalid", "hook")
		if out != "{}\n" {
			t.Fatal("recursive hook did not return the neutral response")
		}
		key := filepath.Join(s.home, "agent.key")
		require(t, os.WriteFile(key, []byte("smoke-agent-key\n"), 0600))
		out = s.run(t, 10*time.Second, 0, nil, `{"prompt":"smoke"}`, "hook", "codex", "UserPromptSubmit",
			"--gateway-url", "http://127.0.0.1:1", "--key-file", key)
		if out != "{}\n" {
			t.Fatal("unreachable gateway did not return the neutral response")
		}
	})
}
