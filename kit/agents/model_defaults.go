package agents

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

type ModelDefaults struct {
	Model               string `json:"model"`
	Provider            string `json:"provider"`
	Source              string `json:"source"`
	SessionID           string `json:"session_id,omitempty"`
	ReasoningEffort     string `json:"reasoning_effort,omitempty"`
	ServiceTier         string `json:"service_tier,omitempty"`
	ThinkingLevel       string `json:"thinking_level,omitempty"`
	ConfigurationSource string `json:"configuration_source,omitempty"`
}
type ModelDefaultsOptions struct{ Environ func(string) string }
type SessionModel struct {
	Model, Provider, SessionID, ReasoningEffort, ServiceTier string
	fields                                                   map[string]string
}

type AgentError struct {
	Code   string
	Status int
}

func (e *AgentError) Error() string { return e.Code }
func agentError(code string) error  { return &AgentError{Code: code, Status: 409} }

func getenv(env func(string) string) func(string) string {
	if env == nil {
		return os.Getenv
	}
	return env
}

func CurrentSessionModel(kind, home string, environ func(string) string) (*SessionModel, error) {
	names := []string{"CODEX_THREAD_ID", "CODEX_SESSION_ID"}
	if kind == HostTraeX {
		names = []string{"TRAE_THREAD_ID", "TRAE_SESSION_ID", "CODEX_THREAD_ID"}
		home = filepath.Join(home, "cli")
	} else if kind != HostCodex {
		return nil, nil
	}
	var identity string
	for _, name := range names {
		identity = getenv(environ)(name)
		if identity != "" {
			break
		}
	}
	if identity == "" || utf8.RuneCountInString(identity) > 128 {
		return nil, nil
	}
	list, err := os.ReadDir(home)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, agentError("agent_session_state_unavailable")
	}
	for i := len(list) - 1; i >= 0; i-- {
		name := list[i].Name()
		if !strings.HasPrefix(name, "state_") || !strings.HasSuffix(name, ".sqlite") {
			continue
		}
		session, err := lookupSession(filepath.Join(home, name), identity)
		if err != nil {
			return nil, agentError("agent_session_state_unavailable")
		}
		if session != nil {
			if session.Model == "" {
				return nil, agentError("agent_session_model_unavailable")
			}
			return session, nil
		}
	}
	return nil, nil
}

func sqliteSessionPath(path string) (string, bool, error) {
	f, err := openRegular(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	var header [20]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return "", false, err
	}
	if string(header[:16]) != "SQLite format 3\x00" {
		return "", false, errors.New("invalid database header")
	}
	var snapshot bool
	if header[18] == 2 || header[19] == 2 {
		present, err := hasSessionSidecars(path)
		if err != nil {
			return "", false, err
		}
		snapshot = !present
	}
	path, err = filepath.Abs(path)
	return canonicalSystemPath(path), snapshot, err
}

func hasSessionSidecars(path string) (bool, error) {
	present := false
	for _, suffix := range []string{"-wal", "-shm"} {
		sidecar, err := openRegular(path + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
		if err := sidecar.Close(); err != nil {
			return false, err
		}
		present = true
	}
	return present, nil
}

func copyClosedSessionDB(path, target string) error {
	source, err := openRegular(path)
	if err != nil {
		return err
	}
	defer source.Close()
	before, err := source.Stat()
	if err != nil {
		return err
	}
	changed := errors.New("session database changed during copy")
	present, err := hasSessionSidecars(path)
	if err != nil {
		return err
	}
	if present {
		return changed
	}
	dest, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.CopyN(dest, source, before.Size())
	if err := errors.Join(copyErr, dest.Close()); err != nil {
		return err
	}
	present, err = hasSessionSidecars(path)
	if err != nil {
		return err
	}
	current, err := openRegular(path)
	if err != nil {
		return err
	}
	defer current.Close()
	after, err := current.Stat()
	if err != nil {
		return err
	}
	if present || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return changed
	}
	return nil
}

