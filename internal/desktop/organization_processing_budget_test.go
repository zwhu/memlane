package desktop

import (
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"testing"
	"time"
)

func TestOrganizationProcessingReadAllowsMetadataProbeBudget(t *testing.T) {
	config := records.ProcessingConfiguration{Runtime: map[string]records.ProcessingReadiness{"fixed-pi": {State: "ready"}}, Current: records.ProcessingSelection{Active: true, Revision: &records.ProcessingRevision{ID: "fixed-pi", Spec: records.ProcessingSpec{Mode: "agent", Agent: "pi"}, Validation: &records.ProcessingValidation{State: "succeeded"}}}}
	a, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op != "processing_get" {
			t.Errorf("unexpected write/op %s", r.Op)
		}
		time.Sleep(5200 * time.Millisecond)
		return serviceipc.Response{OK: true, Result: config}
	})
	a.organization.Epoch = "same-library"
	a.organization.Project = "project"
	a.organization.ReviewMode = true
	a.organization.Chosen = map[string]MemoryItem{"manual": {ID: "manual", Revision: 5}, "duplicate": {ID: "duplicate", Revision: 1}}
	a.organization.Order = []string{"manual", "duplicate"}
	a.refreshOrganizationProcessing()
	select {
	case fn := <-updates:
		fn()
	case <-time.After(8 * time.Second):
		t.Fatal("read callback missing")
	}
	if !a.organization.ProcessingLoaded || a.organization.ProcessingError != "" || !organizationProcessingAvailable(a.organization.Processing) {
		t.Fatalf("slow valid processing ledger rejected: loaded=%t error=%s", a.organization.ProcessingLoaded, a.organization.ProcessingError)
	}
	if a.organization.Project != "project" || !a.organization.ReviewMode || len(a.organization.Chosen) != 2 || a.organization.Order[0] != "manual" || a.organization.Order[1] != "duplicate" {
		t.Fatal("read changed material draft")
	}
}
func TestOrganizationProcessingUnknownOrErrorNeverClaimsInactive(t *testing.T) {
	for _, state := range []string{"unknown", "failed", "failed-with-old-ledger", "loaded-empty"} {
		t.Run(state, func(t *testing.T) {
			a := New("test", nil)
			a.visualFixture = true
			a.organization.New = true
			a.organization.Project = "project"
			if state == "failed" || state == "failed-with-old-ledger" {
				a.organization.ProcessingError = "记忆处理方式读取未完成，保留本次材料；请重试。"
			}
			if state == "failed-with-old-ledger" {
				a.organization.ProcessingLoaded = true
				a.organization.Processing.Current = records.ProcessingSelection{Active: true, Revision: &records.ProcessingRevision{Spec: records.ProcessingSpec{Mode: "agent", Agent: "pi", Model: "old-ledger"}}}
			}
			if state == "loaded-empty" {
				a.organization.ProcessingLoaded = true
			}
			tt := ui.NewTester(func(c *ui.Context) { a.organizationChooser(c) }, 800, 900)
			if tt.HasText("尚未启用记忆处理方式。") != (state == "loaded-empty") {
				t.Fatal("unknown/read failure presented as inactive")
			}
			if (state == "failed" || state == "failed-with-old-ledger") && !tt.HasText(a.organization.ProcessingError) {
				t.Fatal("read failure hidden")
			}
		})
	}
}

func TestOrganizationProcessingReadSequenceRejectsLateReply(t *testing.T) {
	a, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		return serviceipc.Response{OK: true, Result: records.ProcessingConfiguration{Current: records.ProcessingSelection{Active: true}}}
	})
	a.organization.Epoch = "same-library"
	a.refreshOrganizationProcessing()
	a.organization.ProcessingSequence++
	a.organization.ProcessingError = "new owner"
	a.organization.ProcessingLoading = false
	awaitProcessingDispatch(t, updates)
	if a.organization.ProcessingLoaded || a.organization.Processing.Current.Active || a.organization.ProcessingError != "new owner" {
		t.Fatal("late sequence response replaced current owner")
	}
}
