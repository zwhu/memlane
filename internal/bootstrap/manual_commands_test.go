package bootstrap

import (
	"os"
	"path/filepath"

	"context"
	"strings"
	"testing"

	"github.com/zwhu/memlane/engine"
)

func TestManualInstallCommandsReflectSourceModeAndExcludeSecrets(t *testing.T) {
	root := "/tmp/memlane-manual-cmd-test"
	env, err := NewEnvironment(context.Background(), engine.BootstrapAssets, EnvironmentOptions{
		Root:           root,
		DownloadSource: DownloadSourceChina,
	})
	if err != nil {
		t.Fatalf("NewEnvironment failed: %v", err)
	}
	var items []Item
	for _, id := range []string{"uv", "python", "runtime", "cognee-source", "embedding", "agent-plugin"} {
		r, reqErr := env.Requirement(id)
		if reqErr != nil {
			t.Fatalf("Requirement(%s): %v", id, reqErr)
		}
		items = append(items, Item{Requirement: r, Observation: Observation{Status: Missing}})
	}
	plan := Plan{Items: items}

	chinaScript := ManualInstallScript(plan, env, DownloadSourceChina)
	for _, want := range []string{
		"astral.sh/uv/install.sh",
		"registry.npmmirror.com/-/binary/python-build-standalone/",
		"pypi.tuna.tsinghua.edu.cn/simple/",
		"mirrors.ustc.edu.cn/homebrew-bottles/v2/homebrew/core/",
		"modelscope.cn/models/Qdrant/bge-small-zh-v1.5/resolve/master/model_optimized.onnx",
		"gh-proxy.org/https://codeload.github.com/topoteretes/cognee/tar.gz/",
		"重新检测",
	} {
		if !strings.Contains(chinaScript, want) {
			t.Fatalf("china manual script missing %q\n%s", want, chinaScript)
		}
	}

	officialScript := ManualInstallScript(plan, env, DownloadSourceOfficial)
	for _, want := range []string{
		"uv python install 3.12.12",
		"ghcr.io/v2/homebrew/core/openssl/3/blobs/sha256:",
		"huggingface.co/Qdrant/bge-small-zh-v1.5/resolve/",
		"codeload.github.com/topoteretes/cognee/tar.gz/",
	} {
		if !strings.Contains(officialScript, want) {
			t.Fatalf("official manual script missing %q\n%s", want, officialScript)
		}
	}
	clean := func(s string) string {
		// Strip the fixed anonymous public GHCR pull header (`Bearer QQ==`) before secret scanning.
		return strings.ReplaceAll(strings.ToLower(s), "authorization: bearer qq==", "")
	}
	for _, forbidden := range []string{"authorization:", "bearer ", "access_token", "refresh_token", "api_key", "sk-", "eyj", "password", "secret"} {
		if strings.Contains(clean(chinaScript), forbidden) || strings.Contains(clean(officialScript), forbidden) {
			t.Fatalf("manual script must never include %q", forbidden)
		}
	}
}

func TestManualRuntimeCommandsRequireMaterializedLock(t *testing.T) {
	item := Item{Requirement: Requirement{ID: "runtime", Destination: t.TempDir()}}
	lock := filepath.Join(item.Requirement.Destination, "requirements.lock")
	script := ManualInstallCommand(item, nil, DownloadSourceChina)
	if strings.Contains(script, "uv pip sync") || strings.Contains(script, lock) ||
		!strings.Contains(script, "install --plan --component runtime --mirror china --json") ||
		!strings.Contains(script, "install --apply --plan-digest <PLAN_DIGEST> --component runtime") ||
		!strings.Contains(script, "artifacts.plan_digest") || !strings.Contains(script, PyPIIndexURLs(DownloadSourceChina)[0]) {
		t.Fatal(script)
	}
	if err := os.WriteFile(lock, []byte("# materialized"), 0600); err != nil {
		t.Fatal(err)
	}
	script = ManualInstallCommand(item, nil, DownloadSourceChina)
	if !strings.Contains(script, "uv pip sync") || !strings.Contains(script, lock) {
		t.Fatal(script)
	}
}
