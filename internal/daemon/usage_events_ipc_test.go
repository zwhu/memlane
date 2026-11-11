package daemon_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/daemon"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"github.com/zwhu/memlane/internal/store"
)

func TestUsageEventsPrivateIPCReadsRealMetadataAndPreservesFailures(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	stop := startService(t, daemon.Config{Root: root})
	defer stop()
	library := serviceipc.Library{Root: root}
	scopes, err := library.Scopes(ctx)
	requireService(t, err)
	version, err := library.Apply(ctx, records.Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: uuid.NewString(), Dataset: scopes[0].ID, Text: "Generated private IPC metadata test"})
	requireService(t, err)
	query := records.UsageEventsQuery{Memory: version.Memory, Dataset: version.Dataset, ExpectedRevision: version.Revision, Limit: 20}
	page, err := library.UsageEvents(ctx, query)
	requireService(t, err)
	if page.Events == nil || len(page.Events) != 0 || page.Channel != "daily" {
		t.Fatal("default channel fabricated an event", page)
	}
	o := records.UsageObservation{OperationID: uuid.NewString(), Digest: store.UsageDigest("generated browse action"), Channel: "app", Kind: "browse", Targets: []records.UsageTarget{{Memory: version.Memory, Dataset: version.Dataset, Revision: version.Revision}}}
	var receipt records.UsageReceipt
	requireService(t, serviceipc.Call(ctx, root, serviceipc.Request{Op: "usage_browse", UsageObservation: &o}, &receipt))
	query.Channel = "app"
	page, err = library.UsageEvents(ctx, query)
	requireService(t, err)
	if len(page.Events) != 1 || page.Events[0].ID != receipt.ID || page.Events[0].ObservedMS != receipt.ObservedMS || page.Events[0].Revision != version.Revision || page.Events[0].Dataset != version.Dataset || page.Events[0].Kind != "browse" {
		t.Fatal("real receipt provenance was not returned", page)
	}
	query.Channel = "verification"
	page, err = library.UsageEvents(ctx, query)
	requireService(t, err)
	if len(page.Events) != 0 {
		t.Fatal("App browse entered verification history")
	}
	if err = serviceipc.Call(ctx, root, serviceipc.Request{Op: "usage_events"}, nil); err == nil {
		t.Fatal("missing exact target returned fake empty page")
	}
	query.Limit = 51
	if _, err = library.UsageEvents(ctx, query); err == nil {
		t.Fatal("unbounded event page accepted")
	}
	query.Limit = 20
	query.ExpectedRevision++
	if _, err = library.UsageEvents(ctx, query); err == nil {
		t.Fatal("stale detail target returned another version")
	}
}
