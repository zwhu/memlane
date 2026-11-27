package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
	"github.com/zwhu/memlane/core/gateway/completion"
	"github.com/zwhu/memlane/kit/agents"
	"github.com/zwhu/memlane/kit/daemon"
)

var profileID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var receiptCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var modelDigest = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var languageTag = regexp.MustCompile(`^[A-Za-z]{2,8}(?:-[A-Za-z0-9]{1,8})*$`)

type generationView struct {
	domain.GenerationProfile
	HasCredential        bool `json:"has_credential"`
	CredentialConfigured bool `json:"credential_configured"`
}

type agentModelDefaultsView struct {
	Model               string `json:"model"`
	Provider            string `json:"provider"`
	Source              string `json:"source,omitempty"`
	ConfigurationSource string `json:"configuration_source,omitempty"`
	ReasoningEffort     string `json:"reasoning_effort,omitempty"`
	ThinkingLevel       string `json:"thinking_level,omitempty"`
	ServiceTier         string `json:"service_tier,omitempty"`
}

type agentBridgeProfileView struct {
	Mode           string `json:"mode"`
	BridgeKind     string `json:"bridge_kind"`
	BridgeProvider string `json:"bridge_provider,omitempty"`
	Model          string `json:"model"`
}

func sanitizeAgentDefaults(in agentModelDefaultsView, secrets []string) (agentModelDefaultsView, bool) {
	if strings.TrimSpace(in.Model) == "" {
		return agentModelDefaultsView{}, false
	}
	for _, value := range []string{
		in.Model, in.Provider, in.Source, in.ConfigurationSource,
		in.ReasoningEffort, in.ThinkingLevel, in.ServiceTier,
	} {
		if value == "" {
			continue
		}
		if containsCredential(value, secrets) || daemon.RedactLogLine(value) != value {
			return agentModelDefaultsView{}, false
		}
	}
	return in, true
}

func readHostModelDefaults(host agents.DetectedHost, opts agents.DiscoveryOptions, modelOpts agents.ModelDefaultsOptions, credentials []string) (agentModelDefaultsView, bool) {
	profileDir := host.ProfileDir
	if profileDir == "" {
		profileDir = agents.DefaultProfileDir(host.Kind, opts)
	}
	var defaults agents.ModelDefaults
	var err error
	if host.Kind == agents.HostAntigravity {
		if !filepath.IsAbs(host.Executable) || !filepath.IsAbs(profileDir) {
			return agentModelDefaultsView{}, false
		}
		if _, err = filepath.EvalSymlinks(host.Executable); err != nil {
			return agentModelDefaultsView{}, false
		}
		resolvedHome, resolveErr := filepath.EvalSymlinks(profileDir)
		if resolveErr != nil {
			return agentModelDefaultsView{}, false
		}
		st, statErr := os.Stat(resolvedHome)
		if statErr != nil || !st.IsDir() {
			return agentModelDefaultsView{}, false
		}
		defaults, err = agents.ReadModelDefaults(host.Kind, resolvedHome, modelOpts)
	} else {
		runtime, runtimeErr := agents.CheckBridgeRuntime(host.Kind, host.Executable, profileDir, modelOpts)
		if runtimeErr != nil {
			return agentModelDefaultsView{}, false
		}
		defaults, err = agents.ReadModelDefaults(host.Kind, runtime.Home, modelOpts)
	}
	if err != nil || defaults.Model == "" {
		return agentModelDefaultsView{}, false
	}
	return sanitizeAgentDefaults(agentModelDefaultsView{
		Model:               defaults.Model,
		Provider:            defaults.Provider,
		Source:              defaults.Source,
		ConfigurationSource: defaults.ConfigurationSource,
		ReasoningEffort:     defaults.ReasoningEffort,
		ThinkingLevel:       defaults.ThinkingLevel,
		ServiceTier:         defaults.ServiceTier,
	}, credentials)
}

func detectGenerationBridges(opts agents.DiscoveryOptions, credentials []string) ([]string, map[string]agentModelDefaultsView, map[string]agentBridgeProfileView) {
	processing := []string{}
	agentDefaults := map[string]agentModelDefaultsView{}
	agentBridges := map[string]agentBridgeProfileView{}
	// Always isolate session environment variables so GET /api/generation reads
	// stable CLI defaults without copying session state_*.sqlite databases.
	modelOpts := agents.ModelDefaultsOptions{Environ: func(string) string { return "" }}
	for _, host := range agents.DetectHosts(opts) {
		if !host.Detected || !host.SupportsProcessingCLI || host.Executable == "" {
			continue
		}
		processing = append(processing, host.Kind)
		defaults, ok := readHostModelDefaults(host, opts, modelOpts, credentials)
		if !ok {
			continue
		}
		agentDefaults[host.Kind] = defaults
		bridge := agentBridgeProfileView{
			Mode:       "agent_bridge",
			BridgeKind: host.Kind,
			Model:      defaults.Model,
		}
		// domain.ValidateGenerationProfile requires bridge_provider only for pi
		// and rejects non-empty bridge_provider for fixed-provider CLIs.
		if host.Kind == agents.HostPi {
			bridge.BridgeProvider = defaults.Provider
		}
		agentBridges[host.Kind] = bridge
	}
	return processing, agentDefaults, agentBridges
}

func detectBridgeDefaultsForKind(opts agents.DiscoveryOptions, kind string, credentials []string) (agentModelDefaultsView, bool) {
	if kind == "" {
		return agentModelDefaultsView{}, false
	}
	scoped := opts
	scoped.Kinds = []string{kind}
	_, defaults, _ := detectGenerationBridges(scoped, credentials)
	val, ok := defaults[kind]
	return val, ok
}

func (s *Server) generation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if hasPending, err := s.hasPendingGenerationHandoffs(ctx); err == nil && hasPending {
		s.maybeReconcileHandoffs(ctx, false)
	}
	state, err := s.cfg.Store.GetGenerationState(ctx)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	profiles, err := s.cfg.Store.ListGenerationProfiles(ctx)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	var credentials []string
	out := make([]generationView, 0, len(profiles))
	for _, p := range profiles {
		key, err := s.generationCredential(p)
		has := p.CredentialRef != "" && err == nil && key != ""
		if has {
			credentials = append(credentials, key)
		}
		out = append(out, generationView{p, has, has})
	}
	if key, err := s.secrets.read(s.cfg.BridgeTokenFile); err == nil {
		credentials = append(credentials, key)
	}
	validations, err := s.generationValidations(ctx, credentials)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	processing, agentDefaults, agentBridges := detectGenerationBridges(s.cfg.DiscoveryOptions, credentials)
	var model any
	outputLanguage := s.cfg.OutputLanguage
	if !languageTag.MatchString(outputLanguage) || containsCredential(outputLanguage, credentials) {
		outputLanguage = ""
	}
	if s.cfg.Models != nil {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		status, err := s.cfg.Models.ModelStatus(ctx)
		if err != nil {
			model = map[string]any{"ok": false, "error": "model_status_unavailable"}
		} else {
			status.Mode = cleanReceipt(status.Mode, credentials...)
			language := status.OutputLanguage
			if !languageTag.MatchString(language) || containsCredential(language, credentials) || daemon.RedactLogLine(language) != language {
				status.OutputLanguage = ""
			}
			status.ModelDigest = safeModelDigest(status.ModelDigest, credentials...)
			if status.OutputLanguage != "" {
				outputLanguage = status.OutputLanguage
			}
			model = status
		}
	}
	startupRaw := state.StartupError
	if startupRaw == "" {
		startupRaw = s.startupModelError()
	}
	startup := sanitizeError(errors.New(startupRaw))
	for _, key := range credentials {
		startup = strings.ReplaceAll(startup, key, "[REDACTED]")
	}
	resp := map[string]any{"state": map[string]any{"active_profile_id": state.ActiveProfileID,
		"active_revision": state.ActiveRevision, "next_profile_id": state.NextProfileID, "next_revision": state.NextRevision,
		"revision": state.Revision, "startup_error": startup}, "profiles": out, "model_status": model, "validations": validations,
		"handoff_available": s.cfg.Models != nil, "output_language": outputLanguage, "detected_processing_clis": processing,
		"agent_defaults": agentDefaults, "agent_bridges": agentBridges}
	if pending, ok, err := s.latestPendingHandoff(ctx); err == nil && ok {
		resp["active_handoff"] = publicHandoff(pending)
	}
	writeJSON(w, 200, resp)
}

