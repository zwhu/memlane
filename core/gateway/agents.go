package gateway

import (
	"crypto/rand"
	"net/http"
	"strings"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/kit/agents"
)

type agentView struct {
	ID            string             `json:"id"`
	Kind          string             `json:"kind"`
	Name          string             `json:"name"`
	PrincipalID   string             `json:"principal_id"`
	DefaultAccess domain.AccessLevel `json:"default_access"`
	Archived      bool               `json:"archived"`
	RetiredAt     string             `json:"retired_at,omitempty"`
}

func publicAgent(a domain.Agent) agentView {
	return agentView{a.ID, a.Kind, a.Name, a.PrincipalID, a.DefaultAccess, a.Archived, a.RetiredAt}
}

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	registered, err := s.cfg.Store.ListAgents(r.Context(), true)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	out := make([]agentView, 0, len(registered))
	for _, a := range registered {
		out = append(out, publicAgent(a))
	}
	writeJSON(w, 200, map[string]any{"detected_hosts": agents.DetectHosts(s.cfg.DiscoveryOptions), "registered_agents": out})
}

func (s *Server) connectAgent(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID               string `json:"id"`
		AgentID          string `json:"agent_id"`
		Kind             string `json:"kind"`
		Name             string `json:"name"`
		PrincipalID      string `json:"principal_id"`
		KeyFile          string `json:"key_file"`
		ControlTokenFile string `json:"control_token_file"`
		HostProfile      string `json:"host_profile"`
	}
	if !decodeJSON(w, r, s.cfg.MaxBodyBytes, &in) {
		return
	}
	if in.ID == "" {
		in.ID = in.AgentID
	}
	if in.ID == "" {
		in.ID = rand.Text()
	}
	if strings.ContainsAny(in.ID, "/\\") || strings.TrimSpace(in.ID) != in.ID {
		writeDomainError(w, domain.ErrInvalidArgument)
		return
	}
	if in.Name == "" {
		in.Name = in.Kind
		if in.Name == "" {
			in.Name = in.ID
		}
	}
	a := domain.Agent{ID: in.ID, Kind: in.Kind, Name: in.Name, PrincipalID: in.PrincipalID, KeyFile: in.KeyFile,
		ControlTokenFile: in.ControlTokenFile, HostProfile: in.HostProfile, DefaultAccess: domain.AccessNone}
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	if err := s.cfg.Store.ConnectAgent(r.Context(), a); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, 201, publicAgent(a))
}

func (s *Server) retireAgent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	a, err := s.cfg.Store.GetAgent(ctx, r.PathValue("id"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if strings.TrimSpace(a.RetiredAt) == "" {
		a.RetiredAt = domain.NowUTCNS()
		if err := s.cfg.Store.UpsertAgent(ctx, a); err != nil {
			writeDomainError(w, err)
			return
		}
	}
	writeJSON(w, 200, publicAgent(a))
}

type conflictFinding struct {
	Kind    string                `json:"host_kind"`
	Surface string                `json:"surface"`
	Name    string                `json:"entry_name"`
	Path    string                `json:"config_path"`
	Action  agents.ConflictAction `json:"action"`
	Blocked bool                  `json:"blocked"`
}

func appendFindings(out []conflictFinding, source []agents.ConflictFinding) []conflictFinding {
	for _, f := range source {
		out = append(out, conflictFinding{f.HostKind, f.Surface, f.EntryName, f.ConfigPath, f.Action, f.Blocked})
	}
	return out
}

func (s *Server) agentConflicts(w http.ResponseWriter, r *http.Request) {
	in := struct {
		Kind string `json:"kind"`
	}{Kind: r.URL.Query().Get("kind")}
	if r.Method == http.MethodPost && r.ContentLength != 0 && !decodeJSON(w, r, s.cfg.MaxBodyBytes, &in) {
		return
	}
	opts := s.cfg.DiscoveryOptions
	if in.Kind != "" {
		opts.Kinds = agents.NormalizeKinds([]string{in.Kind})
		if len(opts.Kinds) == 0 {
			writeDomainError(w, domain.ErrInvalidArgument)
			return
		}
	}
	managed, thirdParty := []conflictFinding{}, []conflictFinding{}
	total, blocked := 0, false
	for _, host := range agents.DetectHosts(opts) {
		report, err := agents.ScanHostConflicts(host, *s.cfg.ConflictPolicy)
		if err != nil {
			writeError(w, 500, "conflict_scan_failed", "host configuration could not be scanned")
			return
		}
		managed = appendFindings(managed, report.ManagedLegacy)
		thirdParty = appendFindings(thirdParty, report.ThirdPartyConflicts)
		total += report.UnrelatedCount
		blocked = blocked || report.Blocked
	}
	writeJSON(w, 200, map[string]any{"managed_legacy": managed, "third_party_conflicts": thirdParty, "unrelated_count": total, "blocked": blocked})
}
