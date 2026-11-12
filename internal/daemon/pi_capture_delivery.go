package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"time"

	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/records"
)

var piCaptureHash = regexp.MustCompile(`^[0-9a-f]{64}$`)
var piCaptureHostID = regexp.MustCompile(`^[!-~]{1,128}$`)

func piCaptureFingerprint(value any) string {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	sum := sha256.Sum256(bytes.TrimSuffix(encoded.Bytes(), []byte("\n")))
	return hex.EncodeToString(sum[:])
}

func validatePiCaptureBatch(q *records.PiCaptureBatchRequest) ([]string, error) {
	fail := &records.AccessError{Code: "invalid_request", Message: "bounded host capture provenance required"}
	if q == nil || !piCaptureHash.MatchString(q.Batch) || !piCaptureHash.MatchString(q.Grant) || !piCaptureHostID.MatchString(q.HostSession) || len(q.Entries) == 0 || len(q.Entries) > 64 {
		return nil, fail
	}
	encoded, err := json.Marshal(q)
	if err != nil || len(encoded) > 1024*1024 {
		return nil, fail
	}
	session := "pi-" + piCaptureFingerprint([]string{q.Grant, q.HostSession})[:48]
	kinds := make([]string, len(q.Entries))
	seen := map[string]bool{}
	for i, input := range q.Entries {
		if !piCaptureHash.MatchString(input.Operation) || input.Session != session || seen[input.Operation] {
			return nil, fail
		}
		seen[input.Operation] = true
		var entry map[string]json.RawMessage
		if json.Unmarshal(input.Entry, &entry) != nil {
			return nil, fail
		}
		var kind, sourceText string
		if json.Unmarshal(entry["type"], &kind) != nil || (kind != "qa" && kind != "trace") {
			return nil, fail
		}
		key := "context"
		if kind == "trace" {
			key = "memory_context"
		}
		if json.Unmarshal(entry[key], &sourceText) != nil || len(sourceText) > 4096 {
			return nil, fail
		}
		var source struct {
			Origin    string   `json:"origin"`
			Session   string   `json:"host_session"`
			Questions []string `json:"question_entries"`
			Answer    string   `json:"answer_entry"`
			Call      string   `json:"call_entry"`
			Result    string   `json:"result_entry"`
		}
		if json.Unmarshal([]byte(sourceText), &source) != nil || source.Origin != "memlane_pi_capture_v1" || source.Session != q.HostSession {
			return nil, fail
		}
		var operation string
		if kind == "qa" {
			if len(source.Questions) == 0 || len(source.Questions) > 16 || !piCaptureHostID.MatchString(source.Answer) {
				return nil, fail
			}
			ids := map[string]bool{}
			for _, id := range source.Questions {
				if !piCaptureHostID.MatchString(id) || ids[id] {
					return nil, fail
				}
				ids[id] = true
			}
			operation = piCaptureFingerprint([]any{q.Grant, q.HostSession, "qa", source.Questions, source.Answer})
		} else {
			if !piCaptureHostID.MatchString(source.Call) || !piCaptureHostID.MatchString(source.Result) {
				return nil, fail
			}
			operation = piCaptureFingerprint([]string{q.Grant, q.HostSession, "trace", source.Call, source.Result})
		}
		if operation != input.Operation {
			return nil, fail
		}
		kinds[i] = kind
	}
	return kinds, nil
}

func (s *Server) currentPiCaptureBatchGrant(ctx context.Context, q *records.PiCaptureBatchRequest) (records.PiCaptureConsent, error) {
	grant, err := s.authorizePiCapture(ctx, q.Request)
	if err == nil && grant.Key != q.Grant {
		return records.PiCaptureConsent{}, &records.AccessError{Code: "scope_changed", Message: "capture consent changed"}
	}
	return grant, err
}