type validationView struct {
	OperationID        string                  `json:"-"`
	Digest             string                  `json:"digest"`
	RegisteredRevision int                     `json:"registered_revision"`
	Status             string                  `json:"status"`
	PlainPassed        bool                    `json:"plain_passed"`
	SchemaPassed       bool                    `json:"schema_passed"`
	QualityPassed      bool                    `json:"quality_passed"`
	StructuredMode     string                  `json:"structured_mode"`
	ErrorCode          string                  `json:"error_code"`
	AgentDefaults      *agentModelDefaultsView `json:"agent_defaults,omitempty"`
	Reference          struct {
		ID       string `json:"id"`
		Revision int    `json:"revision"`
	} `json:"reference"`
}

func (s *Server) generationValidations(ctx context.Context, secrets []string) (map[string]validationView, error) {
	type validationRow struct {
		view        validationView
		payloadJSON string
	}
	rows, err := readRows(ctx, s.cfg.Store.DB(), `SELECT operation_id,profile_id,profile_revision,digest,registered_revision,
  status,plain_passed,schema_passed,quality_passed,structured_mode,error_code,payload_json FROM generation_validations`, nil,
		func(row rowScanner) (validationRow, error) {
			var r validationRow
			err := row.Scan(&r.view.OperationID, &r.view.Reference.ID, &r.view.Reference.Revision, &r.view.Digest, &r.view.RegisteredRevision,
				&r.view.Status, &r.view.PlainPassed, &r.view.SchemaPassed, &r.view.QualityPassed, &r.view.StructuredMode, &r.view.ErrorCode, &r.payloadJSON)
			return r, err
		})
	if err != nil {
		return nil, err
	}
	out := make(map[string]validationView, len(rows))
	for _, r := range rows {
		v := r.view
		op := safeModelDigest(v.OperationID, secrets...)
		if op == "" {
			continue
		}
		v.Digest = safeModelDigest(v.Digest, secrets...)
		v.Reference.ID = safeModelDigest(v.Reference.ID, secrets...)
		v.Status = cleanReceipt(v.Status, secrets...)
		v.StructuredMode = cleanReceipt(v.StructuredMode, secrets...)
		v.ErrorCode = cleanReceipt(v.ErrorCode, secrets...)
		if r.payloadJSON != "" {
			var payload struct {
				AgentDefaults *agentModelDefaultsView `json:"agent_defaults"`
			}
			if json.Unmarshal([]byte(r.payloadJSON), &payload) == nil && payload.AgentDefaults != nil {
				if clean, ok := sanitizeAgentDefaults(*payload.AgentDefaults, secrets); ok {
					v.AgentDefaults = &clean
				}
			}
		}
		out[op] = v
	}
	return out, nil
}

func (s *Server) generationValidation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	profiles, err := s.cfg.Store.ListGenerationProfiles(ctx)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	secrets := []string{}
	for _, p := range profiles {
		if key, err := s.generationCredential(p); err == nil && key != "" {
			secrets = append(secrets, key)
		}
	}
	if key, err := s.secrets.read(s.cfg.BridgeTokenFile); err == nil {
		secrets = append(secrets, key)
	}
	validations, err := s.generationValidations(ctx, secrets)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	validation, ok := validations[r.PathValue("op")]
	if !ok {
		writeError(w, 404, "generation_validation_not_found", "validation not found")
		return
	}
	writeJSON(w, 200, validation)
}

func checkGenerationRevision(state domain.GenerationState, expected *int) error {
	if expected != nil && state.Revision != *expected {
		return domain.ErrConflict
	}
	return nil
}

func (s *Server) saveProfile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		domain.GenerationProfile
		APIKey           *string                   `json:"api_key"`
		Profile          *domain.GenerationProfile `json:"profile"`
		ExpectedRevision *int                      `json:"expected_revision"`
		OperationID      string                    `json:"operation_id"`
	}
	if !decodeJSON(w, r, s.cfg.MaxBodyBytes, &in) {
		return
	}
	p := in.GenerationProfile
	if in.Profile != nil {
		p = *in.Profile
	}
	if id := r.PathValue("id"); id != "" {
		p.ProfileID = id
	}
	if p.ProfileID == "" {
		p.ProfileID = rand.Text()
	}
	if !profileID.MatchString(p.ProfileID) {
		writeDomainError(w, domain.ErrInvalidArgument)
		return
	}
	p.Revision, p.Digest, p.CreatedAt, p.Removed = 0, "", "", false
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	if in.ExpectedRevision != nil {
		state, err := s.cfg.Store.GetGenerationState(r.Context())
		if err != nil {
			writeDomainError(w, err)
			return
		}
		if err := checkGenerationRevision(state, in.ExpectedRevision); err != nil {
			writeDomainError(w, err)
			return
		}
	}
	if in.Profile != nil && in.APIKey == nil && p.CredentialVersion != "" {
		prior, err := s.cfg.Store.ListGenerationProfiles(r.Context())
		if err != nil {
			writeDomainError(w, err)
			return
		}
		for _, v := range prior {
			if v.ProfileID == p.ProfileID && !v.Removed && v.CredentialVersion == p.CredentialVersion {
				p.CredentialRef = v.CredentialRef
				break
			}
		}
	}
	var key string
	if in.APIKey != nil {
		key = strings.TrimSpace(*in.APIKey)
		if p.Mode != "provider" || p.Provider == "ollama" || key == "" || len(key) > 65536 || strings.ContainsAny(key, "\r\n\x00") {
			writeDomainError(w, domain.ErrInvalidArgument)
			return
		}
		if p.CredentialRef == "" {
			p.CredentialRef = p.ProfileID
		}
		if p.CredentialVersion == "" {
			p.CredentialVersion = rand.Text()
		}
	}
	if err := domain.ValidateGenerationProfile(p); err != nil {
		writeDomainError(w, err)
		return
	}
	if key != "" {
		if err := s.saveCredential(p, key); err != nil {
			writeDomainError(w, err)
			return
		}
	} else if p.CredentialRef != "" {
		var err error
		key, err = s.generationCredential(p)
		if err != nil {
			writeDomainError(w, domain.ErrForbidden)
			return
		}
	}
	if in.OperationID != "" && (!profileID.MatchString(in.OperationID) || containsCredential(in.OperationID, []string{key})) {
		writeDomainError(w, domain.ErrInvalidArgument)
		return
	}
	p, err := s.cfg.Store.SaveGenerationProfile(r.Context(), p, in.OperationID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	status := 200
	if r.Method == http.MethodPost {
		status = 201
	}
	view := generationView{p, key != "", key != ""}
	if in.Profile != nil {
		writeJSON(w, status, struct {
			generationView
			Result map[string]any `json:"result"`
		}{view, map[string]any{"id": p.ProfileID, "revision": p.Revision, "digest": p.Digest}})
		return
	}
	writeJSON(w, status, view)
}

