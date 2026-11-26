package cognee

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/zwhu/memlane/core/engine"
	"github.com/zwhu/memlane/kit/daemon"
)

const (
	apiPrefix               = "/internal/v1"
	defaultHTTPTimeout      = 10 * time.Second
	defaultExtractTimeout   = 30 * time.Minute
	maxRunnerResponseBytes  = 2 << 20
	maxRunnerErrorBytes     = 4096
	maxInternalTokenBytes   = 4096
	modelDigestMismatchCode = "model_digest_mismatch"
	extractTimeoutCode      = "extract_timeout"
	engineTimeoutCode       = "engine_timeout"
)

type client struct {
	origin      string
	tokenFile   string
	http        *http.Client
	extractHTTP *http.Client
}

func newClient(cfg Config) (*client, error) {
	origin, err := daemon.ValidateLoopbackURL(cfg.Endpoint)
	if err != nil || (cfg.Endpoint != origin && cfg.Endpoint != origin+"/") {
		return nil, invalidArgument("engine endpoint must be an explicit loopback HTTP origin", nil)
	}
	if _, err := readInternalToken(cfg.TokenFile); err != nil {
		return nil, err
	}

	timeout := cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	extractTimeout := cfg.ExtractTimeout
	if extractTimeout <= 0 {
		extractTimeout = defaultExtractTimeout
	}
	return &client{
		origin: origin, tokenFile: cfg.TokenFile,
		http: daemon.NewLoopbackClient(timeout), extractHTTP: daemon.NewLoopbackClient(extractTimeout),
	}, nil
}

func invalidInternalToken() error {
	return &engine.EngineError{
		StatusCode: http.StatusUnauthorized,
		Code:       "unauthorized",
		Message:    "engine token must be a nonempty private regular file",
		Err:        engine.ErrUnauthorized,
	}
}

func readInternalToken(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode() != 0o600 {
		return "", invalidInternalToken()
	}

	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", invalidInternalToken()
	}
	defer file.Close()
	current, err := file.Stat()
	if err != nil || current.Mode() != 0o600 || !os.SameFile(info, current) {
		return "", invalidInternalToken()
	}

	data, err := io.ReadAll(io.LimitReader(file, maxInternalTokenBytes+1))
	if err != nil || len(data) > maxInternalTokenBytes {
		return "", invalidInternalToken()
	}
	token := strings.TrimSpace(string(data))
	if token == "" || strings.ContainsFunc(token, func(r rune) bool { return r < 0x21 || r > 0x7e }) {
		return "", invalidInternalToken()
	}
	return token, nil
}

func (c *client) request(ctx context.Context, method, path string, query url.Values, payload, result any) error {
	token, err := readInternalToken(c.tokenFile)
	if err != nil {
		return err
	}

	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return invalidArgument("encode engine request", err)
		}
		body = bytes.NewReader(data)
	}

	endpoint := c.origin + apiPrefix + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return invalidArgument("create engine request", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	httpClient := c.http
	if path == "/extract" || path == "/model/apply" {
		httpClient = c.extractHTTP
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		if isTimeoutError(err) {
			return runnerTimeout(path, err)
		}
		return runnerUnavailable("contact engine runner", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return runnerStatusError(resp)
	}
	if result == nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRunnerResponseBytes+1))
	if err != nil {
		if isTimeoutError(err) {
			return runnerTimeout(path, err)
		}
		return runnerUnavailable("read engine response", err)
	}
	if len(data) > maxRunnerResponseBytes {
		return runnerUnavailable("engine response exceeds size limit", nil)
	}
	if err := json.Unmarshal(data, result); err != nil {
		return runnerUnavailable("decode engine response", err)
	}
	return nil
}

func isTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func runnerTimeout(path string, err error) error {
	code := engineTimeoutCode
	if path == "/extract" {
		code = extractTimeoutCode
	}
	cause := errors.Join(engine.ErrUnavailable, err)
	if !errors.Is(err, context.DeadlineExceeded) {
		cause = errors.Join(engine.ErrUnavailable, context.DeadlineExceeded, err)
	}
	return &engine.EngineError{Code: code, Message: "engine request timed out", Err: cause}
}

func runnerUnavailable(message string, err error) error {
	return &engine.EngineError{Code: "unavailable", Message: message, Err: errors.Join(engine.ErrUnavailable, err)}
}

func invalidArgument(message string, err error) error {
	return &engine.EngineError{Code: "invalid_argument", Message: message, Err: errors.Join(engine.ErrInvalidArgument, err)}
}

func runnerStatusError(resp *http.Response) error {
	code, cause := "unavailable", engine.ErrUnavailable
	switch resp.StatusCode {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		code, cause = "invalid_argument", engine.ErrInvalidArgument
	case http.StatusUnauthorized:
		code, cause = "unauthorized", engine.ErrUnauthorized
	case http.StatusForbidden:
		code, cause = "forbidden", engine.ErrForbidden
	case http.StatusNotFound:
		code, cause = "not_found", engine.ErrNotFound
	case http.StatusConflict:
		code, cause = "conflict", engine.ErrConflict
		if digestMismatchResponse(resp.Body) {
			code, cause = modelDigestMismatchCode, engine.ErrModelDigestMismatch
		}
	}
	return &engine.EngineError{
		StatusCode: resp.StatusCode,
		Code:       code,
		Message:    "engine runner rejected the request",
		Err:        cause,
	}
}

func digestMismatchResponse(body io.Reader) bool {
	data, err := io.ReadAll(io.LimitReader(body, maxRunnerErrorBytes+1))
	if err != nil || len(data) > maxRunnerErrorBytes {
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return bytes.Contains(data, []byte(modelDigestMismatchCode))
	}
	return containsDigestMismatch(fields)
}

func containsDigestMismatch(fields map[string]json.RawMessage) bool {
	if bytes.Contains(fields["detail"], []byte(modelDigestMismatchCode)) {
		return true
	}
	for _, key := range []string{"code", "error", "detail"} {
		raw := fields[key]
		if len(raw) == 0 {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) == nil && (value == modelDigestMismatchCode ||
			key == "detail" && strings.Contains(value, modelDigestMismatchCode)) {
			return true
		}
		var nested map[string]json.RawMessage
		if json.Unmarshal(raw, &nested) == nil && nested != nil && containsDigestMismatch(nested) {
			return true
		}
	}
	return false
}

func itemPath(id string) (string, error) {
	if strings.TrimSpace(id) == "" || id == "." || strings.Contains(id, "..") || strings.ContainsAny(id, "/\\") {
		return "", invalidArgument("invalid engine item ID", nil)
	}
	return "/items/" + url.PathEscape(id), nil
}

func scopeQuery(datasetID, userID string) url.Values {
	query := url.Values{}
	if datasetID != "" {
		query.Set("dataset_id", datasetID)
	}
	if userID != "" {
		query.Set("user_id", userID)
	}
	return query
}
