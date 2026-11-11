package store

import (
	"context"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCapabilityEvidenceLedgerCAS(t *testing.T) {
	s, _ := mergeDB(t)
	scopes, err := s.Scopes(context.Background())
	mustMerge(t, err)
	c, err := s.ConfigureConnection(mergeCtx, records.ConnectionDraft{Agent: "pi", Name: "synthetic", AllowedScopes: []string{scopes[0].ID}})
	mustMerge(t, err)
	r, err := s.SaveProcessing(mergeCtx, records.ProcessingSave{OperationID: uuid.NewString(), Spec: records.ProcessingSpec{Mode: "agent", Agent: "pi", Provider: "deepseek", Model: "fixture", Connection: c.ID, ConnectionGeneration: c.Generation}}, processingTime)
	mustMerge(t, err)
	v, err := s.BeginProcessingValidation(mergeCtx, uuid.NewString(), r.ID, records.MergeDigest("deps"), processingTime)
	mustMerge(t, err)
	e := records.ProcessingCapabilityEvidence{ValidationID: v.ID, Revision: r.ID, Attempt: v.Attempt, DependencyDigest: v.DependencyDigest, Agent: "pi", ResponseComplete: true, StructureValid: true, IsolationConfigured: true, IsolationPolicy: "pi_private_no_session_no_tools_v1", Reaped: true, GroupGone: true}
	for _, field := range []string{"validation", "revision", "attempt", "dependency", "agent", "structure"} {
		bad := e
		switch field {
		case "validation":
			bad.ValidationID = uuid.NewString()
		case "revision":
			bad.Revision = uuid.NewString()
		case "attempt":
			bad.Attempt++
		case "dependency":
			bad.DependencyDigest = records.MergeDigest("other")
		case "agent":
			bad.Agent = "codex"
		case "structure":
			bad.StructureValid = false
		}
		if err = s.FinishAgentProcessingValidation(mergeCtx, v.ID, v.Attempt, "succeeded", records.MergeDigest("result"), "", time.Now(), records.ProcessingValidationDiagnostics{Stage: "postflight"}, &bad); err == nil {
			t.Fatal("accepted", field)
		}
	}
	var n int
	mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM processing_validation_evidence`).Scan(&n))
	if n != 0 {
		t.Fatal("partial write")
	}
	mustMerge(t, s.FinishAgentProcessingValidation(mergeCtx, v.ID, v.Attempt, "succeeded", records.MergeDigest("result"), "", time.Now(), records.ProcessingValidationDiagnostics{Stage: "postflight"}, &e))
	got, err := s.ProcessingValidation(mergeCtx, v.ID)
	mustMerge(t, err)
	if got.Evidence == nil || *got.Evidence != e {
		t.Fatal(got)
	}
	_, snapshot, err := s.snapshot(mergeCtx)
	mustMerge(t, err)
	prepared, _, err := PrepareRestoredSnapshot(mergeCtx, snapshot, uuid.NewString())
	mustMerge(t, err)
	path := filepath.Join(t.TempDir(), "restored.sqlite")
	mustMerge(t, os.WriteFile(path, prepared, 0600))
	restored, err := Open(path)
	mustMerge(t, err)
	defer restored.Close()
	historical, err := restored.ProcessingValidation(mergeCtx, v.ID)
	mustMerge(t, err)
	if historical.State != "superseded" || historical.Evidence == nil || *historical.Evidence != e {
		t.Fatal("restore lost history or retained authority", historical)
	}
	// Both agent and policy remain individually allowed; the real revision is pi.
	_, err = s.DB.Exec(`UPDATE processing_validation_evidence SET evidence=json_set(evidence,'$.agent','codex','$.isolation_policy','codex_private_ephemeral_no_tools_v1') WHERE validation=?`, v.ID)
	mustMerge(t, err)
	if _, err = s.ProcessingValidation(mergeCtx, v.ID); err == nil {
		t.Fatal("cross-Agent evidence read accepted")
	}

}

func TestCapabilityEvidenceMigration30CreatesNoHistoricalProof(t *testing.T) {
	s, path := mergeDB(t)
	_, err := s.DB.Exec(`DROP TABLE processing_validation_evidence;PRAGMA user_version=30`)
	mustMerge(t, err)
	mustMerge(t, s.Close())
	upgraded, err := Open(path)
	mustMerge(t, err)
	defer upgraded.Close()
	if upgraded.Schema != 31 {
		t.Fatal(upgraded.Schema)
	}
	var n int
	mustMerge(t, upgraded.DB.QueryRow(`SELECT count(*) FROM processing_validation_evidence`).Scan(&n))
	if n != 0 {
		t.Fatal("invented historical proof")
	}
}