func (s *Server) removeProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	var in struct {
		ExpectedRevision *int   `json:"expected_revision"`
		OperationID      string `json:"operation_id"`
		ProfileRevision  int    `json:"profile_revision"`
	}
	settingsRequest := r.Method == http.MethodPost && r.ContentLength != 0
	if settingsRequest && !decodeJSON(w, r, s.cfg.MaxBodyBytes, &in) {
		return
	}
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	state, err := s.cfg.Store.GetGenerationState(ctx)
	if err == nil {
		err = checkGenerationRevision(state, in.ExpectedRevision)
	}
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if state.ActiveProfileID == id || state.NextProfileID == id {
		writeError(w, 409, "active_profile_cannot_be_removed", "selected generation profile cannot be removed")
		return
	}
	var pendingRef bool
	if err := s.cfg.Store.DB().QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM generation_handoffs WHERE profile_id=? AND status IN ('applying','result_unknown'))`,
		id).Scan(&pendingRef); err != nil {
		writeDomainError(w, err)
		return
	}
	if pendingRef {
		writeError(w, 409, "pending_handoff_profile_cannot_be_removed", "profile referenced by a pending handoff cannot be removed")
		return
	}
	receipt := map[string]any{"id": id, "deleted": true}
	err = s.cfg.Store.WithTx(ctx, func(tx *sql.Tx) error {
		if in.ProfileRevision > 0 {
			var latest int
			if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(revision),0) FROM generation_profiles WHERE profile_id=? AND removed=0", id).Scan(&latest); err != nil {
				return err
			}
			if latest != in.ProfileRevision {
				return domain.ErrConflict
			}
		}
		result, err := tx.ExecContext(ctx, "UPDATE generation_profiles SET removed=1 WHERE profile_id=?", id)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			return domain.ErrNotFound
		}
		return domain.RecordGenerationOperation(ctx, tx, in.OperationID, receipt)
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if settingsRequest {
		writeJSON(w, 200, map[string]any{"result": receipt})
		return
	}
	w.WriteHeader(204)
}

func (s *Server) generationOperation(w http.ResponseWriter, r *http.Request) {
	// Return only fields written by the Go gateway. Imported operation payloads
	// can have backend diagnostics that are not part of this public receipt.
	raw, err := s.cfg.Store.GetGenerationOperation(r.Context(), r.PathValue("op"))
	if errors.Is(err, domain.ErrNotFound) {
		writeJSON(w, 200, map[string]bool{"recorded": false})
		return
	}
	if err != nil {
		writeDomainError(w, err)
		return
	}
	var record struct {
		Recorded bool `json:"recorded"`
		Result   struct {
			ID       string         `json:"id,omitempty"`
			Revision int            `json:"revision,omitempty"`
			Digest   string         `json:"digest,omitempty"`
			Deleted  bool           `json:"deleted,omitempty"`
			Next     *generationRef `json:"next,omitempty"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &record); err != nil || !record.Recorded ||
		(!profileID.MatchString(record.Result.ID) && (record.Result.Next == nil || !profileID.MatchString(record.Result.Next.ID) || record.Result.Next.Revision <= 0)) {
		writeJSON(w, 200, map[string]bool{"recorded": false})
		return
	}
	record.Result.Digest = safeModelDigest(record.Result.Digest)
	writeJSON(w, 200, record)
}

type generationRef struct {
	ID       string `json:"id"`
	Revision int    `json:"revision"`
}

type profileSelection struct {
	ProfileID        string         `json:"profile_id"`
	Revision         int            `json:"revision"`
	Profile          *generationRef `json:"profile"`
	OperationID      string         `json:"operation_id"`
	ExpectedRevision *int           `json:"expected_revision"`
}

func (s *Server) selectedProfile(ctx context.Context, in profileSelection) (domain.GenerationProfile, error) {
	if in.Profile != nil {
		in.ProfileID, in.Revision = in.Profile.ID, in.Profile.Revision
	}
	if !profileID.MatchString(in.ProfileID) || in.Revision < 0 {
		return domain.GenerationProfile{}, domain.ErrInvalidArgument
	}
	if in.Revision == 0 {
		err := s.cfg.Store.DB().QueryRowContext(ctx, "SELECT revision FROM generation_profiles WHERE profile_id=? ORDER BY revision DESC LIMIT 1",
			in.ProfileID).Scan(&in.Revision)
		if err != nil {
			return domain.GenerationProfile{}, err
		}
	}
	p, err := s.cfg.Store.GetGenerationProfile(ctx, in.ProfileID, in.Revision)
	if err == nil && p.Removed {
		return p, domain.ErrForbidden
	}
	return p, err
}

func cleanReceipt(value string, secrets ...string) string {
	if !receiptCode.MatchString(value) {
		return ""
	}
	return safeModelDigest(value, secrets...)
}

func containsCredential(value string, secrets []string) bool {
	for _, secret := range secrets {
		if secret != "" && strings.Contains(value, secret) {
			return true
		}
	}
	return false
}

func safeModelDigest(value string, secrets ...string) string {
	if modelDigest.MatchString(value) && !containsCredential(value, secrets) {
		return sanitizeError(errors.New(value))
	}
	return ""
}

