package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

// Synthetic bundle/launcher fixtures exercise capability gates, not real IDE
// compatibility. The actual installed bundle is checked separately.
func syntheticTRAE(t *testing.T, version, commit, quality string) (string, string) {
	t.Helper()
	contents := filepath.Join(t.TempDir(), "Trae CN.app", "Contents")
	app := filepath.Join(contents, "Resources", "app")
	path := filepath.Join(app, "bin", "trae-cn")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	product, _ := json.Marshal(map[string]string{"applicationName": "trae-cn", "commit": commit, "quality": quality, "version": "1.107.1"})
	if err := os.WriteFile(filepath.Join(app, "product.json"), product, 0600); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0"?><plist version="1.0"><dict>
<key>LSUIElement</key><true/>
<key>Nested</key><dict><key>CFBundleShortVersionString</key><string>wrong</string></dict>
<key>CFBundleIdentifier</key><string>cn.trae.app</string>
<key>CFBundleShortVersionString</key><string>` + version + `</string>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(plist), 0600); err != nil {
		t.Fatal(err)
	}
	// Marker detects an unintended launch of an unverified version.
	script := "#!/bin/sh\ntouch \"$0.launched\"\ncase \"$1\" in\n--version) printf '1.107.1\\n" + commit + "\\n';;\n--help) printf 'No config file found\\nTrae CN " + version + "\\n--user-data-dir\\n';;\nesac\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path, filepath.Join(contents, "Info.plist")
}

func TestTRAEInstallationDoesNotImplyVerifiedCapability(t *testing.T) {
	const oldCommit = "5d9926ed75dce7fa36dff87dbef5fb53e06b790a"
	for _, tc := range []struct{ name, version, commit, quality string }{
		{"updated", "3.4.15", "7e0c0085ff782a64b30b0d5ce204a53492751f15", "stable"},
		{"different_build", "3.4.14", "unverified-commit", "stable"},
		{"different_channel", "3.4.14", oldCommit, "insider"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, _ := syntheticTRAE(t, tc.version, tc.commit, tc.quality)
			d := Probe(context.Background(), "trae", path)
			if d.Path != path || d.Version != tc.version || d.Channel != "cn/"+tc.quality || d.Error != "" || d.CapabilityError == "" || d.Protocol != "" || d.ConnectionError() == nil {
				t.Fatalf("installation or capability misrepresented: %+v", d)
			}
			if _, err := os.Stat(path + ".launched"); !os.IsNotExist(err) {
				t.Fatalf("unverified IDE was launched: %v", err)
			}
		})
	}
}

func TestTRAEValidatedBuildStillRequiresLauncherChecks(t *testing.T) {
	path, _ := syntheticTRAE(t, "3.4.14", "5d9926ed75dce7fa36dff87dbef5fb53e06b790a", "stable")
	d := Probe(context.Background(), "trae", path)
	if d.ConnectionError() != nil || d.Version != "3.4.14" || d.Protocol != "mcp-ide-external" {
		t.Fatalf("validated synthetic build rejected: %+v", d)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'unrelated launcher\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	d = Probe(context.Background(), "trae", path)
	if d.Error == "" || d.Protocol != "" || d.Version != "3.4.14" {
		t.Fatalf("bundle alone approved incompatible launcher: %+v", d)
	}
}

func TestTRAEUnverifiedInstallPreservesConfiguration(t *testing.T) {
	path, _ := syntheticTRAE(t, "3.4.15", "updated-commit", "stable")
	root := t.TempDir()
	workspace := filepath.Join(root, "repo")
	config := filepath.Join(workspace, ".trae", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(config), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("{\"mcpServers\":{\"user-owned\":{\"command\":\"keep\"}}}\n")
	if err := os.WriteFile(config, original, 0600); err != nil {
		t.Fatal(err)
	}
	c := records.Connection{ID: uuid.NewString(), Agent: "trae", Generation: 1, Enabled: true}
	client := filepath.Join(root, "client")
	_, err := Install(context.Background(), c, InstallOptions{ClientRoot: client, ServiceRoot: filepath.Join(root, "service"), Bridge: path, AgentPath: path, Workspace: workspace})
	if err == nil || !strings.Contains(err.Error(), "尚未验证") {
		t.Fatalf("unverified installation not fenced: %v", err)
	}
	data, err := os.ReadFile(config)
	if err != nil || string(data) != string(original) {
		t.Fatal("unverified version changed user configuration")
	}
	files, err := os.ReadDir(filepath.Dir(config))
	if err != nil || len(files) != 1 {
		t.Fatalf("unverified version created configuration artifacts: %v %v", files, err)
	}
	if _, err := os.Stat(client); !os.IsNotExist(err) {
		t.Fatalf("unverified version created an installation receipt: %v", err)
	}
}

func TestTRAEProductMetadataFailureDoesNotGuessVersion(t *testing.T) {
	path, plist := syntheticTRAE(t, "3.4.15", "updated", "stable")
	for _, data := range []string{
		`<plist><dict><key>Nested</key><dict><key>CFBundleShortVersionString</key><string>3.4.15</string></dict><key>CFBundleIdentifier</key><string>cn.trae.app</string></dict></plist>`,
		`<plist><dict><key>CFBundleIdentifier</key><string>cn.trae.app</string><key>CFBundleShortVersionString</key><string>3.4.15</string><key>CFBundleShortVersionString</key><string>3.4.14</string></dict></plist>`,
		`<plist><dict><key>CFBundleShortVersionString</key><true/></dict></plist>`,
	} {
		if err := os.WriteFile(plist, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		d := Probe(context.Background(), "trae", path)
		if d.Error == "" || d.Version != "" || d.Protocol != "" {
			t.Fatalf("ambiguous product metadata accepted: %+v", d)
		}
	}
}
