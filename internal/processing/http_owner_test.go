package processing

// Loopback HTTP + SQLite prove actual wire/owner/ledger behavior. Credentials
// and model replies below are explicit synthetic fixtures, never real accounts.
import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/credentials"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

type ownedHTTPVault struct {
	mu      sync.Mutex
	entries map[credentials.Reference]struct {
		target credentials.Target
		secret []byte
	}
}

func (v *ownedHTTPVault) Get(ref credentials.Reference, target credentials.Target) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	e, ok := v.entries[ref]
	if !ok || e.target != target {
		return nil, errors.New("fixture credential unavailable")
	}
	return append([]byte(nil), e.secret...), nil
}
func (v *ownedHTTPVault) Put(ref credentials.Reference, target credentials.Target, secret []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.entries == nil {
		v.entries = map[credentials.Reference]struct {
			target credentials.Target
			secret []byte
		}{}
	}
	if _, ok := v.entries[ref]; ok {
		return errors.New("fixture immutable credential exists")
	}
	v.entries[ref] = struct {
		target credentials.Target
		secret []byte
	}{target, append([]byte(nil), secret...)}
	return nil
}
func (v *ownedHTTPVault) Delete(ref credentials.Reference) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	e := v.entries[ref]
	clear(e.secret)
	delete(v.entries, ref)
	return nil
}
func httpOwnerFixture(t *testing.T) (*Owner, *store.Store, *ownedHTTPVault) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "memory.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	vault := &ownedHTTPVault{}
	owner := &Owner{ClientRoot: root, Ledger: db, Lifecycle: ctx, Credentials: vault, Acquire: func(ctx context.Context) (func(), error) { return func() {}, ctx.Err() }}
	t.Cleanup(func() { cancel(); owner.Close(); db.Close() })
	return owner, db, vault
}
func saveHTTPFixture(t *testing.T, o *Owner, db *store.Store, endpoint, protocol, mode string) records.ProcessingRevision {
	t.Helper()
	spec := records.ProcessingSpec{Mode: mode, Endpoint: endpoint, Protocol: protocol, Model: "fixture-model"}
	if mode == "provider" {
		d := records.ProcessingCredentialWrite{Credential: uuid.NewString(), Revision: 1, Provider: "fixture-" + protocol, Endpoint: endpoint, Protocol: protocol, Secret: []byte("owned-synthetic-key")}
		ref, err := o.SaveCredential(context.Background(), d)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range d.Secret {
			if value != 0 {
				t.Fatal("submitted credential bytes retained")
			}
		}
		spec.Provider, spec.Credential, spec.CredentialRevision = d.Provider, ref.Credential, ref.Revision
	}
	revision, err := db.SaveProcessing(context.Background(), records.ProcessingSave{OperationID: uuid.NewString(), Spec: spec}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return revision
}
func probeHTTPReply(protocol string) map[string]any {
	return responseFixture(protocol, `{"marker":"memlane-model-probe","value":7}`)
}

func TestHTTPOwnerAllProtocolsActualValidationActivationAndDurableReceipt(t *testing.T) {
	for _, protocol := range []string{"openai-chat", "openai-responses", "anthropic-messages"} {
		t.Run(protocol, func(t *testing.T) {
			var calls atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") != "Bearer owned-synthetic-key" && r.Header.Get("x-api-key") != "owned-synthetic-key" {
					t.Error("independent credential missing")
				}
				if r.Method == "GET" {
					json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "exact/id:actual", "display_name": "actual directory fixture", "owned_by": "fixture-owner"}}})
					return
				}
				json.NewEncoder(w).Encode(probeHTTPReply(protocol))
			}))
			defer srv.Close()
			owner, db, _ := httpOwnerFixture(t)
			revision := saveHTTPFixture(t, owner, db, srv.URL+"/v1", protocol, "provider")
			before, err := owner.Configuration(context.Background())
			if err != nil || before.Current.Active {
				t.Fatal("saving activated inference", before, err)
			}
			probe, err := owner.Begin(context.Background(), uuid.NewString(), revision.ID)
			if err != nil {
				t.Fatal(err)
			}
			terminal := waitValidation(t, db, probe.ID)
			if terminal.State != "succeeded" || terminal.HTTP == nil || !terminal.HTTP.RequestSent || !terminal.HTTP.NativeSchemaRequested || !terminal.HTTP.ResponseComplete || !terminal.HTTP.SchemaValid || terminal.HTTP.ReportedModel != "fixture-model" {
				t.Fatal("real HTTP receipt not persisted", terminal)
			}
			status, err := owner.Status(context.Background(), probe.ID)
			if err != nil || status.Progress == nil || status.Progress.Started || status.Progress.Reaped || status.Progress.PID != 0 || status.Progress.HTTP == nil {
				t.Fatal("HTTP invented Agent process proof", status, err)
			}
			if _, err = owner.Activate(context.Background(), records.ProcessingActivation{OperationID: uuid.NewString(), Revision: revision.ID}); err != nil {
				t.Fatal(err)
			}
			config, err := owner.Configuration(context.Background())
			if err != nil || !config.Current.Active || config.Runtime[revision.ID].State != "ready" {
				t.Fatal(config, err)
			}
			models, err := owner.Models(context.Background(), records.ProcessingModelQuery{Spec: revision.Spec})
			if err != nil || len(models.Models) != 1 || models.Models[0].ID != "exact/id:actual" || models.Models[0].Provider != "fixture-owner" {
				t.Fatal("catalog IDs/owner guessed", models, err)
			}
			suppliers, err := owner.Suppliers(context.Background())
			if err != nil || len(suppliers) != 1 || suppliers[0].Credential != revision.Spec.Credential {
				t.Fatal(suppliers, err)
			}
			raw, _ := json.Marshal(struct {
				Configuration records.ProcessingConfiguration
				Validation    records.ProcessingValidation
				Suppliers     []records.ProcessingSupplier
			}{config, terminal, suppliers})
			if strings.Contains(string(raw), "owned-synthetic-key") {
				t.Fatal("credential entered durable/public state")
			}
			if calls.Load() != 2 {
				t.Fatal("implicit fallback or repeat inference", calls.Load())
			}
		})
	}
}

