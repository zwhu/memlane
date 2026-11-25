package gateway

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unicode/utf8"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
	"github.com/zwhu/memlane/kit/daemon"
)

func writeJSON(w http.ResponseWriter, status int, body any) {
	encoded, err := json.Marshal(body)
	if err != nil {
		log.Print("gateway response encoding failed")
		status, encoded = 500, []byte(`{"error":"internal_error","message":"response encoding failed"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(encoded); err != nil {
		log.Print("gateway response write failed")
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}

func writeDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, sql.ErrNoRows):
		writeError(w, 404, "not_found", "record not found")
	case errors.Is(err, domain.ErrConflict):
		writeError(w, 409, "conflict", "record conflicts with existing state")
	case errors.Is(err, domain.ErrInvalidArgument):
		writeError(w, 400, "invalid_argument", "invalid request")
	case errors.Is(err, domain.ErrForbidden):
		writeError(w, 403, "forbidden", "operation forbidden")
	default:
		log.Printf("gateway store: %s", sanitizeError(err))
		writeError(w, 500, "internal_error", "store operation failed")
	}
}

func writeEngineError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, engine.ErrNotFound):
		writeError(w, 404, "not_found", "engine item not found")
	case errors.Is(err, engine.ErrUnauthorized), errors.Is(err, engine.ErrForbidden), errors.Is(err, engine.ErrPathDenied):
		writeError(w, 403, "forbidden", "engine access denied")
	case errors.Is(err, engine.ErrConflict), errors.Is(err, engine.ErrModelDigestMismatch):
		writeError(w, 409, "conflict", "engine state conflict")
	case errors.Is(err, engine.ErrInvalidArgument), errors.Is(err, engine.ErrNotUTF8Text):
		writeError(w, 400, "invalid_argument", "engine rejected the request")
	case errors.Is(err, engine.ErrContentTooLarge):
		writeError(w, 413, "content_too_large", "engine content exceeds the limit")
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, 504, "engine_timeout", "engine request timed out")
	case errors.Is(err, engine.ErrUnavailable), errors.Is(err, engine.ErrUnsupportedCapability):
		writeError(w, 503, "engine_unavailable", "engine unavailable")
	default:
		writeError(w, 502, "engine_error", "engine operation failed")
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, maxBytes int64, dst any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeError(w, 415, "unsupported_media_type", "application/json required")
		return false
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBytes))
	var large *http.MaxBytesError
	if errors.As(err, &large) {
		writeError(w, 413, "request_body_too_large", "request exceeds the body limit")
		return false
	}
	raw = bytes.TrimSpace(raw)
	if err != nil || len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, dst) != nil {
		writeError(w, 400, "invalid_json", "valid JSON object required")
		return false
	}
	return true
}

func parseLimit(raw string, def, maxVal int) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	return min(n, maxVal)
}
func parseBool(raw string) bool {
	b, err := strconv.ParseBool(raw)
	return err == nil && b
}
func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	safe := daemon.RedactLogLine(err.Error())
	if len(safe) > 512 {
		safe = safe[:512]
		for !utf8.ValidString(safe) && len(safe) > 0 {
			safe = safe[:len(safe)-1]
		}
	}
	return safe
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type cursorCodec struct{ secret []byte }
type cursorBinding struct{ Endpoint, Dataset, Sort, Filter string }

func (c cursorCodec) encode(binding cursorBinding, values []string) string {
	raw, err := json.Marshal(values)
	if err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(append(c.sign(binding, raw), raw...))
}

func (c cursorCodec) sign(binding cursorBinding, payload []byte) []byte {
	mac := hmac.New(sha256.New, c.secret)
	scope, err := json.Marshal(binding)
	if err != nil {
		panic(err)
	}
	mac.Write(scope)
	mac.Write([]byte{0})
	mac.Write(payload)
	return mac.Sum(nil)
}

func (c cursorCodec) decode(binding cursorBinding, cursor string, count int) ([]string, error) {
	if len(cursor) > 16384 {
		return nil, domain.ErrInvalidArgument
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(cursor)
	if err != nil || len(raw) <= sha256.Size || !hmac.Equal(raw[:sha256.Size], c.sign(binding, raw[sha256.Size:])) {
		return nil, domain.ErrInvalidArgument
	}
	var values []string
	if err := json.Unmarshal(raw[sha256.Size:], &values); err != nil || len(values) != count {
		return nil, domain.ErrInvalidArgument
	}
	return values, nil
}

type secretFileCache struct {
	mu      sync.Mutex
	entries map[string]secretFile
}
type secretFile struct {
	info  os.FileInfo
	stamp syscall.Stat_t
	value string
}

func (c *secretFileCache) read(path string) (string, error) {
	if path == "" {
		return "", domain.ErrNotFound
	}
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
	stamp, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 ||
		info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || info.Size() > 65536 {
		return "", domain.ErrForbidden
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if cached, ok := c.entries[path]; ok && os.SameFile(info, cached.info) && cached.stamp == *stamp {
		return cached.value, nil
	}
	data, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(data))
	if len(data) > 65536 || value == "" {
		return "", domain.ErrForbidden
	}
	after, err := f.Stat()
	if err != nil {
		return "", err
	}
	if *after.Sys().(*syscall.Stat_t) == *stamp {
		if c.entries == nil || len(c.entries) >= 1024 {
			c.entries = make(map[string]secretFile)
		}
		c.entries[path] = secretFile{info, *stamp, value}
	} else {
		return "", domain.ErrConflict
	}
	return value, nil
}

type searchEntry struct {
	Query      string `json:"query"`
	Filter     string `json:"filter"`
	ProjectID  string `json:"project_id,omitempty"`
	DatasetID  string `json:"dataset_id,omitempty"`
	HitCount   int    `json:"hit_count"`
	SearchedAt string `json:"searched_at"`
}

type userSearchHistory struct {
	mu         sync.Mutex
	entries    [100]searchEntry
	next, size int
}

func (h *userSearchHistory) add(entry searchEntry) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries[h.next] = entry
	h.next = (h.next + 1) % len(h.entries)
	h.size = min(h.size+1, len(h.entries))
}

func (h *userSearchHistory) recent(limit int) []searchEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := min(limit, h.size)
	result := make([]searchEntry, n)
	for i := range result {
		result[i] = h.entries[(h.next-1-i+len(h.entries))%len(h.entries)]
	}
	return result
}

type sqlRead interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type rowScanner interface{ Scan(...any) error }

func readRows[T any](ctx context.Context, db sqlRead, query string, args []any, scan func(rowScanner) (T, error)) ([]T, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]T, 0)
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func readingIdentity(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get("X-Memlane-Reading-Identity"))
	if value == "" {
		value = strings.TrimSpace(r.URL.Query().Get("reading_identity"))
	}
	return value
}
