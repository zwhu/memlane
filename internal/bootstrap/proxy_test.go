package bootstrap

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseScutilProxyAndMatching(t *testing.T) {
	sample := `<dictionary> {
  ExceptionsList : <array> {
    0 : *.local
    1 : 127.0.0.1
    2 : localhost
  }
  HTTPEnable : 1
  HTTPPort : 7890
  HTTPProxy : 127.0.0.1
  HTTPSEnable : 1
  HTTPSPort : 7890
  HTTPSProxy : 127.0.0.1
  SOCKSEnable : 1
  SOCKSPort : 7891
  SOCKSProxy : 127.0.0.1
}`
	cfg := parseScutilProxy(sample)
	if cfg.HTTPProxy != "http://127.0.0.1:7890" {
		t.Fatalf("unexpected HTTPProxy: %q", cfg.HTTPProxy)
	}
	if cfg.HTTPSProxy != "http://127.0.0.1:7890" {
		t.Fatalf("unexpected HTTPSProxy: %q", cfg.HTTPSProxy)
	}
	if cfg.SOCKSProxy != "socks5://127.0.0.1:7891" {
		t.Fatalf("unexpected SOCKSProxy: %q", cfg.SOCKSProxy)
	}

	req := httptest.NewRequest(http.MethodGet, "https://pypi.tuna.tsinghua.edu.cn/simple/", nil)
	u, err := cfg.proxyURLFor(req)
	if err != nil || u == nil || u.String() != "http://127.0.0.1:7890" {
		t.Fatalf("unexpected proxy URL for external host: %v %v", u, err)
	}

	localReq := httptest.NewRequest(http.MethodGet, "https://127.0.0.1:8080/health", nil)
	u, err = cfg.proxyURLFor(localReq)
	if err != nil || u != nil {
		t.Fatalf("expected exception host to bypass proxy, got %v %v", u, err)
	}

	localDomainReq := httptest.NewRequest(http.MethodGet, "https://myhost.local/test", nil)
	u, err = cfg.proxyURLFor(localDomainReq)
	if err != nil || u != nil {
		t.Fatalf("expected *.local host to bypass proxy, got %v %v", u, err)
	}
}