func TestHTTPMagpieNoCredentialAndActualGatewayIdentity(t *testing.T) {
	var authorizations atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("x-api-key") != "" {
			authorizations.Add(1)
		}
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "fixture-model", "owned_by": "gateway-fixture"}}})
			return
		}
		w.Header().Set("X-Magpie-Provider", "actual-upstream-fixture")
		w.Header().Set("X-Magpie-Model", "actual-upstream/served-model")
		body := probeHTTPReply("openai-chat")
		body["model"] = "actual-response-model"
		json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()
	owner, db, vault := httpOwnerFixture(t)
	revision := saveHTTPFixture(t, owner, db, srv.URL+"/v1", "openai-chat", "magpie")
	probe, err := owner.Begin(context.Background(), uuid.NewString(), revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	terminal := waitValidation(t, db, probe.ID)
	if terminal.State != "succeeded" || terminal.HTTP == nil || terminal.HTTP.RequestedModel != "fixture-model" || terminal.HTTP.ReportedModel != "actual-response-model" || terminal.HTTP.GatewayModel != "actual-upstream/served-model" {
		t.Fatal("gateway routing identity was replaced with selected model", terminal)
	}
	vault.mu.Lock()
	count := len(vault.entries)
	vault.mu.Unlock()
	if authorizations.Load() != 0 || count != 0 {
		t.Fatal("Magpie read/forwarded independent or Agent credentials")
	}
}

