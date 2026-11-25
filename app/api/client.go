package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"
)

type Config struct {
	BaseURL             string
	ManagementToken     string
	ManagementTokenFile string
	ReadingIdentity     string
	HTTPClient          *http.Client
	Timeout             time.Duration
	MaxResponseBytes    int64
}

type APIError struct {
	Status   int
	Code     string
	Message  string
	Endpoint string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s: HTTP %d (%s): %s", e.Endpoint, e.Status, e.Code, e.Message)
}

func (e *APIError) IsUnauthorized() bool { return e.Status == http.StatusUnauthorized }
func (e *APIError) IsForbidden() bool    { return e.Status == http.StatusForbidden }
func (e *APIError) IsNotFound() bool     { return e.Status == http.StatusNotFound }
func (e *APIError) IsConflict() bool     { return e.Status == http.StatusConflict }
func (e *APIError) IsUnavailable() bool  { return e.Status == http.StatusServiceUnavailable }

type Client struct {
	baseURL          string
	token            string
	readingIdentity  string
	http             *http.Client
	maxResponseBytes int64
}

func NewClient(cfg Config) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil || !isLoopbackOrigin(u) {
		return nil, errors.New("gateway URL must be a loopback HTTP or HTTPS origin")
	}
	if cfg.MaxResponseBytes < 0 || cfg.MaxResponseBytes == 1<<63-1 || cfg.Timeout < 0 {
		return nil, errors.New("invalid gateway client limits")
	}
	token := strings.TrimSpace(cfg.ManagementToken)
	if token == "" && cfg.ManagementTokenFile != "" {
		token, err = readToken(cfg.ManagementTokenFile)
		if err != nil {
			return nil, fmt.Errorf("read management token: %w", err)
		}
	}
	identity := strings.TrimSpace(cfg.ReadingIdentity)
	for _, value := range []string{token, identity} {
		if strings.ContainsFunc(value, func(r rune) bool { return r < ' ' || r == 0x7f }) {
			return nil, errors.New("invalid gateway credential or reading identity")
		}
	}

	hc := http.Client{Timeout: cfg.Timeout}
	if cfg.HTTPClient != nil {
		hc = *cfg.HTTPClient
	} else if hc.Timeout == 0 {
		hc.Timeout = 15 * time.Second
	}
	// A redirect could forward the management header to another origin or route.
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	limit := cfg.MaxResponseBytes
	if limit == 0 {
		limit = 24 << 20
	}
	return &Client{u.Scheme + "://" + u.Host, token, identity, &hc, limit}, nil
}

func isLoopbackOrigin(u *url.URL) bool {
	if (u.Scheme != "http" && u.Scheme != "https") || u.User != nil ||
		u.ForceQuery || u.RawQuery != "" || u.Fragment != "" ||
		(u.EscapedPath() != "" && u.EscapedPath() != "/") {
		return false
	}
	host := u.Hostname()
	return strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback()
}

func readToken(path string) (string, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return "", err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode() != 0o600 {
		return "", errors.New("management token must be a regular 0600 file")
	}
	const maxTokenBytes = 65536
	raw, err := io.ReadAll(io.LimitReader(f, maxTokenBytes+1))
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(raw))
	if len(raw) > maxTokenBytes || token == "" {
		return "", errors.New("invalid management token file contents")
	}
	return token, nil
}

func (c *Client) request(ctx context.Context, method, endpoint string, query url.Values, input, output any) error {
	target := c.baseURL + endpoint
	if len(query) != 0 {
		target += "?" + query.Encode()
	}
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("%s: encode request: %w", endpoint, err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("X-Memlane-Management", c.token)
	}
	if c.readingIdentity != "" {
		req.Header.Set("X-Memlane-Reading-Identity", c.readingIdentity)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, c.maxResponseBytes+1))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		apiErr := &APIError{Status: res.StatusCode, Message: http.StatusText(res.StatusCode), Endpoint: endpoint}
		if int64(len(raw)) <= c.maxResponseBytes {
			var envelope struct {
				Error     string `json:"error"`
				ErrorCode string `json:"error_code"`
				Message   string `json:"message"`
			}
			if json.Unmarshal(raw, &envelope) == nil {
				apiErr.Code = envelope.Error
				if apiErr.Code == "" {
					apiErr.Code = envelope.ErrorCode
				}
				if envelope.Message != "" {
					apiErr.Message = envelope.Message
				}
			}
		}
		return apiErr
	}
	if int64(len(raw)) > c.maxResponseBytes {
		return fmt.Errorf("%s: response exceeds %d bytes", endpoint, c.maxResponseBytes)
	}
	if output != nil && res.StatusCode != http.StatusNoContent {
		if err := json.Unmarshal(raw, output); err != nil {
			return fmt.Errorf("%s: decode response: %w", endpoint, err)
		}
	}
	return nil
}

func requestValue[T any](ctx context.Context, c *Client, method, path string, query url.Values, input any) (T, error) {
	var out T
	err := c.request(ctx, method, path, query, input, &out)
	return out, err
}

func (c *Client) Health(ctx context.Context) (HealthResponse, error) {
	return requestValue[HealthResponse](ctx, c, http.MethodGet, "/api/health", nil, nil)
}

func (c *Client) Capabilities(ctx context.Context) (CapabilitiesResponse, error) {
	return requestValue[CapabilitiesResponse](ctx, c, http.MethodGet, "/api/capabilities", nil, nil)
}

func (c *Client) RuntimeMonitor(ctx context.Context) (RuntimeMonitorResponse, error) {
	return requestValue[RuntimeMonitorResponse](ctx, c, http.MethodGet, "/api/runtime/monitor", nil, nil)
}
