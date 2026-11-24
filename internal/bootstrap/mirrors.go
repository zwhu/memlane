package bootstrap

import (
	"net/url"
	"strings"
)

const (
	DownloadSourceChina    = "china"
	DownloadSourceOfficial = "official"
)

const (
	pinnedUV0123Arm64OfficialSHA256 = "546f7f8a6c70ff13a3a9d2bc958db3427298cebf3e0cb756f9177133b7068843"
	pinnedUV0123Arm64WheelSHA256    = "7c99f2524fe4b11d74dec85cef9b4d8b725d142dec04cd237f1c06fbdae1ee54"
	pinnedUV0123Arm64WheelPath      = "/packages/e4/66/ba257c91d69921d773f523c9e4c3ffb04e233694c415517f5ef797403a4f/uv-0.12.3-py3-none-macosx_11_0_arm64.whl"
	pinnedUV0123Arm64WheelEntry     = "uv-0.12.3.data/scripts/uv"

	pinnedOpenSSL359Arm64BlobSHA256 = "82b33ce156b289eaf95657b9ec16e8f49cb49c882cdeaeaf57e3e7d67fa85e25"
	pinnedOpenSSL359Arm64TunaFile   = "openssl%403-3.5.9.arm64_sequoia.bottle.tar.gz"
)

// ArtifactCandidate describes one verified HTTPS download candidate for a pinned dependency.
type ArtifactCandidate struct {
	URL      string
	SHA256   string
	Format   string // "tar.gz", "wheel", or "raw"
	ZipEntry string
}

func withOfficialFallback(mirrors []ArtifactCandidate, official ArtifactCandidate) []ArtifactCandidate {
	result := make([]ArtifactCandidate, 0, len(mirrors)+1)
	seen := map[string]bool{official.URL: true}
	for _, mirror := range mirrors {
		if !seen[mirror.URL] {
			seen[mirror.URL] = true
			result = append(result, mirror)
		}
	}
	return append(result, official)
}

// NormalizeDownloadSource normalizes a source mode string; empty/unknown defaults to China mirrors.
func NormalizeDownloadSource(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case DownloadSourceOfficial:
		return DownloadSourceOfficial
	case DownloadSourceChina, "":
		return DownloadSourceChina
	default:
		return DownloadSourceChina
	}
}

// PyPIIndexURLs returns ordered PyPI simple index URLs for uv pip sync.
func PyPIIndexURLs(downloadSource string) []string {
	if NormalizeDownloadSource(downloadSource) == DownloadSourceOfficial {
		return []string{"https://pypi.org/simple/"}
	}
	return []string{
		"https://pypi.tuna.tsinghua.edu.cn/simple/",
		"https://mirrors.aliyun.com/pypi/simple/",
		"https://mirrors.ustc.edu.cn/pypi/simple/",
	}
}

// ToolCandidates returns ordered verified download candidates for standalone tools (python, uv).
func ToolCandidates(id, officialURL, officialSHA, downloadSource string) []ArtifactCandidate {
	official := ArtifactCandidate{URL: officialURL, SHA256: officialSHA, Format: "tar.gz"}
	if NormalizeDownloadSource(downloadSource) != DownloadSourceChina {
		return []ArtifactCandidate{official}
	}
	u, err := url.Parse(officialURL)
	if err != nil || u.Scheme != "https" {
		return []ArtifactCandidate{official}
	}
	switch id {
	case "uv":
		var out []ArtifactCandidate
		if officialSHA == pinnedUV0123Arm64OfficialSHA256 {
			out = append(out,
				ArtifactCandidate{
					URL:      "https://pypi.tuna.tsinghua.edu.cn" + pinnedUV0123Arm64WheelPath,
					SHA256:   pinnedUV0123Arm64WheelSHA256,
					Format:   "wheel",
					ZipEntry: pinnedUV0123Arm64WheelEntry,
				},
				ArtifactCandidate{
					URL:      "https://mirrors.aliyun.com/pypi" + pinnedUV0123Arm64WheelPath,
					SHA256:   pinnedUV0123Arm64WheelSHA256,
					Format:   "wheel",
					ZipEntry: pinnedUV0123Arm64WheelEntry,
				},
			)
		}
		if u.Host == "github.com" {
			out = append(out,
				ArtifactCandidate{URL: "https://gh-proxy.org/" + officialURL, SHA256: officialSHA, Format: "tar.gz"},
				ArtifactCandidate{URL: "https://gh-proxy.com/" + officialURL, SHA256: officialSHA, Format: "tar.gz"},
			)
		}
		return withOfficialFallback(out, official)
	case "python":
		const prefix = "/astral-sh/python-build-standalone/releases/download/"
		if u.Host == "github.com" && strings.HasPrefix(u.EscapedPath(), prefix) {
			rel := strings.TrimPrefix(u.EscapedPath(), prefix)
			return withOfficialFallback([]ArtifactCandidate{
				{URL: "https://registry.npmmirror.com/-/binary/python-build-standalone/" + rel, SHA256: officialSHA, Format: "tar.gz"},
				{URL: "https://gh-proxy.org/" + officialURL, SHA256: officialSHA, Format: "tar.gz"},
				{URL: "https://gh-proxy.com/" + officialURL, SHA256: officialSHA, Format: "tar.gz"},
			}, official)
		}
	}
	return []ArtifactCandidate{official}
}

