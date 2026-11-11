package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/records"
)

// Only a persisted, host-approved loopback address is ever fetched. Redirects,
// proxies and unbounded bodies are disabled; response errors never echo data.
func readCognee(ctx context.Context, endpoint, path, token string, out any) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return &records.AccessError{Code: "invalid_request", Message: "approved loopback Cognee service required"}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+path, nil)
	if err != nil {
		return err
	}
	if err = official.SetCredential(request.Header, token); err != nil {
		return err
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 12 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return &records.AccessError{Code: "cognee_unavailable", Message: "Cognee material service unavailable; retry after it recovers"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		code := "cognee_unavailable"
		if response.StatusCode == 401 || response.StatusCode == 403 || response.StatusCode == 404 {
			code = "scope_denied"
		}
		return &records.AccessError{Code: code, Message: fmt.Sprintf("Cognee material read returned HTTP %d", response.StatusCode)}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8*1024*1024+1))
	if err != nil {
		return err
	}
	if len(raw) > 8*1024*1024 {
		return &records.AccessError{Code: "resource_limit", Message: "Cognee material response exceeds 8 MiB"}
	}
	if err = json.Unmarshal(raw, out); err != nil {
		return &records.AccessError{Code: "invalid_provenance", Message: "Cognee returned an invalid material response"}
	}
	return nil
}

// Cognee authorizes independently of Agent credentials. Tokens are explicit
// private service files, never imported from an Agent login or persisted in a
// binding, response, activity or log. No symlink is followed.
func (s *Server) cogneeToken(instance, dataset string) (string, error) {
	parsed, err := uuid.Parse(instance)
	if err != nil || parsed.String() != instance {
		return "", &records.AccessError{Code: "invalid_request", Message: "canonical service instance required"}
	}
	if id, err := uuid.Parse(dataset); err != nil || id.String() != dataset {
		return "", &records.AccessError{Code: "invalid_request", Message: "canonical dataset identity required"}
	}
	path := filepath.Join(s.config.Root, "cognee-auth", instance+"-"+dataset+".token")
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", &records.AccessError{Code: "cognee_authorization_required", Message: "Cognee authorization file is unavailable"}
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 8192 {
		return "", &records.AccessError{Code: "cognee_authorization_required", Message: "Cognee authorization requires a private regular file"}
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != uint32(os.Getuid()) {
		return "", &records.AccessError{Code: "cognee_authorization_required", Message: "Cognee authorization file owner differs"}
	}
	raw, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(raw))
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return "", &records.AccessError{Code: "cognee_authorization_required", Message: "Cognee authorization token is invalid"}
	}
	return token, nil
}

func (s *Server) bindCognee(ctx context.Context, b records.CogneeBinding) (records.CogneeBinding, error) {
	s.agentAccess.RLock()
	err := s.store.AuthorizeCogneeGraphBinding(ctx, b)
	s.agentAccess.RUnlock()
	if err != nil {
		return b, err
	}
	var identity struct {
		Instance string `json:"instance"`
		Version  string `json:"cognee_version"`
		Adapter  string `json:"adapter"`
	}
	token, err := s.cogneeToken(b.Instance, b.Dataset)
	if err != nil {
		return b, err
	}
	if err = readCognee(ctx, b.Endpoint, "/api/v1/memlane/identity", token, &identity); err != nil {
		return b, err
	}
	if identity.Instance != b.Instance || identity.Version != "1.6.7" || identity.Adapter != "memlane-material-graph-v1" {
		return b, &records.AccessError{Code: "invalid_provenance", Message: "service instance or graph adapter changed"}
	}
	s.agentAccess.RLock()
	defer s.agentAccess.RUnlock()
	return s.store.BindCognee(ctx, b)
}

func (s *Server) queryCognee(ctx context.Context, q records.CogneeGraphQuery) (records.CogneeGraph, error) {
	out := records.CogneeGraph{Project: q.Project, State: "unconnected", Entities: []records.CogneeEntity{}, Mentions: []records.CogneeMention{}, Relations: []records.CogneeRelation{}}
	if parsed, err := uuid.Parse(q.Project); err != nil || parsed.String() != q.Project {
		return out, &records.AccessError{Code: "scope_required", Message: "choose a registered project"}
	}
	s.agentAccess.RLock()
	bindings, err := s.store.CogneeBindings(ctx, q.Project)
	if err == nil && q.Dataset != "" {
		selected := []records.CogneeBinding{}
		for _, b := range bindings {
			if b.Dataset == q.Dataset {
				selected = append(selected, b)
			}
		}
		bindings = selected
	}
	if err == nil && len(bindings) == 1 {
		err = s.store.AuthorizeCogneeGraphBinding(ctx, bindings[0])
	}
	s.agentAccess.RUnlock()
	if err != nil {
		return out, err
	}
	if len(bindings) == 0 {
		return out, nil
	}
	if len(bindings) != 1 {
		return out, &records.AccessError{Code: "scope_ambiguous", Message: "choose the approved Cognee dataset"}
	}
	b := bindings[0]
	var raw records.CogneeGraph
	token, err := s.cogneeToken(b.Instance, b.Dataset)
	if err != nil {
		return out, err
	}
	if err = readCognee(ctx, b.Endpoint, "/api/v1/memlane/datasets/"+b.Dataset+"/graph?expected_instance="+b.Instance, token, &raw); err != nil {
		return out, err
	}
	// Network work did not hold the revocation barrier. Recheck before returning
	// any material; disconnect can win while the official read is in flight.
	s.agentAccess.RLock()
	defer s.agentAccess.RUnlock()
	return s.store.ProjectCogneeGraph(ctx, b, raw)
}
