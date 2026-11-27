package mcp

import (
	"net/http"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/gateway/agentauth"
)

const maxSecretBytes = 65536

func readSecret(path string) ([]byte, error) {
	return agentauth.ReadPrivateSecret(path)
}

func requestToken(r *http.Request) string {
	return agentauth.RequestToken(r)
}

func (s *Server) authenticate(r *http.Request) (domain.Agent, int, string) {
	token := requestToken(r)
	if token == "" || len(token) > maxSecretBytes {
		return domain.Agent{}, http.StatusUnauthorized, ""
	}
	res, err := s.authenticator().Authenticate(r.Context(), token)
	if err != nil {
		return domain.Agent{}, http.StatusServiceUnavailable, ""
	}
	switch res.Status {
	case agentauth.StatusOK:
		return res.Agent, http.StatusOK, ""
	case agentauth.StatusRetired, agentauth.StatusAmbiguous:
		return domain.Agent{}, http.StatusForbidden, res.Code
	default:
		return domain.Agent{}, http.StatusUnauthorized, ""
	}
}