func TestHTTPOwnerCancelDrainsLocalTransportAndRejectsLateResult(t *testing.T) {
	started, finish := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-finish:
		case <-r.Context().Done():
		}
		json.NewEncoder(w).Encode(probeHTTPReply("openai-chat"))
	}))
	defer srv.Close()
	defer close(finish)
	owner, db, _ := httpOwnerFixture(t)
	revision := saveHTTPFixture(t, owner, db, srv.URL, "openai-chat", "provider")
	probe, err := owner.Begin(context.Background(), uuid.NewString(), revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if err = owner.Cancel(context.Background(), probe.ID); err != nil {
		t.Fatal(err)
	}
	terminal := waitValidation(t, db, probe.ID)
	if terminal.State != "cancelled" || terminal.HTTP == nil || !terminal.HTTP.RequestSent || !terminal.HTTP.LocalCancelled || terminal.HTTP.ResponseComplete || terminal.ReceiptDigest != "" {
		t.Fatal("cancelled HTTP was reported as successful or process reaped", terminal)
	}
	config, err := owner.Configuration(context.Background())
	if err != nil || config.Current.Active {
		t.Fatal("cancel changed selection", config, err)
	}
	if _, err = owner.Activate(context.Background(), records.ProcessingActivation{OperationID: uuid.NewString(), Revision: revision.ID}); err == nil {
		t.Fatal("cancelled receipt enabled")
	}
}

