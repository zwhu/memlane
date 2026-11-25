package setup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/zwhu/memlane/kit/aiinstaller"
)

func TestMemlaneRequirementsUseFixedManifest(t *testing.T) {
	root := t.TempDir()
	for _, arch := range []string{"arm64", "amd64"} {
		t.Run(arch, func(t *testing.T) {
			m, err := LoadManifests(os.DirFS("../../engine"), arch)
			if err != nil {
				t.Fatal(err)
			}
			native := m.Native != nil
			if native != (arch == "arm64") {
				t.Fatalf("native package availability = %v for %s", native, arch)
			}
			lockHash := sha256.Sum256(m.Lock)
			digest := hex.EncodeToString(lockHash[:])
			official := BuildRequirements(root, m, digest, aiinstaller.DownloadSourceOfficial, m.Native)
			china := BuildRequirements(root, m, digest, "", m.Native)
			var ids []string
			for i, r := range official {
				ids = append(ids, r.ID)
				if r.Source != r.OfficialSource || r.Source == "" || len(r.Digest) != 64 {
					t.Fatalf("official requirement %s has invalid source or digest: %+v", r.ID, r)
				}
				if china[i].Source == r.Source || china[i].OfficialSource != r.Source || china[i].Digest != r.Digest || china[i].Destination != r.Destination {
					t.Fatalf("mirror selection changed pinned identity for %s: %+v", r.ID, china[i])
				}
				if r.ID == "runtime" && (r.Destination != filepath.Join(root, "runtime-"+digest[:12]) || strings.Contains(r.Version, "OpenSSL") != native) {
					t.Fatalf("runtime scope = %+v", r)
				}
			}
			if !reflect.DeepEqual(ids, ComponentIDs()) {
				t.Fatalf("component order = %v", ids)
			}
		})
	}
}

func TestLoadManifestsValidatesSyntheticAssets(t *testing.T) {
	commit := strings.Repeat("1", 40)
	digest := strings.Repeat("2", 64)
	pin := `{"version":"1.0","darwin_arm64":{"url":"https://example.test/tool","sha256":"` + digest + `"}}`
	assets := fstest.MapFS{
		"first-install-components.json": {Data: []byte(`{"schema":1,"python":` + pin + `,"uv":` + pin + `}`)},
		"coding-memory-components.json": {Data: []byte(`{
			"service":{"commit":"` + commit + `","archive_sha256":"` + digest + `","archive_url":"https://example.test/service","packages":["engine"]}
		}`)},
		"embedding-components.json": {Data: []byte(`{
			"schema":1,"model":"example/model","dimensions":64,"repositories":[{"repo":"example/model","revision":"` + commit + `","files":[]}]
		}`)},
		"requirements.lock": {Data: []byte("example==1.0 --hash=sha256:" + digest + "\n")},
	}
	m, err := LoadManifests(assets, "arm64")
	if err != nil || m.UV.Artifact.SHA256 != digest || string(m.Lock) != string(assets["requirements.lock"].Data) {
		t.Fatalf("LoadManifests = %+v, %v", m, err)
	}
	delete(assets, "requirements.lock")
	m, err = LoadManifests(assets, "arm64")
	if err != nil || !bytes.Equal(m.Lock, defaultRequirementsLock) || len(m.Lock) == 0 {
		t.Fatalf("embedded requirements fallback failed: %v", err)
	}
	assets["requirements.lock"] = &fstest.MapFile{Data: []byte(" \n")}
	if _, err := LoadManifests(assets, "arm64"); err == nil {
		t.Fatal("empty explicit requirements lock accepted")
	}
}

type recordingDriver struct {
	probes, installs int
}

func (d *recordingDriver) Probe(context.Context, aiinstaller.ComponentSpec) (aiinstaller.Observation, error) {
	d.probes++
	return aiinstaller.Observation{Status: aiinstaller.Available}, nil
}

func (d *recordingDriver) Install(context.Context, aiinstaller.ComponentSpec) error {
	d.installs++
	return nil
}

