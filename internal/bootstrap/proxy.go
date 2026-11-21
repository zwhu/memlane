package bootstrap

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

type systemProxyConfig struct {
	HTTPProxy  string
	HTTPSProxy string
	SOCKSProxy string
	Exceptions []string
}

func parseScutilProxy(output string) systemProxyConfig {
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
	return systemProxyConfig{
		HTTPProxy:  buildURL("http", "HTTPEnable", "HTTPProxy", "HTTPPort"),
		HTTPSProxy: buildURL("http", "HTTPSEnable", "HTTPSProxy", "HTTPSPort"),
		SOCKSProxy: buildURL("socks5", "SOCKSEnable", "SOCKSProxy", "SOCKSPort"),
		Exceptions: exceptions,
	}
}

func resolveSystemProxy(ctx context.Context) systemProxyConfig {
	if runtime.GOOS != "darwin" {
		return systemProxyConfig{}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, "/usr/sbin/scutil", "--proxy").Output()
	if err != nil {
		return systemProxyConfig{}
	}
	return parseScutilProxy(string(out))
}

func (c systemProxyConfig) matchException(host string) bool {
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

func (c systemProxyConfig) proxyURLFor(req *http.Request) (*url.URL, error) {
	if req == nil || req.URL == nil {
		return nil, nil
	}
	if c.matchException(req.URL.Hostname()) {
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
	return artifactProxyFunc(ctx)
}

func artifactProxyFunc(ctx context.Context) func(*http.Request) (*url.URL, error) {
	sys := resolveSystemProxy(ctx)
	return func(req *http.Request) (*url.URL, error) {
		if u, err := http.ProxyFromEnvironment(req); err != nil || u != nil {
			return u, err
		}
		return sys.proxyURLFor(req)
	}
}

func proxyEnvVars(ctx context.Context) []string {
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
	sys := resolveSystemProxy(ctx)
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
