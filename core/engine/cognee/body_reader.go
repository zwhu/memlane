package cognee

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/zwhu/memlane/core/engine"
	"golang.org/x/sys/unix"
)

const defaultMaxBodyBytes = 8 << 20

type BodyReader struct {
	storePath          string
	encryptionOverride *bool
}

type BodyResult struct {
	Body       string `json:"body"`
	Truncated  bool   `json:"truncated"`
	BytesTotal int64  `json:"bytes_total"`
	Source     string `json:"source"`
}

func NewBodyReader(storePath string, encryptionEnabled *bool) *BodyReader {
	reader := &BodyReader{storePath: storePath}
	if encryptionEnabled != nil {
		value := *encryptionEnabled
		reader.encryptionOverride = &value
	}
	return reader
}

func (r *BodyReader) ReadBody(ctx context.Context, ref engine.LocalBodyRef) (BodyResult, error) {
	if err := ctx.Err(); err != nil {
		return BodyResult{}, err
	}
	if r.encryptionEnabled() {
		return BodyResult{}, fmt.Errorf("local file encryption is enabled: %w", engine.ErrUnsupportedCapability)
	}
	if !isLocalTextMIME(ref.MimeType) {
		return BodyResult{}, engine.ErrNotUTF8Text
	}
	limit := int64(ref.MaxBytes)
	if limit <= 0 {
		limit = defaultMaxBodyBytes
	}
	if limit == math.MaxInt64 {
		return BodyResult{}, engine.ErrInvalidArgument
	}
	if r.storePath == "" {
		return BodyResult{}, engine.ErrPathDenied
	}
	root, err := filepath.Abs(r.storePath)
	if err != nil {
		return BodyResult{}, bodyPathError(err)
	}
	parts, err := bodyPathParts(root, ref.Location)
	if err != nil {
		return BodyResult{}, err
	}
	file, size, err := openBody(root, parts)
	if err != nil {
		return BodyResult{}, err
	}
	defer file.Close()

	raw, err := io.ReadAll(io.LimitReader(bodyContextReader{ctx: ctx, r: file}, limit+1))
	if err != nil {
		return BodyResult{}, fmt.Errorf("read local body: %w", err)
	}
	truncated := int64(len(raw)) > limit || size > limit
	if int64(len(raw)) > limit {
		raw = raw[:limit]
	}
	body, err := boundedUTF8(raw, truncated)
	if err != nil {
		return BodyResult{}, err
	}
	return BodyResult{Body: body, Truncated: truncated, BytesTotal: size, Source: "local_fastpath"}, nil
}

func (r *BodyReader) encryptionEnabled() bool {
	if r.encryptionOverride != nil {
		return *r.encryptionOverride
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FILE_ENCRYPTION_ENABLED"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func isLocalTextMIME(value string) bool {
	raw, _, _ := strings.Cut(value, ";")
	mimeType := strings.ToLower(strings.TrimSpace(raw))
	switch mimeType {
	case "", "application/json", "application/xml", "application/x-yaml", "application/yaml",
		"application/toml", "application/javascript", "application/typescript", "application/x-sh":
		return true
	default:
		return strings.HasPrefix(mimeType, "text/") || strings.HasSuffix(mimeType, "+json") || strings.HasSuffix(mimeType, "+xml")
	}
}

func bodyPathParts(root, location string) ([]string, error) {
	path := strings.TrimSpace(location)
	if strings.Contains(path, "://") {
		uri, err := url.Parse(path)
		if err != nil || uri.Scheme != "file" || uri.User != nil || (uri.Host != "" && uri.Host != "localhost") {
			return nil, engine.ErrPathDenied
		}
		path = uri.Path
	}
	if path == "" || strings.ContainsAny(path, "\\\x00") {
		return nil, engine.ErrPathDenied
	}
	if filepath.IsAbs(path) {
		relative, ok := strings.CutPrefix(path, filepath.Join(root, "data")+"/")
		if !ok {
			resolved, err := filepath.EvalSymlinks(root)
			if err != nil {
				return nil, bodyPathError(err)
			}
			relative, ok = strings.CutPrefix(path, filepath.Join(resolved, "data")+"/")
			if !ok {
				return nil, engine.ErrPathDenied
			}
		}
		path = relative
	}
	var parts []string
	for part := range strings.SplitSeq(path, "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			return nil, engine.ErrPathDenied
		default:
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return nil, engine.ErrPathDenied
	}
	return parts, nil
}

func openBody(root string, parts []string) (*os.File, int64, error) {
	const dirFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	const fileFlags = unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK

	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, 0, bodyPathError(err)
	}
	rootFD, err := unix.Open(resolvedRoot, dirFlags, 0)
	if err != nil {
		return nil, 0, bodyPathError(err)
	}
	dirFD, err := unix.Openat(rootFD, "data", dirFlags, 0)
	unix.Close(rootFD)
	if err != nil {
		return nil, 0, bodyPathError(err)
	}
	for _, part := range parts[:len(parts)-1] {
		nextFD, err := unix.Openat(dirFD, part, dirFlags, 0)
		unix.Close(dirFD)
		if err != nil {
			return nil, 0, bodyPathError(err)
		}
		dirFD = nextFD
	}
	leaf := parts[len(parts)-1]
	fd, err := unix.Openat(dirFD, leaf, fileFlags, 0)
	unix.Close(dirFD)
	if err != nil {
		return nil, 0, bodyPathError(err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		unix.Close(fd)
		return nil, 0, bodyPathError(err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		unix.Close(fd)
		return nil, 0, engine.ErrPathDenied
	}
	return os.NewFile(uintptr(fd), leaf), stat.Size, nil
}

func bodyPathError(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("open local body: %w", engine.ErrNotFound)
	}
	return fmt.Errorf("open local body: %w", engine.ErrPathDenied)
}

type bodyContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r bodyContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func boundedUTF8(raw []byte, truncated bool) (string, error) {
	if bytes.IndexByte(raw, 0) >= 0 {
		return "", engine.ErrNotUTF8Text
	}
	if utf8.Valid(raw) {
		return string(raw), nil
	}
	if truncated {
		for trim := 1; trim < utf8.UTFMax && trim <= len(raw); trim++ {
			end := len(raw) - trim
			if !utf8.FullRune(raw[end:]) && utf8.Valid(raw[:end]) {
				return string(raw[:end]), nil
			}
		}
	}
	return "", engine.ErrNotUTF8Text
}
