package gateway

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

var profileID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var receiptCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var modelDigest = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type generationView struct {
	domain.GenerationProfile
	HasCredential bool `json:"has_credential"`
}

func (s *Server) generation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
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
		out = append(out, generationView{p, has})
	}
	var model any
	if s.cfg.Models != nil {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		status, err := s.cfg.Models.ModelStatus(ctx)
		if err != nil {
			model = map[string]any{"ok": false, "error": "model_status_unavailable"}
		} else {
			status.Mode = cleanReceipt(status.Mode, credentials...)
			status.OutputLanguage = cleanReceipt(status.OutputLanguage, credentials...)
			status.ModelDigest = safeModelDigest(status.ModelDigest, credentials...)
			model = status
		}
	}
	startup := sanitizeError(errors.New(state.StartupError))
	for _, key := range credentials {
		startup = strings.ReplaceAll(startup, key, "[REDACTED]")
	}
	writeJSON(w, 200, map[string]any{"state": map[string]any{"active_profile_id": state.ActiveProfileID,
		"active_revision": state.ActiveRevision, "next_profile_id": state.NextProfileID, "next_revision": state.NextRevision,
		"revision": state.Revision, "startup_error": startup}, "profiles": out, "model_status": model})
}

func (s *Server) saveProfile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		domain.GenerationProfile
		APIKey *string `json:"api_key"`
	}
	if !decodeJSON(w, r, s.cfg.MaxBodyBytes, &in) {
		return
	}
	p := in.GenerationProfile
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
	p, err := s.cfg.Store.PutGenerationProfile(r.Context(), p)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	status := 200
	if r.Method == http.MethodPost {
		status = 201
	}
	writeJSON(w, status, generationView{p, key != ""})
}

func (s *Server) removeProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	err := s.cfg.Store.WithTx(ctx, func(tx *sql.Tx) error {
		var active string
		if err := tx.QueryRowContext(ctx, "SELECT active_profile_id FROM generation_state WHERE singleton=1").Scan(&active); err != nil {
			return err
		}
		if active == id {
			return domain.ErrConflict
		}
		result, err := tx.ExecContext(ctx, "UPDATE generation_profiles SET removed=1 WHERE profile_id=?", id)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err == nil && count == 0 {
			return domain.ErrNotFound
		}
		return err
	})
	if errors.Is(err, domain.ErrConflict) {
		writeError(w, 409, "active_profile_cannot_be_removed", "active generation profile cannot be removed")
		return
	}
	if err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(204)
}

type profileSelection struct {
	ProfileID string `json:"profile_id"`
	Revision  int    `json:"revision"`
}

func (s *Server) selectedProfile(ctx context.Context, in profileSelection) (domain.GenerationProfile, error) {
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

func safeModelDigest(value string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" && strings.Contains(value, secret) {
			return ""
		}
	}
	if modelDigest.MatchString(value) {
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
	res, probeErr := s.cfg.Models.ProbeModel(ctx, engine.ProbeRequest{GenerationMode: p.Mode, BackendKind: backend,
		Environment: probeEnvironment(p, provider, endpoint, key), Runtime: maps.Clone(s.cfg.ProbeRuntime), MaxConcurrentRequests: capacity})
	res.Status = cleanReceipt(res.Status, key)
	res.ErrorCode = cleanReceipt(res.ErrorCode, key)
	res.StructuredMode = cleanReceipt(res.StructuredMode, key)
	if probeErr != nil || res.Status != "passed" && res.Status != "failed" && res.Status != "result_unknown" {
		res = engine.ProbeResult{Status: "result_unknown", ErrorCode: "probe_result_unconfirmed"}
	}
	payload := "{}"
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
			raw, err := json.Marshal(map[string]any{"ollama_schema_evidence": want})
			if err != nil {
				writeDomainError(w, err)
				return
			}
			payload = string(raw)
		}
	}
	var rev int
	if err := s.cfg.Store.DB().QueryRowContext(ctx, `SELECT COALESCE(MAX(registered_revision),0)+1 FROM generation_validations WHERE digest=?`, p.Digest).Scan(&rev); err != nil {
		writeDomainError(w, err)
		return
	}
	op := rand.Text()
	record := domain.GenerationValidation{OperationID: op, ProfileID: p.ProfileID, ProfileRevision: p.Revision, Digest: p.Digest, RegisteredRevision: rev,
		Status: res.Status, PlainPassed: res.PlainPassed, SchemaPassed: res.SchemaPassed, QualityPassed: res.QualityPassed,
		StructuredMode: res.StructuredMode, ErrorCode: res.ErrorCode, PayloadJSON: payload}
	if err := s.cfg.Store.RecordGenerationValidation(ctx, record); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, 200, struct {
		engine.ProbeResult
		OperationID string `json:"operation_id"`
	}{res, op})
}

