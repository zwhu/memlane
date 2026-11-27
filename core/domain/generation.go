package domain

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const generationDDL = `
CREATE TABLE IF NOT EXISTS generation_profiles (
 profile_id TEXT NOT NULL, revision INTEGER NOT NULL, digest TEXT NOT NULL,
 mode TEXT NOT NULL CHECK(mode IN ('agent_bridge','provider')), provider TEXT NOT NULL DEFAULT '',
 bridge_kind TEXT NOT NULL DEFAULT '', bridge_provider TEXT NOT NULL DEFAULT '', model TEXT NOT NULL,
 endpoint TEXT NOT NULL DEFAULT '', credential_ref TEXT NOT NULL DEFAULT '', credential_version TEXT NOT NULL DEFAULT '',
 temperature REAL, max_output_tokens INTEGER, max_concurrent_requests INTEGER, removed INTEGER NOT NULL DEFAULT 0,
 created_at TEXT NOT NULL, PRIMARY KEY(profile_id,revision)
);
CREATE TABLE IF NOT EXISTS generation_validations (
 operation_id TEXT PRIMARY KEY, profile_id TEXT NOT NULL DEFAULT '', profile_revision INTEGER NOT NULL DEFAULT 0,
 digest TEXT NOT NULL, registered_revision INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL, plain_passed INTEGER NOT NULL DEFAULT 0,
 schema_passed INTEGER NOT NULL DEFAULT 0, quality_passed INTEGER NOT NULL DEFAULT 0, structured_mode TEXT NOT NULL DEFAULT '',
 error_code TEXT NOT NULL DEFAULT '', payload_json TEXT NOT NULL DEFAULT '{}', updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_generation_validations_digest ON generation_validations(digest,registered_revision);
CREATE TABLE IF NOT EXISTS generation_state (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1), schema_version INTEGER NOT NULL DEFAULT 2, revision INTEGER NOT NULL DEFAULT 0,
 active_profile_id TEXT NOT NULL DEFAULT '', active_revision INTEGER NOT NULL DEFAULT 0,
 next_profile_id TEXT NOT NULL DEFAULT '', next_revision INTEGER NOT NULL DEFAULT 0, startup_error TEXT NOT NULL DEFAULT '',
 connected_agent_selections_json TEXT NOT NULL DEFAULT '{}', operations_json TEXT NOT NULL DEFAULT '{}',
 removed_profiles_json TEXT NOT NULL DEFAULT '{}', updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS generation_handoffs (
 operation_id TEXT PRIMARY KEY, profile_id TEXT NOT NULL DEFAULT '', profile_revision INTEGER NOT NULL DEFAULT 0,
 digest TEXT NOT NULL DEFAULT '', previous_profile_id TEXT NOT NULL DEFAULT '', previous_revision INTEGER NOT NULL DEFAULT 0,
 status TEXT NOT NULL DEFAULT '', reason TEXT NOT NULL DEFAULT '', started_at REAL NOT NULL DEFAULT 0, ended_at REAL NOT NULL DEFAULT 0,
 active_committed INTEGER NOT NULL DEFAULT 0, cancelled_sessions INTEGER NOT NULL DEFAULT 0,
 pending_sessions INTEGER NOT NULL DEFAULT 0, unconfirmed_sessions INTEGER NOT NULL DEFAULT 0,
 payload_json TEXT NOT NULL DEFAULT '{}', runtime_payload_json TEXT NOT NULL DEFAULT '{}'
);
`

type GenerationProfile struct {
	ProfileID             string   `json:"profile_id,omitempty"`
	Revision              int      `json:"revision,omitempty"`
	Digest                string   `json:"digest,omitempty"`
	Mode                  string   `json:"mode"`
	Provider              string   `json:"provider,omitempty"`
	BridgeKind            string   `json:"bridge_kind,omitempty"`
	BridgeProvider        string   `json:"bridge_provider,omitempty"`
	Model                 string   `json:"model"`
	Endpoint              string   `json:"endpoint,omitempty"`
	CredentialRef         string   `json:"credential_ref,omitempty"`
	CredentialVersion     string   `json:"credential_version,omitempty"`
	Temperature           *float64 `json:"temperature,omitempty"`
	MaxOutputTokens       *int     `json:"max_output_tokens,omitempty"`
	MaxConcurrentRequests *int     `json:"max_concurrent_requests,omitempty"`
	Removed               bool     `json:"removed,omitempty"`
	CreatedAt             string   `json:"created_at,omitempty"`
}