// Network calls are cancelable on revocation; a dispatched external write
// cannot be rolled back. Such an uncertain outcome stays in the receipt ledger
// and is never resent, and revoked results cannot be returned/adopted.
func (s *Server) observePiCaptureAuthority(ctx context.Context, q *records.PiCaptureBatchRequest) (context.Context, func()) {
	operation, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(150 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-operation.Done():
				return
			case <-ticker.C:
				if _, err := s.currentPiCaptureBatchGrant(operation, q); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	return operation, func() { cancel(); <-done }
}

func (s *Server) processPiCaptureBatch(ctx context.Context, q *records.PiCaptureBatchRequest, inspect bool) (records.PiCaptureBatchReceipt, error) {
	var empty records.PiCaptureBatchReceipt
	kinds, err := validatePiCaptureBatch(q)
	if err != nil {
		return empty, err
	}
	grant, err := s.currentPiCaptureBatchGrant(ctx, q)
	if err != nil {
		return empty, err
	}
	raw := make([]json.RawMessage, len(q.Entries))
	for i, input := range q.Entries {
		raw[i] = input.Entry
	}
	// Config paths belong to the host daemon. No request may choose a Python
	// executable, source path, policy environment, service endpoint or credential.
	filtered, err := agents.ApplyOfficialCapturePolicy(ctx, agents.OfficialCapturePolicyOptions{Python: s.config.PiCapturePython, PolicyPath: s.config.PiCapturePolicy}, raw)
	if err != nil {
		return empty, &records.AccessError{Code: "capture_policy_unavailable", Message: "fixed official capture policy unavailable"}
	}
	preparedInputs := make([]official.CaptureEntry, len(q.Entries))
	identities := make([]records.OfficialCaptureIdentity, len(q.Entries))
	// Validate every post-policy item before the first dispatch. A malformed
	// neighbor cannot turn a rejected batch into an accidental partial write.
	for i, input := range q.Entries {
		identity := records.OfficialCaptureIdentity{Operation: input.Operation, Library: grant.Library, GrantDigest: grant.Key, Instance: grant.Binding.Instance, Dataset: grant.Binding.Dataset, Session: input.Session, Kind: kinds[i]}
		if filtered[i].State == "policy_filtered" {
			identity.PayloadDigest = records.MergeDigest(input.Entry)
		} else {
			input.Entry = filtered[i].Entry
			input, err = official.BoundCaptureAfterPolicy(input)
			if err != nil {
				return empty, &records.AccessError{Code: "invalid_request", Message: "post-policy capture entry invalid"}
			}
			prepared, e := official.CaptureIdentity(input)
			if e != nil {
				return empty, &records.AccessError{Code: "invalid_request", Message: "capture identity invalid"}
			}
			identity.PayloadDigest = prepared.Digest
		}
		preparedInputs[i], identities[i] = input, identity
	}
	out := records.PiCaptureBatchReceipt{Batch: q.Batch, Operations: make([]records.PiCaptureOperationReceipt, 0, len(q.Entries))}
	for i, input := range preparedInputs {
		identity := identities[i]
		isFiltered := filtered[i].State == "policy_filtered"
		if _, err = s.currentPiCaptureBatchGrant(ctx, q); err != nil {
			return empty, err
		}
		row, readErr := s.store.OfficialCaptureReceipt(ctx, identity)
		if readErr != nil && readErr != sql.ErrNoRows {
			return empty, readErr
		}
		if inspect && readErr == sql.ErrNoRows {
			out.Operations = append(out.Operations, records.PiCaptureOperationReceipt{Operation: identity.Operation, State: "not_dispatched"})
			continue
		}
		var reader official.SessionReader
		if !isFiltered && (readErr == sql.ErrNoRows || row.State == "dispatching") {
			token, e := s.cogneeToken(grant.Binding.Instance, grant.Binding.Dataset)
			if e != nil {
				return empty, e
			}
			reader = official.SessionReader{Endpoint: grant.Binding.Endpoint, Instance: grant.Binding.Instance, Dataset: grant.Binding.Dataset, Token: token}
			if readErr == sql.ErrNoRows {
				if e = reader.VerifyCaptureService(ctx); e != nil {
					return empty, &records.AccessError{Code: "cognee_unavailable", Message: "capture service preflight unavailable"}
				}
			}
		}
		claimed := false
		if !inspect {
			s.agentAccess.RLock()
			fresh, e := s.store.AuthorizePiCapture(ctx, q.Request, piCapturePolicyDigest)
			if e == nil && fresh.Key != q.Grant {
				e = &records.AccessError{Code: "scope_changed", Message: "capture authority changed before dispatch"}
			}
			if e == nil {
				row, claimed, e = s.store.ClaimOfficialCapture(ctx, identity, isFiltered)
			}
			s.agentAccess.RUnlock()
			if e != nil {
				return empty, e
			}
		}
		if row.State == "dispatching" {
			network, stop := s.observePiCaptureAuthority(ctx, q)
			var receipt *official.CaptureReceipt
			if claimed {
				saved, e := reader.WriteCaptureOnce(network, input)
				if e == nil {
					receipt = &saved
				}
			} else {
				receipt, _ = reader.InspectCapture(network, input)
			}
			stop()
			s.agentAccess.RLock()
			fresh, e := s.store.AuthorizePiCapture(ctx, q.Request, piCapturePolicyDigest)
			if e == nil && fresh.Key != q.Grant {
				e = &records.AccessError{Code: "scope_changed", Message: "capture authority changed during dispatch"}
			}
			if e == nil && receipt != nil {
				e = s.store.SealOfficialCapture(ctx, identity, receipt.ID)
				if e == nil {
					row.State = "saved"
				}
			}
			s.agentAccess.RUnlock()
			if e != nil {
				return empty, e
			}
		}
		state := row.State
		if state == "dispatching" {
			state = "unknown"
		}
		out.Operations = append(out.Operations, records.PiCaptureOperationReceipt{Operation: identity.Operation, State: state})
	}
	if _, err = s.currentPiCaptureBatchGrant(ctx, q); err != nil {
		return empty, err
	}
	return out, nil
}