func (s *Server) applyConfig(ctx context.Context, p domain.GenerationProfile) (map[string]any, string, error) {
	_, endpoint, key, err := s.modelConnection(p)
	if err != nil {
		return nil, "", err
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
		var raw string
		err := s.cfg.Store.DB().QueryRowContext(ctx, `SELECT payload_json FROM generation_validations WHERE digest=?
			ORDER BY registered_revision DESC LIMIT 1`, p.Digest).Scan(&raw)
		if err != nil {
			return nil, "", err
		}
		var payload struct {
			Evidence map[string]string `json:"ollama_schema_evidence"`
		}
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return nil, "", err
		}
		if payload.Evidence != nil {
			cfg["ollama_schema_evidence"] = payload.Evidence
		}
	}
	return cfg, key, nil
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
	ctx := r.Context()
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	p, err := s.selectedProfile(ctx, in)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	valid, err := s.cfg.Store.IsGenerationDigestValidated(ctx, p.Digest)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if !valid {
		writeError(w, 409, "validation_required", "validate this revision before activation")
		return
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
	op := rand.Text()
	receipt := domain.GenerationHandoff{OperationID: op, ProfileID: p.ProfileID, ProfileRevision: p.Revision, Digest: p.Digest,
		PreviousProfileID: prior.ActiveProfileID, PreviousRevision: prior.ActiveRevision, Status: "applying",
		StartedAt: float64(time.Now().UnixMilli()) / 1000}
	if err := s.cfg.Store.UpsertGenerationHandoff(ctx, receipt); err != nil {
		writeDomainError(w, err)
		return
	}
	res, applyErr := s.cfg.Models.ApplyModel(ctx, engine.ApplyModelRequest{GenerationConfig: cfg, OutputLanguage: s.cfg.OutputLanguage, OperationID: op})
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	receipt.EndedAt = float64(time.Now().UnixMilli()) / 1000
	switch {
	case applyErr != nil || !res.OK || !res.Applied:
		receipt.Status, receipt.Reason = "failed", "model_apply_failed"
	case res.OperationID != "" && res.OperationID != op:
		receipt.Status, receipt.Reason = "result_unknown", "operation_receipt_mismatch"
	default:
		err = s.cfg.Store.SetActiveGeneration(persist, p.ProfileID, p.Revision)
		if err == nil {
			receipt.Status, receipt.ActiveCommitted = "applied", true
		} else {
			receipt.Status, receipt.Reason = "result_unknown", "active_commit_failed"
		}
	}
	if err := errors.Join(err, s.cfg.Store.UpsertGenerationHandoff(persist, receipt)); err != nil {
		writeDomainError(w, err)
		return
	}
	if !receipt.ActiveCommitted {
		writeError(w, 502, "model_apply_failed", "engine did not confirm activation")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "applied": true, "active_committed": true, "operation_id": op,
		"profile_id": p.ProfileID, "revision": p.Revision, "model_digest": safeModelDigest(res.ModelDigest, key)})
}
