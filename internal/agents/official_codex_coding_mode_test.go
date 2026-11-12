//go:build darwin || linux

package agents

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
)

const codingModeFixture = `COGNEE_CODING_MEMORY=true
COGNEE_CODEX_BACKEND=cloud
COGNEE_BASE_URL=http://127.0.0.1:12345
COGNEE_API_KEY=synthetic-key
COGNEE_PLUGIN_DATASET=synthetic_dataset
COGNEE_MANAGED_ENDPOINT=true
COGNEE_SHARED_AGENT_MEMORY=false
`

func codingModeOptions(raw string, runtime map[string]string) OfficialCodexCodingModeOptions {
	return OfficialCodexCodingModeOptions{EnvFile: []byte(raw), Runtime: runtime, Endpoint: "http://127.0.0.1:12345", APIKey: "synthetic-key", Dataset: "synthetic_dataset"}
}

func TestOfficialCodexCodingMode(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runtime map[string]string
		issue   string
	}{
		{"explicit_environment", map[string]string{}, ""},
		{"unobserved_environment", nil, "hook_environment_unobserved"},
		{"empty_override", map[string]string{"COGNEE_CODING_MEMORY": ""}, "coding_memory_disabled"},
		{"disabled_override", map[string]string{"COGNEE_CODING_MEMORY": "false"}, "coding_memory_disabled"},
		{"local_override", map[string]string{"COGNEE_CODEX_BACKEND": "local"}, "official_binding_mismatch"},
		{"other_endpoint", map[string]string{"COGNEE_BASE_URL": "http://127.0.0.1:54321"}, "official_binding_mismatch"},
		{"other_key", map[string]string{"COGNEE_API_KEY": "other-secret"}, "official_binding_mismatch"},
		{"other_dataset", map[string]string{"COGNEE_PLUGIN_DATASET": "other"}, "official_binding_mismatch"},
		{"shared_role", map[string]string{"COGNEE_SHARED_AGENT_MEMORY": "true"}, "shared_agent_memory_not_disabled"},
		{"unmanaged", map[string]string{"COGNEE_MANAGED_ENDPOINT": "false"}, "managed_endpoint_unset"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := codingModeOptions(codingModeFixture, tc.runtime)
			before := append([]byte(nil), o.EnvFile...)
			got, err := InspectOfficialCodexCodingMode(o)
			if err != nil {
				t.Fatal(err)
			}
			if tc.issue == "" {
				if got.State != "configuration_matches" {
					t.Fatal(got)
				}
			} else if !strings.Contains(strings.Join(got.Issues, ","), tc.issue) || got.State == "configuration_matches" {
				t.Fatal(got)
			}
			if !reflect.DeepEqual(before, o.EnvFile) {
				t.Fatal("mutated input")
			}
			raw, _ := json.Marshal(got)
			for _, secret := range []string{o.APIKey, o.Endpoint, o.Dataset, "other-secret"} {
				if strings.Contains(string(raw), secret) {
					t.Fatal("configuration value disclosed")
				}
			}
		})
	}
}

func TestOfficialCodexEnvLiteralAndEncoding(t *testing.T) {
	raw := "# comment\r\nexport COGNEE_CODING_MEMORY = 'false'\nCOGNEE_CODING_MEMORY=\"true\"\nPATH=unsafe\nBAD-KEY=ignored\nLITERAL='${HOME} $(touch /tmp/no)'\nINLINE=value # literal\n"
	want := map[string]string{"COGNEE_CODING_MEMORY": "true", "LITERAL": "${HOME} $(touch /tmp/no)", "INLINE": "value # literal"}
	inputs := [][]byte{[]byte(raw), append([]byte{0xef, 0xbb, 0xbf}, []byte(raw)...)}
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		buf := make([]byte, 2)
		order.PutUint16(buf, 0xfeff)
		for _, u := range utf16.Encode([]rune(raw)) {
			b := make([]byte, 2)
			order.PutUint16(b, u)
			buf = append(buf, b...)
		}
		inputs = append(inputs, buf)
	}
	for i, input := range inputs {
		got, err := parseOfficialCodexEnv(input)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("encoding %d: %v %v", i, got, err)
		}
	}
	for _, input := range [][]byte{{0xff}, {0xff, 0xfe, 0x00, 0xd8}, {0xff, 0xfe, 0x00}} {
		if _, err := parseOfficialCodexEnv(input); err == nil {
			t.Fatal("accepted invalid encoding")
		}
	}
}

// Optional real-upstream contract check, run explicitly with a verified fixed
// source tree. It imports only upstream config in a synthetic HOME. It does not
// run hooks, contact the service, inspect sessions or use a model.
func TestOfficialCodexCodingModeUpstreamContract(t *testing.T) {
	scripts := os.Getenv("MEMLANE_OFFICIAL_PLUGIN_SCRIPTS")
	if scripts == "" {
		t.Skip("explicit verified upstream source required")
	}
	for _, runtime := range []map[string]string{
		{}, {"COGNEE_CODING_MEMORY": ""}, {"COGNEE_CODEX_BACKEND": "local"},
		{"COGNEE_CODEX_BACKEND": "invalid", "COGNEE_BACKEND": "local"},
		{"COGNEE_CODEX_BACKEND": " local ", "COGNEE_BACKEND": "cloud"},
		{"COGNEE_CODEX_BACKEND": "cloud", "COGNEE_BACKEND": "local"},
		{"COGNEE_PLUGIN_DATASET": "auto"}, {"COGNEE_PLUGIN_DATASET": "a.b"},
	} {
		home := t.TempDir()
		file := filepath.Join(home, "fixture.env")
		if err := os.WriteFile(file, []byte(codingModeFixture), 0600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("python3", "-B", "-c", `import sys,json
sys.path.insert(0,sys.argv[1])
import config
c=config.load_config()
print(json.dumps({'coding':config.coding_memory_enabled(),'binding':c['base_url']=='http://127.0.0.1:12345' and c['api_key']=='synthetic-key' and c['dataset']=='synthetic_dataset'}))`, scripts)
		cmd.Dir = home
		cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "COGNEE_ENV_FILE=" + file}
		for k, v := range runtime {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		b, err := cmd.Output()
		if err != nil {
			t.Fatalf("upstream config import: %v", err)
		}
		var actual struct {
			Coding  bool
			Binding bool
		}
		if json.Unmarshal(b, &actual) != nil {
			t.Fatal("invalid upstream reply")
		}
		got, err := InspectOfficialCodexCodingMode(codingModeOptions(codingModeFixture, runtime))
		if err != nil || actual.Coding != got.CodingMemory || actual.Binding != got.BindingMatches {
			t.Fatalf("upstream mismatch: runtime keys %v actual %+v inspection %+v err %v", reflect.ValueOf(runtime).MapKeys(), actual, got, err)
		}
	}
}
