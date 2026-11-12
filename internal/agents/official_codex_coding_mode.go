//go:build darwin || linux

package agents

import (
	"encoding/binary"
	"errors"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// These inputs must come from the host's selected official binding, never from
// hook payloads. Runtime is the observed hook process environment; nil means it
// is unknown. An empty non-nil map explicitly means no exported overrides.
type OfficialCodexCodingModeOptions struct {
	EnvFile                   []byte
	Runtime                   map[string]string
	Endpoint, APIKey, Dataset string
}

// A configuration check is not proof of hook trust, service compatibility,
// authorization, capture, model execution or a persisted knowledge artifact.
// No credential, configured dataset or environment value is returned.
type OfficialCodexCodingModeInspection struct {
	State           string   `json:"state"`
	CodingMemory    bool     `json:"coding_memory"`
	BindingMatches  bool     `json:"binding_matches"`
	RuntimeObserved bool     `json:"runtime_observed"`
	Issues          []string `json:"issues"`
}

// InspectOfficialCodexCodingMode is read-only and evaluates only the fixed
// a176248 plugin's configuration semantics. The caller supplies the bytes from
// the env file actually selected by the hook's COGNEE_ENV_FILE (or its default),
// not an arbitrary Memlane-generated file assumed to be in use.
func InspectOfficialCodexCodingMode(o OfficialCodexCodingModeOptions) (OfficialCodexCodingModeInspection, error) {
	out := OfficialCodexCodingModeInspection{State: "configuration_unverified", RuntimeObserved: o.Runtime != nil, Issues: []string{}}
	if o.Endpoint == "" || o.APIKey == "" || o.Dataset == "" || len(o.EnvFile) > 1024*1024 {
		return out, errors.New("official_plugin_configuration_input_invalid")
	}
	env, err := parseOfficialCodexEnv(o.EnvFile)
	if err != nil {
		return out, err
	}
	// Official load_env_file uses setdefault, so even an empty exported value
	// suppresses the corresponding file value. Never use the App environment as
	// a substitute for the separate Codex process's observed environment.
	for key, value := range o.Runtime {
		env[key] = value
	}
	out.CodingMemory = officialEnvTrue(env["COGNEE_CODING_MEMORY"])
	if !out.CodingMemory {
		out.Issues = append(out.Issues, "coding_memory_disabled")
	}
	// The loader scrubs endpoint/key for the first recognized forced-local
	// switch. config.load_config separately applies the last nonempty backend
	// variable, without stripping it. Preserve both upstream behaviors.
	for _, key := range []string{"COGNEE_CODEX_BACKEND", "COGNEE_BACKEND"} {
		v := strings.ToLower(strings.TrimSpace(env[key]))
		if v == "local" || v == "native" || v == "sdk" {
			env["COGNEE_BASE_URL"], env["COGNEE_API_KEY"] = "", ""
			break
		}
		if v == "cloud" || v == "http" || v == "api" || v == "server" {
			break
		}
	}
	backend := "auto"
	for _, key := range []string{"COGNEE_BACKEND", "COGNEE_CODEX_BACKEND"} {
		if env[key] != "" {
			backend = strings.ToLower(env[key])
		}
	}
	if backend == "local" || backend == "native" || backend == "sdk" {
		env["COGNEE_BASE_URL"], env["COGNEE_API_KEY"] = "", ""
	}
	out.BindingMatches = env["COGNEE_BASE_URL"] == o.Endpoint && env["COGNEE_API_KEY"] == o.APIKey && strings.TrimSpace(env["COGNEE_PLUGIN_DATASET"]) == o.Dataset
	// The upstream rewrites spaces/dots and reserves 'auto'. Refuse these as an
	// exact binding instead of silently resolving to another dataset.
	if strings.ContainsAny(o.Dataset, " .") || strings.EqualFold(o.Dataset, "auto") {
		out.BindingMatches = false
	}
	if !out.BindingMatches {
		out.Issues = append(out.Issues, "official_binding_mismatch")
	}
	if !officialEnvTrue(env["COGNEE_MANAGED_ENDPOINT"]) {
		out.Issues = append(out.Issues, "managed_endpoint_unset")
	}
	// No shared-role expansion of the explicit project authorization.
	if !strings.EqualFold(strings.TrimSpace(env["COGNEE_SHARED_AGENT_MEMORY"]), "false") {
		out.Issues = append(out.Issues, "shared_agent_memory_not_disabled")
	}
	if !out.RuntimeObserved {
		out.Issues = append(out.Issues, "hook_environment_unobserved")
	}
	if len(out.Issues) == 0 {
		out.State = "configuration_matches"
	}
	return out, nil
}

func officialEnvTrue(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}

// Match the official stdlib dotenv subset, including BOMs, duplicate keys,
// literal quotes/comments and no shell expansion. Invalid encodings fail this
// check rather than being mistaken for an absent configuration.
func parseOfficialCodexEnv(raw []byte) (map[string]string, error) {
	var text string
	if utf8.Valid(raw) {
		text = strings.TrimPrefix(string(raw), "\ufeff")
	} else if len(raw) >= 2 && (raw[0] == 0xff && raw[1] == 0xfe || raw[0] == 0xfe && raw[1] == 0xff) && len(raw)%2 == 0 {
		var order binary.ByteOrder = binary.LittleEndian
		if raw[0] == 0xfe {
			order = binary.BigEndian
		}
		var units []uint16
		for i := 2; i < len(raw); i += 2 {
			units = append(units, order.Uint16(raw[i:i+2]))
		}
		for i := 0; i < len(units); i++ {
			if units[i] >= 0xd800 && units[i] <= 0xdbff {
				if i+1 >= len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff {
					return nil, errors.New("official_plugin_env_encoding_invalid")
				}
				i++
			} else if units[i] >= 0xdc00 && units[i] <= 0xdfff {
				return nil, errors.New("official_plugin_env_encoding_invalid")
			}
		}
		text = string(utf16.Decode(units))
	} else {
		return nil, errors.New("official_plugin_env_encoding_invalid")
	}
	out := map[string]string{}
	for _, line := range strings.FieldsFunc(text, func(r rune) bool {
		return r == '\n' || r == '\r' || r == '\v' || r == '\f' || r == '\u0085' || r == '\u2028' || r == '\u2029' || r >= '\u001c' && r <= '\u001e'
	}) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimLeftFunc(strings.TrimPrefix(line, "export "), unicode.IsSpace)
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" || strings.IndexFunc(key, func(r rune) bool { return r != '_' && !unicode.IsLetter(r) && !unicode.IsNumber(r) }) >= 0 {
			continue
		}
		if key == "PATH" || key == "HOME" || key == "PYTHONPATH" || key == "PYTHONHOME" || key == "SHELL" || key == "USER" || strings.HasPrefix(key, "LD_") || strings.HasPrefix(key, "DYLD_") {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == value[len(value)-1] && (value[0] == '\'' || value[0] == '"') {
			value = value[1 : len(value)-1]
		}
		out[key] = value
	}
	return out, nil
}
