package gateway

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
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
	processing := []string{}
	for _, host := range agents.DetectHosts(s.cfg.DiscoveryOptions) {
		if host.Detected && host.SupportsProcessingCLI && host.Executable != "" {
			processing = append(processing, host.Kind)
		}
	}
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
	startup := sanitizeError(errors.New(state.StartupError))
	for _, key := range credentials {
		startup = strings.ReplaceAll(startup, key, "[REDACTED]")
	}
	writeJSON(w, 200, map[string]any{"state": map[string]any{"active_profile_id": state.ActiveProfileID,
		"active_revision": state.ActiveRevision, "next_profile_id": state.NextProfileID, "next_revision": state.NextRevision,
		"revision": state.Revision, "startup_error": startup}, "profiles": out, "model_status": model, "validations": validations,
		"handoff_available": s.cfg.Models != nil, "output_language": outputLanguage, "detected_processing_clis": processing})
}

type validationView struct {
	OperationID        string `json:"-"`
	Digest             string `json:"digest"`
	RegisteredRevision int    `json:"registered_revision"`
	Status             string `json:"status"`
	PlainPassed        bool   `json:"plain_passed"`
	SchemaPassed       bool   `json:"schema_passed"`
	QualityPassed      bool   `json:"quality_passed"`
	StructuredMode     string `json:"structured_mode"`
	ErrorCode          string `json:"error_code"`
	Reference          struct {
		ID       string `json:"id"`
		Revision int    `json:"revision"`
	} `json:"reference"`
}

func (s *Server) generationValidations(ctx context.Context, secrets []string) (map[string]validationView, error) {
	rows, err := readRows(ctx, s.cfg.Store.DB(), `SELECT operation_id,profile_id,profile_revision,digest,registered_revision,
  status,plain_passed,schema_passed,quality_passed,structured_mode,error_code FROM generation_validations`, nil,
		func(row rowScanner) (validationView, error) {
			var v validationView
			err := row.Scan(&v.OperationID, &v.Reference.ID, &v.Reference.Revision, &v.Digest, &v.RegisteredRevision,
				&v.Status, &v.PlainPassed, &v.SchemaPassed, &v.QualityPassed, &v.StructuredMode, &v.ErrorCode)
			return v, err
		})
	if err != nil {
		return nil, err
	}
	out := make(map[string]validationView, len(rows))
	for _, v := range rows {
		op := safeModelDigest(v.OperationID, secrets...)
		if op == "" {
			continue
		}
		v.Digest = safeModelDigest(v.Digest, secrets...)
		v.Reference.ID = safeModelDigest(v.Reference.ID, secrets...)
		v.Status = cleanReceipt(v.Status, secrets...)
		v.StructuredMode = cleanReceipt(v.StructuredMode, secrets...)
		v.ErrorCode = cleanReceipt(v.ErrorCode, secrets...)
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
		OperationID string            `json:"operation_id"`
		Result      map[string]string `json:"result"`
	}{res, op, map[string]string{"operation_id": op}})
}

func (s *Server) applyConfig(ctx context.Context, p domain.GenerationProfile) (map[string]any, string, error) {
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
		var raw string
		err := s.cfg.Store.DB().QueryRowContext(ctx, `SELECT payload_json FROM generation_validations WHERE digest=?
			ORDER BY registered_revision DESC LIMIT 1`, p.Digest).Scan(&raw)
		if err != nil {
			return nil, "", err
		}
		var payload struct {
			Evidence       map[string]string `json:"ollama_schema_evidence"`
			LegacyEvidence map[string]string `json:"native_schema_evidence"`
		}
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return nil, "", err
		}
		if payload.Evidence == nil {
			payload.Evidence = payload.LegacyEvidence
		}
		if payload.Evidence != nil {
			cfg["ollama_schema_evidence"] = payload.Evidence
		}
	}
	return cfg, key, nil
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
	case "model_apply_failed", "operation_receipt_mismatch", "active_commit_failed":
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
	valid, err := s.cfg.Store.IsGenerationDigestValidated(ctx, p.Digest)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if !valid {
		writeError(w, 409, "validation_required", "validate this revision before selecting it")
		return
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
	if r.Pattern == "POST /v1/generation/handoffs" {
		existing, err := s.cfg.Store.GetGenerationHandoff(ctx, op)
		if err == nil {
			if existing.ProfileID != p.ProfileID || existing.ProfileRevision != p.Revision {
				writeError(w, 409, "operation_id_conflict", "handoff operation already recorded")
				return
			}
			writeJSON(w, 200, map[string]any{"handoff": publicHandoff(existing)})
			return
		}
		if !errors.Is(err, domain.ErrNotFound) {
			writeDomainError(w, err)
			return
		}
	}

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
	if r.Pattern == "POST /v1/generation/handoffs" {
		writeJSON(w, 200, map[string]any{"handoff": publicHandoff(receipt)})
		return
	}
	if !receipt.ActiveCommitted {
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
	state, err := s.cfg.Store.GetGenerationState(ctx)
	if err != nil {
		return errors.New("could not read active generation selection")
	}
	if state.ActiveProfileID == "" || state.ActiveRevision <= 0 {
		return nil
	}
	p, err := s.cfg.Store.GetGenerationProfile(ctx, state.ActiveProfileID, state.ActiveRevision)
	var reason string
	switch {
	case err != nil:
		reason = "startup_profile_unavailable"
	case p.Removed:
		return nil
	case s.cfg.Models == nil:
		reason = "startup_model_controller_unavailable"
	default:
		config, _, configErr := s.applyConfig(ctx, p)
		if configErr != nil {
			reason = "startup_model_configuration_unavailable"
		} else {
			res, applyErr := s.cfg.Models.ApplyModel(ctx, engine.ApplyModelRequest{
				GenerationConfig: config, OutputLanguage: s.cfg.OutputLanguage, OperationID: "startup",
			})
			if applyErr != nil || !res.OK || !res.Applied || (res.OperationID != "" && res.OperationID != "startup") {
				reason = "startup_model_apply_failed"
			}
		}
	}
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.cfg.Store.SetGenerationStartupError(persist, reason); err != nil {
		return fmt.Errorf("could not persist startup generation status (%s)", reason)
	}
	if reason != "" {
		return errors.New(reason)
	}
	return nil
}