func TestHTTPQueuedCredentialRevocationPreventsAnyRequest(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer srv.Close()
	owner, db, vault := httpOwnerFixture(t)
	revision := saveHTTPFixture(t, owner, db, srv.URL, "openai-chat", "provider")
	queued, release := make(chan struct{}), make(chan struct{})
	owner.Acquire = func(ctx context.Context) (func(), error) {
		close(queued)
		select {
		case <-release:
			return func() {}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	probe, err := owner.Begin(context.Background(), uuid.NewString(), revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	<-queued
	vault.Delete(credentials.Reference{ID: revision.Spec.Credential, Revision: revision.Spec.CredentialRevision})
	close(release)
	terminal := waitValidation(t, db, probe.ID)
	if terminal.State != "failed" || terminal.ErrorCode != "independent_credential_unavailable" || terminal.Diagnostics == nil || terminal.Diagnostics.Stage != "preflight" || calls.Load() != 0 {
		t.Fatal("revoked queued credential invoked provider", terminal, calls.Load())
	}
}

func TestHTTPSupplierAddressChangeNeedsNewKeyAndKeepsOtherSupplier(t *testing.T) {
	owner, db, vault := httpOwnerFixture(t)
	first := records.ProcessingCredentialWrite{Credential: uuid.NewString(), Revision: 1, Provider: "supplier-one", Endpoint: "https://one.invalid/v1", Protocol: "openai-chat", Secret: []byte("first-owned-fixture")}
	a, err := owner.SaveCredential(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	second := records.ProcessingCredentialWrite{Credential: uuid.NewString(), Revision: 1, Provider: "supplier-two", Endpoint: "https://two.invalid/v1", Protocol: "openai-responses", Secret: []byte("second-owned-fixture")}
	b, err := owner.SaveCredential(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	changed := first
	changed.Endpoint = "https://changed.invalid/v1"
	changed.ExpectedRevision = a.Supplier.Revision
	changed.Secret = []byte("first-owned-fixture")
	if _, err = owner.SaveCredential(context.Background(), changed); err == nil {
		t.Fatal("address change reused existing credential version")
	}
	suppliers, err := db.ProcessingSuppliers(context.Background())
	if err != nil || len(suppliers) != 2 {
		t.Fatal(suppliers, err)
	}
	changed.Credential = uuid.NewString()
	changed.Secret = []byte("new-target-owned-fixture")
	c, err := owner.SaveCredential(context.Background(), changed)
	if err != nil {
		t.Fatal(err)
	}
	if c.Supplier.Revision != 2 || c.Supplier.Endpoint != changed.Endpoint {
		t.Fatal(c)
	}
	other, err := vault.Get(credentials.Reference{ID: b.Credential, Revision: b.Revision}, credentials.Target{Provider: second.Provider, Endpoint: second.Endpoint, Protocol: second.Protocol})
	if err != nil || string(other) != "second-owned-fixture" {
		t.Fatal("supplier update changed another key")
	}
	clear(other)
	old, err := vault.Get(credentials.Reference{ID: a.Credential, Revision: a.Revision}, credentials.Target{Provider: first.Provider, Endpoint: first.Endpoint, Protocol: first.Protocol})
	if err != nil || string(old) != "first-owned-fixture" {
		t.Fatal("old frozen credential destroyed")
	}
	clear(old)
	conflict := changed
	conflict.Credential = uuid.NewString()
	conflict.Secret = []byte("unreferenced-owned-fixture")
	conflict.ExpectedRevision = 0
	if _, err = owner.SaveCredential(context.Background(), conflict); !errors.Is(err, records.ErrConflict) {
		t.Fatal("stale supplier metadata replaced", err)
	}
	if _, err = vault.Get(credentials.Reference{ID: conflict.Credential, Revision: 1}, credentials.Target{Provider: conflict.Provider, Endpoint: conflict.Endpoint, Protocol: conflict.Protocol}); err == nil {
		t.Fatal("failed metadata left newly owned unreferenced key")
	}
	values, err := owner.Suppliers(context.Background())
	if err != nil || len(values) != 2 || !reflect.DeepEqual(values, suppliers) && values[0].Revision != 2 {
		t.Fatal(values, err)
	}
}

type revokeBeforeHTTPBindVault struct {
	*ownedHTTPVault
	reads atomic.Int64
}

func (v *revokeBeforeHTTPBindVault) Get(ref credentials.Reference, target credentials.Target) ([]byte, error) {
	if v.reads.Add(1) >= 3 {
		return nil, errors.New("explicit fixture revoke between preflight and request")
	}
	return v.ownedHTTPVault.Get(ref, target)
}
func TestHTTPCredentialRevokeBeforeRequestHasNoFakeReceiptOrOwnerPoison(t *testing.T) {
	owner, db, vault := httpOwnerFixture(t)
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer srv.Close()
	revision := saveHTTPFixture(t, owner, db, srv.URL, "openai-chat", "provider")
	owner.Credentials = &revokeBeforeHTTPBindVault{ownedHTTPVault: vault}
	var poisoned atomic.Int64
	owner.Poison = func() { poisoned.Add(1) }
	probe, err := owner.Begin(context.Background(), uuid.NewString(), revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	terminal := waitValidation(t, db, probe.ID)
	if terminal.State != "failed" || terminal.ErrorCode != "independent_credential_unavailable" || terminal.HTTP != nil || calls.Load() != 0 || poisoned.Load() != 0 {
		t.Fatal("normal credential revocation fabricated HTTP proof or stopped owner", terminal, calls.Load(), poisoned.Load())
	}
}

func TestHTTPMagpieDirectoryDriftFailsPostflightWithoutAssumingSelectedRoute(t *testing.T) {
	var changed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			provider := "first-fixture-route"
			if changed.Load() {
				provider = "changed-fixture-route"
			}
			json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "fixture-model", "owned_by": provider}}})
			return
		}
		changed.Store(true)
		json.NewEncoder(w).Encode(probeHTTPReply("openai-chat"))
	}))
	defer srv.Close()
	owner, db, _ := httpOwnerFixture(t)
	revision := saveHTTPFixture(t, owner, db, srv.URL, "openai-chat", "magpie")
	probe, err := owner.Begin(context.Background(), uuid.NewString(), revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	terminal := waitValidation(t, db, probe.ID)
	if terminal.State != "failed" || terminal.ErrorCode != "dependency_changed" || terminal.Diagnostics == nil || terminal.Diagnostics.Stage != "postflight" || !reflect.DeepEqual(terminal.Diagnostics.ChangedNames, []string{"gateway_model"}) || terminal.HTTP == nil || !terminal.HTTP.SchemaValid {
		t.Fatal("gateway directory drift inherited success", terminal)
	}
}
