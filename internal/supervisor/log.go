package supervisor

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// DefaultLogLimitBytes bounds an active service log file before tail rotation.
const DefaultLogLimitBytes int64 = 20 * 1024 * 1024

var redactionRules = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?i)(Bearer\s+)\S+`), `${1}[REDACTED]`},
	{regexp.MustCompile(`(?i)(X-Api-Key\s*:\s*)\S+`), `${1}[REDACTED]`},
	{regexp.MustCompile(`(?i)(X-Memlane-Management\s*:\s*)\S+`), `${1}[REDACTED]`},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}`), `sk-[REDACTED]`},
	{regexp.MustCompile(`(?i)(\b(?:api[_-]?key|access[_-]?token|auth[_-]?token|token|secret|password)\b\s*[=:]\s*)(\S+)`), `${1}[REDACTED]`},
}

// OpenPrivateLog opens or creates an append-only 0600 regular log file.
func OpenPrivateLog(path string) (*os.File, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("service log path must be absolute")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 || dirInfo.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("service log directory must be private and non-symlink")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			return nil, errors.New("service log must be a private regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.New("service log must be a regular file")
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

// TrimServiceLog copies up to limit trailing bytes into path+".previous" and
// truncates path in place so active O_APPEND child descriptors keep the inode.
func TrimServiceLog(path string, limit int64) error {
	if limit <= 0 {
		limit = DefaultLogLimitBytes
	}
	if !filepath.IsAbs(path) {
		return errors.New("service log path must be absolute")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("service log cannot be a symlink or non-regular file")
	}
	if info.Size() <= limit {
		return nil
	}

	source, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	srcInfo, err := source.Stat()
	if err != nil {
		_ = source.Close()
		return err
	}
	if !srcInfo.Mode().IsRegular() {
		_ = source.Close()
		return errors.New("service log cannot be a non-regular file")
	}
	if srcInfo.Size() <= limit {
		_ = source.Close()
		return nil
	}
	offset := srcInfo.Size() - limit
	if _, err := source.Seek(offset, io.SeekStart); err != nil {
		_ = source.Close()
		return err
	}
	tail, err := io.ReadAll(io.LimitReader(source, limit))
	_ = source.Close()
	if err != nil {
		return err
	}

	prevPath := path + ".previous"
	if prevInfo, err := os.Lstat(prevPath); err == nil {
		if prevInfo.Mode()&os.ModeSymlink != 0 || !prevInfo.Mode().IsRegular() {
			return errors.New("previous log path must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	prevFile, err := os.OpenFile(prevPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	prevInfo, err := prevFile.Stat()
	if err != nil {
		_ = prevFile.Close()
		return err
	}
	if !prevInfo.Mode().IsRegular() {
		_ = prevFile.Close()
		return errors.New("previous log path must be a regular file")
	}
	if err := prevFile.Chmod(0o600); err != nil {
		_ = prevFile.Close()
		return err
	}
	if _, err := prevFile.Write(tail); err != nil {
		_ = prevFile.Close()
		return err
	}
	if err := prevFile.Sync(); err != nil {
		_ = prevFile.Close()
		return err
	}
	if err := prevFile.Close(); err != nil {
		return err
	}

	target, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer target.Close()
	targetInfo, err := target.Stat()
	if err != nil {
		return err
	}
	if !targetInfo.Mode().IsRegular() {
		return errors.New("service log must be a regular file")
	}
	if err := target.Truncate(0); err != nil {
		return fmt.Errorf("truncate service log: %w", err)
	}
	return nil
}

// TrimServiceLogDir trims all direct regular *.log files inside a private log directory
// while preserving live inodes and skipping symlinks or non-regular entries.
func TrimServiceLogDir(dir string, limit int64) error {
	if !filepath.IsAbs(dir) {
		return errors.New("service log directory must be absolute")
	}
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return errors.New("service log directory must be private and non-symlink")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var errs []error
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".log") || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if trimErr := TrimServiceLog(filepath.Join(dir, name), limit); trimErr != nil {
			errs = append(errs, trimErr)
		}
	}
	return errors.Join(errs...)
}

// RedactLogLine strips bearer tokens, API keys, and credential assignments.
func RedactLogLine(line string) string {
	result := line
	for _, rule := range redactionRules {
		result = rule.pattern.ReplaceAllString(result, rule.replacement)
	}
	return result
}

// ReadLogTail reads trailing redacted lines from a private regular log file.
func ReadLogTail(path string, maxLines int, maxBytes int64) ([]string, error) {
	if maxLines <= 0 {
		maxLines = 50
	}
	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}
	if !filepath.IsAbs(path) {
		return nil, errors.New("service log path must be absolute")
	}
	lstat, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if lstat.Mode()&os.ModeSymlink != 0 || !lstat.Mode().IsRegular() || lstat.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("service log must be a private regular file")
	}

	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("service log must be a private regular file")
	}
	size := info.Size()
	if size <= 0 {
		return nil, nil
	}

	span := size
	if span > maxBytes {
		span = maxBytes
	}
	if size > span {
		if _, err := f.Seek(size-span, io.SeekStart); err != nil {
			return nil, err
		}
	}
	raw, err := io.ReadAll(io.LimitReader(f, span))
	if err != nil {
		return nil, err
	}
	if size > span {
		if idx := bytes.IndexByte(raw, '\n'); idx >= 0 {
			raw = raw[idx+1:]
		}
	}

	var nonEmpty []string
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		nonEmpty = append(nonEmpty, RedactLogLine(line))
	}
	if len(nonEmpty) > maxLines {
		nonEmpty = nonEmpty[len(nonEmpty)-maxLines:]
	}
	return nonEmpty, nil
}
