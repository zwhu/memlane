package aiinstaller

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
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

func WithOfficialFallback(mirrors []ArtifactCandidate, official ArtifactCandidate) []ArtifactCandidate {
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
		return WithOfficialFallback(out, official)
	case "python":
		const prefix = "/astral-sh/python-build-standalone/releases/download/"
		if u.Host == "github.com" && strings.HasPrefix(u.EscapedPath(), prefix) {
			rel := strings.TrimPrefix(u.EscapedPath(), prefix)
			return WithOfficialFallback([]ArtifactCandidate{
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
		return WithOfficialFallback(out, official)
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
		return WithOfficialFallback([]ArtifactCandidate{
			{URL: "https://gh-proxy.org/" + officialURL, SHA256: officialSHA, Format: "tar.gz"},
			{URL: "https://gh-proxy.com/" + officialURL, SHA256: officialSHA, Format: "tar.gz"},
		}, official)
	}
	return []ArtifactCandidate{official}
}

// ModelFileCandidates returns ordered verified download candidates for a pinned embedding model file.
func ModelFileCandidates(repo string, file ModelFileRef, downloadSource string) []ArtifactCandidate {
	official := ArtifactCandidate{URL: file.URL, SHA256: file.SHA256, Format: "raw"}
	if NormalizeDownloadSource(downloadSource) != DownloadSourceChina {
		return []ArtifactCandidate{official}
	}
	u, err := url.Parse(file.URL)
	if err != nil || u.Scheme != "https" || u.Host != "huggingface.co" || repo == "" || file.Name == "" {
		return []ArtifactCandidate{official}
	}
	return WithOfficialFallback([]ArtifactCandidate{
		{
			URL:    "https://modelscope.cn/models/" + repo + "/resolve/master/" + file.Name,
			SHA256: file.SHA256,
			Format: "raw",
		},
	}, official)
}

type ModelFileRef struct {
	Name   string
	SHA256 string
	URL    string
}

type SystemProxyConfig struct {
	HTTPProxy  string
	HTTPSProxy string
	SOCKSProxy string
	Exceptions []string
}

func ParseScutilProxy(output string) SystemProxyConfig {
	kv := map[string]string{}
	var exceptions []string
	inExceptions := false
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "ExceptionsList") && strings.Contains(line, "{") {
			inExceptions = true
			continue
		}
		if inExceptions {
			if line == "}" {
				inExceptions = false
				continue
			}
			if idx := strings.Index(line, ":"); idx >= 0 {
				val := strings.TrimSpace(line[idx+1:])
				if val != "" {
					exceptions = append(exceptions, val)
				}
			}
			continue
		}
		if idx := strings.Index(line, ":"); idx >= 0 {
			k := strings.TrimSpace(line[:idx])
			v := strings.TrimSpace(line[idx+1:])
			kv[k] = v
		}
	}

	buildURL := func(scheme, enableKey, hostKey, portKey string) string {
		if kv[enableKey] != "1" || kv[hostKey] == "" {
			return ""
		}
		host := kv[hostKey]
		port := kv[portKey]
		if port != "" && port != "0" {
			host = host + ":" + port
		}
		return scheme + "://" + host
	}
	return SystemProxyConfig{
		HTTPProxy:  buildURL("http", "HTTPEnable", "HTTPProxy", "HTTPPort"),
		HTTPSProxy: buildURL("http", "HTTPSEnable", "HTTPSProxy", "HTTPSPort"),
		SOCKSProxy: buildURL("socks5", "SOCKSEnable", "SOCKSProxy", "SOCKSPort"),
		Exceptions: exceptions,
	}
}

func ResolveSystemProxy(ctx context.Context) SystemProxyConfig {
	if runtime.GOOS != "darwin" {
		return SystemProxyConfig{}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, "/usr/sbin/scutil", "--proxy").Output()
	if err != nil {
		return SystemProxyConfig{}
	}
	return ParseScutilProxy(string(out))
}

func (c SystemProxyConfig) MatchException(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" {
		return false
	}
	for _, pattern := range c.Exceptions {
		p := strings.ToLower(strings.TrimSpace(pattern))
		if p == "" {
			continue
		}
		if p == h {
			return true
		}
		if strings.HasPrefix(p, "*.") && strings.HasSuffix(h, p[1:]) {
			return true
		}
		if strings.HasPrefix(p, ".") && strings.HasSuffix(h, p) {
			return true
		}
	}
	return false
}

func (c SystemProxyConfig) ProxyURLFor(req *http.Request) (*url.URL, error) {
	if req == nil || req.URL == nil {
		return nil, nil
	}
	if c.MatchException(req.URL.Hostname()) {
		return nil, nil
	}
	var raw string
	switch req.URL.Scheme {
	case "https":
		raw = c.HTTPSProxy
		if raw == "" {
			raw = c.HTTPProxy
		}
		if raw == "" {
			raw = c.SOCKSProxy
		}
	case "http":
		raw = c.HTTPProxy
		if raw == "" {
			raw = c.SOCKSProxy
		}
	default:
		raw = c.SOCKSProxy
	}
	if raw == "" {
		return nil, nil
	}
	return url.Parse(raw)
}

// ProxyFunc returns a proxy selector honoring environment variables and macOS system proxy settings.
func ProxyFunc(ctx context.Context) func(*http.Request) (*url.URL, error) {
	sys := ResolveSystemProxy(ctx)
	return func(req *http.Request) (*url.URL, error) {
		if u, err := http.ProxyFromEnvironment(req); err != nil || u != nil {
			return u, err
		}
		return sys.ProxyURLFor(req)
	}
}

func ProxyEnvVars(ctx context.Context) []string {
	var env []string
	hasHTTP, hasHTTPS, hasAll := false, false, false
	for _, key := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
		if val := os.Getenv(key); val != "" {
			env = append(env, key+"="+val)
			switch strings.ToUpper(key) {
			case "HTTP_PROXY":
				hasHTTP = true
			case "HTTPS_PROXY":
				hasHTTPS = true
			case "ALL_PROXY":
				hasAll = true
			}
		}
	}
	if hasHTTP || hasHTTPS || hasAll {
		return env
	}
	sys := ResolveSystemProxy(ctx)
	if sys.HTTPProxy != "" {
		env = append(env, "HTTP_PROXY="+sys.HTTPProxy, "http_proxy="+sys.HTTPProxy)
	}
	if sys.HTTPSProxy != "" {
		env = append(env, "HTTPS_PROXY="+sys.HTTPSProxy, "https_proxy="+sys.HTTPSProxy)
	} else if sys.HTTPProxy != "" {
		env = append(env, "HTTPS_PROXY="+sys.HTTPProxy, "https_proxy="+sys.HTTPProxy)
	}
	if sys.SOCKSProxy != "" {
		env = append(env, "ALL_PROXY="+sys.SOCKSProxy, "all_proxy="+sys.SOCKSProxy)
	}
	if len(sys.Exceptions) > 0 {
		joined := strings.Join(sys.Exceptions, ",")
		env = append(env, "NO_PROXY="+joined, "no_proxy="+joined)
	}
	return env
}
