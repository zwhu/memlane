package bootstrap

import (
	"net/http"
	"net/url"

	"github.com/zwhu/memlane/kit/aiinstaller"
)

type systemProxyConfig struct{ aiinstaller.SystemProxyConfig }

func (c systemProxyConfig) proxyURLFor(req *http.Request) (*url.URL, error) {
	return c.ProxyURLFor(req)
}
func parseScutilProxy(output string) systemProxyConfig {
	return systemProxyConfig{aiinstaller.ParseScutilProxy(output)}
}

var (
	ProxyFunc         = aiinstaller.ProxyFunc
	artifactProxyFunc = aiinstaller.ProxyFunc
	proxyEnvVars      = aiinstaller.ProxyEnvVars
)
