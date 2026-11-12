package desktop

// These are Unix IPC and headless UI fixtures, not provider/Keychain/native
// acceptance. The actual owner/vault/HTTP protocol tests live in their packages.
import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestProcessingHTTPProviderCredentialCatalogValidationAndExplicitActivation(t *testing.T) {
	var credentialWrites atomic.Int64
	credentialID := ""
	savedSpec := records.ProcessingSpec{}
	active := false
	revisionID, validationID := uuid.NewString(), uuid.NewString()
	app, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		switch r.Op {
		case "processing_credential_save":
			d := r.ProcessingCredentialWrite
			if !r.UserConfirmed || d == nil || string(d.Secret) != "synthetic-independent-provider-key" || d.Provider != "deepseek" {
				return serviceipc.Response{Error: "bad explicit credential fixture"}
			}
			credentialWrites.Add(1)
			credentialID = d.Credential
			return serviceipc.Response{OK: true, Result: records.ProcessingCredentialReference{Credential: d.Credential, Revision: d.Revision, Supplier: &records.ProcessingSupplier{Provider: d.Provider, Endpoint: d.Endpoint, Protocol: d.Protocol, Credential: d.Credential, CredentialRevision: d.Revision, Revision: 1}}}
		case "processing_models":
			q := r.ProcessingModelQuery
			if q == nil || q.Spec.Credential != credentialID || q.Spec.Model != "" {
				return serviceipc.Response{Error: "catalog did not use independent profile"}
			}
			return serviceipc.Response{OK: true, Result: records.ProcessingModelCatalog{Models: []records.ProcessingModel{{ID: "returned-model-one", Name: "Returned one", Provider: "upstream-account"}, {ID: "returned-model-two", Provider: "upstream-account"}}, Endpoint: q.Spec.Endpoint, Protocol: q.Spec.Protocol, ReadMS: time.Now().UnixMilli()}}
		case "processing_save":
			savedSpec = r.ProcessingSave.Spec
			return serviceipc.Response{OK: true, Result: records.ProcessingRevision{ID: revisionID, Spec: savedSpec}}
		case "processing_validate":
			if !r.UserConfirmed || r.ProcessingValidation.Revision != revisionID {
				return serviceipc.Response{Error: "bad validation identity"}
			}
			return serviceipc.Response{OK: true, Result: records.ProcessingValidation{ID: validationID, Revision: revisionID, State: "validating"}}
		case "processing_validation":
			return serviceipc.Response{OK: true, Result: records.ProcessingValidationStatus{Validation: records.ProcessingValidation{ID: validationID, Revision: revisionID, State: "succeeded"}, Progress: &records.ProcessingValidationProgress{HTTP: &records.ProcessingHTTPReceipt{RequestedModel: savedSpec.Model, ReportedModel: "upstream-reported-model", RequestSent: true, ResponseComplete: true, SchemaValid: true}}}}
		case "processing_activate":
			if !r.UserConfirmed || r.ProcessingActivation.Revision != revisionID {
				return serviceipc.Response{Error: "bad explicit activation"}
			}
			active = true
			return serviceipc.Response{OK: true, Result: records.ProcessingActivationReceipt{Revision: revisionID, Generation: 1}}
		case "processing_get":
			v := records.ProcessingRevision{ID: revisionID, Spec: savedSpec, Validation: &records.ProcessingValidation{ID: validationID, Revision: revisionID, State: "succeeded"}}
			return serviceipc.Response{OK: true, Result: records.ProcessingConfiguration{Saved: map[string]records.ProcessingRevision{"provider": v}, Runtime: map[string]records.ProcessingReadiness{revisionID: {State: "ready"}}, Current: records.ProcessingSelection{Revision: &v, Active: active}}}
		default:
			return serviceipc.Response{Error: "unexpected op " + r.Op}
		}
	})
	app.processing.Mode = "provider"
	app.processing.ensure()
	app.processing.ensureHTTP()
	h := &app.processing.HTTP
	h.ProfilesLoaded = true
	h.Editing = "deepseek"
	h.Endpoint = "https://provider.invalid/v1"
	h.Protocol = "openai-chat"
	h.Secret = "synthetic-independent-provider-key"
	app.saveProcessingSupplier()
	awaitProcessingDispatch(t, updates)
	awaitProcessingDispatch(t, updates)
	if h.Secret != "" || credentialWrites.Load() != 1 || !app.processing.catalog("provider").Loaded || app.processing.Configuration.Current.Active || len(app.processing.Configuration.Saved) != 0 {
		t.Fatal("credential/catalog read enabled a processing candidate")
	}
	app.chooseProcessingHTTPModel("provider", "deepseek", "returned-model-one")
	first := app.processing.Drafts["provider"]
	app.chooseProcessingHTTPModel("provider", "deepseek", "returned-model-two")
	if first.Credential != app.processing.Drafts["provider"].Credential || credentialWrites.Load() != 1 {
		t.Fatal("models failed to reuse supplier configuration")
	}
	if !app.processing.CanSave() || app.processing.CanValidate() {
		t.Fatal("unsaved provider bypassed save-first gate")
	}
	app.saveProcessing()
	awaitProcessingDispatch(t, updates)
	app.validateProcessing()
	awaitProcessingDispatch(t, updates)
	awaitProcessingDispatch(t, updates)
	awaitProcessingDispatch(t, updates)
	if app.processing.Configuration.Current.Active || !app.processing.CanActivate() || app.processing.Progress.HTTP.ReportedModel != "upstream-reported-model" {
		t.Fatal("validation inferred activation or lost reported identity")
	}
	app.activateProcessing()
	awaitProcessingDispatch(t, updates)
	if !app.processing.Configuration.Current.Active {
		t.Fatal("explicit activation receipt not reflected")
	}
}