func lookupSession(path, identity string) (*SessionModel, error) {
	path, snapshot, err := sqliteSessionPath(path)
	if err != nil {
		return nil, err
	}
	if snapshot {
		// SQLite can create WAL sidecars during a read-only open. Confine
		// those files to a private copy when the source has no sidecars.
		dir, err := os.MkdirTemp("", "agent-session-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(dir)
		target := filepath.Join(dir, "state.sqlite")
		if err := copyClosedSessionDB(path, target); err != nil {
			return nil, err
		}
		path = target
	}
	uri := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&immutable=0&_pragma=query_only(ON)&_pragma=busy_timeout(1000)"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	columns, err := sessionColumns(db)
	if err != nil || !slices.Contains(columns, "id") {
		return nil, err
	}
	var fields []string
	for _, field := range []string{"model", "model_provider", "reasoning_effort", "service_tier"} {
		if slices.Contains(columns, field) {
			fields = append(fields, field)
		}
	}
	selected := strings.Join(append([]string{"id"}, fields...), ",")
	cells := make([]sql.NullString, len(fields)+1)
	args := make([]any, len(cells))
	for i := range cells {
		args[i] = &cells[i]
	}
	err = db.QueryRow("SELECT "+selected+" FROM threads WHERE id = ? COLLATE BINARY LIMIT 1", identity).Scan(args...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for i, name := range fields {
		if cells[i+1].Valid {
			values[name] = cells[i+1].String
		}
	}
	return &SessionModel{Model: values["model"], Provider: values["model_provider"], SessionID: identity,
		ReasoningEffort: values["reasoning_effort"], ServiceTier: values["service_tier"], fields: values}, nil
}

func sessionColumns(db *sql.DB) ([]string, error) {
	rows, err := db.Query("PRAGMA table_info(threads)")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var name string
		var cid, typ, notNull, defaultValue, primary any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &primary); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	return columns, rows.Err()
}

func ReadModelDefaults(kind, home string, opts ModelDefaultsOptions) (ModelDefaults, error) {
	invalid := agentError("agent_default_model_unavailable")
	out := ModelDefaults{Source: "agent_config"}
	var doc map[string]any
	var err error
	switch kind {
	case HostCodex, HostTraeX:
		name := "config.toml"
		out.Provider = "openai"
		if kind == HostTraeX {
			name = "traecli.toml"
			out.Provider = "trae"
		}
		raw, e := SafeReadFile(filepath.Join(home, name), configLimit)
		if e != nil {
			return ModelDefaults{}, invalid
		}
		doc, err = ParseTOML(raw)
		if err != nil {
			return ModelDefaults{}, invalid
		}
		if value := doc["profile"]; value != nil && value != "" {
			profile, ok := value.(string)
			profiles, _ := doc["profiles"].(map[string]any)
			selected, found := profiles[profile].(map[string]any)
			if !ok || !found {
				return ModelDefaults{}, invalid
			}
			for k, v := range selected {
				doc[k] = v
			}
		}
		session, err := CurrentSessionModel(kind, home, opts.Environ)
		if err != nil {
			return ModelDefaults{}, err
		}
		if session != nil {
			for k, v := range session.fields {
				if k == "reasoning_effort" {
					k = "model_reasoning_effort"
				}
				doc[k] = v
			}
			out.Source, out.SessionID = "agent_session", session.SessionID
		}
		if provider := doc["model_provider"]; provider != nil && provider != "" && provider != out.Provider {
			return ModelDefaults{}, agentError("agent_default_provider_unsupported")
		}
		if out.ReasoningEffort, err = optionalScalar(doc, "model_reasoning_effort"); err != nil {
			return ModelDefaults{}, err
		}
		if out.ServiceTier, err = optionalScalar(doc, "service_tier"); err != nil {
			return ModelDefaults{}, err
		}
	case HostPi, HostAntigravity:
		path := filepath.Join(home, "settings.json")
		if kind == HostAntigravity {
			path = agySettings(home)
		}
		raw, e := SafeReadFile(path, configLimit)
		if e != nil || json.Unmarshal(raw, &doc) != nil || doc == nil {
			return ModelDefaults{}, invalid
		}
		if kind == HostPi {
			out.Provider, _ = doc["defaultProvider"].(string)
			if out.Provider != "deepseek" && out.Provider != "ollama" {
				return ModelDefaults{}, agentError("pi_provider_schema_unsupported")
			}
			if out.ThinkingLevel, err = optionalScalar(doc, "defaultThinkingLevel"); err != nil {
				return ModelDefaults{}, err
			}
			doc["model"] = doc["defaultModel"]
		} else {
			out.Provider, out.ConfigurationSource = "google", "agy_cli_settings"
		}
	default:
		return ModelDefaults{}, agentError("unsupported_generation_bridge")
	}
	if out.Model, _ = doc["model"].(string); !validScalar(out.Model, 200) {
		return ModelDefaults{}, invalid
	}
	return out, nil
}

func optionalScalar(doc map[string]any, key string) (string, error) {
	if doc[key] == nil {
		return "", nil
	}
	value, ok := doc[key].(string)
	if !ok || !validScalar(value, 32) {
		return "", agentError("agent_default_model_unavailable")
	}
	return value, nil
}

func validScalar(value string, limit int) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= limit &&
		strings.IndexFunc(value, func(r rune) bool { return r < 32 }) < 0
}

