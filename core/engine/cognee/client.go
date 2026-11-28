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
	maxRunnerErrorBytes       = 4096
	maxInternalTokenBytes     = 4096
	modelDigestMismatchCode   = "model_digest_mismatch"
	datasetNotProvisionedCode = "dataset_not_provisioned"
	extractTimeoutCode        = "extract_timeout"
	engineTimeoutCode         = "engine_timeout"
	engineInternalErrorCode   = "engine_internal_error"
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
	if strings.TrimSpace(cfg.TokenFile) == "" {
		return nil, invalidInternalToken()
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
	if errors.Is(err, os.ErrNotExist) {
		return "", runnerUnavailable("engine token file not ready", err)
	}
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
	if path == "/extract" || path == "/model/apply" || path == "/code-index" {
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
	message := "engine runner rejected the request"
	switch resp.StatusCode {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		code, cause = "invalid_argument", engine.ErrInvalidArgument
	case http.StatusUnauthorized:
		code, cause = "unauthorized", engine.ErrUnauthorized
	case http.StatusForbidden:
		code, cause = "forbidden", engine.ErrForbidden
	case http.StatusNotFound:
		code, cause = "not_found", engine.ErrNotFound
		if exactJSONErrorCode(resp.Body, datasetNotProvisionedCode) {
			code = datasetNotProvisionedCode
			cause = errors.Join(engine.ErrDatasetNotProvisioned, engine.ErrNotFound)
		}
	case http.StatusConflict:
		code, cause = "conflict", engine.ErrConflict
		if responseContainsCode(resp.Body, modelDigestMismatchCode) {
			code, cause = modelDigestMismatchCode, engine.ErrModelDigestMismatch
		}
	case http.StatusInternalServerError:
		code, cause = engineInternalErrorCode, engine.ErrInternal
		message = "engine runner internal error"
		if category := classifyInternalError(resp.Body); category != "" {
			message = "engine runner internal error (" + category + ")"
		}
	case http.StatusBadGateway:
		code, cause = "unavailable", engine.ErrUnavailable
	case http.StatusServiceUnavailable:
		if data, err := io.ReadAll(io.LimitReader(resp.Body, maxRunnerErrorBytes+1)); err == nil && len(data) <= maxRunnerErrorBytes {
			switch {
			case bytes.Contains(data, []byte("generation_not_configured")):
				code = "generation_not_configured"
			case bytes.Contains(data, []byte("official_extract_failed")):
				code = "official_extract_failed"
			}
		}
	}
	return &engine.EngineError{
		StatusCode: resp.StatusCode,
		Code:       code,
		Message:    message,
		Err:        cause,
	}
}

var knownInternalErrorCategories = map[string]bool{
	"graph_wal_corrupted": true,
	"graph_db_size_limit": true,
	"fd_limit_exceeded":   true,
	"disk_full":           true,
	"database_locked":     true,
	"vector_store_error":  true,
	"graph_store_error":   true,
	"storage_io_error":    true,
	"broken_pipe":         true,
	"out_of_memory":       true,
}

// classifyInternalError maps bounded HTTP 500 runner bodies to a fixed,
// secret-free diagnostic category token without ever echoing raw body text.
func classifyInternalError(body io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(body, maxRunnerErrorBytes))
	if err != nil || len(data) == 0 {
		return ""
	}
	var payload struct {
		Category string `json:"category"`
	}
	if json.Unmarshal(data, &payload) == nil && knownInternalErrorCategories[payload.Category] {
		return payload.Category
	}
	lower := strings.ToLower(string(data))
	switch {
	case strings.Contains(lower, "graph_wal_corrupted"),
		strings.Contains(lower, "corrupted wal"),
		strings.Contains(lower, "wal file is corrupted"),
		strings.Contains(lower, "invalid wal record"),
		strings.Contains(lower, "wal.checkpoint"),
		strings.Contains(lower, ".lbug.shadow"),
		strings.Contains(lower, "replay shadow pages"):
		return "graph_wal_corrupted"
	case strings.Contains(lower, "graph_db_size_limit"),
		strings.Contains(lower, "maximum database size"),
		strings.Contains(lower, "max_db_size"),
		strings.Contains(lower, "buffer manager exception"):
		return "graph_db_size_limit"
	case strings.Contains(lower, "too many open files"),
		strings.Contains(lower, "os error 24"),
		strings.Contains(lower, "errno 24"),
		strings.Contains(lower, "emfile"):
		return "fd_limit_exceeded"
	case strings.Contains(lower, "no space left on device"),
		strings.Contains(lower, "os error 28"),
		strings.Contains(lower, "errno 28"),
		strings.Contains(lower, "enospc"):
		return "disk_full"
	case strings.Contains(lower, "database is locked"),
		strings.Contains(lower, "database table is locked"),
		strings.Contains(lower, "could not set lock on file"):
		return "database_locked"
	case strings.Contains(lower, "lanceerror"),
		strings.Contains(lower, "lance error"),
		strings.Contains(lower, "lancedb"):
		return "vector_store_error"
	case strings.Contains(lower, "ladybug"),
		strings.Contains(lower, "kuzu"):
		return "graph_store_error"
	case strings.Contains(lower, "disk i/o error"),
		strings.Contains(lower, "sqlite"),
		strings.Contains(lower, "operationalerror"):
		return "storage_io_error"
	case strings.Contains(lower, "broken pipe"),
		strings.Contains(lower, "brokenpipeerror"):
		return "broken_pipe"
	case strings.Contains(lower, "memoryerror"),
		strings.Contains(lower, "out of memory"),
		strings.Contains(lower, "cannot allocate memory"):
		return "out_of_memory"
	default:
		return ""
	}
}

func exactJSONErrorCode(body io.Reader, target string) bool {
	data, err := io.ReadAll(io.LimitReader(body, maxRunnerErrorBytes+1))
	if err != nil || len(data) > maxRunnerErrorBytes {
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return false
	}
	return containsExactErrorCode(fields, target)
}

func containsExactErrorCode(fields map[string]json.RawMessage, target string) bool {
	for _, key := range []string{"code", "error", "detail"} {
		raw := fields[key]
		if len(raw) == 0 {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) == nil && value == target {
			return true
		}
		var nested map[string]json.RawMessage
		if json.Unmarshal(raw, &nested) == nil && nested != nil && containsExactErrorCode(nested, target) {
			return true
		}
	}
	return false
}

func responseContainsCode(body io.Reader, target string) bool {
	data, err := io.ReadAll(io.LimitReader(body, maxRunnerErrorBytes+1))
	if err != nil || len(data) > maxRunnerErrorBytes {
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return bytes.Contains(data, []byte(target))
	}
	return containsErrorCode(fields, target)
}

func containsErrorCode(fields map[string]json.RawMessage, target string) bool {
	if bytes.Contains(fields["detail"], []byte(target)) {
		return true
	}
	for _, key := range []string{"code", "error", "detail"} {
		raw := fields[key]
		if len(raw) == 0 {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) == nil && (value == target ||
			key == "detail" && strings.Contains(value, target)) {
			return true
		}
		var nested map[string]json.RawMessage
		if json.Unmarshal(raw, &nested) == nil && nested != nil && containsErrorCode(nested, target) {
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
