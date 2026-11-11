package processing

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/credentials"
	"github.com/zwhu/memlane/internal/records"
)

const HTTPAdapterVersion = "memlane-http-completion-v1"
const DefaultMagpieEndpoint = "http://127.0.0.1:3425/v1"

type CredentialStore interface {
	CredentialReader
	Put(credentials.Reference, credentials.Target, []byte) error
}
type providerConfigurationLedger interface {
	SaveProcessingSupplier(context.Context, records.ProcessingCredentialWrite) (records.ProcessingSupplier, error)
	ProcessingSuppliers(context.Context) ([]records.ProcessingSupplier, error)
	ProcessingCredentialReferenced(context.Context, string, int64) (bool, error)
}
type httpValidationLedger interface {
	FinishHTTPProcessingValidation(context.Context, string, int64, string, string, string, time.Time, records.ProcessingValidationDiagnostics, *records.ProcessingHTTPReceipt) error
}

func (o *Owner) credentialStore() (CredentialStore, error) {
	if o.Credentials != nil {
		return o.Credentials, nil
	}
	return credentials.New(o.ClientRoot)
}

func validateHTTPConfiguration(spec records.ProcessingSpec, modelRequired bool) error {
	if spec.Mode != "provider" && spec.Mode != "magpie" || spec.Agent != "" || spec.Connection != "" || spec.ConnectionGeneration != 0 {
		return records.MergeError("invalid_configuration")
	}
	if modelRequired && (strings.TrimSpace(spec.Model) == "" || strings.TrimSpace(spec.Model) != spec.Model || len(spec.Model) > 256) {
		return records.MergeError("invalid_configuration")
	}
	if spec.Protocol != "openai-chat" && spec.Protocol != "openai-responses" && spec.Protocol != "anthropic-messages" {
		return records.MergeError("unsupported_processing")
	}
	u, err := url.Parse(spec.Endpoint)
	local := err == nil && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(spec.Endpoint) > 2048 || u.Scheme != "https" && !(local && u.Scheme == "http") {
		return records.MergeError("invalid_configuration")
	}
	if spec.Mode == "magpie" {
		if !local || u.Scheme != "http" || u.Port() == "" || spec.Credential != "" || spec.CredentialRevision != 0 || spec.Provider != "" {
			return records.MergeError("invalid_configuration")
		}
	} else {
		id, err := uuid.Parse(spec.Credential)
		if err != nil || id.String() != spec.Credential || spec.CredentialRevision < 1 {
			return records.MergeError("independent_credential_required")
		}
		if strings.TrimSpace(spec.Provider) == "" || strings.TrimSpace(spec.Provider) != spec.Provider || len(spec.Provider) > 128 {
			return records.MergeError("invalid_configuration")
		}
	}
	return nil
}
func (o *Owner) httpBinding(ctx context.Context, spec records.ProcessingSpec, modelRequired bool) (HTTPBinding, string, error) {
	if err := validateHTTPConfiguration(spec, modelRequired); err != nil {
		return HTTPBinding{}, "", err
	}
	var vault CredentialReader
	if spec.Mode == "provider" {
		v, err := o.credentialStore()
		if err != nil {
			return HTTPBinding{}, "", records.MergeError("independent_credential_unavailable")
		}
		vault = v
	}
	binding, dependency, err := ResolveHTTP(ctx, spec, vault)
	if err != nil {
		return binding, "", err
	}
	return binding, records.MergeDigest([]string{HTTPAdapterVersion, dependency}), nil
}
func (o *Owner) resolveHTTP(ctx context.Context, spec records.ProcessingSpec) (Binding, error) {
	http, dependency, err := o.httpBinding(ctx, spec, true)
	if err != nil {
		return Binding{}, err
	}
	defer http.Close()
	parts := map[string]string{"adapter_protocol": records.MergeDigest(HTTPAdapterVersion), "processing_configuration": records.MergeDigest(spec)}
	if spec.Mode == "provider" {
		parts["independent_credential"] = records.MergeDigest(http.Credential)
	}
	if spec.Mode == "magpie" {
		catalog, err := ReadModels(ctx, http, "")
		if err != nil {
			return Binding{}, err
		}
		selected := ""
		for _, model := range catalog.Models {
			if model.ID == spec.Model {
				selected = records.MergeDigest(struct{ ID, Provider string }{model.ID, model.Provider})
				break
			}
		}
		if selected == "" {
			if catalog.HasMore {
				return Binding{}, records.MergeError("model_catalog_incomplete")
			}
			return Binding{}, records.MergeError("model_not_available")
		}
		parts["gateway_model"] = selected
		dependency = records.MergeDigest([]string{dependency, selected})
	}
	return Binding{Spec: spec, Dependency: dependency, DependencyParts: parts}, nil
}

// Models only contacts the explicitly configured endpoint. It never reads an
// Agent/Magpie key or silently tries another supplier. IDs are API-provided.
func (o *Owner) Models(ctx context.Context, q records.ProcessingModelQuery) (records.ProcessingModelCatalog, error) {
	spec := q.Spec
	if spec.Mode == "magpie" {
		if spec.Endpoint == "" {
			spec.Endpoint = DefaultMagpieEndpoint
		}
		if spec.Protocol == "" {
			spec.Protocol = "openai-chat"
		}
	}
	binding, _, err := o.httpBinding(ctx, spec, false)
	if err != nil {
		return records.ProcessingModelCatalog{}, err
	}
	defer binding.Close()
	return ReadModels(ctx, binding, q.After)
}

