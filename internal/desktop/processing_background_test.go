package desktop

import (
	"testing"
	"time"

	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestProcessingBackgroundObservationKeepsActionsUsableAndRejectsLateState(t *testing.T) {
	spec := records.ProcessingSpec{Mode: "agent", Agent: "pi", Connection: "connection", ConnectionGeneration: 3, Provider: "deepseek", Model: "deepseek-flash"}
	configuration := records.ProcessingConfiguration{Saved: map[string]records.ProcessingRevision{"agent": {ID: "saved", Spec: spec}}}
	a, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		switch r.Op {
		case "processing_get":
			return serviceipc.Response{OK: true, Result: configuration}
		case "connections":
			return serviceipc.Response{OK: true, Result: []records.Connection{}}
		default:
			return serviceipc.Response{Error: "unexpected " + r.Op}
		}
	})
	a.processing = ProcessingUI{Mode: "agent", Loaded: true, Configuration: configuration, Drafts: map[string]records.ProcessingSpec{"agent": spec}, CheckedAt: time.Now().Add(-time.Minute)}
	if !a.processing.CanValidate() {
		t.Fatal("saved candidate initially unavailable")
	}
	a.pollProcessing()
	if !a.processing.Refreshing || a.processing.Loading || !a.processing.CanValidate() || a.processingModeState() == "正在读取真实配置" {
		t.Fatal("background observation blocked the loaded form")
	}
	// A user operation advances the same CAS generation while observation is
	// in flight. The old reply must release its spinner, not replace new state.
	a.processing.Sequence++
	a.processing.Configuration.Saved = map[string]records.ProcessingRevision{"agent": {ID: "newer", Spec: spec}}
	awaitProcessingDispatch(t, updates)
	if a.processing.Refreshing || a.processing.Loading || a.processing.Configuration.Saved["agent"].ID != "newer" {
		t.Fatal("late observation replaced user state or left refresh locked")
	}
	a.processing.CheckedAt = time.Now()
	sequence := a.processing.Sequence
	a.pollProcessing()
	if a.processing.Sequence != sequence || a.processing.Refreshing {
		t.Fatal("routine tick immediately repeated an expensive probe")
	}
}
