package store

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func TestCogneeSubmissionExactVersionIndependentProvenance(t *testing.T) {
	s, _, b, g, k := graphContractFixture(t)
	ctx := context.Background()
	b.MaterialSubmission = true
	revision, err := s.SaveProcessing(ctx, records.ProcessingSave{OperationID: uuid.NewString(), Spec: processingProvider("submission-fixture")}, processingTime)
	mustMerge(t, err)
	dependency := records.MergeDigest("submission-fixture-dependency")
	processingVerifiedFixture(t, s, revision.ID, dependency)
	active, err := s.ActivateProcessing(ctx, records.ProcessingActivation{OperationID: uuid.NewString(), Revision: revision.ID}, dependency, processingTime)
	mustMerge(t, err)
	q := records.CogneeSubmissionRequest{Operation: uuid.NewString(), Project: b.Project, Dataset: b.Dataset, Sources: []records.MergeSource{{Memory: k.Record.Memory, Revision: 1}}, Processing: records.ProcessingTaskAuthorization{Revision: revision.ID, Generation: active.Generation}}
	materials, err := s.PrepareCogneeSubmission(ctx, b, q, dependency)
	mustMerge(t, err)
	if len(materials) != 1 || materials[0].Processing.DependencyDigest != dependency {
		t.Fatal("frozen processing missing", materials)
	}
	// New admission cannot reuse old imported document even though it exists.
	out, err := s.ProjectCogneeGraph(ctx, b, g)
	mustMerge(t, err)
	if len(out.Entities) != 0 {
		t.Fatal("new grant admitted old import")
	}
	m := materials[0]
	r := records.CogneeSubmissionReceipt{ProcessingRevisionDigest: m.Processing.Revision.Digest, ProcessingDependencyDigest: m.Processing.DependencyDigest, Library: m.Library, Operation: m.Operation, Instance: m.Instance, Dataset: m.Dataset, Project: m.Project, Memory: m.Memory, Revision: m.Revision, Digest: m.Digest, Document: uuid.NewString(), State: "processed", CogneeVersion: "1.6.7"}
	bad := r
	bad.Library = uuid.NewString()
	if s.CompleteCogneeSubmission(ctx, bad) == nil {
		t.Fatal("foreign library accepted")
	}
	bad = r
	bad.State = "pending"
	if s.CompleteCogneeSubmission(ctx, bad) == nil {
		t.Fatal("unprocessed accepted")
	}
	mustMerge(t, s.ClaimCogneeSubmission(ctx, q.Operation, m.Library))
	mustMerge(t, s.CompleteCogneeSubmission(ctx, r))
	mustMerge(t, s.CompleteCogneeSubmission(ctx, r))
	mustMerge(t, s.SealCogneeSubmissionExecutions(ctx, q.Operation, m.Library, []byte(`[{"success":true,"fixture":true}]`)))
	mustMerge(t, s.FinishCogneeSubmission(ctx, q.Operation, m.Library, "completed", ""))
	for i := range g.Mentions {
		g.Mentions[i].Document = r.Document
	}
	for i := range g.Relations {
		g.Relations[i].Sources[0].Document = r.Document
	}
	out, err = s.ProjectCogneeGraph(ctx, b, g)
	mustMerge(t, err)
	if len(out.Entities) != 2 || out.Mentions[0].Memory != m.Memory || out.Mentions[0].Revision != m.Revision {
		t.Fatal("exact submitted evidence not projected", out)
	}
	replay, err := s.PrepareCogneeSubmission(ctx, b, q, dependency)
	mustMerge(t, err)
	if replay[0].Library != m.Library || replay[0].Processing.FrozenMS != m.Processing.FrozenMS {
		t.Fatal("replay not frozen")
	}
	changed := q
	changed.Sources = nil
	if _, err = s.PrepareCogneeSubmission(ctx, b, changed, dependency); err == nil {
		t.Fatal("changed selection replay accepted")
	}
	// Original records keep their own identity/kind and may carry graph
	// evidence without becoming knowledge or rewriting their source chain.
	var rawID string
	mustMerge(t, s.DB.QueryRow(`SELECT m.id FROM memories m JOIN memory_metadata md ON md.memory=m.id WHERE m.dataset=? AND md.origin='raw' LIMIT 1`, b.Project).Scan(&rawID))
	rawVersion, err := s.Current(ctx, rawID)
	mustMerge(t, err)
	rawRequest := q
	rawRequest.Operation = uuid.NewString()
	rawRequest.Sources = []records.MergeSource{{Memory: rawID, Revision: rawVersion.Revision}}
	rawMaterials, err := s.PrepareCogneeSubmission(ctx, b, rawRequest, dependency)
	mustMerge(t, err)
	rawMaterial := rawMaterials[0]
	rawReceipt := r
	rawReceipt.Operation, rawReceipt.Memory, rawReceipt.Revision, rawReceipt.Digest, rawReceipt.Document = rawRequest.Operation, rawID, rawMaterial.Revision, rawMaterial.Digest, uuid.NewString()
	mustMerge(t, s.ClaimCogneeSubmission(ctx, rawRequest.Operation, rawMaterial.Library))
	mustMerge(t, s.CompleteCogneeSubmission(ctx, rawReceipt))
	mustMerge(t, s.SealCogneeSubmissionExecutions(ctx, rawRequest.Operation, rawMaterial.Library, []byte(`[{"success":true,"fixture":true}]`)))
	mustMerge(t, s.FinishCogneeSubmission(ctx, rawRequest.Operation, rawMaterial.Library, "completed", ""))
	rawGraph := g
	rawGraph.Mentions = append([]records.CogneeMention{}, g.Mentions...)
	rawGraph.Relations = nil
	for i := range rawGraph.Mentions {
		rawGraph.Mentions[i].Document = rawReceipt.Document
		rawGraph.Mentions[i].Quote = rawMaterial.Text
	}
	rawProjection, err := s.ProjectCogneeGraph(ctx, b, rawGraph)
	mustMerge(t, err)
	if len(rawProjection.Mentions) == 0 || rawProjection.Mentions[0].Origin != "raw" || rawProjection.Mentions[0].Memory != rawID {
		t.Fatal("original source mislabeled as knowledge", rawProjection)
	}
	afterRaw, err := s.Current(ctx, rawID)
	mustMerge(t, err)
	if !reflect.DeepEqual(rawVersion.SourceDetail, afterRaw.SourceDetail) || afterRaw.Origin != "raw" || afterRaw.Revision != rawVersion.Revision {
		t.Fatal("original source lineage changed")
	}
	_, err = s.Apply(ctx, records.Mutation{OperationID: uuid.NewString(), Kind: "edit", Dataset: b.Project, Memory: m.Memory, ExpectedRevision: 1, Text: m.Text + " changed"})
	mustMerge(t, err)
	replayTask, found, replayErr := s.ReplayCogneeSubmission(ctx, q)
	mustMerge(t, replayErr)
	if !found || replayTask.State != "completed" {
		t.Fatal("source edit erased old receipt", replayTask)
	}
	out, err = s.ProjectCogneeGraph(ctx, b, g)
	mustMerge(t, err)
	if len(out.Entities) != 0 {
		t.Fatal("old submitted version visible")
	}
}