type GenerationValidation struct {
	OperationID, ProfileID                            string
	ProfileRevision                                   int
	Digest                                            string
	RegisteredRevision                                int
	Status                                            string
	PlainPassed, SchemaPassed, QualityPassed          bool
	StructuredMode, ErrorCode, PayloadJSON, UpdatedAt string
}

type GenerationState struct {
	SchemaVersion, Revision                                                                    int
	ActiveProfileID                                                                            string
	ActiveRevision                                                                             int
	NextProfileID                                                                              string
	NextRevision                                                                               int
	StartupError, ConnectedAgentSelectionsJSON, OperationsJSON, RemovedProfilesJSON, UpdatedAt string
}

type GenerationHandoff struct {
	OperationID, ProfileID                                  string
	ProfileRevision                                         int
	Digest, PreviousProfileID                               string
	PreviousRevision                                        int
	Status, Reason                                          string
	StartedAt, EndedAt                                      float64
	ActiveCommitted                                         bool
	CancelledSessions, PendingSessions, UnconfirmedSessions int
	PayloadJSON, RuntimePayloadJSON                         string
}

var generationIdentifier = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func ValidateGenerationProfile(p GenerationProfile) error {
	if strings.TrimSpace(p.Model) == "" || utf8.RuneCountInString(p.Model) > 200 || strings.ContainsFunc(p.Model, func(r rune) bool { return r < 32 }) {
		return fmt.Errorf("invalid generation model: %w", ErrInvalidArgument)
	}
	if p.Temperature != nil && (math.IsNaN(*p.Temperature) || *p.Temperature < 0 || *p.Temperature > 2) {
		return fmt.Errorf("invalid generation temperature: %w", ErrInvalidArgument)
	}
	if p.MaxOutputTokens != nil && (*p.MaxOutputTokens < 1 || *p.MaxOutputTokens > 131072) {
		return fmt.Errorf("invalid generation output limit: %w", ErrInvalidArgument)
	}
	maxCapacity := 256
	if p.Mode == "agent_bridge" {
		maxCapacity = 32
	}
	if p.MaxConcurrentRequests != nil && (*p.MaxConcurrentRequests < 1 || *p.MaxConcurrentRequests > maxCapacity) {
		return fmt.Errorf("invalid generation capacity: %w", ErrInvalidArgument)
	}
	switch p.Mode {
	case "agent_bridge":
		return validateBridgeProfile(p)
	case "provider":
		return validateProviderProfile(p)
	default:
		return fmt.Errorf("unsupported generation mode: %w", ErrInvalidArgument)
	}
}

func validateBridgeProfile(p GenerationProfile) error {
	if p.Provider != "" || p.Endpoint != "" || p.CredentialRef != "" || p.CredentialVersion != "" || p.Temperature != nil || p.MaxOutputTokens != nil {
		return fmt.Errorf("invalid fields for agent bridge: %w", ErrInvalidArgument)
	}
	switch p.BridgeKind {
	case "pi":
		if p.BridgeProvider == "deepseek" || p.BridgeProvider == "ollama" {
			return nil
		}
	case "codex", "traex", "antigravity":
		if p.BridgeProvider == "" {
			return nil
		}
	}
	return fmt.Errorf("unsupported generation bridge: %w", ErrInvalidArgument)
}

