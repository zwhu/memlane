// Package setup configures the shared installer for Memlane.
package setup

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zwhu/memlane/kit/aiinstaller"
)

const DefaultVersion = "0.1.0-beta.3"

func MemlaneBrand() aiinstaller.AppBrand {
	return aiinstaller.AppBrand{
		Name:                "Memlane",
		ExecutablePath:      "/Applications/Memlane.app/Contents/MacOS/Memlane",
		UserAgent:           "Memlane/" + DefaultVersion,
		StateDirName:        "Memlane",
		CLIEnvPrefix:        "MEMLANE",
		DoctorArgs:          []string{"doctor", "--json"},
		PrivacyGuardNote:    "不要读取或复制凭证、登录文件、聊天记录、真实记忆内容，不创建替代日常库。",
		DiagnosticRetryNote: "保留已完成进度、原操作ID和同一问题累计失败次数；累计第4次验收失败就停止并汇报。",
	}
}

func ComponentIDs() []string {
	return []string{"uv", "python", "runtime", "cognee-source", "embedding"}
}

func DisplaySourceURL(id, officialURL, officialSHA, downloadSource string) string {
	mode := aiinstaller.NormalizeDownloadSource(downloadSource)
	if mode != aiinstaller.DownloadSourceChina {
		return officialURL
	}
	switch id {
	case "uv", "python":
		return aiinstaller.ToolCandidates(id, officialURL, officialSHA, mode)[0].URL
	case "runtime":
		return aiinstaller.PyPIIndexURLs(mode)[0]
	case "cognee-source":
		return aiinstaller.SourceCandidates(officialURL, officialSHA, mode)[0].URL
	case "embedding":
		return "https://modelscope.cn/models/Qdrant/bge-small-zh-v1.5/resolve/master/model_optimized.onnx"
	default:
		return officialURL
	}
}

func Diagnostics(input aiinstaller.DiagnosticInput) (report, prompt string, err error) {
	return aiinstaller.Diagnostics(MemlaneBrand(), input)
}

func DiagnosticArchive(input aiinstaller.DiagnosticInput) ([]byte, error) {
	return aiinstaller.DiagnosticArchive(MemlaneBrand(), input)
}

func ExportDiagnostic(path string, input aiinstaller.DiagnosticInput) error {
	return aiinstaller.ExportDiagnostic(MemlaneBrand(), path, input)
}

func NewInstaller(root string, driver aiinstaller.Driver) aiinstaller.Installer {
	return aiinstaller.Installer{Root: root, Driver: driver}
}

func NewDownloader() aiinstaller.Downloader {
	return aiinstaller.Downloader{Options: aiinstaller.DownloadOptions{
		UserAgent:             MemlaneBrand().UserAgent,
		ResponseHeaderTimeout: 30 * time.Second,
		ReadStallTimeout:      30 * time.Second,
		TotalTimeout:          30 * time.Minute,
		ExtraHeaders: func(u *url.URL) http.Header {
			if !aiinstaller.IsHomebrewRegistryURL(u) {
				return nil
			}
			// GHCR uses this fixed header for anonymous pulls of public Homebrew bottles.
			return http.Header{"Authorization": {"Bearer QQ=="}}
		},
		AllowAuthOnRedirect: aiinstaller.IsHomebrewRegistryURL,
	}}
}

// ManualCommandContext describes the manifests needed to render individual commands.
type ManualCommandContext struct {
	ModelSpec  *ModelSpec
	NativeSpec *NativeSupportSpec
}