func TestCogneeSubmissionRequiresExplicitProcessingAndSelection(t *testing.T) {
	s, _, b, _, k := graphContractFixture(t)
	b.MaterialSubmission = true
	q := records.CogneeSubmissionRequest{Operation: uuid.NewString(), Project: b.Project, Dataset: b.Dataset, Sources: []records.MergeSource{{Memory: k.Record.Memory, Revision: 1}}}
	if _, err := s.PrepareCogneeSubmission(context.Background(), b, q, "missing"); err == nil {
		t.Fatal("unconfigured processing accepted")
	}
	var count int
	mustMerge(t, s.DB.QueryRow(`SELECT count(*) FROM cognee_submissions`).Scan(&count))
	if count != 0 {
		t.Fatal("invalid submission partially saved")
	}
}

func TestCogneeSubmissionRestoreRetiresIdentityAndPendingOwner(t *testing.T) {
	s, _, b, _, k := graphContractFixture(t)
	ctx := context.Background()
	var identity string
	mustMerge(t, s.DB.QueryRow(`SELECT identity FROM cognee_library_identity WHERE id=1`).Scan(&identity))
	operation := uuid.NewString()
	_, err := s.DB.Exec(`INSERT INTO cognee_submission_tasks(operation,digest,processing,binding,request,state) VALUES(?,'fixture','{}','{}','{}','running')`, operation)
	mustMerge(t, err)
	_, err = s.DB.Exec(`INSERT INTO cognee_submissions(operation,memory,revision,library,instance,dataset,project,digest,payload,document,receipt) VALUES(?,?,1,?,?,?,?,?,'{}',?,'fixture')`, operation, k.Record.Memory, identity, b.Instance, b.Dataset, b.Project, "fixture", uuid.NewString())
	mustMerge(t, err)
	_, snapshot, err := s.snapshot(ctx)
	mustMerge(t, err)
	restored, _, err := PrepareRestoredSnapshot(ctx, snapshot, uuid.NewString())
	mustMerge(t, err)
	path := filepath.Join(t.TempDir(), "restored.sqlite")
	mustMerge(t, os.WriteFile(path, restored, 0600))
	copy, err := Open(path)
	mustMerge(t, err)
	defer copy.Close()
	var after string
	mustMerge(t, copy.DB.QueryRow(`SELECT identity FROM cognee_library_identity WHERE id=1`).Scan(&after))
	if identity == after {
		t.Fatal("restore reused library submission identity")
	}
	for _, table := range []string{"cognee_submissions", "cognee_submission_tasks"} {
		var count int
		mustMerge(t, copy.DB.QueryRow(`SELECT count(*) FROM `+table).Scan(&count))
		if count != 0 {
			t.Fatal("restore retained paid owner", table)
		}
	}
	v, err := copy.Current(ctx, k.Record.Memory)
	mustMerge(t, err)
	if v.Revision != 1 || v.Origin != "knowledge" {
		t.Fatal("restore changed content kind/version", v)
	}
	if copy.CompleteCogneeSubmission(ctx, records.CogneeSubmissionReceipt{Library: identity, Operation: operation, Memory: k.Record.Memory, State: "processed", CogneeVersion: "1.6.7", Document: uuid.NewString()}) == nil {
		t.Fatal("old owner callback survived restore")
	}
}

