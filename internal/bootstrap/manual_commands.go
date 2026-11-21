package bootstrap

import (
	"path/filepath"
	"strings"
)

// ManualInstallCommand returns copyable terminal commands using the user's real paths
// and selected download source so users can prepare a missing dependency manually and
// click "重新检测" to verify SHA256 digests and continue setup.
func ManualInstallCommand(item Item, env *Environment, downloadSource string) string {
	mode := NormalizeDownloadSource(downloadSource)
	r := item.Requirement
	switch r.ID {
	case "uv":
		if mode == DownloadSourceChina {
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
		if mode == DownloadSourceChina {
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
	case "cognee-source", "agent-plugin":
		if env != nil && r.ID == "agent-plugin" {
			target := filepath.Join(r.Destination, "integrations-upstream", "service.tar.gz")
			url := SourceCandidates(env.Plugin.Spec.URL, env.Plugin.Spec.SHA256, mode)[0].URL
			lines := []string{
				"curl -fL --create-dirs -o " + shellLiteral(target) + " " + shellLiteral(url),
			}
			if mode == DownloadSourceChina {
				lines = append(lines, "# 国外官方地址："+env.Plugin.Spec.URL)
			}
			return strings.Join(lines, "\n")
		}
		target := filepath.Join(r.Destination, "service.tar.gz")
		official := r.OfficialSource
		if official == "" {
			official = r.Source
		}
		url := SourceCandidates(official, r.Digest, mode)[0].URL
		lines := []string{
			"curl -fL --create-dirs -o " + shellLiteral(target) + " " + shellLiteral(url),
		}
		if mode == DownloadSourceChina && official != "" && official != url {
			lines = append(lines, "# 国外官方地址："+official)
		}
		return strings.Join(lines, "\n")
	case "embedding":
		if env == nil || len(env.Model.Spec.Repositories) == 0 {
			return "# 模型清单尚未加载，请先点击「重新检测」"
		}
		var lines []string
		for _, repo := range env.Model.Spec.Repositories {
			snap := modelSnapshot(r.Destination, repo)
			refs := filepath.Join(r.Destination, "hub", "models--"+strings.ReplaceAll(repo.Repo, "/", "--"), "refs", "main")
			lines = append(lines, "mkdir -p "+shellLiteral(filepath.Dir(refs))+" && printf %s "+shellLiteral(repo.Revision)+" > "+shellLiteral(refs))
			for _, f := range repo.Files {
				url := ModelFileCandidates(repo.Repo, f, mode)[0].URL
				lines = append(lines, "curl -fL --create-dirs -o "+shellLiteral(filepath.Join(snap, f.Name))+" "+shellLiteral(url))
			}
		}
		return strings.Join(lines, "\n")
	case "runtime":
		venvPython := filepath.Join(r.Destination, "venv", "bin", "python")
		lockPath := filepath.Join(r.Destination, "requirements.lock")
		lines := []string{
			"uv venv --python 3.12 " + shellLiteral(filepath.Join(r.Destination, "venv")),
		}
		if mode == DownloadSourceChina {
			lines = append(lines,
				"uv pip sync --python "+shellLiteral(venvPython)+" --default-index "+shellLiteral(PyPIIndexURLs(mode)[0])+" --require-hashes --build-constraints "+shellLiteral(lockPath)+" "+shellLiteral(lockPath),
			)
		} else {
			lines = append(lines,
				"uv pip sync --python "+shellLiteral(venvPython)+" --require-hashes --build-constraints "+shellLiteral(lockPath)+" "+shellLiteral(lockPath),
			)
		}
		if env != nil && env.Runtime.Native != nil {
			nativeArchive := filepath.Join(r.Destination, "native", "artifact.tar.gz")
			nativeURL := NativeCandidates(env.Runtime.Native.Artifact.URL, env.Runtime.Native.Artifact.SHA256, mode)[0].URL
			if mode == DownloadSourceOfficial {
				lines = append(lines, "curl -fL --create-dirs -H \"Authorization: Bearer QQ==\" -o "+shellLiteral(nativeArchive)+" "+shellLiteral(nativeURL))
			} else {
				lines = append(lines, "curl -fL --create-dirs -o "+shellLiteral(nativeArchive)+" "+shellLiteral(nativeURL))
			}
		}
		return strings.Join(lines, "\n")
	default:
		return ""
	}
}

// ManualInstallScript joins copyable terminal commands for all missing/incompatible items.
func ManualInstallScript(plan Plan, env *Environment, downloadSource string) string {
	var sections []string
	for _, item := range plan.Items {
		if item.Observation.Status == Available {
			continue
		}
		cmd := ManualInstallCommand(item, env, downloadSource)
		if cmd == "" {
			continue
		}
		sections = append(sections, "# === "+item.Requirement.Name+" ===\n"+cmd)
	}
	if len(sections) == 0 {
		return "# 全部依赖均已就绪，无需手动安装。"
	}
	return strings.Join(sections, "\n\n") + "\n\n# 完成后请回到 Memlane 点击「重新检测」校验完整性。\n"
}
