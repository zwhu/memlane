package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	defaultTimeout       = 125 * time.Second
	defaultResponseBytes = 2 << 20
	maxTokenBytes        = 64 << 10
)

var (
	sharedLoopbackTransport = NewLoopbackTransport()
	loopbackOriginPattern   = regexp.MustCompile(`^http://127\.0\.0\.1:([0-9]+)/?$`)
)

type Config struct {
	URL                 string        `json:"url"`
	RegistryFile        string        `json:"registry_file,omitempty"`
	ManagementTokenFile string        `json:"management_token_file"`
	PluginDir           string        `json:"plugin_dir,omitempty"`
	Timeout             time.Duration `json:"-"`
}

type Client struct {
	Config Config
	HTTP   *http.Client
}

func NewClient(cfg Config) *Client {
	return &Client{Config: cfg}
}

func NoRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func NewLoopbackTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return transport
}

func NewLoopbackHTTPClient(override *http.Client, timeout time.Duration) *http.Client {
	if override != nil {
		cloned := *override
		cloned.CheckRedirect = NoRedirect
		if cloned.Transport == nil {
			cloned.Transport = sharedLoopbackTransport
		}
		return &cloned
	}
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &http.Client{Transport: sharedLoopbackTransport, Timeout: timeout, CheckRedirect: NoRedirect}
}

// ValidateLoopbackOrigin returns the origin without its optional trailing slash.
func ValidateLoopbackOrigin(raw string) (string, error) {
	match := loopbackOriginPattern.FindStringSubmatch(raw)
	if match != nil {
		port, err := strconv.ParseUint(match[1], 10, 16)
		if err == nil && port != 0 {
			return strings.TrimSuffix(raw, "/"), nil
		}
	}
	return "", errors.New("项目管理服务地址必须为带端口的 http://127.0.0.1")
}

func ReadPrivateToken(path string, minLen int, requirePrivate bool) (string, error) {
	flags := os.O_RDONLY | syscall.O_NONBLOCK
	unavailable := errors.New("连接凭据不可用")
	if requirePrivate {
		flags |= syscall.O_NOFOLLOW
	}

	f, err := os.OpenFile(path, flags, 0)
	if err != nil {
		return "", unavailable
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || (requirePrivate && info.Mode().Perm()&0o077 != 0) {
		return "", unavailable
	}
	if info.Size() > maxTokenBytes {
		return "", errors.New("连接凭据超过大小限制")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxTokenBytes+1))
	if err != nil {
		return "", unavailable
	}
	if len(raw) > maxTokenBytes {
		return "", errors.New("连接凭据超过大小限制")
	}
	token := strings.TrimSpace(string(raw))
	if len(token) < minLen {
		return "", errors.New("连接凭据长度不足")
	}
	return token, nil
}

func (c *Client) Request(ctx context.Context, method, path string, input, output any) error {
	return c.RequestWithHeaders(ctx, method, path, nil, input, output, defaultResponseBytes)
}

func (c *Client) RequestWithHeaders(ctx context.Context, method, path string, headers map[string]string, input, output any, maxBytes int64) error {
	origin, err := ValidateLoopbackOrigin(c.Config.URL)
	if err != nil {
		return err
	}
	if !validRequestPath(path) {
		return errors.New("项目管理请求路径无效")
	}
	token, err := ReadPrivateToken(c.Config.ManagementTokenFile, 32, true)
	if err != nil {
		return newControlError(http.StatusUnauthorized, "control_credential_unavailable", "")
	}
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return errors.New("项目管理请求无法编码")
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, origin+path, body)
	if err != nil {
		return errors.New("项目管理请求无效")
	}
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		if name != "" && value != "" {
			req.Header.Set(name, value)
		}
	}
	req.Header.Set("Authorization", "Bearer "+token)

	response, err := NewLoopbackHTTPClient(c.HTTP, c.Config.Timeout).Do(req)
	if err != nil {
		// Transport errors may echo the URL, headers, or proxy credentials.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if method == http.MethodGet || method == http.MethodHead {
			return errors.New("项目管理服务暂时不可用")
		}
		return errors.New("项目管理操作结果未确认，请核对原操作回执")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return decodeControlError(response.StatusCode, response.Body, token)
	}

	if maxBytes <= 0 {
		maxBytes = defaultResponseBytes
	}
	maxBytes = min(maxBytes, math.MaxInt64-1)
	limited := io.LimitReader(response.Body, maxBytes+1)
	if output == nil {
		n, err := io.Copy(io.Discard, limited)
		if err == nil && n <= maxBytes {
			return nil
		}
	} else {
		data, err := io.ReadAll(limited)
		if err == nil && int64(len(data)) <= maxBytes && json.Unmarshal(data, output) == nil {
			return nil
		}
	}
	return errors.New("项目管理回执未确认，请刷新核对")
}

func validRequestPath(path string) bool {
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "#") {
		return false
	}
	u, err := url.ParseRequestURI(path)
	if err != nil || u.IsAbs() || u.Host != "" || strings.HasPrefix(u.Path, "//") || strings.Contains(u.Path, "\\") {
		return false
	}
	for segment := range strings.SplitSeq(u.Path, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}
