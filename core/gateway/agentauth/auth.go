package agentauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"io"
	"maps"
	"net/http"
	"os"
	"strings"
	"sync"
	"syscall"

	"github.com/zwhu/memlane/core/domain"
)

const maxSecretBytes = 65536

type Status uint8

const (
	StatusOK Status = iota
	StatusNotRegistered
	StatusRetired
	StatusAmbiguous
)

type Result struct {
	Agent  domain.Agent
	Status Status
	Code   string
}

type Config struct {
	Store        *domain.Store
	AgentTokens  map[string]string
	SecretReader func(path string) ([]byte, error)
}

type Authenticator struct {
	store        *domain.Store
	tokens       map[string]string
	secretReader func(path string) ([]byte, error)
	cache        SecretFileCache
}

func New(cfg Config) *Authenticator {
	return &Authenticator{
		store:        cfg.Store,
		tokens:       maps.Clone(cfg.AgentTokens),
		secretReader: cfg.SecretReader,
	}
}

func SecretMatch(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	left, right := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(left[:], right[:]) == 1
}

func BearerToken(r *http.Request) string {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if ok && strings.EqualFold(scheme, "Bearer") {
		return strings.TrimSpace(token)
	}
	return ""
}

// RequestToken extracts a single unambiguous agent token from Authorization or X-Api-Key.
func RequestToken(r *http.Request) string {
	auth, key := r.Header.Values("Authorization"), r.Header.Values("X-Api-Key")
	if len(auth) > 1 || len(key) > 1 {
		return ""
	}
	if len(auth) == 1 {
		fields := strings.Fields(auth[0])
		if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
			return ""
		}
		if len(key) == 1 && strings.TrimSpace(key[0]) != fields[1] {
			return ""
		}
		return fields[1]
	}
	if len(key) == 1 {
		return strings.TrimSpace(key[0])
	}
	return ""
}

func (a *Authenticator) readSecret(path string) (string, error) {
	if path == "" {
		return "", domain.ErrNotFound
	}
	if a.secretReader != nil {
		raw, err := a.secretReader(path)
		if err != nil {
			return "", err
		}
		if len(raw) > maxSecretBytes {
			return "", domain.ErrForbidden
		}
		value := strings.TrimSpace(string(raw))
		if value == "" {
			return "", domain.ErrForbidden
		}
		return value, nil
	}
	return a.cache.Read(path)
}

func (a *Authenticator) Authenticate(ctx context.Context, tokens ...string) (Result, error) {
	validTokens := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if token != "" && len(token) <= maxSecretBytes {
			validTokens = append(validTokens, token)
		}
	}
	if len(validTokens) == 0 {
		return Result{Status: StatusNotRegistered, Code: "agent_not_registered"}, nil
	}
	all, err := a.store.ListAgents(ctx, true)
	if err != nil {
		return Result{}, err
	}
	var matched domain.Agent
	active, retired := 0, false
	for _, ag := range all {
		secrets := make([]string, 0, 3)
		if memTok := a.tokens[ag.ID]; memTok != "" {
			secrets = append(secrets, memTok)
		}
		for _, path := range []string{ag.KeyFile, ag.ControlTokenFile} {
			if path == "" {
				continue
			}
			// Only a verified private file can contribute an agent credential.
			if value, err := a.readSecret(path); err == nil {
				secrets = append(secrets, value)
			}
		}
		found := false
		for _, token := range validTokens {
			for _, key := range secrets {
				if SecretMatch(token, key) {
					found = true
				}
			}
		}
		if !found {
			continue
		}
		if ag.Archived || strings.TrimSpace(ag.RetiredAt) != "" {
			retired = true
		} else {
			matched, active = ag, active+1
		}
	}
	switch {
	case retired:
		return Result{Status: StatusRetired, Code: "agent_retired"}, nil
	case active == 1:
		return Result{Agent: matched, Status: StatusOK}, nil
	case active > 1:
		return Result{Status: StatusAmbiguous, Code: "agent_ambiguous"}, nil
	default:
		return Result{Status: StatusNotRegistered, Code: "agent_not_registered"}, nil
	}
}

type SecretFileCache struct {
	mu      sync.Mutex
	entries map[string]secretFileEntry
}

// fileStamp compares file identity and content/metadata timestamps while excluding Atim
// so reads on non-noatime mounts do not trigger spurious conflicts.
type fileStamp struct {
	dev  int32
	ino  uint64
	mode uint16
	size int64
	mtim syscall.Timespec
	ctim syscall.Timespec
}

func statStamp(st *syscall.Stat_t) fileStamp {
	return fileStamp{
		dev:  st.Dev,
		ino:  st.Ino,
		mode: st.Mode,
		size: st.Size,
		mtim: st.Mtimespec,
		ctim: st.Ctimespec,
	}
}

type secretFileEntry struct {
	info  os.FileInfo
	stamp fileStamp
	value string
}

// Read reads a 0600 regular secret file using O_NOFOLLOW and caches its trimmed content by stat identity.
func (c *SecretFileCache) Read(path string) (string, error) {
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
	rawStamp, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 ||
		info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || info.Size() <= 0 || info.Size() > maxSecretBytes {
		return "", domain.ErrForbidden
	}
	stamp := statStamp(rawStamp)
	c.mu.Lock()
	defer c.mu.Unlock()
	if cached, ok := c.entries[path]; ok && os.SameFile(info, cached.info) && cached.stamp == stamp {
		return cached.value, nil
	}
	data, err := io.ReadAll(io.LimitReader(f, maxSecretBytes+1))
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(data))
	if len(data) > maxSecretBytes || value == "" {
		return "", domain.ErrForbidden
	}
	after, err := f.Stat()
	if err != nil {
		return "", err
	}
	if afterRaw, ok := after.Sys().(*syscall.Stat_t); ok && statStamp(afterRaw) == stamp {
		if c.entries == nil || len(c.entries) >= 1024 {
			c.entries = make(map[string]secretFileEntry)
		}
		c.entries[path] = secretFileEntry{info: info, stamp: stamp, value: value}
	} else {
		return "", domain.ErrConflict
	}
	return value, nil
}

// ReadPrivateSecret performs a one-shot verified read of a 0600 regular file with O_NOFOLLOW.
func ReadPrivateSecret(path string) ([]byte, error) {
	var cache SecretFileCache
	value, err := cache.Read(path)
	if err != nil {
		return nil, err
	}
	return []byte(value), nil
}