func TestProcessingHTTPMagpieDetectionIsCredentialFreeAndReportsEmpty(t *testing.T) {
	var calls atomic.Int64
	app, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op != "processing_models" || r.ProcessingModelQuery == nil {
			return serviceipc.Response{Error: "unexpected non-directory operation"}
		}
		spec := r.ProcessingModelQuery.Spec
		if spec.Mode != "magpie" || spec.Endpoint != "http://127.0.0.1:3425/v1" || spec.Credential != "" || spec.Provider != "" || spec.CredentialRevision != 0 {
			return serviceipc.Response{Error: "gateway requested credentials"}
		}
		calls.Add(1)
		models := []records.ProcessingModel{}
		if calls.Load() > 1 {
			models = append(models, records.ProcessingModel{ID: "gateway-returned-id", Provider: "deepseek"})
		}
		return serviceipc.Response{OK: true, Result: records.ProcessingModelCatalog{Models: models, Endpoint: spec.Endpoint, Protocol: spec.Protocol, ReadMS: time.Now().UnixMilli()}}
	})
	app.page, app.settingsSection = "设置", "processing"
	app.processing.Mode = "magpie"
	app.processing.ensureHTTP()
	app.readProcessingModels("magpie", false)
	awaitProcessingDispatch(t, updates)
	tt := ui.NewTester(app.View, 1200, 820)
	if !tt.HasText("Magpie 没有返回可用模型") {
		t.Fatal("empty successful gateway catalog was not distinguished")
	}
	if _, exists := tt.Find("供应商独立 API Key"); exists {
		t.Fatal("gateway requested an upstream credential")
	}
	app.readProcessingModels("magpie", false)
	awaitProcessingDispatch(t, updates)
	app.chooseProcessingHTTPModel("magpie", "deepseek", "gateway-returned-id")
	draft := app.processing.Drafts["magpie"]
	if draft.Model != "gateway-returned-id" || draft.Provider != "" || draft.Credential != "" || !app.processing.CanSave() || app.processing.Configuration.Current.Active {
		t.Fatal("gateway catalog selection changed upstream credentials or activation")
	}
	app.processing.HTTP.GatewayEndpoint = "http://remote.invalid:3425/v1"
	app.readProcessingModels("magpie", false)
	if calls.Load() != 2 || app.processing.catalog("magpie").Error == "" {
		t.Fatal("nonlocal gateway was contacted")
	}
}

func TestProcessingHTTPCatalogFailurePreservesSavedSelectionAndDoesNotEchoError(t *testing.T) {
	app, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		return serviceipc.Response{Code: "provider_authentication_failed", Error: "synthetic-secret-must-not-be-displayed"}
	})
	app.processing.Mode = "provider"
	app.processing.ensure()
	app.processing.ensureHTTP()
	profile := records.ProcessingSpec{Mode: "provider", Provider: "deepseek", Endpoint: "https://provider.invalid/v1", Protocol: "openai-chat", Credential: uuid.NewString(), CredentialRevision: 1}
	app.processing.HTTP.Profiles["deepseek"] = profile
	catalog := app.processing.catalog("provider")
	catalog.Loaded = true
	catalog.Catalog.Models = []records.ProcessingModel{{ID: "existing-real-model", Provider: "deepseek"}}
	revision := records.ProcessingRevision{ID: uuid.NewString(), Spec: profile}
	app.processing.Configuration.Current = records.ProcessingSelection{Active: true, Generation: 4, Revision: &revision}
	app.readProcessingModels("provider", false)
	awaitProcessingDispatch(t, updates)
	if len(catalog.Catalog.Models) != 1 || catalog.Catalog.Models[0].ID != "existing-real-model" || !app.processing.Configuration.Current.Active || app.processing.Configuration.Current.Generation != 4 {
		t.Fatal("catalog error cleared saved data or selection")
	}
	if !strings.Contains(catalog.Error, "独立凭据") || strings.Contains(catalog.Error, "synthetic-secret") {
		t.Fatal("untrusted error reached UI or useful failure was hidden")
	}
}