func validateProviderProfile(p GenerationProfile) error {
	if p.BridgeKind != "" || p.BridgeProvider != "" {
		return fmt.Errorf("invalid fields for generation provider: %w", ErrInvalidArgument)
	}
	switch p.Provider {
	case "openai", "anthropic", "gemini", "custom", "ollama":
	default:
		return fmt.Errorf("unsupported generation provider: %w", ErrInvalidArgument)
	}
	endpoint, err := url.Parse(p.Endpoint)
	if err != nil {
		return fmt.Errorf("invalid generation endpoint: %w", ErrInvalidArgument)
	}
	host := strings.ToLower(endpoint.Hostname())
	local := host == "127.0.0.1" || host == "::1" || host == "localhost"
	if host == "" || endpoint.User != nil || strings.ContainsAny(p.Endpoint, "?#") ||
		(endpoint.Scheme != "https" && !(endpoint.Scheme == "http" && local)) {
		return fmt.Errorf("invalid generation endpoint: %w", ErrInvalidArgument)
	}
	if port := endpoint.Port(); port != "" {
		if _, err := strconv.ParseUint(port, 10, 16); err != nil {
			return fmt.Errorf("invalid generation port: %w", ErrInvalidArgument)
		}
	}
	if p.Provider == "ollama" {
		if !local || (endpoint.EscapedPath() != "" && endpoint.EscapedPath() != "/") || p.CredentialRef != "" || p.CredentialVersion != "" {
			return fmt.Errorf("invalid local Ollama profile: %w", ErrInvalidArgument)
		}
		return nil
	}
	if !generationIdentifier.MatchString(p.CredentialRef) || !generationIdentifier.MatchString(p.CredentialVersion) {
		return fmt.Errorf("private generation credential reference and version required: %w", ErrInvalidArgument)
	}
	return nil
}

func (s *Store) checkGenerationCredential(p GenerationProfile) error {
	if p.CredentialRef == "" {
		return nil
	}
	dir := filepath.Join(filepath.Dir(strings.TrimSpace(s.path)), "generation-credentials")
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("generation credential directory unavailable: %w", ErrForbidden)
	}
	info, err = os.Lstat(filepath.Join(dir, p.CredentialRef+"-"+p.CredentialVersion))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() == 0 {
		return fmt.Errorf("generation credential requires an existing private file: %w", ErrForbidden)
	}
	return nil
}

const generationProfileColumns = "profile_id,revision,digest,mode,provider,bridge_kind,bridge_provider,model,endpoint,credential_ref,credential_version,temperature,max_output_tokens,max_concurrent_requests,removed,created_at"

func scanGenerationProfile(row scanner) (GenerationProfile, error) {
	var p GenerationProfile
	err := row.Scan(&p.ProfileID, &p.Revision, &p.Digest, &p.Mode, &p.Provider, &p.BridgeKind, &p.BridgeProvider, &p.Model,
		&p.Endpoint, &p.CredentialRef, &p.CredentialVersion, &p.Temperature, &p.MaxOutputTokens, &p.MaxConcurrentRequests, &p.Removed, &p.CreatedAt)
	return p, rowError(err)
}

func (s *Store) PutGenerationProfile(ctx context.Context, p GenerationProfile) (GenerationProfile, error) {
	return s.SaveGenerationProfile(ctx, p, "")
}