func TestDriverRouterRejectsScopeChanges(t *testing.T) {
	ctx := context.Background()
	spec := aiinstaller.ComponentSpec{ID: "runtime", Destination: "/runtime"}
	driver := &recordingDriver{}
	router := DriverRouter{Specs: []aiinstaller.ComponentSpec{spec}, Drivers: map[string]aiinstaller.Driver{spec.ID: driver}}
	modified := spec
	modified.Destination = "/other"
	if _, err := router.Probe(ctx, modified); err == nil {
		t.Fatal("probe accepted a changed scope")
	}
	if err := router.Install(ctx, modified); err == nil {
		t.Fatal("install accepted a changed scope")
	}
	if driver.probes != 0 || driver.installs != 0 {
		t.Fatal("a changed scope reached the component driver")
	}
	observed, err := router.Probe(ctx, spec)
	if err != nil || observed.Status != aiinstaller.Available {
		t.Fatalf("probe = %+v, %v", observed, err)
	}
	if err := router.Install(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if driver.probes != 1 || driver.installs != 1 {
		t.Fatalf("fixed scope dispatch = %+v", driver)
	}
}

func TestNewDownloaderScopesPublicHomebrewHeader(t *testing.T) {
	downloader := NewDownloader()
	if got := downloader.Options.UserAgent; got != "Memlane/0.1.0-beta.3" {
		t.Fatalf("user agent = %q", got)
	}
	hosts := map[string]bool{
		"https://ghcr.io/v2/homebrew/core/openssl/3/blobs/sha256:x":                          true,
		"https://ghcr.io/v2/other/core/openssl/3/blobs/sha256:x":                             false,
		"https://mirrors.ustc.edu.cn/homebrew-bottles/v2/homebrew/core/openssl/3/blobs/file": false,
		"https://ghcr.io.example.com/v2/homebrew/core/openssl/3/blobs/file":                  false,
	}
	for address, authorized := range hosts {
		u, err := url.Parse(address)
		if err != nil {
			t.Fatal(err)
		}
		want := ""
		if authorized {
			want = "Bearer QQ=="
		}
		if got := downloader.Options.ExtraHeaders(u).Get("Authorization"); got != want {
			t.Errorf("%s authorization = %q, want %q", address, got, want)
		}
		if got := downloader.Options.AllowAuthOnRedirect(u); got != authorized {
			t.Errorf("%s allows authorization on redirect = %v", address, got)
		}
	}
}

func TestMemlaneBrandDiagnosticsAndManualCommands(t *testing.T) {
	brand := MemlaneBrand()
	if brand.Name != "Memlane" || brand.StateDirName != "Memlane" || brand.CLIEnvPrefix != "MEMLANE" || brand.UserAgent != "Memlane/"+DefaultVersion {
		t.Fatalf("brand = %+v", brand)
	}
	input := aiinstaller.DiagnosticInput{Error: "password=private-value"}
	report, prompt, err := Diagnostics(input)
	if err != nil || !strings.Contains(prompt, brand.ExecutablePath+" doctor --json") || !strings.Contains(prompt, brand.PrivacyGuardNote) || strings.Contains(report, "private-value") {
		t.Fatalf("Diagnostics() = %s, %s, %v", report, prompt, err)
	}
	archive, err := DiagnosticArchive(input)
	if err != nil || !strings.HasPrefix(string(archive), "PK") {
		t.Fatalf("DiagnosticArchive() = %d bytes, %v", len(archive), err)
	}
	item := aiinstaller.Item{
		Requirement: aiinstaller.ComponentSpec{ID: "runtime", Name: "Runtime", Destination: filepath.Join(t.TempDir(), "runtime")},
		Observation: aiinstaller.Observation{Status: aiinstaller.Missing},
	}
	cmd := ManualInstallCommand(item, ManualCommandContext{}, aiinstaller.DownloadSourceOfficial)
	if !strings.Contains(cmd, brand.ExecutablePath+" install --plan --component runtime --mirror official --json") ||
		!strings.Contains(cmd, "install --apply --plan-digest <PLAN_DIGEST>") {
		t.Fatal(cmd)
	}
	script := ManualInstallScript(aiinstaller.Plan{Items: []aiinstaller.Item{item}}, ManualCommandContext{}, aiinstaller.DownloadSourceOfficial)
	if !strings.Contains(script, "# === Runtime ===\n"+cmd) || !strings.Contains(script, "回到 "+brand.Name) {
		t.Fatal(script)
	}
}