func (s *Server) validateProfile(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Models == nil {
		writeError(w, 503, "generation_validation_unavailable", "model validation unavailable")
		return
	}
	var in profileSelection
	if !decodeJSON(w, r, s.cfg.MaxBodyBytes, &in) {
		return
	}
	ctx := r.Context()
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	state, err := s.cfg.Store.GetGenerationState(ctx)
	if err == nil {
		err = checkGenerationRevision(state, in.ExpectedRevision)
	}
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if in.OperationID != "" {
		if !profileID.MatchString(in.OperationID) {
			writeDomainError(w, domain.ErrInvalidArgument)
			return
		}
		var exists bool
		if err := s.cfg.Store.DB().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM generation_validations WHERE operation_id=?)`, in.OperationID).Scan(&exists); err != nil {
			writeDomainError(w, err)
			return
		}
		if exists {
			writeError(w, 409, "operation_id_conflict", "validation operation already recorded")
			return
		}
	}
	p, err := s.selectedProfile(ctx, in)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	provider, endpoint, key, err := s.modelConnection(p)
	if err != nil {
		writeError(w, 503, "generation_credential_unavailable", "model credential unavailable")
		return
	}
	capacity := 0
	if p.MaxConcurrentRequests != nil {
		capacity = *p.MaxConcurrentRequests
	}
	backend := provider
	if p.BridgeKind != "" {
		backend = p.BridgeKind
	}
	// When validating an agent_bridge candidate in daemon mode, mount an isolated
	// ephemeral completion bridge for that candidate so probing an inactive CLI
	// never collides with the currently active profile.
	if p.Mode == "agent_bridge" && s.cfg.BridgeEndpoint != "" {
		var secret [32]byte
		if _, randErr := rand.Read(secret[:]); randErr != nil {
			writeDomainError(w, randErr)
			return
		}
		probeToken := hex.EncodeToString(secret[:])
		candidate := &completion.ActiveProfile{
			Mode:           p.Mode,
			BridgeKind:     p.BridgeKind,
			BridgeProvider: p.BridgeProvider,
			Model:          p.Model,
		}
		if p.BridgeKind != "" {
			opts := s.cfg.DiscoveryOptions
			opts.Kinds = []string{p.BridgeKind}
			if hosts := agents.DetectHosts(opts); len(hosts) > 0 {
				candidate.Executable = hosts[0].Executable
				candidate.AuthHome = hosts[0].ProfileDir
			}
			if candidate.AuthHome == "" {
				candidate.AuthHome = agents.DefaultProfileDir(p.BridgeKind, s.cfg.DiscoveryOptions)
			}
		}
		probeSrv, srvErr := completion.NewServer(completion.Config{
			Token: probeToken,
			Gate:  completion.StaticProfileGate{Profile: candidate},
		})
		if srvErr != nil {
			writeDomainError(w, srvErr)
			return
		}
		addr, listenErr := probeSrv.StartLoopback("127.0.0.1:0")
		if listenErr != nil {
			writeDomainError(w, listenErr)
			return
		}
		defer func() {
			shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = probeSrv.Shutdown(shutCtx)
		}()
		endpoint = "http://" + addr + "/v1"
		key = probeToken
	}
	res, probeErr := s.cfg.Models.ProbeModel(ctx, engine.ProbeRequest{GenerationMode: p.Mode, BackendKind: backend,
		Environment: probeEnvironment(p, provider, endpoint, key), Runtime: maps.Clone(s.cfg.ProbeRuntime), MaxConcurrentRequests: capacity})
	res.Status = cleanReceipt(res.Status, key)
	res.ErrorCode = cleanReceipt(res.ErrorCode, key)
	res.StructuredMode = cleanReceipt(res.StructuredMode, key)
	if probeErr != nil || res.Status != "passed" && res.Status != "failed" && res.Status != "result_unknown" {
		code := "probe_result_unconfirmed"
		if errors.Is(probeErr, context.DeadlineExceeded) {
			code = "validation_timeout"
		}
		res = engine.ProbeResult{Status: "result_unknown", ErrorCode: code}
	}
	payloadObj := map[string]any{}
	var validatedDefaults *agentModelDefaultsView
	if p.Mode == "agent_bridge" && res.Status == "passed" {
		if defaults, ok := detectBridgeDefaultsForKind(s.cfg.DiscoveryOptions, p.BridgeKind, []string{key}); ok {
			snapshot := defaults
			validatedDefaults = &snapshot
			payloadObj["agent_defaults"] = snapshot
		}
	}
	if p.Provider == "ollama" && res.Status == "passed" {
		want := map[string]string{"contract": "ollama-native-nonce-v1", "status": "verified",
			"model": qualifiedModel(provider, p.Model), "endpoint_sha256": digest([]byte(endpoint))}
		evidence, ok := res.Details["native_schema_evidence"].(map[string]any)
		matches := ok && len(evidence) == len(want)
		for field, value := range want {
			if evidence[field] != value {
				matches = false
			}
		}
		if matches {
			payloadObj["ollama_schema_evidence"] = want
		}
	}
	payload := "{}"
	if len(payloadObj) > 0 {
		raw, err := json.Marshal(payloadObj)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		payload = string(raw)
	}
	var rev int
	if err := s.cfg.Store.DB().QueryRowContext(ctx, `SELECT COALESCE(MAX(registered_revision),0)+1 FROM generation_validations WHERE digest=?`, p.Digest).Scan(&rev); err != nil {
		writeDomainError(w, err)
		return
	}
	op := in.OperationID
	if op == "" {
		op = rand.Text()
	} else if !modelDigest.MatchString(op) || containsCredential(op, []string{key}) {
		writeDomainError(w, domain.ErrInvalidArgument)
		return
	}
	record := domain.GenerationValidation{OperationID: op, ProfileID: p.ProfileID, ProfileRevision: p.Revision, Digest: p.Digest, RegisteredRevision: rev,
		Status: res.Status, PlainPassed: res.PlainPassed, SchemaPassed: res.SchemaPassed, QualityPassed: res.QualityPassed,
		StructuredMode: res.StructuredMode, ErrorCode: res.ErrorCode, PayloadJSON: payload}
	if err := s.cfg.Store.RecordGenerationValidation(ctx, record); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, 200, struct {
		engine.ProbeResult
		AgentDefaults *agentModelDefaultsView `json:"agent_defaults,omitempty"`
		OperationID   string                  `json:"operation_id"`
		Result        map[string]string       `json:"result"`
	}{res, validatedDefaults, op, map[string]string{"operation_id": op}})
}

func (s *Server) applyConfig(ctx context.Context, p domain.GenerationProfile) (map[string]any, string, error) {
	return s.applyConfigWithEvidence(ctx, p, nil)
}

func (s *Server) applyConfigWithEvidence(ctx context.Context, p domain.GenerationProfile, frozenEvidence map[string]string) (map[string]any, string, error) {
	_, endpoint, key, err := s.modelConnection(p)
	if err != nil {
		return nil, "", err
	}
	if p.Mode == "provider" && p.Provider != "ollama" && key == "" {
		return nil, "", domain.ErrForbidden
	}
	cfg := map[string]any{"mode": p.Mode, "model": p.Model}
	for name, value := range map[string]string{"provider": p.Provider, "endpoint": endpoint, "bridge_kind": p.BridgeKind,
		"bridge_provider": p.BridgeProvider, "api_key": key} {
		if value != "" {
			cfg[name] = value
		}
	}
	if p.Temperature != nil {
		cfg["temperature"] = *p.Temperature
	}
	if p.MaxOutputTokens != nil {
		cfg["max_output_tokens"] = *p.MaxOutputTokens
	}
	if p.MaxConcurrentRequests != nil {
		cfg["max_concurrent_requests"] = *p.MaxConcurrentRequests
	}
	if p.Provider == "ollama" {
		if len(frozenEvidence) > 0 {
			cfg["ollama_schema_evidence"] = maps.Clone(frozenEvidence)
		} else {
			evidence, err := s.latestOllamaSchemaEvidence(ctx, p.Digest)
			if err != nil {
				return nil, "", err
			}
			if len(evidence) > 0 {
				cfg["ollama_schema_evidence"] = evidence
			}
		}
	}
	return cfg, key, nil
}

func (s *Server) latestOllamaSchemaEvidence(ctx context.Context, digest string) (map[string]string, error) {
	rows, err := readRows(ctx, s.cfg.Store.DB(), `SELECT status,schema_passed,payload_json FROM generation_validations
		WHERE digest=? ORDER BY registered_revision DESC`, []any{digest},
		func(row rowScanner) (struct {
			status       string
			schemaPassed bool
			payloadJSON  string
		}, error) {
			var r struct {
				status       string
				schemaPassed bool
				payloadJSON  string
			}
			err := row.Scan(&r.status, &r.schemaPassed, &r.payloadJSON)
			return r, err
		})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, sql.ErrNoRows
	}
	var fallback map[string]string
	for _, r := range rows {
		var payload struct {
			Evidence       map[string]string `json:"ollama_schema_evidence"`
			LegacyEvidence map[string]string `json:"native_schema_evidence"`
		}
		if err := json.Unmarshal([]byte(r.payloadJSON), &payload); err != nil {
			continue
		}
		ev := payload.Evidence
		if len(ev) == 0 {
			ev = payload.LegacyEvidence
		}
		if len(ev) == 0 {
			continue
		}
		if r.status == "passed" && r.schemaPassed {
			return ev, nil
		}
		if r.status == "passed" && fallback == nil {
			fallback = ev
		}
	}
	return fallback, nil
}

type handoffStoredPayload struct {
	OllamaSchemaEvidence map[string]string `json:"ollama_schema_evidence,omitempty"`
	AppliedModelDigest   string            `json:"applied_model_digest,omitempty"`
}

func decodeHandoffPayload(raw string) handoffStoredPayload {
	var out handoffStoredPayload
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

func encodeHandoffPayload(p handoffStoredPayload) string {
	if len(p.OllamaSchemaEvidence) == 0 && p.AppliedModelDigest == "" {
		return "{}"
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func (s *Server) serveHandoffs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	inbox, err := s.inbox.Summary(ctx, "")
	if err != nil {
		writeDomainError(w, err)
		return
	}
	var result any
	if op := r.PathValue("op"); op != "" {
		var row domain.GenerationHandoff
		row, err = s.cfg.Store.GetGenerationHandoff(ctx, op)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		if row.Status == "result_unknown" || row.Status == "applying" {
			s.maybeReconcileHandoffs(ctx, true)
			if refreshed, refreshErr := s.cfg.Store.GetGenerationHandoff(ctx, op); refreshErr == nil {
				row = refreshed
			}
		}
		result = publicHandoff(row)
	}
	writeJSON(w, 200, map[string]any{"handoff": result, "pending_sessions": inbox.Pending, "running_sessions": inbox.Processing})
}

func publicHandoff(v domain.GenerationHandoff) map[string]any {
	status := cleanReceipt(v.Status)
	switch status {
	case "failed":
		status = "not_applied"
	case "result_unknown":
		status = "unknown"
	}
	reason := ""
	switch v.Reason {
	case "model_apply_failed", "model_apply_unconfirmed", "operation_receipt_mismatch", "active_commit_failed":
		reason = v.Reason
	}
	return map[string]any{"operation_id": safeModelDigest(v.OperationID), "status": status, "reason": reason,
		"reference": map[string]any{"id": safeModelDigest(v.ProfileID), "revision": v.ProfileRevision}, "cancelled_sessions": v.CancelledSessions}
}

func (s *Server) selectNextGeneration(w http.ResponseWriter, r *http.Request, in profileSelection) {
	ctx := r.Context()
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	state, err := s.cfg.Store.GetGenerationState(ctx)
	if err == nil {
		err = checkGenerationRevision(state, in.ExpectedRevision)
	}
	if err != nil {
		writeDomainError(w, err)
		return
	}
	p, err := s.selectedProfile(ctx, in)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if p.Mode != "agent_bridge" {
		valid, err := s.cfg.Store.IsGenerationDigestValidated(ctx, p.Digest)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		if !valid {
			writeError(w, 409, "validation_required", "validate this revision before selecting it")
			return
		}
	}
	receipt := map[string]any{"next": generationRef{p.ProfileID, p.Revision}}
	err = s.cfg.Store.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE generation_state SET next_profile_id=?,next_revision=?,revision=revision+1,updated_at=?
   WHERE singleton=1 AND (next_profile_id<>? OR next_revision<>?)`, p.ProfileID, p.Revision, domain.NowUTCMicro(), p.ProfileID, p.Revision)
		if err != nil {
			return err
		}
		return domain.RecordGenerationOperation(ctx, tx, in.OperationID, receipt)
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"result": receipt})
}