// SaveGenerationProfile records the save receipt with its immutable revision.
func (s *Store) SaveGenerationProfile(ctx context.Context, p GenerationProfile, operation string) (GenerationProfile, error) {
	if !generationIdentifier.MatchString(p.ProfileID) || p.Revision < 0 {
		return GenerationProfile{}, fmt.Errorf("invalid generation profile identity: %w", ErrInvalidArgument)
	}
	if err := ValidateGenerationProfile(p); err != nil {
		return GenerationProfile{}, err
	}
	if err := s.checkGenerationCredential(p); err != nil {
		return GenerationProfile{}, err
	}
	if p.Digest == "" {
		config := p
		config.ProfileID, config.Revision, config.Removed, config.CreatedAt = "", 0, false, ""
		encoded, err := json.Marshal(config)
		if err != nil {
			return GenerationProfile{}, err
		}
		sum := sha256.Sum256(encoded)
		p.Digest = hex.EncodeToString(sum[:])
	}
	p.CreatedAt = defaultText(p.CreatedAt, NowUTCMicro())
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		if p.Revision == 0 {
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision),0)+1 FROM generation_profiles WHERE profile_id=?`, p.ProfileID).
				Scan(&p.Revision); err != nil {
				return err
			}
		}
		err := insertError(execSQL(ctx, tx, `INSERT INTO generation_profiles (`+generationProfileColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			p.ProfileID, p.Revision, p.Digest, p.Mode, p.Provider, p.BridgeKind, p.BridgeProvider, p.Model, p.Endpoint, p.CredentialRef,
			p.CredentialVersion, p.Temperature, p.MaxOutputTokens, p.MaxConcurrentRequests, boolToInt(p.Removed), p.CreatedAt))
		if err != nil {
			return err
		}
		return RecordGenerationOperation(ctx, tx, operation, map[string]any{"id": p.ProfileID, "revision": p.Revision, "digest": p.Digest})
	})
	if err != nil {
		return GenerationProfile{}, err
	}
	return p, nil
}

func (s *Store) GetGenerationProfile(ctx context.Context, profileID string, revision int) (GenerationProfile, error) {
	return scanGenerationProfile(s.db.QueryRowContext(ctx, `SELECT `+generationProfileColumns+` FROM generation_profiles WHERE profile_id=? AND revision=?`, profileID, revision))
}

func (s *Store) ListGenerationProfiles(ctx context.Context) ([]GenerationProfile, error) {
	return queryList(ctx, s.db, scanGenerationProfile, `SELECT `+generationProfileColumns+` FROM generation_profiles ORDER BY profile_id,revision`)
}

const generationValidationColumns = `operation_id, profile_id, profile_revision, digest, registered_revision,
	status, plain_passed, schema_passed, quality_passed, structured_mode, error_code, payload_json, updated_at`

func scanGenerationValidation(row scanner) (GenerationValidation, error) {
	var v GenerationValidation
	err := row.Scan(&v.OperationID, &v.ProfileID, &v.ProfileRevision, &v.Digest, &v.RegisteredRevision,
		&v.Status, &v.PlainPassed, &v.SchemaPassed, &v.QualityPassed, &v.StructuredMode, &v.ErrorCode,
		&v.PayloadJSON, &v.UpdatedAt)
	return v, rowError(err)
}

func (s *Store) GetGenerationValidation(ctx context.Context, operationID string) (GenerationValidation, error) {
	if strings.TrimSpace(operationID) == "" {
		return GenerationValidation{}, ErrNotFound
	}
	return scanGenerationValidation(s.db.QueryRowContext(ctx,
		`SELECT `+generationValidationColumns+` FROM generation_validations WHERE operation_id=?`, operationID))
}

func (s *Store) ListGenerationValidations(ctx context.Context) ([]GenerationValidation, error) {
	return queryList(ctx, s.db, scanGenerationValidation, `SELECT `+generationValidationColumns+` FROM generation_validations`)
}

func (s *Store) RecordGenerationValidation(ctx context.Context, v GenerationValidation) error {
	if err := requireIDs(v.OperationID, v.Digest, v.Status); err != nil {
		return err
	}
	if err := validateGenerationPayload(v.PayloadJSON); err != nil {
		return err
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		return upsertGenerationValidationTx(ctx, tx, v)
	})
}

// RecordGenerationValidationAutoRevision atomically assigns registered_revision
// and persists the terminal validation record. When preservePassed is true and
// the digest is currently validated at registered_revision >= 1, the new row is
// written with registered_revision=0 so an interrupted revalidation does not
// revoke the existing passed state; otherwise registered_revision is MAX+1.
func (s *Store) RecordGenerationValidationAutoRevision(ctx context.Context, v GenerationValidation, preservePassed bool) (GenerationValidation, error) {
	if err := requireIDs(v.OperationID, v.Digest, v.Status); err != nil {
		return GenerationValidation{}, err
	}
	if err := validateGenerationPayload(v.PayloadJSON); err != nil {
		return GenerationValidation{}, err
	}
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		var maxRev int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(registered_revision),0) FROM generation_validations WHERE digest=?`, v.Digest).Scan(&maxRev); err != nil {
			return err
		}
		rev := maxRev + 1
		if preservePassed && maxRev >= 1 {
			var currentlyPassed bool
			if err := tx.QueryRowContext(ctx,
				`SELECT COALESCE(MIN(status='passed' AND plain_passed=1 AND schema_passed=1 AND quality_passed=1),0)
				 FROM generation_validations WHERE digest=? AND registered_revision=?`,
				v.Digest, maxRev).Scan(&currentlyPassed); err != nil {
				return err
			}
			if currentlyPassed {
				rev = 0
			}
		}
		v.RegisteredRevision = rev
		v.UpdatedAt = defaultText(v.UpdatedAt, NowUTCMicro())
		v.PayloadJSON = defaultText(v.PayloadJSON, "{}")
		return upsertGenerationValidationTx(ctx, tx, v)
	})
	if err != nil {
		return GenerationValidation{}, err
	}
	return v, nil
}

