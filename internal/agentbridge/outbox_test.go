package agentbridge

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func deliveryFixture(t *testing.T) (Config, records.AgentArguments) {
	t.Helper()
	root := t.TempDir()
	work := t.TempDir()
	canonical, e := filepath.EvalSymlinks(work)
	if e != nil {
		t.Fatal(e)
	}
	return Config{Root: root, Workspace: canonical, ConnectionID: uuid.NewString(), Generation: 1, DurableDelivery: true}, records.AgentArguments{OperationID: "synthetic-original-operation", Text: "synthetic offline exact content", Title: "synthetic", Origin: "knowledge"}
}
func TestD04UnverifiedSurvivesRestartNeverSends(t *testing.T) {
	c, a := deliveryFixture(t)
	out := c.Call(context.Background(), "memory_save", a)
	if out.OK || out.Delivery == nil || out.Delivery.State != "pending_unverified" || out.Delivery.PersistedConfirmed {
		t.Fatalf("%+v", out)
	}
	d, e := openDelivery(c.Root)
	if e != nil {
		t.Fatal(e)
	}
	entry := d.find(out.Delivery.ID)
	if entry.Payload.Arguments.Text != a.Text || entry.Payload.Arguments.OperationID != a.OperationID {
		t.Fatal(entry)
	}
	d.close()
	retry := RetryDelivery(context.Background(), c.Root, out.Delivery.ID, out.Delivery.ReviewDigest, true)
	if retry.OK || retry.Error.Code != "delivery_unverified" {
		t.Fatal(retry)
	}
	a.Text = "different"
	conflict := c.Call(context.Background(), "memory_save", a)
	if conflict.Error.Code != "delivery_operation_conflict" {
		t.Fatal(conflict)
	}
}
func startDeliveryIPC(t *testing.T, c Config, handler func(serviceipc.Request) (any, string, bool)) {
	t.Helper()
	path := serviceipc.SocketPath(c.Root)
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	l, e := net.Listen("unix", path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { l.Close(); os.RemoveAll(filepath.Dir(path)) })
	go func() {
		for {
			conn, e := l.Accept()
			if e != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				var r serviceipc.Request
				if json.NewDecoder(conn).Decode(&r) != nil {
					return
				}
				value, code, drop := handler(r)
				if g, ok := value.(records.AgentDeliveryGrant); ok && g.WorkspacePhysical == "" {
					g.WorkspacePhysical, _ = workspacePhysical(c.Workspace)
					value = g
				}
				if drop {
					return
				}
				_ = json.NewEncoder(conn).Encode(map[string]any{"protocol": 1, "id": r.ID, "ok": code == "", "result": value, "code": code, "error": "synthetic safe failure"})
			}(conn)
		}
	}()
}
func TestD04LostReceiptExplicitRetryKeepsOperationAndGrant(t *testing.T) {
	c, a := deliveryFixture(t)
	var sends atomic.Int32
	g := records.AgentDeliveryGrant{LibraryID: uuid.NewString(), ConnectionID: c.ConnectionID, Generation: 1, Scope: uuid.NewString(), Workspace: c.Workspace, WorkspaceDigest: "server-observed"}
	memory := uuid.NewSHA1(uuid.NameSpaceOID, []byte(c.ConnectionID+"\x00"+a.OperationID)).String()
	startDeliveryIPC(t, c, func(r serviceipc.Request) (any, string, bool) {
		if r.Op == "agent_delivery_authorize" {
			return g, "", false
		}
		if r.AgentRequest.ExpectedLibraryID != g.LibraryID || r.AgentRequest.DeliveryGrant == nil || r.AgentRequest.Arguments.OperationID != a.OperationID || r.AgentRequest.Arguments.Text != a.Text {
			t.Error("original boundary changed")
		}
		if sends.Add(1) == 1 {
			return nil, "", true
		}
		return records.SaveReceipt{Persisted: true, SavedRevision: 1, Record: records.Version{Memory: memory, Dataset: g.Scope, Revision: 2}}, "", false
	})
	first := c.Call(context.Background(), "memory_save", a)
	if first.OK || first.Delivery == nil || first.Delivery.State != "waiting" {
		t.Fatalf("%+v", first)
	}
	repeat := c.Call(context.Background(), "memory_save", a)
	if repeat.OK || sends.Load() != 1 {
		t.Fatal("ordinary repeated call auto-replayed pending")
	}
	bad := RetryDelivery(context.Background(), c.Root, first.Delivery.ID, "wrong", true)
	if bad.Error.Code != "delivery_review_required" || sends.Load() != 1 {
		t.Fatal(bad)
	}
	out := RetryDelivery(context.Background(), c.Root, first.Delivery.ID, first.Delivery.ReviewDigest, true)
	if !out.OK || out.Delivery.SavedRevision != 1 || !out.Delivery.PersistedConfirmed || sends.Load() != 2 {
		t.Fatalf("%+v sends=%d", out, sends.Load())
	}
	again := RetryDelivery(context.Background(), c.Root, first.Delivery.ID, first.Delivery.ReviewDigest, true)
	if again.Error.Code != "delivery_already_confirmed" || sends.Load() != 2 {
		t.Fatal(again)
	}
}
func TestD04RevokedPendingBlockedAndNoRebind(t *testing.T) {
	c, a := deliveryFixture(t)
	g := records.AgentDeliveryGrant{LibraryID: "original-library", ConnectionID: c.ConnectionID, Generation: 1, Scope: "original-scope", Workspace: c.Workspace, WorkspaceDigest: "observed"}
	d, e := openDelivery(c.Root)
	if e != nil {
		t.Fatal(e)
	}
	entry, e := d.capture(deliveryPayload{Root: c.Root, Connection: c.ConnectionID, Generation: 1, Workspace: c.Workspace, Arguments: a, Grant: &g})
	if e != nil {
		t.Fatal(e)
	}
	status := deliveryStatus(entry)
	d.close()
	var sends atomic.Int32
	startDeliveryIPC(t, c, func(r serviceipc.Request) (any, string, bool) {
		sends.Add(1)
		return nil, "delivery_library_changed", false
	})
	first := RetryDelivery(context.Background(), c.Root, status.ID, status.ReviewDigest, true)
	if first.Delivery.State != "blocked" || first.Error.Code != "delivery_library_changed" {
		t.Fatal(first)
	}
	RetryDelivery(context.Background(), c.Root, status.ID, status.ReviewDigest, true)
	if sends.Load() != 1 {
		t.Fatal("blocked operation was replayed")
	}
	d, e = openDelivery(c.Root)
	if e != nil {
		t.Fatal(e)
	}
	defer d.close()
	if d.find(status.ID).Payload.Grant.LibraryID != "original-library" {
		t.Fatal("rebound epoch")
	}
}
func TestD04PrivateStorageCorruptionAndCapacityFailClosed(t *testing.T) {
	c, a := deliveryFixture(t)
	d, e := openDelivery(c.Root)
	if e != nil {
		t.Fatal(e)
	}
	if other, e := openDelivery(c.Root); e == nil {
		other.close()
		t.Fatal("lock not exclusive")
	}
	for i := 0; i < deliveryLimit; i++ {
		a.OperationID = uuid.NewString()
		if _, e = d.capture(deliveryPayload{Root: c.Root, Connection: c.ConnectionID, Generation: 1, Workspace: c.Workspace, Arguments: a}); e != nil {
			t.Fatal(e)
		}
	}
	a.OperationID = "overflow"
	if _, e = d.capture(deliveryPayload{Root: c.Root, Connection: c.ConnectionID, Generation: 1, Workspace: c.Workspace, Arguments: a}); e == nil {
		t.Fatal("silently discarded pending")
	}
	d.close()
	file := filepath.Join(c.Root, "agent-outbox", "ledger.json")
	if e = os.WriteFile(file, []byte("corrupt"), 0600); e != nil {
		t.Fatal(e)
	}
	if d, e = openDelivery(c.Root); e == nil {
		d.close()
		t.Fatal("corrupt ledger accepted")
	}
	os.Remove(file)
	if e = os.Symlink(filepath.Join(t.TempDir(), "other"), file); e != nil {
		t.Fatal(e)
	}
	if d, e = openDelivery(c.Root); e == nil {
		d.close()
		t.Fatal("symlink accepted")
	}
}
func TestD04CrashedSendingRetainsOriginalAndNoAutomaticStartup(t *testing.T) {
	c, a := deliveryFixture(t)
	g := records.AgentDeliveryGrant{LibraryID: "original-epoch", ConnectionID: c.ConnectionID, Generation: 1, Scope: uuid.NewString(), Workspace: c.Workspace, WorkspaceDigest: "observed"}
	d, e := openDelivery(c.Root)
	if e != nil {
		t.Fatal(e)
	}
	entry, e := d.capture(deliveryPayload{Root: c.Root, Connection: c.ConnectionID, Generation: 1, Workspace: c.Workspace, Arguments: a, Grant: &g})
	if e != nil {
		t.Fatal(e)
	}
	entry.State = "sending"
	if e = d.save(); e != nil {
		t.Fatal(e)
	}
	status := deliveryStatus(entry)
	d.close()
	var sends atomic.Int32
	startDeliveryIPC(t, c, func(r serviceipc.Request) (any, string, bool) {
		sends.Add(1)
		memory := uuid.NewSHA1(uuid.NameSpaceOID, []byte(c.ConnectionID+"\x00"+a.OperationID)).String()
		return records.SaveReceipt{Persisted: true, SavedRevision: 1, Record: records.Version{Memory: memory, Dataset: g.Scope, Revision: 1}}, "", false
	})
	d, e = openDelivery(c.Root)
	if e != nil {
		t.Fatal(e)
	}
	if d.find(status.ID).State != "sending" || sends.Load() != 0 {
		t.Fatal("opening outbox sent data")
	}
	d.close()
	out := RetryDelivery(context.Background(), c.Root, status.ID, status.ReviewDigest, true)
	if !out.OK || sends.Load() != 1 {
		t.Fatal(out)
	}
}
func TestD04OnlyOrdinaryDailySavesMayRetain(t *testing.T) {
	for _, mode := range []string{"connection", "initialization", "processing", "organization"} {
		c, a := deliveryFixture(t)
		c.ToolMode = mode
		c.Call(context.Background(), "memory_save", a)
		if _, e := os.Stat(filepath.Join(c.Root, "agent-outbox")); !os.IsNotExist(e) {
			t.Fatalf("mode %s created outbox", mode)
		}
	}
	for _, channel := range []string{"verification", "background", "app", "unknown"} {
		c, a := deliveryFixture(t)
		c.Channel = channel
		c.Call(context.Background(), "memory_save", a)
		if _, e := os.Stat(filepath.Join(c.Root, "agent-outbox")); !os.IsNotExist(e) {
			t.Fatalf("channel %s created outbox", channel)
		}
	}
}
func TestD04CaptureOwnsExactSourceAndGrantSnapshot(t *testing.T) {
	c, a := deliveryFixture(t)
	a.Source = &records.AgentSource{Fragment: "synthetic original fragment", References: []string{"synthetic original reference"}}
	g := records.AgentDeliveryGrant{LibraryID: "original-library", Scope: "original-scope"}
	d, e := openDelivery(c.Root)
	if e != nil {
		t.Fatal(e)
	}
	entry, e := d.capture(deliveryPayload{Root: c.Root, Connection: c.ConnectionID, Generation: 1, Workspace: c.Workspace, Arguments: a, Grant: &g})
	if e != nil {
		t.Fatal(e)
	}
	id := entry.ID
	a.Source.Fragment = "changed by caller"
	a.Source.References[0] = "changed reference"
	g.LibraryID = "later library"
	if e = d.save(); e != nil {
		t.Fatal(e)
	}
	d.close()
	review, e := ReviewDelivery(c.Root, id)
	if e != nil || review.Arguments.Source.Fragment != "synthetic original fragment" || review.Arguments.Source.References[0] != "synthetic original reference" || review.Item.LibraryID != "original-library" {
		t.Fatal("immutable local original changed", review, e)
	}
}
func TestD04FirstIPCAlreadyHasDurableOriginalAndNoBodyInAuthorization(t *testing.T) {
	c, a := deliveryFixture(t)
	startDeliveryIPC(t, c, func(r serviceipc.Request) (any, string, bool) {
		raw, e := os.ReadFile(filepath.Join(c.Root, "agent-outbox", "ledger.json"))
		if e != nil || !strings.Contains(string(raw), a.Text) {
			t.Error("first IPC preceded original durable capture", e)
		}
		if r.Op != "agent_delivery_authorize" || r.AgentRequest.Arguments.Text != "" || r.AgentRequest.Arguments.OperationID != "" {
			t.Error("authorization transported content or attempted write")
		}
		return nil, "scope_denied", false
	})
	out := c.Call(context.Background(), "memory_save", a)
	if out.Delivery == nil || out.Delivery.State != "blocked" || out.Delivery.PersistedConfirmed || out.Error.Code != "scope_denied" {
		t.Fatal(out)
	}
	review, e := ReviewDelivery(c.Root, out.Delivery.ID)
	if e != nil || review.Arguments.Text != a.Text || review.Item.HasGrant {
		t.Fatal(review, e)
	}
}
func TestD04InitialBindingChangesReviewNotOriginalRequestAndRejectsOldConsent(t *testing.T) {
	c, a := deliveryFixture(t)
	var original, unboundReview string
	var saves atomic.Int32
	g := records.AgentDeliveryGrant{Agent: "pi", LibraryID: "live-library", ConnectionID: c.ConnectionID, Generation: 1, Scope: uuid.NewString(), Workspace: c.Workspace, WorkspaceDigest: "observed"}
	startDeliveryIPC(t, c, func(r serviceipc.Request) (any, string, bool) {
		if r.Op == "agent_delivery_authorize" {
			raw, e := os.ReadFile(filepath.Join(c.Root, "agent-outbox", "ledger.json"))
			if e != nil {
				t.Error(e)
			}
			var ledger deliveryLedger
			if json.Unmarshal(raw, &ledger) != nil || len(ledger.Entries) != 1 {
				t.Fatal("missing initial capture")
			}
			original = ledger.Entries[0].RequestDigest
			unboundReview = ledger.Entries[0].Digest
			if ledger.Entries[0].Payload.Grant != nil {
				t.Error("grant invented before first observation")
			}
			return g, "", false
		}
		saves.Add(1)
		return nil, "", true
	})
	out := c.Call(context.Background(), "memory_save", a)
	if out.Delivery == nil || out.Delivery.RequestDigest != original || out.Delivery.ReviewDigest == unboundReview || out.Delivery.PersistedConfirmed {
		t.Fatal(out)
	}
	stale := RetryDelivery(context.Background(), c.Root, out.Delivery.ID, unboundReview, true)
	if stale.Error.Code != "delivery_review_required" || saves.Load() != 1 {
		t.Fatal("stale consent survived first authority annotation", stale)
	}
}
func TestD04LostInitialAuthorizationNeverBindsOnRestartOrDuplicate(t *testing.T) {
	c, a := deliveryFixture(t)
	var calls atomic.Int32
	startDeliveryIPC(t, c, func(r serviceipc.Request) (any, string, bool) { calls.Add(1); return nil, "", true })
	first := c.Call(context.Background(), "memory_save", a)
	if first.Delivery == nil || first.Delivery.State != "pending_unverified" {
		t.Fatal(first)
	}
	c.Call(context.Background(), "memory_save", a)
	retry := RetryDelivery(context.Background(), c.Root, first.Delivery.ID, first.Delivery.ReviewDigest, true)
	if calls.Load() != 1 || retry.Error.Code != "delivery_unverified" {
		t.Fatal("lost authorization was reacquired for an existing operation", retry, calls.Load())
	}
	review, e := ReviewDelivery(c.Root, first.Delivery.ID)
	if e != nil || review.Item.HasGrant {
		t.Fatal(review, e)
	}
}
func TestD04CancelledInitialAuthorityCannotBindOrSend(t *testing.T) {
	c, a := deliveryFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var saves atomic.Int32
	startDeliveryIPC(t, c, func(r serviceipc.Request) (any, string, bool) {
		if r.Op != "agent_delivery_authorize" {
			saves.Add(1)
		}
		cancel()
		return records.AgentDeliveryGrant{LibraryID: "live-library", ConnectionID: c.ConnectionID, Generation: 1, Scope: uuid.NewString(), Workspace: c.Workspace, WorkspaceDigest: "observed"}, "", false
	})
	out := c.Call(ctx, "memory_save", a)
	if out.Delivery == nil || out.Delivery.State != "pending_unverified" || saves.Load() != 0 {
		t.Fatal("cancelled owner sent after late authority", out)
	}
	review, e := ReviewDelivery(c.Root, out.Delivery.ID)
	if e != nil || review.Item.HasGrant {
		t.Fatal("late grant was bound", review, e)
	}
}
func TestD04KilledHelperDuringInitialAuthorizationRecoversOnlyUnverified(t *testing.T) {
	if root := os.Getenv("MEMLANE_D04_CRASH_ROOT"); root != "" {
		c := Config{Root: root, Workspace: os.Getenv("MEMLANE_D04_CRASH_WORK"), ConnectionID: os.Getenv("MEMLANE_D04_CRASH_CONNECTION"), Generation: 1, DurableDelivery: true}
		c.Call(context.Background(), "memory_save", records.AgentArguments{OperationID: "crash-original", Text: "synthetic crash-retained original"})
		os.Exit(7)
	}
	c, _ := deliveryFixture(t)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	defer close(release)
	var writes atomic.Int32
	startDeliveryIPC(t, c, func(r serviceipc.Request) (any, string, bool) {
		if r.Op == "agent_delivery_authorize" {
			started <- struct{}{}
			<-release
		} else {
			writes.Add(1)
		}
		return nil, "", true
	})
	child := exec.Command(os.Args[0], "-test.run=^TestD04KilledHelperDuringInitialAuthorizationRecoversOnlyUnverified$")
	child.Env = []string{"PATH=/usr/bin:/bin", "MEMLANE_D04_CRASH_ROOT=" + c.Root, "MEMLANE_D04_CRASH_WORK=" + c.Workspace, "MEMLANE_D04_CRASH_CONNECTION=" + c.ConnectionID}
	if e := child.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if child.ProcessState == nil {
			child.Process.Kill()
			child.Wait()
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("synthetic child never reached authorization")
	}
	if e := child.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	if e := child.Wait(); e == nil {
		t.Fatal("child did not crash")
	}
	page, e := ListDeliveries(c.Root)
	if e != nil || len(page.Items) != 1 || page.Items[0].HasGrant || page.Items[0].Status.State != "pending_unverified" {
		t.Fatal(page, e)
	}
	item := page.Items[0]
	review, e := ReviewDelivery(c.Root, item.Status.ID)
	if e != nil || review.Arguments.Text != "synthetic crash-retained original" || review.Arguments.OperationID != "crash-original" {
		t.Fatal(review, e)
	}
	out := RetryDelivery(context.Background(), c.Root, item.Status.ID, item.Status.ReviewDigest, true)
	if out.Error.Code != "delivery_unverified" || writes.Load() != 0 {
		t.Fatal("crashed first call was rebound/replayed", out)
	}
}
func TestD04NetworkWaitDoesNotBlockOtherCaptureOrRead(t *testing.T) {
	c, a := deliveryFixture(t)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	defer close(release)
	g := records.AgentDeliveryGrant{Agent: "pi", LibraryID: "original-library", ConnectionID: c.ConnectionID, Generation: 1, Scope: uuid.NewString(), Workspace: c.Workspace, WorkspaceDigest: "daemon-derived"}
	startDeliveryIPC(t, c, func(r serviceipc.Request) (any, string, bool) {
		if r.Op == "agent_delivery_authorize" {
			return g, "", false
		}
		if r.AgentRequest.Arguments.OperationID == a.OperationID {
			started <- struct{}{}
			<-release
		}
		memory := uuid.NewSHA1(uuid.NameSpaceOID, []byte(c.ConnectionID+"\x00"+r.AgentRequest.Arguments.OperationID)).String()
		return records.SaveReceipt{Persisted: true, SavedRevision: 1, Record: records.Version{Memory: memory, Dataset: g.Scope, Revision: 1}}, "", false
	})
	first := make(chan Result, 1)
	go func() { first <- c.Call(context.Background(), "memory_save", a) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first send not waiting")
	}
	before := time.Now()
	page, e := ListDeliveries(c.Root)
	if e != nil || len(page.Items) != 1 {
		t.Fatal("network wait held list lock", page, e)
	}
	if _, e = ReviewDelivery(c.Root, page.Items[0].Status.ID); e != nil {
		t.Fatal("network wait held review lock", e)
	}
	second := a
	second.OperationID = "second-independent-operation"
	second.Text = "second independent synthetic body"
	out := c.Call(context.Background(), "memory_save", second)
	if !out.OK || out.Delivery == nil || !out.Delivery.PersistedConfirmed {
		t.Fatal("another capture could not progress during first network wait", out)
	}
	if elapsed := time.Since(before); elapsed > time.Second {
		t.Fatal("local storage waited on unrelated network", elapsed)
	}
	t.Log("list/review/other submission completed while first send stayed blocked")
	page, e = ListDeliveries(c.Root)
	if e != nil || len(page.Items) != 2 {
		t.Fatal("concurrent write lost a retained entry", page, e)
	}
	release <- struct{}{}
	select {
	case out = <-first:
		if !out.OK {
			t.Fatal(out)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first send not completed")
	}
}
func TestD04LateErrorCannotDowngradeNewerConfirmedReceipt(t *testing.T) {
	c, a := deliveryFixture(t)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	g := records.AgentDeliveryGrant{Agent: "pi", LibraryID: "original-library", ConnectionID: c.ConnectionID, Generation: 1, Scope: uuid.NewString(), Workspace: c.Workspace, WorkspaceDigest: "daemon-derived"}
	memory := uuid.NewSHA1(uuid.NameSpaceOID, []byte(c.ConnectionID+"\x00"+a.OperationID)).String()
	startDeliveryIPC(t, c, func(r serviceipc.Request) (any, string, bool) {
		if r.Op == "agent_delivery_authorize" {
			return g, "", false
		}
		if calls.Add(1) == 1 {
			started <- struct{}{}
			<-release
			return nil, "service_unavailable", false
		}
		return records.SaveReceipt{Persisted: true, SavedRevision: 1, Record: records.Version{Memory: memory, Dataset: g.Scope, Revision: 2}}, "", false
	})
	first := make(chan Result, 1)
	go func() { first <- c.Call(context.Background(), "memory_save", a) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first send not waiting")
	}
	page, e := ListDeliveries(c.Root)
	if e != nil || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
	item := page.Items[0]
	out := RetryDelivery(context.Background(), c.Root, item.Status.ID, item.Status.ReviewDigest, true)
	if !out.OK || !out.Delivery.PersistedConfirmed {
		t.Fatal(out)
	}
	before, e := ReviewDelivery(c.Root, item.Status.ID)
	if e != nil {
		t.Fatal(e)
	}
	release <- struct{}{}
	select {
	case out = <-first:
		if out.OK || out.Delivery == nil || !out.Delivery.PersistedConfirmed {
			t.Fatal("late failure lost known acknowledgement", out)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("late completion blocked")
	}
	after, e := ReviewDelivery(c.Root, item.Status.ID)
	if e != nil || after.Item.Status.State != "confirmed" || after.Item.Status.SavedRevision != 1 || after.Item.Status.Code != "" || after.Item.UpdatedMS != before.Item.UpdatedMS || calls.Load() != 2 {
		t.Fatal("stale attempt downgraded confirmed receipt", after, e)
	}
}
func TestD04InitialAuthorizationCannotBindReplacementDirectoryAtSamePath(t *testing.T) {
	c, a := deliveryFixture(t)
	var writes atomic.Int32
	startDeliveryIPC(t, c, func(r serviceipc.Request) (any, string, bool) {
		if r.Op == "agent_delivery_authorize" {
			if e := os.Rename(c.Workspace, c.Workspace+"-old"); e != nil {
				t.Error(e)
			}
			if e := os.Mkdir(c.Workspace, 0700); e != nil {
				t.Error(e)
			}
			return records.AgentDeliveryGrant{LibraryID: "current-library", ConnectionID: c.ConnectionID, Generation: 1, Scope: uuid.NewString(), Workspace: c.Workspace, WorkspaceDigest: "new-directory-observation"}, "", false
		}
		writes.Add(1)
		return nil, "", false
	})
	out := c.Call(context.Background(), "memory_save", a)
	if out.Delivery == nil || out.Delivery.State != "blocked" || writes.Load() != 0 || out.Error.Code != "delivery_authorization_changed" {
		t.Fatal("late authority rebound the original workspace", out)
	}
	review, e := ReviewDelivery(c.Root, out.Delivery.ID)
	if e != nil || review.Item.HasGrant || review.Arguments.Text != a.Text {
		t.Fatal(review, e)
	}
}
