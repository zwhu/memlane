package gateway

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zwhu/memlane/core/domain"
)

func (s *Server) credentialDir() string {
	return filepath.Join(filepath.Dir(s.cfg.Store.Path()), "generation-credentials")
}

func (s *Server) generationCredential(p domain.GenerationProfile) (string, error) {
	if p.CredentialRef == "" && p.CredentialVersion == "" {
		return "", nil
	}
	if !profileID.MatchString(p.CredentialRef) || !profileID.MatchString(p.CredentialVersion) {
		return "", domain.ErrInvalidArgument
	}
	dir := s.credentialDir()
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return "", domain.ErrForbidden
	}
	return s.secrets.read(filepath.Join(dir, p.CredentialRef+"-"+p.CredentialVersion))
}

func (s *Server) saveCredential(p domain.GenerationProfile, key string) (err error) {
	dir := s.credentialDir()
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return domain.ErrForbidden
	}
	f, err := os.CreateTemp(dir, ".credential-*")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.Remove(f.Name())) }()
	_, writeErr := f.WriteString(key)
	if err := errors.Join(writeErr, f.Sync(), f.Close()); err != nil {
		return err
	}
	path := filepath.Join(dir, p.CredentialRef+"-"+p.CredentialVersion)
	// Each immutable revision keeps the credential it was validated against.
	if err := os.Link(f.Name(), path); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		value, readErr := s.generationCredential(p)
		if readErr != nil {
			return readErr
		}
		if !secretMatch(value, key) {
			return domain.ErrConflict
		}
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

func (s *Server) modelConnection(p domain.GenerationProfile) (provider, endpoint, key string, err error) {
	provider, endpoint = p.Provider, p.Endpoint
	if p.Mode == "agent_bridge" {
		provider, endpoint = "openai", s.cfg.BridgeEndpoint
		if endpoint == "" && s.cfg.BridgeTokenFile == "" {
			return provider, "", "", nil
		}
		u, parseErr := url.Parse(endpoint)
		if parseErr != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return "", "", "", domain.ErrInvalidArgument
		}
		key, err = s.secrets.read(s.cfg.BridgeTokenFile)
	} else {
		key, err = s.generationCredential(p)
	}
	return
}

func qualifiedModel(provider, model string) string {
	prefix := provider
	switch provider {
	case "custom":
		prefix = "openai"
	case "ollama":
		prefix = "ollama_chat"
	}
	if strings.HasPrefix(model, prefix+"/") {
		return model
	}
	return prefix + "/" + model
}

func probeEnvironment(p domain.GenerationProfile, provider, endpoint, key string) map[string]string {
	if provider == "ollama" {
		key = "ollama"
	}
	env := map[string]string{"LLM_PROVIDER": provider, "LLM_ENDPOINT": endpoint, "LLM_MODEL": qualifiedModel(provider, p.Model),
		"STRUCTURED_OUTPUT_FRAMEWORK": "litellm_native", "LITELLM_LOCAL_MODEL_COST_MAP": "true"}
	if key != "" {
		env["LLM_API_KEY"] = key
	}
	if p.Temperature != nil {
		env["LLM_TEMPERATURE"] = strconv.FormatFloat(*p.Temperature, 'f', -1, 64)
	}
	if p.MaxOutputTokens != nil {
		env["LLM_MAX_COMPLETION_TOKENS"] = strconv.Itoa(*p.MaxOutputTokens)
		env["LLM_ARGS"] = fmt.Sprintf(`{"max_tokens":%d}`, *p.MaxOutputTokens)
	}
	return env
}