func upsertGenerationValidationTx(ctx context.Context, tx *sql.Tx, v GenerationValidation) error {
	return execSQL(ctx, tx, `INSERT INTO generation_validations VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(operation_id) DO UPDATE SET profile_id=excluded.profile_id,profile_revision=excluded.profile_revision,
		digest=excluded.digest,registered_revision=excluded.registered_revision,status=excluded.status,plain_passed=excluded.plain_passed,
		schema_passed=excluded.schema_passed,quality_passed=excluded.quality_passed,structured_mode=excluded.structured_mode,
		error_code=excluded.error_code,payload_json=excluded.payload_json,updated_at=excluded.updated_at`,
		v.OperationID, v.ProfileID, v.ProfileRevision, v.Digest, v.RegisteredRevision, v.Status, boolToInt(v.PlainPassed),
		boolToInt(v.SchemaPassed), boolToInt(v.QualityPassed), v.StructuredMode, v.ErrorCode, defaultText(v.PayloadJSON, "{}"),
		defaultText(v.UpdatedAt, NowUTCMicro()))
}

func (s *Store) IsGenerationDigestValidated(ctx context.Context, digest string) (bool, error) {
	var passed bool
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MIN(status='passed' AND plain_passed=1 AND schema_passed=1 AND quality_passed=1),0)
		FROM generation_validations WHERE digest=? AND registered_revision=(SELECT MAX(registered_revision) FROM generation_validations WHERE digest=?)`,
		digest, digest).Scan(&passed)
	return passed, err
}

func (s *Store) GetGenerationState(ctx context.Context) (GenerationState, error) {
	var state GenerationState
	err := s.db.QueryRowContext(ctx, `SELECT schema_version,revision,active_profile_id,active_revision,next_profile_id,next_revision,startup_error,
		connected_agent_selections_json,operations_json,removed_profiles_json,updated_at FROM generation_state WHERE singleton=1`).
		Scan(&state.SchemaVersion, &state.Revision, &state.ActiveProfileID, &state.ActiveRevision, &state.NextProfileID, &state.NextRevision,
			&state.StartupError, &state.ConnectedAgentSelectionsJSON, &state.OperationsJSON, &state.RemovedProfilesJSON, &state.UpdatedAt)
	return state, rowError(err)
}

func (s *Store) SetActiveGeneration(ctx context.Context, profileID string, revision int) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		var removed bool
		if err := tx.QueryRowContext(ctx, `SELECT removed FROM generation_profiles WHERE profile_id=? AND revision=?`, profileID, revision).Scan(&removed); err != nil {
			return rowError(err)
		}
		if removed {
			return fmt.Errorf("generation profile removed: %w", ErrForbidden)
		}
		return execSQL(ctx, tx, `UPDATE generation_state SET active_profile_id=?,active_revision=?,revision=revision+1,updated_at=?
			WHERE singleton=1 AND (active_profile_id<>? OR active_revision<>?)`, profileID, revision, NowUTCMicro(), profileID, revision)
	})
}

func (s *Store) GetGenerationOperation(ctx context.Context, id string) (json.RawMessage, error) {
	var value json.RawMessage
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM generation_state, json_each(operations_json)
  WHERE singleton=1 AND key=? AND type='object'`, id).Scan(&raw)
	if err == nil {
		value = json.RawMessage(raw)
	}
	return value, rowError(err)
}

func RecordGenerationOperation(ctx context.Context, tx *sql.Tx, id string, result any) error {
	if id == "" {
		return nil
	}
	if !generationIdentifier.MatchString(id) {
		return ErrInvalidArgument
	}
	record, err := json.Marshal(map[string]any{id: map[string]any{"recorded": true, "result": result}})
	if err != nil {
		return err
	}
	update, err := tx.ExecContext(ctx, `UPDATE generation_state
  SET operations_json=json_patch(operations_json,?)
  WHERE singleton=1 AND NOT EXISTS(SELECT 1 FROM json_each(operations_json) WHERE key=?)`, string(record), id)
	if err != nil {
		return err
	}
	changed, err := update.RowsAffected()
	if err == nil && changed != 1 {
		return ErrConflict
	}
	return err
}