// NativeCandidates returns ordered verified download candidates for Homebrew bottle archives.
func NativeCandidates(officialURL, officialSHA, downloadSource string) []ArtifactCandidate {
	official := ArtifactCandidate{URL: officialURL, SHA256: officialSHA, Format: "tar.gz"}
	if NormalizeDownloadSource(downloadSource) != DownloadSourceChina {
		return []ArtifactCandidate{official}
	}
	u, err := url.Parse(officialURL)
	if err != nil || u.Scheme != "https" {
		return []ArtifactCandidate{official}
	}
	if u.Host == "ghcr.io" && strings.HasPrefix(u.Path, "/v2/homebrew/core/") {
		out := []ArtifactCandidate{
			{URL: "https://mirrors.ustc.edu.cn/homebrew-bottles" + u.Path, SHA256: officialSHA, Format: "tar.gz"},
		}
		if officialSHA == pinnedOpenSSL359Arm64BlobSHA256 {
			out = append(out, ArtifactCandidate{
				URL:    "https://mirrors.tuna.tsinghua.edu.cn/homebrew-bottles/" + pinnedOpenSSL359Arm64TunaFile,
				SHA256: officialSHA,
				Format: "tar.gz",
			})
		}
		return withOfficialFallback(out, official)
	}
	return []ArtifactCandidate{official}
}

// SourceCandidates returns ordered verified download candidates for GitHub codeload/archive tarballs.
func SourceCandidates(officialURL, officialSHA, downloadSource string) []ArtifactCandidate {
	official := ArtifactCandidate{URL: officialURL, SHA256: officialSHA, Format: "tar.gz"}
	if NormalizeDownloadSource(downloadSource) != DownloadSourceChina {
		return []ArtifactCandidate{official}
	}
	u, err := url.Parse(officialURL)
	if err != nil || u.Scheme != "https" {
		return []ArtifactCandidate{official}
	}
	if u.Host == "codeload.github.com" || u.Host == "github.com" {
		return withOfficialFallback([]ArtifactCandidate{
			{URL: "https://gh-proxy.org/" + officialURL, SHA256: officialSHA, Format: "tar.gz"},
			{URL: "https://gh-proxy.com/" + officialURL, SHA256: officialSHA, Format: "tar.gz"},
		}, official)
	}
	return []ArtifactCandidate{official}
}

// ModelFileCandidates returns ordered verified download candidates for a pinned embedding model file.
func ModelFileCandidates(repo string, file ModelFile, downloadSource string) []ArtifactCandidate {
	official := ArtifactCandidate{URL: file.URL, SHA256: file.SHA256, Format: "raw"}
	if NormalizeDownloadSource(downloadSource) != DownloadSourceChina {
		return []ArtifactCandidate{official}
	}
	u, err := url.Parse(file.URL)
	if err != nil || u.Scheme != "https" || u.Host != "huggingface.co" || repo == "" || file.Name == "" {
		return []ArtifactCandidate{official}
	}
	// ModelScope mirrors Qdrant/bge-small-zh-v1.5 and BAAI/bge-small-zh-v1.5 on master
	// with byte-for-byte identical files verified against the pinned SHA256 digests.
	return withOfficialFallback([]ArtifactCandidate{
		{
			URL:    "https://modelscope.cn/models/" + repo + "/resolve/master/" + file.Name,
			SHA256: file.SHA256,
			Format: "raw",
		},
	}, official)
}

// DisplaySourceURL returns the primary URL shown to the user for a given requirement ID and download source mode.
func DisplaySourceURL(id, officialURL, officialSHA, downloadSource string) string {
	mode := NormalizeDownloadSource(downloadSource)
	if mode != DownloadSourceChina {
		return officialURL
	}
	switch id {
	case "uv", "python":
		return ToolCandidates(id, officialURL, officialSHA, mode)[0].URL
	case "runtime":
		return PyPIIndexURLs(mode)[0]
	case "cognee-source", "agent-plugin":
		return SourceCandidates(officialURL, officialSHA, mode)[0].URL
	case "embedding":
		return "https://modelscope.cn/models/Qdrant/bge-small-zh-v1.5/resolve/master/model_optimized.onnx"
	default:
		return officialURL
	}
}
