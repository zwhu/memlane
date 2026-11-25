package cli

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"syscall"

	"github.com/zwhu/memlane/kit/aiinstaller"
)

// SecretGuard recognizes credential flags and checks only the environment
// variables in the caller's namespace or explicit list.
type SecretGuard struct {
	// EnvPrefix is the product namespace, such as "EXAMPLE".
	EnvPrefix string
	// EnvNames contains additional complete environment variable names.
	EnvNames []string
}

// ContainsSecretInput reports whether a flag or configured environment name
// supplies credential input.
func (g SecretGuard) ContainsSecretInput(args []string, environ func(string) string) bool {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		name = strings.ReplaceAll(strings.ToLower(name), "_", "-")
		if slices.Contains([]string{"api-key", "apikey", "llm-api-key", "openai-api-key", "anthropic-api-key", "deepseek-api-key", "gemini-api-key",
			"token", "access-token", "bearer", "x-api-key", "secret", "password", "passwd", "credential", "private-key"}, name) {
			return true
		}
		for _, suffix := range []string{"-api-key", "-token", "-secret", "-password"} {
			if strings.HasSuffix(name, suffix) {
				return true
			}
		}
	}
	if environ == nil {
		environ = os.Getenv
	}
	for _, name := range DefaultSecretEnvNames(g.EnvPrefix) {
		if environ(name) != "" {
			return true
		}
	}
	for _, name := range g.EnvNames {
		if name != "" && environ(name) != "" {
			return true
		}
	}
	return false
}

// DefaultSecretEnvNames lists the credential variables for a product namespace.
func DefaultSecretEnvNames(prefix string) []string {
	if prefix == "" {
		return nil
	}
	suffixes := []string{"API_KEY", "LLM_API_KEY", "TOKEN", "OWNER_TOKEN", "MANAGEMENT_TOKEN", "SECRET", "PASSWORD"}
	names := make([]string, 0, len(suffixes))
	for _, suffix := range suffixes {
		names = append(names, prefix+"_"+suffix)
	}
	return names
}

var (
	localPath   = regexp.MustCompile(`(?i)(?:^|[\s"'=:/])\.local(?:/[^\s"'<>:,;)]*)?`)
	privateText = regexp.MustCompile(`(?i)owner\.token|management\.token|service-environment\.json|x-api-key|owner_token|hooks\.json`)
)

// SanitizeString is for display paths and known metadata. Arbitrary errors and
// log content must first be projected onto a finite set of safe values.
func SanitizeString(s, home string, extraRedactors []func(string) string, extraSecrets ...string) string {
	for _, secret := range extraSecrets {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "[redacted]")
		}
	}
	if home != "" && home != string(filepath.Separator) &&
		(s == home || strings.HasPrefix(s, home+string(filepath.Separator))) {
		s = "~" + strings.TrimPrefix(s, home)
	}
	if privateText.MatchString(s) {
		return "[redacted-private-content]"
	}
	s = localPath.ReplaceAllString(s, "[redacted-local-path]")
	s = aiinstaller.RedactText(s)
	for _, redact := range extraRedactors {
		s = redact(s)
	}
	return s
}

// SanitizeLogLine keeps one placeholder for each nonempty line outside the
// caller's allowlist.
func SanitizeLogLine(line, home string, allowedExact []string, extraRedactors []func(string) string, extraSecrets ...string) string {
	safe := strings.TrimSpace(SanitizeString(line, home, extraRedactors, extraSecrets...))
	if safe == "" || slices.Contains(allowedExact, safe) {
		return safe
	}
	return "[redacted-log-line]"
}

var (
	instanceID          = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	operationIdentifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
	longHash            = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)
	shortHash           = regexp.MustCompile(`^[a-fA-F0-9]{16}$`)
	versionPattern      = regexp.MustCompile(`^[0-9]+\.[0-9]+(?:\.[0-9]+)?(?:-[A-Za-z0-9.-]{1,60})?(?:\+[A-Za-z0-9.-]{1,60})?$`)
)

// SafeHash returns a hexadecimal digest of the expected length or an empty string.
func SafeHash(value string, short bool) string {
	if short && shortHash.MatchString(value) || !short && longHash.MatchString(value) {
		return value
	}
	return ""
}

// SafeOperationID returns a recognized operation identifier or an empty string.
func SafeOperationID(value string) string {
	if operationIdentifier.MatchString(value) {
		return value
	}
	return ""
}

// SafeInstanceID returns a recognized instance identifier or an empty string.
func SafeInstanceID(value string) string {
	if instanceID.MatchString(value) {
		return value
	}
	return ""
}

// Known returns the value if it is allowlisted.
func Known(value string, values ...string) string {
	if slices.Contains(values, value) {
		return value
	}
	return ""
}

// ValidVersion reports whether a value uses the supported version format.
func ValidVersion(value string) bool {
	return versionPattern.MatchString(value)
}

// ErrUnsafePath marks a rejected path or file.
var ErrUnsafePath = errors.New("unsafe path")

// CheckPath validates a path using ResolveSafePath.
func CheckPath(path string) error {
	_, err := ResolveSafePath(path)
	return err
}

// ResolveSafePath validates lexical components before normalization so traversal
// cannot hide a symlink. It permits the system aliases /tmp and /var on macOS.
func ResolveSafePath(path string) (string, error) {
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return "", ErrUnsafePath
	}
	root := string(filepath.Separator)
	current := root
	for part := range strings.SplitSeq(path, root) {
		switch part {
		case "", ".":
			continue
		case "..":
			return "", ErrUnsafePath
		}
		first := current == root
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		if runtime.GOOS != "darwin" || !first || (current != "/tmp" && current != "/var") {
			return "", ErrUnsafePath
		}
		resolved, err := filepath.EvalSymlinks(current)
		if err != nil || resolved != "/private"+current {
			return "", ErrUnsafePath
		}
		current = resolved
	}
	return current, nil
}

// ReadBoundedJSON decodes a regular file of at most 2 MiB.
func ReadBoundedJSON(path string, dst any) error {
	path, err := ResolveSafePath(path)
	if err != nil {
		return err
	}
	dir, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	f, err := dir.OpenFile(filepath.Base(path), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	const limit = 2 << 20
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return ErrUnsafePath
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return err
	}
	if len(raw) > limit {
		return ErrUnsafePath
	}
	return json.Unmarshal(raw, dst)
}
