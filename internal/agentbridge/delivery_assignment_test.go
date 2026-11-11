package agentbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func assignmentFixture(t *testing.T) (Config, records.AgentArguments, records.DeliveryAssignmentIntent) {
	t.Helper()
	c, a := deliveryFixture(t)
	out := c.Call(context.Background(), "memory_save", a)
	if out.Delivery == nil || out.Delivery.State != "pending_unverified" {
		t.Fatal(out)
	}
	return c, a, records.DeliveryAssignmentIntent{OperationID: uuid.NewString(), LibraryID: uuid.NewString(), EntryID: out.Delivery.ID, RequestDigest: out.Delivery.RequestDigest, ReviewDigest: out.Delivery.ReviewDigest, Project: uuid.NewString(), WorkspaceDigest: deliveryHash("synthetic-project-workspace")}
}
func assignmentReceipt(q records.DeliveryAssignmentIntent, c Config, a records.AgentArguments) records.DeliveryAssignmentReceipt {
	return records.DeliveryAssignmentReceipt{OperationID: q.OperationID, LibraryID: q.LibraryID, EntryID: q.EntryID, RequestDigest: q.RequestDigest, ReviewDigest: q.ReviewDigest, Project: q.Project, WorkspaceDigest: q.WorkspaceDigest, Memory: uuid.NewString(), Revision: 1, OriginalOperationID: a.OperationID, OriginalConnectionID: c.ConnectionID, Source: "user_assignment", State: "saved", CreatedMS: time.Now().UnixMilli()}
}
func TestE05JournalClaimRestartAndIndependentAck(t *testing.T) {
	c, a, q := assignmentFixture(t)
	if intent, e := ReadDeliveryAssignmentIntent(c.Root, q.EntryID); e != nil || intent != nil {
		t.Fatal(intent, e)
	}
	if _, e := JournalDeliveryAssignment(c.Root, q); e != nil {
		t.Fatal(e)
	}
	if _, e := ReadDeliveryAssignmentClaim(c.Root, q); e == nil {
		t.Fatal("journal alone acted as claimed")
	}
	intent, e := ReadDeliveryAssignmentIntent(c.Root, q.EntryID)
	if e != nil || intent == nil || *intent != q {
		t.Fatal("original user request not durable", intent, e)
	}
	if e = PrepareDeliveryAssignment(c.Root, q); e != nil {
		t.Fatal(e)
	}
	source, e := ReadDeliveryAssignmentClaim(c.Root, q)
	if e != nil || source.Review.Arguments.Text != a.Text || source.Review.Arguments.OperationID != a.OperationID || source.WorkspacePhysical == "" {
		t.Fatal(source, e)
	}
	if _, e = ClaimDeliveryAssignment(c.Root, q); e != nil {
		t.Fatal("same claim could not recover", e)
	}
	r := assignmentReceipt(q, c, a)
	if e = AckDeliveryAssignment(c.Root, q, r); e != nil {
		t.Fatal(e)
	}
	r.SpoolAckPending = true
	if e = AckDeliveryAssignment(c.Root, q, r); e != nil {
		t.Fatal("transient ack flag changed receipt identity", e)
	}
	review, e := ReviewDelivery(c.Root, q.EntryID)
	if e != nil || review.Item.Assignment == nil || review.Item.Assignment.State != "saved" || review.Item.Assignment.Memory != r.Memory || review.Item.Status.PersistedConfirmed || review.Item.Status.SavedRevision != 0 || review.Item.Status.Memory != "" || review.Arguments.OperationID != a.OperationID || review.Item.Status.RequestDigest != q.RequestDigest || review.Item.Status.ReviewDigest != q.ReviewDigest {
		t.Fatal("user receipt impersonated Agent save", review, e)
	}
	again := c.Call(context.Background(), "memory_save", a)
	if again.OK || again.Delivery == nil || again.Delivery.PersistedConfirmed {
		t.Fatal("old Agent submission silently replayed", again)
	}
}
func TestE05ClaimRejectsRetargetAndWrongReceipt(t *testing.T) {
	c, a, q := assignmentFixture(t)
	if e := PrepareDeliveryAssignment(c.Root, q); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*records.DeliveryAssignmentIntent){func(v *records.DeliveryAssignmentIntent) { v.OperationID = uuid.NewString() }, func(v *records.DeliveryAssignmentIntent) { v.LibraryID = uuid.NewString() }, func(v *records.DeliveryAssignmentIntent) { v.Project = uuid.NewString() }, func(v *records.DeliveryAssignmentIntent) { v.RequestDigest = deliveryHash("different") }, func(v *records.DeliveryAssignmentIntent) { v.ReviewDigest = deliveryHash("different") }, func(v *records.DeliveryAssignmentIntent) { v.WorkspaceDigest = deliveryHash("different") }} {
		other := q
		mutate(&other)
		if _, e := ClaimDeliveryAssignment(c.Root, other); e == nil {
			t.Fatal("retargeted claim accepted", other)
		}
		if _, e := ReadDeliveryAssignmentClaim(c.Root, other); e == nil {
			t.Fatal("wrong-library/intent claim readable", other)
		}
	}
	r := assignmentReceipt(q, c, a)
	for _, mutate := range []func(*records.DeliveryAssignmentReceipt){func(v *records.DeliveryAssignmentReceipt) { v.LibraryID = uuid.NewString() }, func(v *records.DeliveryAssignmentReceipt) { v.Project = uuid.NewString() }, func(v *records.DeliveryAssignmentReceipt) { v.OriginalOperationID = "other" }, func(v *records.DeliveryAssignmentReceipt) { v.Source = "agent" }} {
		wrong := r
		mutate(&wrong)
		if e := AckDeliveryAssignment(c.Root, q, wrong); e == nil {
			t.Fatal("wrong receipt accepted", wrong)
		}
	}
	current, e := ReadDeliveryAssignmentClaim(c.Root, q)
	if e != nil || current.Review.Item.Assignment.State != "claimed" {
		t.Fatal(current, e)
	}
}
func TestE05ClaimRetiresInitialAuthorizationBeforeLateResponse(t *testing.T) {
	c, a := deliveryFixture(t)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	defer close(release)
	var saves atomic.Int32
	g := records.AgentDeliveryGrant{LibraryID: uuid.NewString(), ConnectionID: c.ConnectionID, Generation: c.Generation, Scope: uuid.NewString(), Workspace: c.Workspace, WorkspaceDigest: "current-daemon-workspace"}
	startDeliveryIPC(t, c, func(r serviceipc.Request) (any, string, bool) {
		if r.Op == "agent_delivery_authorize" {
			started <- struct{}{}
			<-release
			return g, "", false
		}
		saves.Add(1)
		return nil, "", true
	})
	result := make(chan Result, 1)
	go func() { result <- c.Call(context.Background(), "memory_save", a) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("initial authorization did not wait")
	}
	page, e := ListDeliveries(c.Root)
	if e != nil || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
	item := page.Items[0]
	q := records.DeliveryAssignmentIntent{OperationID: uuid.NewString(), LibraryID: g.LibraryID, EntryID: item.Status.ID, RequestDigest: item.Status.RequestDigest, ReviewDigest: item.Status.ReviewDigest, Project: g.Scope, WorkspaceDigest: g.WorkspaceDigest}
	if e = PrepareDeliveryAssignment(c.Root, q); e != nil {
		t.Fatal(e)
	}
	release <- struct{}{}
	select {
	case out := <-result:
		if out.OK || saves.Load() != 0 {
			t.Fatal("late initial authorization sent the Agent operation", out)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("late initial response did not finish")
	}
	source, e := ReadDeliveryAssignmentClaim(c.Root, q)
	if e != nil || source.Review.Item.HasGrant || source.Review.Item.Attempts != 0 || source.Review.Item.Status.PersistedConfirmed {
		t.Fatal(source, e)
	}
}
func TestE05JournalGapCannotClaimAfterOriginalAgentBound(t *testing.T) {
	c, a, q := assignmentFixture(t)
	if _, e := JournalDeliveryAssignment(c.Root, q); e != nil {
		t.Fatal(e)
	}
	d, e := openDelivery(c.Root)
	if e != nil {
		t.Fatal(e)
	}
	entry := d.find(q.EntryID)
	entry.Payload.Grant = &records.AgentDeliveryGrant{LibraryID: q.LibraryID, ConnectionID: c.ConnectionID, Generation: c.Generation, Scope: q.Project, Workspace: c.Workspace, WorkspaceDigest: q.WorkspaceDigest}
	entry.Digest = payloadDigest(entry.Payload)
	entry.State = "waiting"
	if e = d.save(); e != nil {
		t.Fatal(e)
	}
	d.close()
	if e = PrepareDeliveryAssignment(c.Root, q); e == nil {
		t.Fatal("journal gap overrode bound Agent request")
	}
	source, e := ReadDeliveryAssignmentSource(c.Root, q.EntryID, q.ReviewDigest)
	if e != nil || !source.Review.Item.HasGrant || source.Review.Arguments.Text != a.Text {
		t.Fatal("original facts lost after journal conflict", source, e)
	}
}
func TestE05VersionBarrierPreservesD04AndRejectsOldWriter(t *testing.T) {
	c, _, q := assignmentFixture(t)
	d, e := legacyD04OpenDelivery(c.Root)
	if e != nil {
		t.Fatal("unchanged D04 v1 cannot be read by its original reader", e)
	}
	if d.ledger.Version != 1 {
		t.Fatal("D04 alone changed format")
	}
	d.close()
	if _, e = JournalDeliveryAssignment(c.Root, q); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(c.Root, "agent-outbox", "ledger.json")
	before, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	d, e = openDelivery(c.Root)
	if e != nil {
		t.Fatal(e)
	}
	if d.ledger.Version != 2 {
		t.Fatal("assignment journal did not fence legacy writers")
	}
	d.close()
	if old, e := legacyD04OpenDelivery(c.Root); e == nil {
		old.close()
		t.Fatal("old whole-file writer can erase new metadata")
	}
	after, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("old reader rejection changed journal", e)
	}
	var ledger map[string]any
	if json.Unmarshal(before, &ledger) != nil {
		t.Fatal("bad fixture")
	}
	ledger["version"] = 3
	raw, _ := json.Marshal(ledger)
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	_, e = openDelivery(c.Root)
	failure := deliveryStorageFailure(e)
	if failure.Code != "delivery_format_unsupported" || !strings.Contains(failure.Message, "重新加载") {
		t.Fatal("format upgrade misreported as ordinary corruption", failure)
	}
}