func (s *Server) activateProfile(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Models == nil {
		writeError(w, 503, "model_controller_unavailable", "model activation unavailable")
		return
	}
	var in profileSelection
	if !decodeJSON(w, r, s.cfg.MaxBodyBytes, &in) {
		return
	}
	if in.Profile != nil && r.Pattern == "PUT /v1/generation/next" {
		s.selectNextGeneration(w, r, in)
		return
	}
	ctx := r.Context()
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	p, err := s.selectedProfile(ctx, in)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if p.Mode != "agent_bridge" {
		valid, err := s.cfg.Store.IsGenerationDigestValidated(ctx, p.Digest)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		if !valid {
			writeError(w, 409, "validation_required", "validate this revision before activation")
			return
		}
	}
	cfg, key, err := s.applyConfig(ctx, p)
	if err != nil {
		writeError(w, 503, "generation_credential_unavailable", "model configuration unavailable")
		return
	}
	prior, err := s.cfg.Store.GetGenerationState(ctx)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if err := checkGenerationRevision(prior, in.ExpectedRevision); err != nil {
		writeDomainError(w, err)
		return
	}
	op := in.OperationID
	if op == "" {
		op = rand.Text()
	} else if !profileID.MatchString(op) || containsCredential(op, []string{key}) {
		writeDomainError(w, domain.ErrInvalidArgument)
		return
	}
	if existing, err := s.cfg.Store.GetGenerationHandoff(ctx, op); err == nil {
		if existing.ProfileID != p.ProfileID || existing.ProfileRevision != p.Revision {
			writeError(w, 409, "operation_id_conflict", "handoff operation already recorded")
			return
		}
		if existing.Status == "result_unknown" || existing.Status == "applying" {
			s.reconcileHandoffsLocked(ctx, true)
			if refreshed, refreshErr := s.cfg.Store.GetGenerationHandoff(ctx, op); refreshErr == nil {
				existing = refreshed
			}
		}
		if r.Pattern == "POST /v1/generation/handoffs" {
			writeJSON(w, 200, map[string]any{"handoff": publicHandoff(existing)})
			return
		}
		if !existing.ActiveCommitted {
			if existing.Status == "result_unknown" || existing.Status == "applying" {
				writeError(w, 502, "result_unknown", "model activation outcome unconfirmed")
				return
			}
			writeError(w, 502, "model_apply_failed", "engine did not confirm activation")
			return
		}
		frozen := decodeHandoffPayload(existing.PayloadJSON).AppliedModelDigest
		writeJSON(w, 200, map[string]any{"ok": true, "applied": true, "active_committed": true, "operation_id": op,
			"profile_id": p.ProfileID, "revision": p.Revision, "model_digest": safeModelDigest(frozen, key)})
		return
	} else if !errors.Is(err, domain.ErrNotFound) {
		writeDomainError(w, err)
		return
	}

	// R1-04: Reject concurrent second switch while another handoff is still unconfirmed.
	if hasPending, err := s.hasPendingGenerationHandoffs(ctx); err != nil {
		writeDomainError(w, err)
		return
	} else if hasPending {
		s.reconcileHandoffsLocked(ctx, true)
		stillPending, err := s.hasPendingGenerationHandoffs(ctx)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		if stillPending {
			writeError(w, 409, "generation_handoff_in_progress", "another model handoff is awaiting confirmation")
			return
		}
		// Reload prior state if reconciliation just committed a different active profile.
		prior, err = s.cfg.Store.GetGenerationState(ctx)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		if err := checkGenerationRevision(prior, in.ExpectedRevision); err != nil {
			writeDomainError(w, err)
			return
		}
	}

	storedPayload := handoffStoredPayload{}
	if ev, ok := cfg["ollama_schema_evidence"].(map[string]string); ok && len(ev) > 0 {
		storedPayload.OllamaSchemaEvidence = maps.Clone(ev)
	}
	receipt := domain.GenerationHandoff{OperationID: op, ProfileID: p.ProfileID, ProfileRevision: p.Revision, Digest: p.Digest,
		PreviousProfileID: prior.ActiveProfileID, PreviousRevision: prior.ActiveRevision, Status: "applying",
		StartedAt: float64(time.Now().UnixMilli()) / 1000, PayloadJSON: encodeHandoffPayload(storedPayload)}
	if err := s.cfg.Store.UpsertGenerationHandoff(ctx, receipt); err != nil {
		writeDomainError(w, err)
		return
	}
	res, applyErr := s.cfg.Models.ApplyModel(ctx, engine.ApplyModelRequest{GenerationConfig: cfg, OutputLanguage: s.cfg.OutputLanguage, OperationID: op})
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	receipt.EndedAt = float64(time.Now().UnixMilli()) / 1000
	switch {
	case applyErr != nil && isExplicitEngineRejection(applyErr):
		receipt.Status, receipt.Reason = "failed", "model_apply_failed"
	case applyErr == nil && (!res.OK || !res.Applied):
		receipt.Status, receipt.Reason = "failed", "model_apply_failed"
	case applyErr != nil:
		receipt.Status, receipt.Reason = "result_unknown", "model_apply_unconfirmed"
	case res.OperationID != "" && res.OperationID != op:
		receipt.Status, receipt.Reason = "result_unknown", "operation_receipt_mismatch"
	default:
		err = s.cfg.Store.SetActiveGeneration(persist, p.ProfileID, p.Revision)
		if err == nil {
			err = s.clearStartupModelError(persist)
		}
		if err == nil {
			appliedDigest := safeModelDigest(res.ModelDigest, key)
			if appliedDigest == "" {
				appliedDigest, _ = computeApplyModelDigest(cfg)
			}
			storedPayload.AppliedModelDigest = appliedDigest
			receipt.PayloadJSON = encodeHandoffPayload(storedPayload)
			receipt.Status, receipt.ActiveCommitted = "applied", true
			s.freezeActiveDigest(p.ProfileID, p.Revision, appliedDigest)
		} else {
			receipt.Status, receipt.Reason = "result_unknown", "active_commit_failed"
		}
	}
	if err := errors.Join(err, s.cfg.Store.UpsertGenerationHandoff(persist, receipt)); err != nil {
		writeDomainError(w, err)
		return
	}
	if receipt.Status == "result_unknown" {
		s.resetReconcileBackoff()
	} else if receipt.ActiveCommitted && s.inbox != nil {
		s.inbox.resetExtractBackoff()
		s.inbox.Notify()
	}
	if r.Pattern == "POST /v1/generation/handoffs" {
		writeJSON(w, 200, map[string]any{"handoff": publicHandoff(receipt)})
		return
	}
	if !receipt.ActiveCommitted {
		if receipt.Status == "result_unknown" {
			writeError(w, 502, "result_unknown", "model activation outcome unconfirmed")
			return
		}
		writeError(w, 502, "model_apply_failed", "engine did not confirm activation")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "applied": true, "active_committed": true, "operation_id": op,
		"profile_id": p.ProfileID, "revision": p.Revision, "model_digest": safeModelDigest(res.ModelDigest, key)})
}