func TestCogneeProjectMaterialBindingReconnectionPreservesLegacyGate(t *testing.T) {
	s, _, legacy, _, _ := graphContractFixture(t)
	ctx := context.Background()
	converted := legacy
	converted.MaterialSubmission = true
	converted.Operation = uuid.NewString()
	if _, err := s.BindProjectCogneeMaterial(ctx, converted); err == nil {
		t.Fatal("legacy imported dataset silently converted")
	}
	material := converted
	material.Dataset = uuid.NewString()
	first, err := s.BindProjectCogneeMaterial(ctx, material)
	mustMerge(t, err)
	material.Operation = uuid.NewString()
	replay, err := s.BindProjectCogneeMaterial(ctx, material)
	mustMerge(t, err)
	if replay != first {
		t.Fatal("idempotent host authorization forked binding")
	}
	_, err = s.DB.Exec(`UPDATE connections SET generation=generation+1 WHERE id=?`, material.ConnectionID)
	mustMerge(t, err)
	material.Generation++
	renewed, err := s.BindProjectCogneeMaterial(ctx, material)
	mustMerge(t, err)
	if renewed.Generation != material.Generation || renewed.Dataset != first.Dataset {
		t.Fatal("reconnect lost private dataset", renewed)
	}
	if err = s.AuthorizeCogneeBinding(ctx, renewed); err == nil {
		t.Fatal("material authorization opened historical session import")
	}
	graph := records.CogneeGraph{Instance: renewed.Instance, Dataset: renewed.Dataset, CogneeVersion: "1.6.7"}
	projection, e := s.ProjectCogneeGraph(ctx, renewed, graph)
	mustMerge(t, e)
	if len(projection.Entities) != 0 {
		t.Fatal("new binding fabricated graph")
	}

	latest, err := s.LatestCogneeSubmission(ctx, material.Project)
	mustMerge(t, err)
	if latest.State != "none" || latest.Operation != "" {
		t.Fatal("new authorization fabricated extraction task", latest)
	}
}