// Frozen D04 reader from the immutable .base. Older programs refuse v2.
func legacyD04OpenDelivery(root string) (*deliveryFile, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("absolute delivery root required")
	}
	// Open each owned outbox object without following links. Parent root is the
	// configured daemon location, not a model-controlled tool argument.
	parent, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(parent)
	var parentStat unix.Stat_t
	if err = unix.Fstat(parent, &parentStat); err != nil {
		return nil, err
	}
	if parentStat.Mode&0022 != 0 || parentStat.Uid != uint32(os.Getuid()) {
		return nil, errors.New("delivery root must be owned and not writable by other users")
	}
	path := filepath.Join(root, "agent-outbox")
	if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	if err = unix.Fsync(parent); err != nil {
		return nil, err
	}
	dir, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	d := &deliveryFile{dir: dir, lock: -1, ledger: deliveryLedger{Version: 1, Entries: []deliveryEntry{}, Grants: map[string]records.AgentDeliveryGrant{}}}
	fail := func(e error) (*deliveryFile, error) { d.close(); return nil, e }
	if err = privateFD(dir, true); err != nil {
		return fail(err)
	}
	d.lock, err = unix.Openat(dir, "lock", unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return fail(err)
	}
	if err = privateFD(d.lock, false); err != nil {
		return fail(err)
	}
	if err = unix.Flock(d.lock, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fail(errors.New("delivery_busy"))
	}
	fd, err := unix.Openat(dir, "ledger.json", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return d, nil
	}
	if err != nil {
		return fail(err)
	}
	f := os.NewFile(uintptr(fd), "ledger.json")
	defer f.Close()
	if err = privateFD(fd, false); err != nil {
		return fail(err)
	}
	raw, err := io.ReadAll(io.LimitReader(f, deliveryBytes+1))
	if err != nil {
		return fail(err)
	}
	if len(raw) > deliveryBytes {
		return fail(errors.New("delivery_capacity"))
	}
	if err = json.Unmarshal(raw, &d.ledger); err != nil {
		return fail(errors.New("delivery_corrupt"))
	}
	if d.ledger.Version != 1 || len(d.ledger.Entries) > deliveryLimit || d.ledger.Grants == nil {
		return fail(errors.New("delivery_corrupt"))
	}
	seen := map[string]bool{}
	for _, e := range d.ledger.Entries {
		if seen[e.ID] || e.ID != deliveryKey(Config{ConnectionID: e.Payload.Connection}, e.Payload.Arguments) || e.Digest != payloadDigest(e.Payload) || e.RequestDigest != originalRequestDigest(e.Payload) || e.Payload.Root != root {
			return fail(errors.New("delivery_corrupt"))
		}
		seen[e.ID] = true
		switch e.State {
		case "pending_unverified", "waiting", "sending", "confirmed", "blocked":
		default:
			return fail(errors.New("delivery_corrupt"))
		}
	}
	return d, nil
}