func (s *Store) SetGenerationStartupError(ctx context.Context, code string) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		return execSQL(ctx, tx, `UPDATE generation_state SET startup_error=?,updated_at=?
  WHERE singleton=1 AND startup_error<>?`, code, NowUTCMicro(), code)
	})
}

func validateGenerationPayload(payload string) error {
	payload = defaultText(payload, "{}")
	if !json.Valid([]byte(payload)) {
		return fmt.Errorf("invalid generation receipt JSON: %w", ErrInvalidArgument)
	}
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()
	if err := scanGenerationJSON(decoder); err != nil {
		return fmt.Errorf("validate generation receipt: %w", err)
	}
	return nil
}

var generationSecretFieldNormalizer = strings.NewReplacer("_", "", "-", "")

func scanGenerationJSON(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil || (token != json.Delim('{') && token != json.Delim('[')) {
		return err
	}
	for decoder.More() {
		if token == json.Delim('{') {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			field := generationSecretFieldNormalizer.Replace(strings.ToLower(key.(string)))
			switch {
			case strings.HasSuffix(field, "apikey"), field == "authorization", field == "accesstoken", field == "sourcecredentials":
				return ErrForbidden
			}
		}
		if err := scanGenerationJSON(decoder); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

const generationHandoffColumns = `operation_id, profile_id, profile_revision, digest, previous_profile_id, previous_revision, status,
		reason, started_at, ended_at, active_committed, cancelled_sessions, pending_sessions, unconfirmed_sessions,
		payload_json, runtime_payload_json`

func scanGenerationHandoff(row scanner) (GenerationHandoff, error) {
	var p GenerationHandoff
	err := row.Scan(&p.OperationID, &p.ProfileID, &p.ProfileRevision, &p.Digest, &p.PreviousProfileID, &p.PreviousRevision, &p.Status,
		&p.Reason, &p.StartedAt, &p.EndedAt, &p.ActiveCommitted, &p.CancelledSessions, &p.PendingSessions, &p.UnconfirmedSessions,
		&p.PayloadJSON, &p.RuntimePayloadJSON)
	return p, rowError(err)
}

func (s *Store) UpsertGenerationHandoff(ctx context.Context, p GenerationHandoff) error {
	if err := requireIDs(p.OperationID); err != nil {
		return err
	}
	for _, payload := range []string{p.PayloadJSON, p.RuntimePayloadJSON} {
		if err := validateGenerationPayload(payload); err != nil {
			return err
		}
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		return execSQL(ctx, tx, `INSERT INTO generation_handoffs (`+generationHandoffColumns+`)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(operation_id) DO UPDATE SET
			profile_id=excluded.profile_id, profile_revision=excluded.profile_revision, digest=excluded.digest,
			previous_profile_id=excluded.previous_profile_id, previous_revision=excluded.previous_revision, status=excluded.status,
			reason=excluded.reason, started_at=excluded.started_at, ended_at=excluded.ended_at,
			active_committed=excluded.active_committed, cancelled_sessions=excluded.cancelled_sessions, pending_sessions=excluded.pending_sessions,
			unconfirmed_sessions=excluded.unconfirmed_sessions, payload_json=excluded.payload_json, runtime_payload_json=excluded.runtime_payload_json`,
			p.OperationID, p.ProfileID, p.ProfileRevision, p.Digest, p.PreviousProfileID, p.PreviousRevision, p.Status,
			p.Reason, p.StartedAt, p.EndedAt, boolToInt(p.ActiveCommitted), p.CancelledSessions, p.PendingSessions, p.UnconfirmedSessions,
			defaultText(p.PayloadJSON, "{}"), defaultText(p.RuntimePayloadJSON, "{}"))
	})
}

func (s *Store) GetGenerationHandoff(ctx context.Context, op string) (GenerationHandoff, error) {
	return scanGenerationHandoff(s.db.QueryRowContext(ctx, `SELECT `+generationHandoffColumns+` FROM generation_handoffs WHERE operation_id=?`, op))
}

func (s *Store) ListGenerationHandoffs(ctx context.Context) ([]GenerationHandoff, error) {
	return queryList(ctx, s.db, scanGenerationHandoff, `SELECT `+generationHandoffColumns+` FROM generation_handoffs ORDER BY started_at,operation_id`)
}