// ManualInstallCommand returns copyable terminal commands using the user's real paths
// and selected download source so users can prepare a missing dependency manually and
// click "重新检测" to verify SHA256 digests and continue setup.
func ManualInstallCommand(item aiinstaller.Item, ctx ManualCommandContext, downloadSource string) string {
	mode := aiinstaller.NormalizeDownloadSource(downloadSource)
	r := item.Requirement
	switch r.ID {
	case "uv":
		if mode == aiinstaller.DownloadSourceChina {
			return strings.Join([]string{
				"# 方式一（推荐国内 PyPI 镜像或 Homebrew）：",
				"brew install uv",
				"# 或运行官网安装脚本（默认安装到 ~/.local/bin/uv）：",
				"curl -LsSf https://astral.sh/uv/install.sh | sh",
			}, "\n")
		}
		return strings.Join([]string{
			"curl -LsSf https://astral.sh/uv/install.sh | sh",
			"# 或 Homebrew：brew install uv",
		}, "\n")
	case "python":
		if mode == aiinstaller.DownloadSourceChina {
			return strings.Join([]string{
				"UV_PYTHON_INSTALL_MIRROR=\"https://registry.npmmirror.com/-/binary/python-build-standalone/\" \\",
				"  uv python install 3.12.12",
				"# 若已安装 Homebrew：brew install python@3.12",
			}, "\n")
		}
		return strings.Join([]string{
			"uv python install 3.12.12",
			"# 或 Homebrew：brew install python@3.12",
		}, "\n")
	case "cognee-source":
		target := filepath.Join(r.Destination, "service.tar.gz")
		official := r.OfficialSource
		if official == "" {
			official = r.Source
		}
		url := aiinstaller.SourceCandidates(official, r.Digest, mode)[0].URL
		lines := []string{
			"curl -fL --create-dirs -o " + aiinstaller.ShellLiteral(target) + " " + aiinstaller.ShellLiteral(url),
		}
		if mode == aiinstaller.DownloadSourceChina && official != "" && official != url {
			lines = append(lines, "# 国外官方地址："+official)
		}
		return strings.Join(lines, "\n")
	case "embedding":
		if ctx.ModelSpec == nil || len(ctx.ModelSpec.Repositories) == 0 {
			return "# 模型清单尚未加载，请先点击「重新检测」"
		}
		var lines []string
		for _, repo := range ctx.ModelSpec.Repositories {
			hubRepo := filepath.Join(r.Destination, "hub", "models--"+strings.ReplaceAll(repo.Repo, "/", "--"))
			snap := filepath.Join(hubRepo, "snapshots", repo.Revision)
			refs := filepath.Join(hubRepo, "refs", "main")
			lines = append(lines, "mkdir -p "+aiinstaller.ShellLiteral(filepath.Dir(refs))+" && printf %s "+aiinstaller.ShellLiteral(repo.Revision)+" > "+aiinstaller.ShellLiteral(refs))
			for _, f := range repo.Files {
				url := aiinstaller.ModelFileCandidates(repo.Repo, aiinstaller.ModelFileRef{Name: f.Name, SHA256: f.SHA256, URL: f.URL}, mode)[0].URL
				lines = append(lines, "curl -fL --create-dirs -o "+aiinstaller.ShellLiteral(filepath.Join(snap, f.Name))+" "+aiinstaller.ShellLiteral(url))
			}
		}
		return strings.Join(lines, "\n")
	case "runtime":
		venvPython := filepath.Join(r.Destination, "venv", "bin", "python")
		lockPath := filepath.Join(r.Destination, "requirements.lock")
		var lines []string
		if info, err := os.Stat(lockPath); err != nil || !info.Mode().IsRegular() {
			const lockNote = "requirements.lock 内嵌在 Memlane 中，安装时会自动写入。"
			lines = append(lines, aiinstaller.ManualCLIInstallCommand(MemlaneBrand(), r.ID, mode, lockNote))
			if mode == aiinstaller.DownloadSourceChina {
				lines = append(lines, "# 将使用 PyPI 镜像源："+aiinstaller.PyPIIndexURLs(mode)[0])
			}
		} else {
			sync := "uv pip sync --python " + aiinstaller.ShellLiteral(venvPython)
			if mode == aiinstaller.DownloadSourceChina {
				sync += " --default-index " + aiinstaller.ShellLiteral(aiinstaller.PyPIIndexURLs(mode)[0])
			}
			lines = append(lines,
				"uv venv --python 3.12 "+aiinstaller.ShellLiteral(filepath.Join(r.Destination, "venv")),
				sync+" --require-hashes --build-constraints "+aiinstaller.ShellLiteral(lockPath)+" "+aiinstaller.ShellLiteral(lockPath),
			)
		}
		if ctx.NativeSpec != nil {
			nativeArchive := filepath.Join(r.Destination, "native", "artifact.tar.gz")
			nativeURL := aiinstaller.NativeCandidates(ctx.NativeSpec.Artifact.URL, ctx.NativeSpec.Artifact.SHA256, mode)[0].URL
			if mode == aiinstaller.DownloadSourceOfficial {
				lines = append(lines, "curl -fL --create-dirs -H \"Authorization: Bearer QQ==\" -o "+aiinstaller.ShellLiteral(nativeArchive)+" "+aiinstaller.ShellLiteral(nativeURL))
			} else {
				lines = append(lines, "curl -fL --create-dirs -o "+aiinstaller.ShellLiteral(nativeArchive)+" "+aiinstaller.ShellLiteral(nativeURL))
			}
		}
		return strings.Join(lines, "\n")
	default:
		return ""
	}
}

// ManualInstallScript joins copyable terminal commands for missing dependencies.
func ManualInstallScript(plan aiinstaller.Plan, ctx ManualCommandContext, downloadSource string) string {
	return aiinstaller.ManualInstallScript(MemlaneBrand(), plan, func(item aiinstaller.Item) string {
		return ManualInstallCommand(item, ctx, downloadSource)
	})
}
