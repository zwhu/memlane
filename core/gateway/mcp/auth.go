package mcp

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"syscall"

	"github.com/zwhu/memlane/core/domain"
)

const maxSecretBytes = 4096

func readSecret(path string) ([]byte, error) {
	invalid := errors.New("invalid private key file")
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	private := func(info os.FileInfo) bool {
		return info.Mode() == 0o600 && info.Size() > 0 && info.Size() <= maxSecretBytes
	}
	if !private(before) {
		return nil, invalid
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !private(after) || !os.SameFile(before, after) {
		return nil, invalid
	}
	secret, err := io.ReadAll(io.LimitReader(file, maxSecretBytes+1))
	if err != nil {
		return nil, err
	}
	if len(secret) > maxSecretBytes {
		return nil, invalid
	}
	return secret, nil
}

func requestToken(r *http.Request) string {
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

func (s *Server) authenticate(r *http.Request) (domain.Agent, int) {
	token := requestToken(r)
	if token == "" || len(token) > maxSecretBytes {
		return domain.Agent{}, http.StatusUnauthorized
	}
	agents, err := s.cfg.Store.ListAgents(r.Context(), true)
	if err != nil {
		return domain.Agent{}, http.StatusServiceUnavailable
	}
	digest := sha256.Sum256([]byte(token))
	var match domain.Agent
	matches, denied := 0, false
	for _, agent := range agents {
		if agent.KeyFile == "" {
			continue
		}
		raw, err := s.cfg.SecretReader(agent.KeyFile)
		if err != nil || len(raw) > maxSecretBytes {
			continue
		}
		key := strings.TrimSpace(string(raw))
		if key == "" {
			continue
		}
		expected := sha256.Sum256([]byte(key))
		if subtle.ConstantTimeCompare(expected[:], digest[:]) == 1 {
			match = agent
			matches++
			denied = denied || agent.Archived || strings.TrimSpace(agent.RetiredAt) != ""
		}
	}
	switch {
	case matches == 0:
		return domain.Agent{}, http.StatusUnauthorized
	case matches != 1 || denied:
		return domain.Agent{}, http.StatusForbidden
	default:
		return match, http.StatusOK
	}
}
