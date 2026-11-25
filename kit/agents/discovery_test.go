package agents

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func putFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func TestDiscovery(t *testing.T) {
	home := t.TempDir()
	application := filepath.Join(home, "Applications", "Gemini.app")
	putFile(t, filepath.Join(application, "Contents/Info.plist"),
		`<?xml version="1.0"?><plist><dict><key>CFBundleVersion</key><string>42</string><key>CFBundleShortVersionString</key><string>1.2</string></dict></plist>`, 0o600)
	putFile(t, filepath.Join(application, "Contents/Helpers/agy"), "#!/bin/sh\n", 0o700)
	putFile(t, filepath.Join(home, "Applications/Trae CN.app/Contents/MacOS/Electron"), "#!/bin/sh\n", 0o700)
	bin := filepath.Join(home, "bin")
	for _, name := range []string{"codex", "pi", "traecli", "claude", "cursor", "opencode"} {
		putFile(t, filepath.Join(bin, name), "#!/bin/sh\n", 0o700)
	}
	opts := DiscoveryOptions{Home: home, AppDirs: []string{filepath.Join(home, "Applications")},
		LookPath: func(name string) (string, error) { return filepath.Join(bin, name), nil },
		Environ: func(key string) string {
			if key == "XDG_CONFIG_HOME" {
				return filepath.Join(home, "xdg")
			}
			return ""
		}}
	hosts := DetectHosts(opts)
	if len(hosts) != 8 || len(AllowedKinds()) != 8 {
		t.Fatalf("hosts: %+v", hosts)
	}
	for _, host := range hosts {
		if !host.Detected || !host.SupportsCapture || host.Executable == "" || host.DisplayName == "" {
			t.Errorf("host: %+v", host)
		}
		if host.Kind == HostAntigravity && (host.Version != "1.2" || host.Application != application) {
			t.Errorf("bundle: %+v", host)
		}
		if host.Kind == HostOpenCode && host.ProfileDir != filepath.Join(home, "xdg", "opencode") {
			t.Errorf("profile: %s", host.ProfileDir)
		}
	}
	opts.Kinds = []string{HostPi, "invalid", HostPi}
	opts.RuntimeOverrides = map[string]HostOverride{HostPi: {Executable: filepath.Join(bin, "cursor"), ProfileDir: home}}
	selected := DetectHosts(opts)
	runtime := ResolveHostRuntime(HostPi, opts)
	if len(selected) != 1 || runtime.Executable != filepath.Join(bin, "cursor") || runtime.ProfileDir != home || !selected[0].ProfileFound {
		t.Fatalf("override: %+v %+v", selected, runtime)
	}
	if path := DetectExecutable(HostPi, "relative", opts); path != "" {
		t.Errorf("relative override accepted: %s", path)
	}
	putFile(t, filepath.Join(application, "Contents/Info.plist"), strings.Repeat("X", 2<<20+1), 0o600)
	if ReadBundleVersion(application) != "" {
		t.Fatal("oversized plist accepted")
	}
}

