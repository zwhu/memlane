package supervisor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLegacyOwnerReceiptRoundTrip(t *testing.T) {
	store := t.TempDir()
	record := OwnerRecord{
		PID: 1234, Started: "example start", Phase: "ready",
		Config: filepath.Join(store, "runtime.json"), OfficialData: store,
		ServiceURL: "http://127.0.0.1:4001", CompletionURL: "http://127.0.0.1:4002",
		ProjectControlURL: "http://127.0.0.1:4003", ResourcesDir: "/example/resources",
		ResourcesHash: "hash", RuntimeConfig: "/example/runtime.json", GenerationMode: "mode",
		GenerationReference: &GenerationReference{ID: "profile", Revision: 3},
		Children:            []int{1235}, ServiceChildren: map[string]int{"worker": 1235},
		ChildStartTimes:     map[string]string{"1235": "child start"},
		ObservedDescendants: map[string]string{"1236": "descendant start"},
	}

	lock, err := AcquireOwner(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release() })
	if err := lock.WriteRecord(record); err != nil {
		t.Fatal(err)
	}
	receipt := lock.ReceiptPath()
	if filepath.Base(receipt) != ".memlane-dev-owner.json" {
		t.Fatalf("unexpected receipt path: %s", receipt)
	}
	raw, err := os.ReadFile(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{
		"pid", "started", "phase", "config", "official_data", "service_url",
		"completion_url", "project_control_url", "resources_dir", "resources_hash",
		"runtime_config", "generation_mode", "generation_reference", "children",
		"service_children", "child_start_times", "observed_descendants",
	}
	if len(data) != len(wantKeys) {
		t.Fatalf("unexpected receipt schema: %s", raw)
	}
	for _, key := range wantKeys {
		if _, ok := data[key]; !ok {
			t.Errorf("receipt missing %q", key)
		}
	}
	var reference GenerationReference
	if err := json.Unmarshal(data["generation_reference"], &reference); err != nil || reference != *record.GenerationReference {
		t.Fatalf("generation reference = %+v, %v", reference, err)
	}
	read, _, err := ReadOwnerRecord(store)
	if err != nil || !reflect.DeepEqual(record, read) {
		t.Fatalf("receipt round trip = %+v, %v", read, err)
	}
}
