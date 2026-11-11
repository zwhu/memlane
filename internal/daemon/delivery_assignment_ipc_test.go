package daemon_test

import (
	"context"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agentbridge"
	"github.com/zwhu/memlane/internal/daemon"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestE05AssignmentActualSpoolPrivateIPCAndOriginalNamespaceGate(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	stop := startService(t, daemon.Config{Root: root})
	l := serviceipc.Library{Root: root}
	repo := filepath.Join(t.TempDir(), "repo")
	if out, e := exec.Command("git", "init", "-q", repo).CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	project, e := l.RegisterProject(ctx, records.ScopeDraft{Name: "Generated retained source project", Path: repo})
	requireService(t, e)
	c, e := l.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "pi", Name: "Generated empty-grant source", ConnectionOnly: true})
	requireService(t, e)
	stop()
	a := records.AgentArguments{OperationID: uuid.NewString(), Title: "Generated original source", Text: "Actual retained private spool body", Origin: "raw", Source: &records.AgentSource{Fragment: "Retained source fragment", References: []string{"synthetic original"}}}
	config := agentbridge.Config{Root: root, ConnectionID: c.ID, Generation: c.Generation, Workspace: project.Path, DurableDelivery: true}
	queued := config.Call(ctx, "memory_save", a)
	if queued.OK || queued.Delivery == nil || !queued.Delivery.LocalRetained || queued.Delivery.PersistedConfirmed {
		t.Fatal("offline retention was not a real unsaved spool item", queued)
	}
	stop = startService(t, daemon.Config{Root: root})
	defer stop()
	query := records.DeliveryAssignmentPreviewQuery{EntryID: queued.Delivery.ID, ReviewDigest: queued.Delivery.ReviewDigest}
	preview, e := l.DeliveryAssignmentPreview(ctx, query)
	requireService(t, e)
	if preview.Code != "choose_project" || preview.CanConfirm || preview.MatchingProject == nil || preview.MatchingProject.ID != project.ID || preview.Arguments.Text != a.Text {
		t.Fatal(preview)
	}
	query.Project = uuid.NewString()
	preview, e = l.DeliveryAssignmentPreview(ctx, query)
	requireService(t, e)
	if preview.Code != "workspace_mismatch" || preview.CanConfirm {
		t.Fatal(preview)
	}
	query.Project = project.ID
	preview, e = l.DeliveryAssignmentPreview(ctx, query)
	requireService(t, e)
	if !preview.CanConfirm || preview.Code != "ready" {
		t.Fatal(preview)
	}
	intent := records.DeliveryAssignmentIntent{OperationID: uuid.NewString(), LibraryID: preview.LibraryID, EntryID: preview.EntryID, RequestDigest: preview.RequestDigest, ReviewDigest: preview.ReviewDigest, Project: project.ID, WorkspaceDigest: preview.WorkspaceDigest}
	if e = serviceipc.Call(ctx, root, serviceipc.Request{Op: "delivery_assignment_confirm", DeliveryAssignmentIntent: &intent}, nil); e == nil {
		t.Fatal("missing explicit confirmation accepted")
	}
	var saved records.DeliveryAssignmentReceipt
	// Extra caller-supplied content cannot replace the source read from the spool.
	requireService(t, serviceipc.Call(ctx, root, serviceipc.Request{Op: "delivery_assignment_confirm", DeliveryAssignmentIntent: &intent, UserConfirmed: true, Mutation: records.Mutation{Text: "Forged caller body", Origin: "knowledge"}}, &saved))
	if saved.Source != "user_assignment" || saved.State != "saved" || saved.SpoolAckPending || saved.OriginalOperationID != a.OperationID {
		t.Fatal(saved)
	}
	v, e := l.Get(ctx, saved.Memory)
	requireService(t, e)
	if v.Text != a.Text || v.Origin != "raw" || v.Revision != 1 || !reflect.DeepEqual(v.SourceDetail, a.Source) {
		t.Fatal("caller replaced retained content", v)
	}
	connections, e := l.Connections(ctx)
	requireService(t, e)
	if len(connections) != 1 || len(connections[0].AllowedScopes) != 0 || connections[0].Generation != c.Generation {
		t.Fatal("user assignment granted Agent scope", connections)
	}
	retained, e := agentbridge.ReviewDelivery(root, intent.EntryID)
	requireService(t, e)
	if retained.Item.Status.PersistedConfirmed || retained.Item.Status.Memory != "" || retained.Item.OperationID != a.OperationID || retained.Item.Assignment == nil || retained.Item.Assignment.State != "saved" {
		t.Fatal("independent ack forged original Agent receipt", retained)
	}
	stop()
	stop = startService(t, daemon.Config{Root: root})
	same, e := l.ConfirmDeliveryAssignment(ctx, intent)
	requireService(t, e)
	if !reflect.DeepEqual(saved, same) {
		t.Fatal("restart changed SQL assignment receipt", same)
	}
	preview, e = l.DeliveryAssignmentPreview(ctx, query)
	requireService(t, e)
	if preview.Code != "assignment_saved" || preview.CanConfirm || preview.Assignment == nil || preview.Assignment.OperationID != intent.OperationID {
		t.Fatal(preview)
	}
	// Actual SQL receipt recovery precedes source reads; simulate a lost local ack store.
	requireService(t, os.Rename(filepath.Join(root, "agent-outbox"), filepath.Join(root, "agent-outbox-held")))
	same, e = l.ConfirmDeliveryAssignment(ctx, intent)
	requireService(t, e)
	if !same.SpoolAckPending || same.Memory != saved.Memory {
		t.Fatal("lost local ack hid a committed save", same)
	}
	requireService(t, os.RemoveAll(filepath.Join(root, "agent-outbox")))
	requireService(t, os.Rename(filepath.Join(root, "agent-outbox-held"), filepath.Join(root, "agent-outbox")))
	same, e = l.ConfirmDeliveryAssignment(ctx, intent)
	requireService(t, e)
	if same.SpoolAckPending || same.Memory != saved.Memory {
		t.Fatal("local ack did not recover", same)
	}
	// A later explicit grant is separate; it still cannot resend this old Agent op.
	updated, e := l.ChangeProjectAccess(ctx, records.ConnectionScopeChange{ConnectionID: c.ID, ExpectedGeneration: c.Generation, Project: project.ID, Allowed: true})
	requireService(t, e)
	request := records.AgentRequest{Protocol: 1, ConnectionID: updated.ID, Generation: updated.Generation, Workspace: project.Path, Tool: "memory_save", Arguments: a}
	if e = serviceipc.Call(ctx, root, serviceipc.Request{Op: "agent", AgentRequest: request}, nil); e == nil {
		t.Fatal("late legacy Agent operation duplicated assigned content")
	}
	var grant records.AgentDeliveryGrant
	requireService(t, serviceipc.Call(ctx, root, serviceipc.Request{Op: "agent_delivery_authorize", AgentRequest: request}, &grant))
	request.ExpectedLibraryID = grant.LibraryID
	request.DeliveryGrant = &grant
	if e = serviceipc.Call(ctx, root, serviceipc.Request{Op: "agent", AgentRequest: request}, nil); e == nil {
		t.Fatal("late D04 Agent operation duplicated assigned content")
	}
	page, e := l.List(ctx, records.ListQuery{Datasets: []string{project.ID}, Kind: "raw"})
	requireService(t, e)
	if len(page.Items) != 1 {
		t.Fatal("duplicate knowledge appeared", page)
	}
}