func stateDatabase(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE threads(id TEXT COLLATE NOCASE,model TEXT,model_provider TEXT,reasoning_effort TEXT,service_tier TEXT);
 INSERT INTO threads VALUES ('Exact','session-model',NULL,'high',NULL),('other','ignored',NULL,NULL,NULL),('empty',NULL,NULL,NULL,NULL)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestModelDefaults(t *testing.T) {
	for _, kind := range []string{HostCodex, HostTraeX} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			file, key, provider, dbHome := "config.toml", "CODEX_THREAD_ID", "openai", home
			if kind == HostTraeX {
				file, key, provider, dbHome = "traecli.toml", "TRAE_THREAD_ID", "trae", filepath.Join(home, "cli")
			}
			putFile(t, filepath.Join(home, file), "profile = 'work'\nmodel = 'root'\nmodel_provider = '"+provider+"'\n[profiles.work]\nmodel='profile'\nservice_tier='fast'\n", 0o600)
			path := filepath.Join(dbHome, "state_2.sqlite")
			stateDatabase(t, path)
			stateDatabase(t, filepath.Join(dbHome, "state_1.sqlite"))
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			list, err := os.ReadDir(dbHome)
			if err != nil {
				t.Fatal(err)
			}
			opts := func(id string) ModelDefaultsOptions {
				return ModelDefaultsOptions{Environ: func(k string) string {
					if k == key {
						return id
					}
					return ""
				}}
			}
			actual, err := ReadModelDefaults(kind, home, opts("Exact"))
			if err != nil || actual.Model != "session-model" || actual.Provider != provider || actual.Source != "agent_session" ||
				actual.SessionID != "Exact" || actual.ReasoningEffort != "high" || actual.ServiceTier != "fast" {
				t.Fatalf("%+v %v", actual, err)
			}
			actual, err = ReadModelDefaults(kind, home, opts("exact"))
			if err != nil || actual.Model != "profile" || actual.Source != "agent_config" {
				t.Fatalf("exact identity: %+v %v", actual, err)
			}
			_, err = ReadModelDefaults(kind, home, opts("empty"))
			requireAgentError(t, err, "agent_session_model_unavailable")
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("database contents changed: %v", err)
			}
			afterList, err := os.ReadDir(dbHome)
			if err != nil || len(afterList) != len(list) {
				t.Fatalf("database sidecars changed: %v %v", afterList, err)
			}
			putFile(t, filepath.Join(dbHome, "state_3.sqlite"), "corrupt", 0o600)
			_, err = ReadModelDefaults(kind, home, opts("Exact"))
			requireAgentError(t, err, "agent_session_state_unavailable")
			if row, err := CurrentSessionModel(kind, home, opts(strings.Repeat("x", 129)).Environ); row != nil || err != nil {
				t.Fatalf("overlong session: %+v %v", row, err)
			}
		})
	}
	home := t.TempDir()
	putFile(t, filepath.Join(home, "settings.json"), `{"defaultModel":"d","defaultProvider":"ollama","defaultThinkingLevel":"high"}`, 0o600)
	defaults, err := ReadModelDefaults(HostPi, home, ModelDefaultsOptions{})
	if err != nil || defaults.Model != "d" || defaults.ThinkingLevel != "high" {
		t.Fatalf("%+v %v", defaults, err)
	}
	putFile(t, filepath.Join(home, "settings.json"), `{"defaultModel":"d","defaultProvider":"unsupported"}`, 0o600)
	_, err = ReadModelDefaults(HostPi, home, ModelDefaultsOptions{})
	requireAgentError(t, err, "pi_provider_schema_unsupported")
	agy := filepath.Join(home, ".gemini", "config")
	putFile(t, agySettings(agy), `{"model":"google_alias"}`, 0o600)
	defaults, err = ReadModelDefaults(HostAntigravity, agy, ModelDefaultsOptions{})
	if err != nil || defaults.Model != "google_alias" || defaults.Provider != "google" || defaults.ConfigurationSource != "agy_cli_settings" {
		t.Fatalf("%+v %v", defaults, err)
	}
}

func requireAgentError(t *testing.T, err error, code string) {
	t.Helper()
	var got *AgentError
	if !errors.As(err, &got) || got.Code != code {
		t.Fatalf("error %v, want %s", err, code)
	}
}

func TestBridgeRuntimeFingerprint(t *testing.T) {
	home := t.TempDir()
	exe := filepath.Join(home, "pi")
	putFile(t, exe, "#!/bin/sh\n", 0o700)
	auth := filepath.Join(home, "auth.json")
	putFile(t, auth, "{}", 0o600)
	opts := ModelDefaultsOptions{}
	stamp := func(extension string) string {
		value, err := BridgeRuntimeFingerprint(HostPi, exe, home, []byte(extension), opts)
		if err != nil || len(value) != 64 {
			t.Fatalf("fingerprint: %q %v", value, err)
		}
		return value
	}
	first := stamp("ext")
	if first != stamp("ext") || first == stamp("alt") {
		t.Fatal("extension dependency missing")
	}
	putFile(t, filepath.Join(home, "models.json"), `{"providers":{}}`, 0o600)
	models := stamp("ext")
	if first == models {
		t.Fatal("models dependency missing")
	}
	modified := time.Unix(100, 0)
	if err := os.Chtimes(exe, modified, modified); err != nil {
		t.Fatal(err)
	}
	if models == stamp("ext") {
		t.Fatal("executable metadata dependency missing")
	}
	if err := os.Chmod(auth, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := CheckBridgeRuntime(HostPi, exe, home, opts)
	requireAgentError(t, err, "generation_bridge_authentication_required")
}

func TestReadOnlySessionWithoutWALSidecars(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "state_1.sqlite")
	stateDatabase(t, path)
	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := writer.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode); err != nil || mode != "wal" {
		writer.Close()
		t.Fatalf("configure writable test database: %q %v", mode, err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
			t.Fatalf("unexpected sidecar before read: %v", err)
		}
	}
	environ := func(key string) string {
		if key == "CODEX_THREAD_ID" {
			return "Exact"
		}
		return ""
	}
	session, err := CurrentSessionModel(HostCodex, home, environ)
	if err != nil || session == nil || session.Model != "session-model" {
		t.Fatalf("read cleanly closed WAL database: %+v %v", session, err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Lstat(path + suffix); !os.IsNotExist(err) {
			t.Fatalf("read created %s: %v", suffix, err)
		}
	}
	if err := os.Symlink(path, path+"-wal"); err != nil {
		t.Fatal(err)
	}
	_, err = CurrentSessionModel(HostCodex, home, environ)
	requireAgentError(t, err, "agent_session_state_unavailable")
}
