package bootstrap

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zwhu/memlane/internal/supervisor"
)

func TestActivateFirstRuntimeReceiptRecovery(t *testing.T) {
	for _, tc := range []struct {
		name, status, phase string
		matching, supersede bool
	}{
		{name: "pre-mutation rejection", status: "rejected", phase: "pre_mutation", supersede: true},
		{name: "check matching pre-mutation rejection", status: "rejected", phase: "pre_mutation", matching: true},
		{name: "legacy rejection", status: "rejected"},
		{name: "post-mutation rejection", status: "rejected", phase: "post_mutation"},
		{name: "approved mismatch", status: "approved"},
		{name: "stopping mismatch", status: "stopping"},
		{name: "starting mismatch", status: "starting"},
		{name: "unknown result mismatch", status: "result_unknown"},
		{name: "unknown result match", status: "result_unknown", matching: true},
		{name: "approved retry", status: "approved", matching: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prepared := newReadOnlyRuntimeFixture(t)
			prepared.Resources = filepath.Join(prepared.HostHome, "installation", "runtime")
			config, err := loadFirstRuntimeConfig(prepared.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(prepared.StoreRoot, "first-install-activation.json")
			existing := FirstActivationRequest{
				Schema: 1, StoreID: config.First.StoreID, Operation: "first-original",
				Reference: FirstGenerationReference{ID: "profile", Revision: 1},
				Digest:    strings.Repeat("a", 64), Revision: 1, Status: tc.status, Phase: tc.phase,
			}
			writeBootstrapTestJSON(t, path, existing)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			// Continue through the real receipt path without launching a runtime.
			lock, err := supervisor.AcquireUpgradeLock(prepared.StoreRoot)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Release()
			reference, digest, revision := existing.Reference, existing.Digest, existing.Revision
			if !tc.matching {
				reference.Revision++
				digest = strings.Repeat("b", 64)
				revision++
			}
			err = ActivateFirstRuntime(t.Context(), prepared, reference, digest, revision)
			if tc.supersede || (tc.matching && tc.status == "approved") {
				if !errors.Is(err, supervisor.ErrUpgradeLocked) {
					t.Fatalf("activation did not proceed to startup: %v", err)
				}
			} else if tc.matching && tc.phase == "pre_mutation" {
				if !errors.Is(err, ErrFirstActivationRejected) {
					t.Fatalf("matching rejection could not be reconciled: %v", err)
				}
				if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("acknowledged pre_mutation rejection should be cleared for same-revision retry, got stat err=%v", statErr)
				}
				// Retrying with the exact same (reference, digest, revision) must now create a fresh operation.
				retryErr := ActivateFirstRuntime(t.Context(), prepared, reference, digest, revision)
				if !errors.Is(retryErr, supervisor.ErrUpgradeLocked) {
					t.Fatalf("same-revision retry after pre_mutation rejection did not proceed: %v", retryErr)
				}
				return
			} else if err == nil || err.Error() != "原启用操作尚待核对，未创建另一项操作" {
				t.Fatalf("activation did not refuse the retained receipt: %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.supersede {
				if !bytes.Equal(before, after) {
					t.Fatal("activation changed the retained receipt")
				}
				return
			}
			var got FirstActivationRequest
			if err := json.Unmarshal(after, &got); err != nil {
				t.Fatal(err)
			}
			suffix, ok := strings.CutPrefix(got.Operation, "first-")
			id, err := hex.DecodeString(suffix)
			if !ok || err != nil || len(id) != 16 || got.Operation == existing.Operation {
				t.Fatalf("invalid replacement operation ID: %q", got.Operation)
			}
			want := FirstActivationRequest{
				Schema: 1, StoreID: existing.StoreID, Operation: got.Operation,
				Reference: reference, Digest: digest, Revision: revision, Status: "approved",
			}
			if got != want {
				t.Fatalf("replacement receipt = %+v, want %+v", got, want)
			}
		})
	}
}