// ApplyActiveGeneration restores the selected profile without changing its selection.
func (s *Server) ApplyActiveGeneration(ctx context.Context) error {
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	// R2-01: Reconcile any pending handoffs before applying Store's active profile so
	// reconnecting to a live engine that already switched to B never overwrites B with old A.
	if hasPending, err := s.hasPendingGenerationHandoffs(ctx); err == nil && hasPending {
		s.reconcileHandoffsLocked(ctx, true)
	}
	state, err := s.cfg.Store.GetGenerationState(ctx)
	if err != nil {
		s.setStartupModelError("startup_state_unavailable")
		return errors.New("could not read active generation selection")
	}
	if state.ActiveProfileID == "" || state.ActiveRevision <= 0 {
		s.setStartupModelError("")
		s.freezeActiveDigest("", 0, "")
		if state.StartupError != "" {
			persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = s.cfg.Store.SetGenerationStartupError(persist, "")
		}
		return nil
	}
	p, err := s.cfg.Store.GetGenerationProfile(ctx, state.ActiveProfileID, state.ActiveRevision)
	var reason string
	var appliedDigest string
	switch {
	case err != nil || p.Removed:
		reason = "startup_profile_unavailable"
	case s.cfg.Models == nil:
		reason = "startup_model_controller_unavailable"
	default:
		frozenEv := s.latestAppliedEvidenceForProfile(ctx, p.ProfileID, p.Revision)
		config, key, configErr := s.applyConfigWithEvidence(ctx, p, frozenEv)
		if configErr != nil {
			reason = "startup_model_configuration_unavailable"
		} else {
			op := "startup-" + rand.Text()
			res, applyErr := s.cfg.Models.ApplyModel(ctx, engine.ApplyModelRequest{
				GenerationConfig: config, OutputLanguage: s.cfg.OutputLanguage, OperationID: op,
			})
			if applyErr != nil || !res.OK || !res.Applied || (res.OperationID != "" && res.OperationID != op) {
				reason = "startup_model_apply_failed"
			} else {
				appliedDigest = safeModelDigest(res.ModelDigest, key)
				if appliedDigest == "" {
					appliedDigest, _ = computeApplyModelDigest(config)
				}
			}
		}
	}
	s.setStartupModelError(reason)
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.cfg.Store.SetGenerationStartupError(persist, reason); err != nil {
		return fmt.Errorf("could not persist startup generation status (%s)", reason)
	}
	if reason != "" {
		return errors.New(reason)
	}
	s.freezeActiveDigest(state.ActiveProfileID, state.ActiveRevision, appliedDigest)
	if s.inbox != nil {
		s.inbox.resetExtractBackoff()
		s.inbox.Notify()
	}
	return nil
}

// RecordStartupGenerationFailure ensures startup model failures remain visible
// and pause extraction even when persisting startup_error failed.
func (s *Server) RecordStartupGenerationFailure(ctx context.Context, err error) {
	if err == nil {
		return
	}
	code := s.startupModelError()
	if code == "" {
		code = cleanReceipt(err.Error())
	}
	if code == "" {
		code = "startup_model_apply_failed"
	}
	s.setStartupModelError(code)
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = s.cfg.Store.SetGenerationStartupError(persist, code)
}

func (s *Server) startupModelError() string {
	s.startupModelMu.Lock()
	defer s.startupModelMu.Unlock()
	return s.startupModelErr
}

func (s *Server) setStartupModelError(code string) {
	s.startupModelMu.Lock()
	s.startupModelErr = code
	s.startupModelMu.Unlock()
}

func (s *Server) clearStartupModelError(ctx context.Context) error {
	if err := s.cfg.Store.SetGenerationStartupError(ctx, ""); err != nil {
		return err
	}
	s.setStartupModelError("")
	return nil
}

func (s *Server) freezeActiveDigest(profileID string, revision int, modelDigest string) {
	s.activeDigestMu.Lock()
	s.activeDigestProfileID = profileID
	s.activeDigestRevision = revision
	s.activeDigestValue = modelDigest
	s.activeDigestMu.Unlock()
}

func (s *Server) frozenActiveDigest(profileID string, revision int) string {
	s.activeDigestMu.Lock()
	defer s.activeDigestMu.Unlock()
	if s.activeDigestProfileID == profileID && s.activeDigestRevision == revision && s.activeDigestValue != "" {
		return s.activeDigestValue
	}
	return ""
}

func (s *Server) latestAppliedPayloadForProfile(ctx context.Context, profileID string, revision int) handoffStoredPayload {
	rows, err := s.cfg.Store.ListGenerationHandoffs(ctx)
	if err != nil {
		return handoffStoredPayload{}
	}
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].ProfileID == profileID && rows[i].ProfileRevision == revision && rows[i].Status == "applied" {
			return decodeHandoffPayload(rows[i].PayloadJSON)
		}
	}
	return handoffStoredPayload{}
}