func agySettings(home string) string {
	return filepath.Join(filepath.Dir(home), "antigravity-cli", "settings.json")
}

type BridgeRuntimeInfo struct{ Kind, Executable, LauncherDir, Home, AuthHome, AuthFile string }

func CheckBridgeRuntime(kind, executable, home string, opts ModelDefaultsOptions) (BridgeRuntimeInfo, error) {
	invalid := agentError("generation_bridge_authentication_required")
	if !filepath.IsAbs(home) || !filepath.IsAbs(executable) {
		return BridgeRuntimeInfo{}, invalid
	}
	launcherDir := filepath.Dir(filepath.Clean(executable))
	if resolvedDir, err := filepath.EvalSymlinks(launcherDir); err == nil && filepath.IsAbs(resolvedDir) {
		launcherDir = resolvedDir
	}
	executable, err := filepath.EvalSymlinks(executable)
	if err != nil || !isHostExecutable(executable) {
		return BridgeRuntimeInfo{}, invalid
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return BridgeRuntimeInfo{}, invalid
	}
	if st, err := os.Stat(home); err != nil || !st.IsDir() {
		return BridgeRuntimeInfo{}, invalid
	}
	result := BridgeRuntimeInfo{Kind: kind, Executable: executable, LauncherDir: launcherDir, Home: home, AuthHome: home}
	switch kind {
	case HostCodex, HostPi:
		result.AuthFile = filepath.Join(home, "auth.json")
	case HostTraeX:
		result.AuthFile = filepath.Join(home, "cli", "auth.json")
	case HostAntigravity:
		_, err := ReadModelDefaults(kind, home, opts)
		return result, err
	default:
		return BridgeRuntimeInfo{}, agentError("unsupported_generation_bridge")
	}
	f, err := openRegular(result.AuthFile)
	if err != nil {
		return BridgeRuntimeInfo{}, invalid
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || st.Mode().Perm()&0o077 != 0 {
		return BridgeRuntimeInfo{}, invalid
	}
	return result, nil
}

type executableIdentity struct {
	Device, Inode uint64
	Size          int64
	MtimeNS       int64
}

func executableStat(path string) (executableIdentity, error) {
	info, err := os.Stat(path)
	if err != nil {
		return executableIdentity{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return executableIdentity{}, errors.New("executable metadata unavailable")
	}
	return executableIdentity{uint64(stat.Dev), uint64(stat.Ino), info.Size(), info.ModTime().UnixNano()}, nil
}

func BridgeRuntimeFingerprint(kind, executable, home string, extensionBytes []byte, opts ModelDefaultsOptions) (string, error) {
	selected, err := CheckBridgeRuntime(kind, executable, home, opts)
	if err != nil {
		return "", err
	}
	stat, err := executableStat(selected.Executable)
	if err != nil {
		return "", agentError("generation_bridge_authentication_required")
	}
	value := map[string]any{"kind": kind, "executable": selected.Executable, "home": selected.Home, "file": stat}
	switch kind {
	case HostPi:
		data, err := ReadPiModels(selected.Home)
		if err != nil {
			return "", err
		}
		value["models_sha256"], value["extension_sha256"] = digest(data), digest(extensionBytes)
	case HostAntigravity:
		data, err := SafeReadFile(agySettings(selected.Home), configLimit)
		if err != nil {
			return "", agentError("agent_default_model_unavailable")
		}
		value["settings_sha256"] = digest(data)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return digest(raw), nil
}

func ReadPiModels(home string) ([]byte, error) {
	raw, err := SafeReadFile(filepath.Join(home, "models.json"), configLimit)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	var doc map[string]any
	if err != nil || json.Unmarshal(raw, &doc) != nil || doc == nil {
		return nil, agentError("pi_provider_configuration_invalid")
	}
	if commandCredential(doc) {
		return nil, agentError("pi_command_credential_unsupported")
	}
	return raw, nil
}

func commandCredential(value any) bool {
	switch v := value.(type) {
	case string:
		return strings.HasPrefix(v, "!")
	case []any:
		for _, item := range v {
			if commandCredential(item) {
				return true
			}
		}
	case map[string]any:
		for key, item := range v {
			if strings.HasPrefix(key, "!") || commandCredential(item) {
				return true
			}
		}
	}
	return false
}
