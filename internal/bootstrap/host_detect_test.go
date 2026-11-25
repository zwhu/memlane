package bootstrap

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestHostDetectFixesTraeCN(t *testing.T) {
	for _, variant := range []struct{ trae, codex string }{
		{"Trae CN.app", "Codex.app"},
		{"TRAE CN.app", "ChatGPT.app"},
	} {
		t.Run(variant.trae, func(t *testing.T) {
			home := t.TempDir()
			apps := filepath.Join(home, "Applications")
			write := func(path, data string, mode os.FileMode) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(data), mode); err != nil {
					t.Fatal(err)
				}
			}
			const plist = `<plist><dict><key>CFBundleVersion</key><string>999</string><key>CFBundleShortVersionString</key><string>3.1.4</string></dict></plist>`
			marker := filepath.Join(home, "executed")
			for _, app := range []string{variant.trae, variant.codex, "Gemini.app"} {
				write(filepath.Join(apps, app, "Contents/Info.plist"), plist, 0600)
			}
			executable := func(path string) { write(path, "#!/bin/sh\ntouch '"+marker+"'\n", 0700) }
			electron := filepath.Join(apps, variant.trae, "Contents/MacOS/Electron")
			codex := filepath.Join(apps, variant.codex, "Contents/Resources/codex-cli/bin/codex")
			agy := filepath.Join(apps, "Gemini.app/Contents/Helpers/agy")
			for _, path := range []string{electron, codex, agy} {
				executable(path)
			}
			lookup := func(name string) (string, error) {
				if name == "" {
					t.Fatal("looked up an empty command")
				}
				return "", os.ErrNotExist
			}
			opts := HostDetectOptions{Home: home, AppDirs: []string{apps}, LookPath: lookup, Environ: func(string) string { return "" }, BetaPrimaryOnly: true}
			hosts := DetectLocalHosts(opts)
			if len(hosts) != 5 {
				t.Fatal(hosts)
			}
			byKind := map[string]DetectedHost{}
			for _, host := range hosts {
				byKind[host.Kind] = host
			}
			for kind, executable := range map[string]string{"trae_ide": electron, "codex": codex, "antigravity": agy} {
				if host := byKind[kind]; !host.Detected || !sameHostFile(host.Executable, executable) || host.Version != "3.1.4" {
					t.Fatalf("%s: %+v", kind, host)
				}
			}
			if host := byKind["traex"]; host.Detected || host.Application != "" {
				t.Fatal(host)
			}
			bunCodex := filepath.Join(home, ".bun/bin/codex")
			executable(bunCodex)
			if got := DetectLocalHosts(opts)[1]; got.Executable != bunCodex || got.Application != filepath.Join(apps, variant.codex) {
				t.Fatal(got)
			}
			diagnoses := map[string]DiagnosticAgent{}
			for _, agent := range ExistingDiagnosticAgents(opts.AppDirs, lookup) {
				diagnoses[agent.Name] = agent
			}
			if !sameHostFile(diagnoses["TRAE CN"].Application, filepath.Join(apps, variant.trae)) || diagnoses["Antigravity"].Executable != agy {
				t.Fatal(diagnoses)
			}
			if _, found := diagnoses["TraeX"]; found {
				t.Fatal(diagnoses)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("host executable ran", err)
			}
			write(filepath.Join(apps, variant.codex, "Contents/Info.plist"), `<plist><dict><key>CFBundleVersion</key><string>999</string></dict></plist>`, 0600)
			if got := ReadBundleVersion(filepath.Join(apps, variant.codex)); got != "999" {
				t.Fatal(got)
			}
		})
	}
}

func TestHostProcessingCapabilities(t *testing.T) {
	want := []RecommendedProcessingCLI{
		{
			Kind: "codex", DisplayName: "Codex", BundlesProcessingCLI: true,
			InstallCommand: "npm i -g @openai/codex",
			DocsURL:        "https://developers.openai.com/codex/cli", LoginHint: "安装后在终端运行 codex 完成登录",
		},
		{
			Kind: "pi", DisplayName: "pi",
			InstallCommand: "npm i -g @mariozechner/pi-coding-agent",
			DocsURL:        "https://github.com/badlogic/pi-mono", LoginHint: "安装后运行 pi 并配置模型登录",
		},
		{
			Kind: "traex", DisplayName: "TRAE CLI",
			InstallCommand: "curl -fsSL https://lf-cdn.trae.com.cn/obj/trae-com-cn/pkg/cli/install.sh | bash",
			DocsURL:        "https://www.trae.cn/", LoginHint: "安装后运行 traecli / traex 完成登录",
		},
		{
			Kind: "antigravity", DisplayName: "Antigravity", BundlesProcessingCLI: true,
			DocsURL: "https://gemini.google.com/", LoginHint: "安装并登录 Gemini App 后即可使用内置 agy CLI",
		},
	}
	if got := RecommendedProcessingCLIs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("recommended CLIs = %+v, want %+v", got, want)
	}
	processing := make(map[string]RecommendedProcessingCLI)
	for _, cli := range want {
		processing[cli.Kind] = cli
	}
	hosts := DetectLocalHosts(HostDetectOptions{
		Home: t.TempDir(), AppDirs: []string{},
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
		Environ:  func(string) string { return "" },
	})
	var kinds []string
	for _, host := range hosts {
		kinds = append(kinds, host.Kind)
		cli, supported := processing[host.Kind]
		if !host.SupportsCapture || host.SupportsProcessingCLI != supported ||
			host.BundlesProcessingCLI != cli.BundlesProcessingCLI || host.InstallCommand != cli.InstallCommand ||
			host.DocsURL != cli.DocsURL || host.LoginHint != cli.LoginHint {
			t.Errorf("unexpected capabilities for %s: %+v", host.Kind, host)
		}
	}
	wantKinds := []string{"trae_ide", "codex", "pi", "traex", "antigravity", "claude_code", "cursor", "opencode"}
	if !reflect.DeepEqual(kinds, wantKinds) {
		t.Fatalf("detected host kinds = %v, want %v", kinds, wantKinds)
	}
}

func TestHasDetectedProcessingCLI(t *testing.T) {
	ineligible := []DetectedHost{
		{Detected: true, Executable: "/app/Electron"},
		{Detected: true, SupportsProcessingCLI: true, Application: "/app/Codex.app"},
		{SupportsProcessingCLI: true, Executable: "/bin/pi"},
	}
	if HasDetectedProcessingCLI(nil) || HasDetectedProcessingCLI(ineligible) {
		t.Fatal("reported a processing CLI without a detected, supported executable")
	}
	if !HasDetectedProcessingCLI(append(ineligible, DetectedHost{Detected: true, SupportsProcessingCLI: true, Executable: "/bin/pi"})) {
		t.Fatal("missed a detected, supported executable")
	}
}

func sameHostFile(first, second string) bool {
	a, err := os.Stat(first)
	if err != nil {
		return false
	}
	b, err := os.Stat(second)
	return err == nil && os.SameFile(a, b)
}