func (s *Server) latestAppliedEvidenceForProfile(ctx context.Context, profileID string, revision int) map[string]string {
	return s.latestAppliedPayloadForProfile(ctx, profileID, revision).OllamaSchemaEvidence
}

func isExplicitEngineRejection(err error) bool {
	if err == nil {
		return false
	}
	if strings.Contains(err.Error(), "generation_stop_unconfirmed") {
		return true
	}
	if errors.Is(err, engine.ErrConflict) || errors.Is(err, engine.ErrModelDigestMismatch) {
		return false
	}
	var engErr *engine.EngineError
	if errors.As(err, &engErr) && engErr != nil {
		if engErr.Code == "generation_stop_unconfirmed" {
			return true
		}
		if engErr.StatusCode == http.StatusConflict {
			return false
		}
		if engErr.StatusCode >= 400 && engErr.StatusCode < 500 {
			return true
		}
		if engErr.StatusCode >= 500 || engErr.StatusCode == 0 {
			return false
		}
	}
	return errors.Is(err, engine.ErrInvalidArgument) ||
		errors.Is(err, engine.ErrUnauthorized) ||
		errors.Is(err, engine.ErrForbidden) ||
		errors.Is(err, engine.ErrNotFound)
}

func computeApplyModelDigest(cfg map[string]any) (string, error) {
	raw, err := encodePythonCanonicalJSON(cfg)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// encodePythonCanonicalJSON mirrors Python's
// json.dumps(obj, sort_keys=True, ensure_ascii=False, separators=(',', ':'), allow_nan=False)
// for objects transmitted from Go over HTTP JSON.
func encodePythonCanonicalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := writePythonJSONValue(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writePythonJSONValue(buf *bytes.Buffer, v any) error {
	switch val := v.(type) {
	case nil:
		buf.WriteString("null")
		return nil
	case bool:
		if val {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
		return nil
	case string:
		writePythonJSONString(buf, val)
		return nil
	case int:
		buf.WriteString(strconv.Itoa(val))
		return nil
	case int64:
		buf.WriteString(strconv.FormatInt(val, 10))
		return nil
	case float64:
		s, err := formatPythonJSONFloat(val)
		if err != nil {
			return err
		}
		buf.WriteString(s)
		return nil
	case map[string]string:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writePythonJSONString(buf, k)
			buf.WriteByte(':')
			writePythonJSONString(buf, val[k])
		}
		buf.WriteByte('}')
		return nil
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writePythonJSONString(buf, k)
			buf.WriteByte(':')
			if err := writePythonJSONValue(buf, val[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
		return nil
	case []any:
		buf.WriteByte('[')
		for i, item := range val {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writePythonJSONValue(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil
	default:
		var tmp bytes.Buffer
		enc := json.NewEncoder(&tmp)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(val); err != nil {
			return err
		}
		var normalized any
		dec := json.NewDecoder(bytes.NewReader(tmp.Bytes()))
		dec.UseNumber()
		if err := dec.Decode(&normalized); err != nil {
			return err
		}
		return writePythonJSONValue(buf, normalized)
	}
}

func writePythonJSONString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	for i := 0; i < len(s); {
		b := s[i]
		if b < utf8.RuneSelf {
			switch b {
			case '"':
				buf.WriteString(`\"`)
			case '\\':
				buf.WriteString(`\\`)
			case '\b':
				buf.WriteString(`\b`)
			case '\f':
				buf.WriteString(`\f`)
			case '\n':
				buf.WriteString(`\n`)
			case '\r':
				buf.WriteString(`\r`)
			case '\t':
				buf.WriteString(`\t`)
			default:
				if b < 0x20 {
					fmt.Fprintf(buf, `\u%04x`, b)
				} else {
					buf.WriteByte(b)
				}
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			buf.WriteString(`\ufffd`)
		} else {
			buf.WriteString(s[i : i+size])
		}
		i += size
	}
	buf.WriteByte('"')
}

func formatPythonJSONFloat(f float64) (string, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "", errors.New("non-finite float in generation config")
	}
	// Go's encoding/json sends integral floats in [1e-6, 1e21) (e.g. 0, 1, 2) without a
	// decimal point, which Python's json.loads parses as int and re-dumps without ".0".
	goWire, err := json.Marshal(f)
	if err != nil {
		return "", err
	}
	wireStr := string(goWire)
	if !strings.ContainsAny(wireStr, ".eE") {
		return wireStr, nil
	}
	// Otherwise Python parses it as float and json.dumps formats via Python repr(float):
	// scientific notation when abs < 1e-4 or abs >= 1e16, fixed decimal otherwise.
	abs := math.Abs(f)
	if abs != 0 && (abs < 1e-4 || abs >= 1e16) {
		return strconv.FormatFloat(f, 'e', -1, 64), nil
	}
	out := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.ContainsRune(out, '.') {
		out += ".0"
	}
	return out, nil
}

func (s *Server) expectedProfileDigest(ctx context.Context, profileID string, revision int) (string, error) {
	return s.expectedProfileDigestWithEvidence(ctx, profileID, revision, nil)
}

func (s *Server) expectedProfileDigestWithEvidence(ctx context.Context, profileID string, revision int, frozenEvidence map[string]string) (string, error) {
	if profileID == "" || revision <= 0 {
		return "", domain.ErrNotFound
	}
	p, err := s.cfg.Store.GetGenerationProfile(ctx, profileID, revision)
	if err != nil {
		return "", err
	}
	if p.Removed {
		return "", domain.ErrForbidden
	}
	cfg, _, err := s.applyConfigWithEvidence(ctx, p, frozenEvidence)
	if err != nil {
		return "", err
	}
	return computeApplyModelDigest(cfg)
}

func (s *Server) hasPendingGenerationHandoffs(ctx context.Context) (bool, error) {
	var exists bool
	err := s.cfg.Store.DB().QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM generation_handoffs WHERE status IN ('applying','result_unknown'))`).Scan(&exists)
	return exists, err
}

func (s *Server) latestPendingHandoff(ctx context.Context) (domain.GenerationHandoff, bool, error) {
	rows, err := s.cfg.Store.ListGenerationHandoffs(ctx)
	if err != nil {
		return domain.GenerationHandoff{}, false, err
	}
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].Status == "applying" || rows[i].Status == "result_unknown" {
			return rows[i], true, nil
		}
	}
	return domain.GenerationHandoff{}, false, nil
}

func (s *Server) resetReconcileBackoff() {
	s.reconcileMu.Lock()
	s.reconcileNext = time.Time{}
	s.reconcileBackoff = 0
	s.reconcileMu.Unlock()
}

func (s *Server) advanceReconcileBackoff() {
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()
	if s.reconcileBackoff <= 0 {
		s.reconcileBackoff = time.Second
	} else {
		s.reconcileBackoff = min(s.reconcileBackoff*2, 15*time.Second)
	}
	s.reconcileNext = time.Now().Add(s.reconcileBackoff)
}

func (s *Server) maybeReconcileHandoffs(ctx context.Context, force bool) {
	if s.cfg.Models == nil {
		return
	}
	if !force {
		s.reconcileMu.Lock()
		wait := !s.reconcileNext.IsZero() && time.Now().Before(s.reconcileNext)
		s.reconcileMu.Unlock()
		if wait {
			return
		}
	}
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	s.reconcileHandoffsLocked(ctx, force)
}

func (s *Server) reconcileHandoffsLocked(ctx context.Context, force bool) {
	if s.cfg.Models == nil {
		return
	}
	if !force {
		s.reconcileMu.Lock()
		wait := !s.reconcileNext.IsZero() && time.Now().Before(s.reconcileNext)
		s.reconcileMu.Unlock()
		if wait {
			return
		}
	}
	rows, err := s.cfg.Store.ListGenerationHandoffs(ctx)
	if err != nil {
		s.advanceReconcileBackoff()
		return
	}
	var pending []domain.GenerationHandoff
	for _, row := range rows {
		if row.Status == "result_unknown" || row.Status == "applying" {
			pending = append(pending, row)
		}
	}
	if len(pending) == 0 {
		s.resetReconcileBackoff()
		return
	}
	persist, pCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer pCancel()
	resolvedAny := false
	stillPending := false
	now := float64(time.Now().UnixMilli()) / 1000
	readStatus := func() (engine.ModelStatusResult, error) {
		statusCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		return s.cfg.Models.ModelStatus(statusCtx)
	}
	for _, row := range pending {
		stored := decodeHandoffPayload(row.PayloadJSON)
		status, statusErr := readStatus()
		p, pErr := s.cfg.Store.GetGenerationProfile(persist, row.ProfileID, row.ProfileRevision)
		if errors.Is(pErr, domain.ErrNotFound) || (pErr == nil && p.Removed) {
			// R2-03: Unlock orphaned legacy handoffs whose target profile was removed,
			// once live ModelStatus confirms the engine is not running that target digest.
			if statusErr == nil && status.OK && status.ModelDigest != "" &&
				status.ModelDigest != row.Digest && status.ModelDigest != stored.AppliedModelDigest {
				row.Status = "failed"
				row.Reason = "model_apply_failed"
				row.ActiveCommitted = false
				row.EndedAt = now
				if upsertErr := s.cfg.Store.UpsertGenerationHandoff(persist, row); upsertErr == nil {
					resolvedAny = true
					continue
				}
			}
			stillPending = true
			continue
		}
		if pErr != nil {
			stillPending = true
			continue
		}
		cfg, key, cfgErr := s.applyConfigWithEvidence(persist, p, stored.OllamaSchemaEvidence)
		if cfgErr != nil {
			stillPending = true
			continue
		}
		targetDigest, digestErr := computeApplyModelDigest(cfg)
		if digestErr != nil {
			stillPending = true
			continue
		}

		// 1. Fast path: fresh ModelStatus already confirmed targetDigest.
		if statusErr == nil && status.OK && status.ModelDigest != "" && status.ModelDigest == targetDigest {
			if s.commitReconciledApplied(persist, &row, stored, targetDigest, now) {
				resolvedAny = true
			} else {
				stillPending = true
			}
			continue
		}

		// 2. R1-03: Never infer failure from an old ModelStatus snapshot (engine may still be in _drain()).
		// Replay ApplyModel idempotently with the original OperationID to learn the authoritative outcome.
		replayCtx, replayCancel := context.WithTimeout(ctx, 5*time.Second)
		res, applyErr := s.cfg.Models.ApplyModel(replayCtx, engine.ApplyModelRequest{
			GenerationConfig: cfg,
			OutputLanguage:   s.cfg.OutputLanguage,
			OperationID:      row.OperationID,
		})
		replayCancel()

		switch {
		case applyErr == nil && res.OK && res.Applied && (res.OperationID == "" || res.OperationID == row.OperationID):
			appliedDigest := safeModelDigest(res.ModelDigest, key)
			if appliedDigest == "" {
				appliedDigest = targetDigest
			}
			// R2-01/R2-02: Verify live ModelStatus matches appliedDigest before committing active,
			// preventing a stale cached receipt from committing B when the engine is actually on A.
			verifyStatus, verifyErr := readStatus()
			if verifyErr != nil || !verifyStatus.OK || verifyStatus.ModelDigest != appliedDigest {
				stillPending = true
				continue
			}
			if s.commitReconciledApplied(persist, &row, stored, appliedDigest, now) {
				resolvedAny = true
			} else {
				stillPending = true
			}
		case (applyErr != nil && isExplicitEngineRejection(applyErr)) || (applyErr == nil && (!res.OK || !res.Applied)):
			row.Status = "failed"
			row.Reason = "model_apply_failed"
			row.ActiveCommitted = false
			row.EndedAt = now
			if upsertErr := s.cfg.Store.UpsertGenerationHandoff(persist, row); upsertErr == nil {
				resolvedAny = true
			} else {
				stillPending = true
			}
		default:
			// 409 busy/in_progress/operation_id_conflict, 5xx, or transport error -> remain result_unknown and backoff.
			stillPending = true
		}
	}
	if stillPending {
		s.advanceReconcileBackoff()
	} else {
		s.resetReconcileBackoff()
	}
	if resolvedAny && s.inbox != nil {
		s.inbox.resetExtractBackoff()
		s.inbox.Notify()
	}
}

func (s *Server) commitReconciledApplied(ctx context.Context, row *domain.GenerationHandoff, stored handoffStoredPayload, appliedDigest string, now float64) bool {
	if err := s.cfg.Store.SetActiveGeneration(ctx, row.ProfileID, row.ProfileRevision); err != nil {
		return false
	}
	if err := s.clearStartupModelError(ctx); err != nil {
		return false
	}
	stored.AppliedModelDigest = appliedDigest
	row.PayloadJSON = encodeHandoffPayload(stored)
	row.Status = "applied"
	row.Reason = ""
	row.ActiveCommitted = true
	row.EndedAt = now
	if err := s.cfg.Store.UpsertGenerationHandoff(ctx, *row); err != nil {
		return false
	}
	s.freezeActiveDigest(row.ProfileID, row.ProfileRevision, appliedDigest)
	return true
}

func (s *Server) expectedActiveModelDigest(ctx context.Context) (string, error) {
	// R1-06: Always allow pending handoff reconciliation to run before checking startup_error,
	// so a successful recovery handoff can commit and clear startup_error.
	if hasPending, err := s.hasPendingGenerationHandoffs(ctx); err != nil {
		return "", err
	} else if hasPending {
		s.maybeReconcileHandoffs(ctx, false)
		stillPending, checkErr := s.hasPendingGenerationHandoffs(ctx)
		if checkErr != nil {
			return "", checkErr
		}
		if stillPending {
			return "", fmt.Errorf("%w: handoff_unconfirmed", errExtractionPausedModelUnverified)
		}
	}
	if code := s.startupModelError(); code != "" {
		return "", fmt.Errorf("%w: %s", errExtractionPausedModelUnverified, code)
	}
	state, err := s.cfg.Store.GetGenerationState(ctx)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(state.StartupError) != "" {
		return "", fmt.Errorf("%w: %s", errExtractionPausedModelUnverified, state.StartupError)
	}
	if state.ActiveProfileID == "" || state.ActiveRevision <= 0 {
		return "", fmt.Errorf("%w: no_active_profile", errExtractionPausedModelUnverified)
	}
	if frozen := s.frozenActiveDigest(state.ActiveProfileID, state.ActiveRevision); frozen != "" {
		return frozen, nil
	}
	appliedPayload := s.latestAppliedPayloadForProfile(ctx, state.ActiveProfileID, state.ActiveRevision)
	if appliedPayload.AppliedModelDigest != "" {
		s.freezeActiveDigest(state.ActiveProfileID, state.ActiveRevision, appliedPayload.AppliedModelDigest)
		return appliedPayload.AppliedModelDigest, nil
	}
	digest, err := s.expectedProfileDigestWithEvidence(ctx, state.ActiveProfileID, state.ActiveRevision, appliedPayload.OllamaSchemaEvidence)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errExtractionPausedModelUnverified, err)
	}
	return digest, nil
}