func TestProcessingHTTPSupplierAddressChangeNeedsNewSecretAndRetryKeepsIdentity(t *testing.T) {
	ids := []string{}
	idReceipts := make(chan string, 2)
	app, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op == "processing_models" {
			return serviceipc.Response{OK: true, Result: records.ProcessingModelCatalog{Models: []records.ProcessingModel{}}}
		}
		if r.Op != "processing_credential_save" {
			return serviceipc.Response{Error: "unexpected op"}
		}
		d := r.ProcessingCredentialWrite
		ids = append(ids, d.Credential)
		idReceipts <- d.Credential
		if len(ids) == 1 {
			return serviceipc.Response{Error: "explicit lost receipt fixture"}
		}
		return serviceipc.Response{OK: true, Result: records.ProcessingCredentialReference{Credential: d.Credential, Revision: d.Revision, Supplier: &records.ProcessingSupplier{Provider: d.Provider, Endpoint: d.Endpoint, Protocol: d.Protocol, Credential: d.Credential, CredentialRevision: d.Revision, Revision: 2}}}
	})
	app.processing.Mode = "provider"
	app.processing.ensureHTTP()
	h := &app.processing.HTTP
	h.ProfilesLoaded = true
	h.Editing = "deepseek"
	h.Endpoint = "https://new.invalid/v1"
	h.Protocol = "openai-chat"
	h.Profiles["deepseek"] = records.ProcessingSpec{Mode: "provider", Provider: "deepseek", Endpoint: "https://old.invalid/v1", Protocol: "openai-chat", Credential: uuid.NewString(), CredentialRevision: 1}
	h.SupplierRevisions["deepseek"] = 1
	if app.canSaveProcessingSupplier() {
		t.Fatal("new address inherited old key")
	}
	h.Secret = "synthetic-new-key"
	app.saveProcessingSupplier()
	awaitProcessingDispatch(t, updates)
	if h.Secret == "" || h.Error == "" {
		t.Fatal("failed save lost masked input")
	}
	app.saveProcessingSupplier()
	awaitProcessingDispatch(t, updates)
	awaitProcessingDispatch(t, updates)
	firstID, secondID := <-idReceipts, <-idReceipts
	if firstID != secondID || h.Secret != "" {
		t.Fatal("retry changed immutable credential identity or exposed secret")
	}
}

func TestProcessingHTTPGatewayReceiptKeepsRequestedAndReportedModelsDistinct(t *testing.T) {
	a := New("test", nil)
	a.page, a.settingsSection = "设置", "processing"
	a.processing.Mode = "magpie"
	a.processing.ensureHTTP()
	a.processing.catalog("magpie").Loaded = true
	a.processing.catalog("magpie").Catalog.Endpoint = a.processing.HTTP.GatewayEndpoint
	spec := records.ProcessingSpec{Mode: "magpie", Endpoint: a.processing.HTTP.GatewayEndpoint, Protocol: "openai-chat", Model: "requested-id"}
	a.processing.Drafts = map[string]records.ProcessingSpec{"magpie": spec}
	a.processing.Configuration.Saved = map[string]records.ProcessingRevision{"magpie": {ID: uuid.NewString(), Spec: spec, Validation: &records.ProcessingValidation{State: "succeeded", HTTP: &records.ProcessingHTTPReceipt{RequestedModel: "requested-id", ReportedModel: "reported-id", GatewayProvider: "actual-provider", GatewayModel: "actual-gateway-id"}}}}
	tt := ui.NewTester(a.View, 1200, 820)
	for _, text := range []string{"请求模型", "requested-id", "返回模型", "reported-id", "网关报告供应商", "actual-provider", "网关报告模型", "actual-gateway-id"} {
		if !tt.HasText(text) {
			t.Fatal(fmt.Sprintf("missing real receipt field %s", text))
		}
	}
}

func TestProcessingHTTPSecretRemainsMaskedAndGatewayNeverRendersKeyField(t *testing.T) {
	a := New("test", nil)
	a.page, a.settingsSection = "设置", "processing"
	a.processing.Mode = "provider"
	a.processing.ensureHTTP()
	h := &a.processing.HTTP
	h.ProfilesLoaded = true
	h.View = "suppliers"
	h.Editing = "deepseek"
	h.Endpoint = "https://provider.invalid/v1"
	h.Protocol = "openai-chat"
	h.Secret = "synthetic-password-must-remain-masked"
	tt := ui.NewTester(a.View, 1200, 820)
	if tt.HasText(h.Secret) {
		t.Fatal("supplier password appeared in visible text")
	}
	if err := tt.Click("Magpie"); err != nil {
		t.Fatal(err)
	}
	if _, exists := tt.Find("供应商独立 API Key"); exists {
		t.Fatal("gateway rendered independent or upstream key field")
	}
	if tt.HasText(h.Secret) || a.processing.Configuration.Current.Active {
		t.Fatal("tab switch exposed key or activated a method")
	}
}