// SaveCredential writes only a caller-provided independent supplier key to the
// existing private OS vault. Its immutable version is bound to this endpoint.
func (o *Owner) SaveCredential(ctx context.Context, d records.ProcessingCredentialWrite) (records.ProcessingCredentialReference, error) {
	defer clear(d.Secret)
	if ctx.Err() != nil {
		return records.ProcessingCredentialReference{}, ctx.Err()
	}
	if len(d.Secret) == 0 || len(d.Secret) > 8192 || bytes.ContainsAny(d.Secret, "\r\n") {
		return records.ProcessingCredentialReference{}, records.MergeError("invalid_credential")
	}
	spec := records.ProcessingSpec{Mode: "provider", Provider: d.Provider, Endpoint: d.Endpoint, Protocol: d.Protocol, Credential: d.Credential, CredentialRevision: d.Revision}
	if err := validateHTTPConfiguration(spec, false); err != nil {
		return records.ProcessingCredentialReference{}, err
	}
	metadata, ok := o.Ledger.(providerConfigurationLedger)
	if !ok {
		return records.ProcessingCredentialReference{}, records.MergeError("supplier_configuration_unavailable")
	}
	o.credentialMu.Lock()
	defer o.credentialMu.Unlock()
	o.mu.Lock()
	closed := o.closed
	o.mu.Unlock()
	if closed || o.Lifecycle == nil || o.Lifecycle.Err() != nil {
		return records.ProcessingCredentialReference{}, records.MergeError("service_stopping")
	}
	vault, err := o.credentialStore()
	if err != nil {
		return records.ProcessingCredentialReference{}, records.MergeError("independent_credential_unavailable")
	}
	ref := credentials.Reference{ID: d.Credential, Revision: d.Revision}
	target := credentials.Target{Provider: d.Provider, Endpoint: d.Endpoint, Protocol: d.Protocol}
	created := false
	if prior, e := vault.Get(ref, target); e == nil {
		equal := subtle.ConstantTimeCompare(prior, d.Secret) == 1
		clear(prior)
		if !equal {
			return records.ProcessingCredentialReference{}, records.ErrConflict
		}
	} else {
		if err = vault.Put(ref, target, d.Secret); err != nil {
			return records.ProcessingCredentialReference{}, records.MergeError("independent_credential_store_failed")
		}
		created = true
	}
	supplier, err := metadata.SaveProcessingSupplier(ctx, d)
	if err != nil {
		// Compensate only a newly owned, durably unreferenced secret. An existing
		// version or one referenced by another accepted configuration is retained.
		if created {
			cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
			defer stop()
			used, e := metadata.ProcessingCredentialReferenced(cleanup, ref.ID, ref.Revision)
			if e == nil && !used {
				if deleter, ok := vault.(interface {
					Delete(credentials.Reference) error
				}); ok {
					_ = deleter.Delete(ref)
				}
			}
		}
		return records.ProcessingCredentialReference{}, err
	}
	return records.ProcessingCredentialReference{Credential: ref.ID, Revision: ref.Revision, Supplier: &supplier}, nil
}
func (o *Owner) Suppliers(ctx context.Context) ([]records.ProcessingSupplier, error) {
	metadata, ok := o.Ledger.(providerConfigurationLedger)
	if !ok {
		return nil, records.MergeError("supplier_configuration_unavailable")
	}
	return metadata.ProcessingSuppliers(ctx)
}

func processingHTTPErrorCode(ctx context.Context, err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "model_timeout"
	}
	if errors.Is(err, context.Canceled) || ctx.Err() != nil {
		return "owner_interrupted"
	}
	var access *records.AccessError
	if errors.As(err, &access) {
		return access.Code
	}
	return "provider_request_failed"
}
func (o *Owner) completeHTTP(ctx context.Context, binding Binding, system, prompt string, schema json.RawMessage, onEvent func(string)) (HTTPCompletion, error) {
	http, dependency, err := o.httpBinding(ctx, binding.Spec, true)
	if err != nil {
		return HTTPCompletion{}, err
	}
	defer http.Close()
	if binding.Spec.Mode == "magpie" {
		actual, err := o.resolveHTTP(ctx, binding.Spec)
		if err != nil {
			return HTTPCompletion{}, err
		}
		if actual.Dependency != binding.Dependency {
			return HTTPCompletion{}, records.MergeError("dependency_changed")
		}
	} else if dependency != binding.Dependency {
		return HTTPCompletion{}, records.MergeError("dependency_changed")
	}
	http.OnEvent = onEvent
	run := o.HTTPComplete
	if run == nil {
		run = CompleteHTTP
	}
	return run(ctx, http, system, prompt, schema)
}
func (o *Owner) verifyHTTP(ctx context.Context, id string, binding Binding) (HTTPCompletion, error) {
	schema := json.RawMessage(`{"type":"object","additionalProperties":false,"required":["marker","value"],"properties":{"marker":{"type":"string","const":"memlane-model-probe"},"value":{"type":"integer","const":7}}}`)
	result, err := o.completeHTTP(ctx, binding, "You are performing a synthetic model capability probe. Return the requested JSON only.", "Return marker memlane-model-probe and integer value 7. This probe contains no user knowledge.", schema, func(event string) {
		o.mu.Lock()
		active := o.runs[id]
		active.progress.Events[event]++
		o.runs[id] = active
		o.mu.Unlock()
	})
	o.mu.Lock()
	active := o.runs[id]
	if result.Receipt.Protocol != "" {
		receipt := result.Receipt
		active.progress.HTTP = &receipt
	} else {
		active.progress.HTTP = nil
	}
	o.runs[id] = active
	o.mu.Unlock()
	return result, err
}
