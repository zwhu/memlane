package agentbridge

// The outbox is an explicit recovery mechanism, never a background replayer.
// Local retention is not a persisted-knowledge receipt. Only a current daemon
// can acknowledge the original immutable operation under its library epoch.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"golang.org/x/sys/unix"
)

const deliveryLimit = 64
const deliveryBytes = 32 << 20

type deliveryPayload struct {
	WorkspacePhysical string                      `json:"workspace_physical"`
	Root              string                      `json:"root"`
	Connection        string                      `json:"connection"`
	Generation        int64                       `json:"generation"`
	Workspace         string                      `json:"workspace"`
	Arguments         records.AgentArguments      `json:"arguments"`
	Grant             *records.AgentDeliveryGrant `json:"grant,omitempty"`
}
type deliveryEntry struct {
	AssignmentOperation string          `json:"assignment_operation,omitempty"`
	InitialOwner        string          `json:"initial_owner,omitempty"`
	ActiveAttempt       string          `json:"active_attempt,omitempty"`
	RequestDigest       string          `json:"request_digest"`
	Attempts            int             `json:"attempts,omitempty"`
	CreatedMS           int64           `json:"created_ms,omitempty"`
	UpdatedMS           int64           `json:"updated_ms,omitempty"`
	ID                  string          `json:"id"`
	Digest              string          `json:"digest"`
	Payload             deliveryPayload `json:"payload"`
	State               string          `json:"state"`
	Code                string          `json:"code,omitempty"`
	SavedRevision       int64           `json:"saved_revision,omitempty"`
	Memory              string          `json:"memory,omitempty"`
}
type deliveryLedger struct {
	Assignments map[string]deliveryAssignmentJournal  `json:"assignments,omitempty"`
	Version     int                                   `json:"version"`
	Entries     []deliveryEntry                       `json:"entries"`
	Grants      map[string]records.AgentDeliveryGrant `json:"grants"`
}
type deliveryFile struct {
	dir, lock int
	ledger    deliveryLedger
}

func deliveryHash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func deliveryKey(c Config, a records.AgentArguments) string {
	return deliveryHash([]string{c.ConnectionID, a.OperationID})
}
func grantKey(p deliveryPayload) string {
	return deliveryHash([]any{p.Connection, p.Generation, p.Workspace, p.WorkspacePhysical, p.Arguments.Scope})
}
func originalRequestDigest(p deliveryPayload) string { p.Grant = nil; return deliveryHash(p) }
func payloadDigest(p deliveryPayload) string {
	if p.Grant != nil {
		g := *p.Grant
		g.IssuedMS = 0
		p.Grant = &g
	}
	return deliveryHash(p)
}
func privateFD(fd int, dir bool) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	want := uint32(unix.S_IFREG)
	if dir {
		want = unix.S_IFDIR
	}
	if uint32(st.Mode)&unix.S_IFMT != want || st.Mode&0077 != 0 || st.Uid != uint32(os.Getuid()) {
		return errors.New("delivery storage must be private and owned by current user")
	}
	return nil
}
func openDelivery(root string) (*deliveryFile, error) {
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
	if d.ledger.Version != 1 && d.ledger.Version != 2 {
		return fail(assignmentError("delivery_format_unsupported"))
	}
	if len(d.ledger.Entries) > deliveryLimit || d.ledger.Grants == nil {
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
	if err = validateDeliveryAssignments(d, root); err != nil {
		return fail(err)
	}
	return d, nil
}
func (d *deliveryFile) close() {
	if d.lock >= 0 {
		unix.Flock(d.lock, unix.LOCK_UN)
		unix.Close(d.lock)
		d.lock = -1
	}
	if d.dir >= 0 {
		unix.Close(d.dir)
		d.dir = -1
	}
}
func (d *deliveryFile) save() error {
	raw, err := json.Marshal(d.ledger)
	if err != nil {
		return err
	}
	if len(raw) > deliveryBytes {
		return errors.New("delivery_capacity")
	}
	name := "pending-" + uuid.NewString()
	fd, err := unix.Openat(d.dir, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	defer unix.Unlinkat(d.dir, name, 0)
	f := os.NewFile(uintptr(fd), name)
	n, err := f.Write(raw)
	if err == nil && n != len(raw) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	if err = unix.Renameat(d.dir, name, d.dir, "ledger.json"); err != nil {
		return err
	}
	return unix.Fsync(d.dir)
}
func (d *deliveryFile) find(id string) *deliveryEntry {
	for i := range d.ledger.Entries {
		if d.ledger.Entries[i].ID == id {
			return &d.ledger.Entries[i]
		}
	}
	return nil
}
func (d *deliveryFile) capture(p deliveryPayload) (*deliveryEntry, error) {
	// The ledger owns a deep copy, including source fragments/references.
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, errors.New("delivery_invalid_request")
	}
	var frozen deliveryPayload
	if json.Unmarshal(raw, &frozen) != nil {
		return nil, errors.New("delivery_invalid_request")
	}
	p = frozen
	id := deliveryKey(Config{ConnectionID: p.Connection}, p.Arguments)
	if old := d.find(id); old != nil {
		compare := p
		compare.Grant = old.Payload.Grant
		if payloadDigest(compare) != old.Digest {
			return nil, errors.New("delivery_operation_conflict")
		}
		return old, nil
	}
	// Only confirmed acknowledgements can be evicted; unresolved payloads never
	// disappear to make room. The daemon retains operation deduplication.
	if len(d.ledger.Entries) >= deliveryLimit {
		kept := d.ledger.Entries[:0]
		for _, e := range d.ledger.Entries {
			if e.State != "confirmed" || e.AssignmentOperation != "" {
				kept = append(kept, e)
			}
		}
		d.ledger.Entries = kept
	}
	if len(d.ledger.Entries) >= deliveryLimit {
		return nil, errors.New("delivery_capacity")
	}
	state := "waiting"
	if p.Grant == nil {
		state = "pending_unverified"
	}
	d.ledger.Entries = append(d.ledger.Entries, deliveryEntry{ID: id, RequestDigest: originalRequestDigest(p), Digest: payloadDigest(p), Payload: p, State: state, CreatedMS: time.Now().UnixMilli(), UpdatedMS: time.Now().UnixMilli()})
	if err := d.save(); err != nil {
		return nil, err
	}
	return &d.ledger.Entries[len(d.ledger.Entries)-1], nil
}
func deliveryStatus(e *deliveryEntry) *records.AgentDeliveryStatus {
	return &records.AgentDeliveryStatus{ID: e.ID, RequestDigest: e.RequestDigest, ReviewDigest: e.Digest, State: e.State, Code: e.Code, LocalRetained: true, PersistedConfirmed: e.State == "confirmed", SavedRevision: e.SavedRevision, Memory: e.Memory}
}
func deliveryFailure(code string) *Failure {
	message := "本机暂存仍保留；未收到保存回执，请核对后明确重试补发。"
	if code == "delivery_unverified" {
		message = "已在本机暂存，原库授权尚未核验；未发送，不能直接补发。"
	}
	if code == "delivery_storage_unavailable" {
		message = "本机待发存储不可用；本次未发送。"
	}
	if code == "delivery_already_confirmed" {
		message = "同一条原始提交已确认保存，无需再次补发。"
	}
	return &Failure{Code: code, Message: message, Retryable: false}
}
func authorizeDelivery(ctx context.Context, c Config, a records.AgentArguments) (records.AgentDeliveryGrant, error) {
	var g records.AgentDeliveryGrant
	err := serviceipc.Call(ctx, c.Root, serviceipc.Request{Op: "agent_delivery_authorize", AgentRequest: records.AgentRequest{Protocol: 1, ConnectionID: c.ConnectionID, Generation: c.Generation, Workspace: c.Workspace, Channel: "daily", Tool: "memory_save", Arguments: records.AgentArguments{Scope: a.Scope}}}, &g)
	if err == nil && (g.LibraryID == "" || g.ConnectionID != c.ConnectionID || g.Generation != c.Generation || g.Scope == "" || g.Workspace != c.Workspace || g.WorkspaceDigest == "") {
		return g, errors.New("delivery_invalid_grant")
	}
	return g, err
}
func (c Config) callDurableSave(ctx context.Context, a records.AgentArguments) Result {
	if strings.TrimSpace(a.OperationID) == "" || strings.TrimSpace(a.Text) == "" || len(a.Text) > 524288 || len(a.Title) > 512 || len(a.OperationID) > 128 || c.ConnectionID == "" || c.Generation < 1 || a.Memory != "" || (a.Origin != "" && a.Origin != "knowledge" && a.Origin != "raw") {
		return Result{Protocol: 1, Error: &Failure{Code: "invalid_request", Message: "ordinary new save required"}}
	}
	raw, _ := json.Marshal(a)
	if err := validate("memory_save", raw); err != nil {
		return Result{Protocol: 1, Error: &Failure{Code: "invalid_request", Message: err.Error()}}
	}
	if a.Source != nil && (a.Source.History != nil || a.Source.Native != nil || a.Source.Official != nil) {
		return Result{Protocol: 1, Error: &Failure{Code: "invalid_source", Message: "protected provenance cannot be queued"}}
	}
	// Freeze the physical host workspace before capture, never infer from cwd.
	workspace, err := filepath.EvalSymlinks(c.Workspace)
	if err != nil || !filepath.IsAbs(workspace) {
		return Result{Protocol: 1, Error: &Failure{Code: "workspace_mismatch", Message: "trusted workspace required for local retention"}}
	}
	c.Workspace = workspace
	d, err := openDelivery(c.Root)
	if err != nil {
		return Result{Protocol: 1, Error: deliveryStorageFailure(err)}
	}
	defer d.close()
	p := deliveryPayload{Root: c.Root, Connection: c.ConnectionID, Generation: c.Generation, Workspace: workspace, Arguments: a}
	physical, physicalErr := workspacePhysical(workspace)
	if physicalErr != nil {
		return Result{Protocol: 1, Error: deliveryFailure("workspace_mismatch")}
	}
	p.WorkspacePhysical = physical
	old := d.find(deliveryKey(c, a))
	if cached, ok := d.ledger.Grants[grantKey(p)]; ok {
		p.Grant = &cached
	}
	e, err := d.capture(p)
	if err != nil {
		return Result{Protocol: 1, Error: &Failure{Code: err.Error(), Message: "delivery not sent; existing retention unchanged"}}
	}
	if old != nil {
		code := "delivery_explicit_retry_required"
		if e.State == "confirmed" {
			code = "delivery_already_confirmed"
		}
		return Result{Protocol: 1, Error: deliveryFailure(code), Delivery: deliveryStatus(e)}
	}
	e.InitialOwner = uuid.NewString()
	if err = d.save(); err != nil {
		return Result{Protocol: 1, Error: deliveryFailure("delivery_storage_unavailable"), Delivery: deliveryStatus(e)}
	}
	initial := *e
	d.close() // Never hold the shared storage lock while waiting on IPC.
	authctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	g, authErr := authorizeDelivery(authctx, c, initial.Payload.Arguments)
	cancel()
	ready, finishErr := finishInitialDelivery(ctx, c.Root, initial, g, authErr)
	if finishErr != nil {
		return Result{Protocol: 1, Error: deliveryFailure(finishErr.Error()), Delivery: deliveryStatus(&ready)}
	}
	return sendDelivery(ctx, c.Root, ready.ID, ready.Digest)
}

func finishInitialDelivery(ctx context.Context, root string, initial deliveryEntry, g records.AgentDeliveryGrant, authErr error) (deliveryEntry, error) {
	d, err := openDelivery(root)
	if err != nil {
		return initial, errors.New("delivery_storage_unavailable")
	}
	defer d.close()
	e := d.find(initial.ID)
	if e == nil {
		return initial, errors.New("delivery_not_found")
	}
	// Initial binding is a short compare-and-swap, owned only by the live first
	// capture. Existing/reopened operations have no path to this capability.
	if e.InitialOwner != initial.InitialOwner || e.RequestDigest != initial.RequestDigest || e.RequestDigest != originalRequestDigest(e.Payload) || e.Digest != initial.Digest || e.Attempts != 0 {
		return *e, errors.New("delivery_attempt_superseded")
	}
	e.InitialOwner = ""
	if ctx.Err() != nil {
		e.Code = "cancelled"
		if err = d.save(); err != nil {
			return *e, errors.New("delivery_storage_unavailable")
		}
		return *e, errors.New("cancelled")
	}
	if authErr == nil {
		if len(d.ledger.Grants) >= 256 {
			for key := range d.ledger.Grants {
				delete(d.ledger.Grants, key)
				break
			}
		}
		d.ledger.Grants[grantKey(e.Payload)] = g
		if e.Payload.Grant == nil {
			if g.WorkspacePhysical == "" || g.WorkspacePhysical != e.Payload.WorkspacePhysical {
				e.State = "blocked"
				e.Code = "delivery_authorization_changed"
				_ = d.save()
				return *e, errors.New(e.Code)
			}
			e.Payload.Grant = &g
			e.Digest = payloadDigest(e.Payload)
			e.State = "waiting"
		}
	} else {
		var remote *serviceipc.ServiceError
		if errors.As(authErr, &remote) {
			e.State = "blocked"
			e.Code = "delivery_authorization_denied"
			switch remote.Code {
			case "scope_denied", "scope_required", "disconnected", "reload_required", "restore_reconnect_required", "connection_not_found", "workspace_mismatch", "authorization_expired":
				e.Code = remote.Code
			}
		}
	}
	if err = d.save(); err != nil {
		return *e, errors.New("delivery_storage_unavailable")
	}
	if e.State == "blocked" {
		return *e, errors.New(e.Code)
	}
	if e.Payload.Grant == nil {
		return *e, errors.New("delivery_unverified")
	}
	return *e, nil
}

func sendDelivery(ctx context.Context, root, id, reviewDigest string) Result {
	if ctx.Err() != nil {
		return Result{Protocol: 1, Error: deliveryFailure("cancelled")}
	}
	d, err := openDelivery(root)
	if err != nil {
		return Result{Protocol: 1, Error: deliveryFailure("delivery_storage_unavailable")}
	}
	e := d.find(id)
	if e == nil || e.Digest != reviewDigest {
		d.close()
		return Result{Protocol: 1, Error: deliveryFailure("delivery_review_required")}
	}
	if e.State == "confirmed" {
		status := deliveryStatus(e)
		d.close()
		return Result{Protocol: 1, Error: deliveryFailure("delivery_already_confirmed"), Delivery: status}
	}
	if e.State == "blocked" || e.Payload.Grant == nil {
		code := e.Code
		if e.Payload.Grant == nil {
			code = "delivery_unverified"
		}
		if code == "" {
			code = "delivery_authorization_denied"
		}
		status := deliveryStatus(e)
		d.close()
		return Result{Protocol: 1, Error: deliveryFailure(code), Delivery: status}
	}
	e.State = "sending"
	e.InitialOwner = ""
	e.Attempts++
	e.ActiveAttempt = uuid.NewString()
	e.UpdatedMS = time.Now().UnixMilli()
	if err = d.save(); err != nil {
		status := deliveryStatus(e)
		d.close()
		return Result{Protocol: 1, Error: deliveryFailure("delivery_storage_unavailable"), Delivery: status}
	}
	attempt := *e
	d.close()
	p := attempt.Payload
	g := p.Grant
	c := Config{Root: root, ConnectionID: p.Connection, Generation: p.Generation, Workspace: p.Workspace, Channel: "daily", ExpectedLibraryID: g.LibraryID, DeliveryGrant: g}
	out := c.callDirect(ctx, "memory_save", p.Arguments)
	var receipt records.SaveReceipt
	if out.OK {
		expected := uuid.NewSHA1(uuid.NameSpaceOID, []byte(p.Connection+"\x00"+p.Arguments.OperationID)).String()
		if json.Unmarshal(out.Result, &receipt) != nil || !receipt.Persisted || receipt.SavedRevision < 1 || receipt.Record.Memory != expected || receipt.Record.Dataset != g.Scope {
			out = Result{Protocol: 1, Error: deliveryFailure("delivery_invalid_receipt")}
		}
	}
	return finishDeliveryAttempt(root, attempt, out, receipt)
}
func finishDeliveryAttempt(root string, attempt deliveryEntry, out Result, receipt records.SaveReceipt) Result {
	d, err := openDelivery(root)
	if err != nil {
		return Result{Protocol: 1, Error: deliveryFailure("delivery_receipt_retention_failed"), Delivery: deliveryStatus(&attempt)}
	}
	defer d.close()
	e := d.find(attempt.ID)
	if e == nil || e.Digest != attempt.Digest || e.RequestDigest != attempt.RequestDigest {
		return Result{Protocol: 1, Error: deliveryFailure("delivery_review_required")}
	}
	// A confirmed daemon receipt is monotone for this exact immutable operation.
	// Errors only update their own active attempt, never a newer attempt/receipt.
	if e.State == "confirmed" {
		out.Delivery = deliveryStatus(e)
		if !out.OK {
			out.Error = deliveryFailure("delivery_already_confirmed")
		}
		return out
	}
	if out.OK {
		e.State = "confirmed"
		e.Code = ""
		e.Memory = receipt.Record.Memory
		e.SavedRevision = receipt.SavedRevision
	} else if e.ActiveAttempt == attempt.ActiveAttempt && e.Attempts == attempt.Attempts {
		e.State = "waiting"
		e.Code = "receipt_unknown"
		if out.Error != nil {
			switch out.Error.Code {
			case "service_unavailable", "timeout", "cancelled", "permission_denied", "service_error", "index_unavailable", "delivery_invalid_receipt":
				e.Code = out.Error.Code
			case "disconnected", "reload_required", "restore_reconnect_required", "connection_not_found", "scope_denied", "scope_required", "workspace_mismatch", "delivery_library_changed", "delivery_authorization_changed", "authorization_expired", "conflict", "invalid_request", "invalid_source":
				e.State = "blocked"
				e.Code = out.Error.Code
			}
		}
	} else {
		out = Result{Protocol: 1, Error: deliveryFailure("delivery_attempt_superseded"), Delivery: deliveryStatus(e)}
		return out
	}
	e.UpdatedMS = time.Now().UnixMilli()
	if err = d.save(); err != nil {
		return Result{Protocol: 1, Error: deliveryFailure("delivery_receipt_retention_failed"), Delivery: deliveryStatus(&attempt)}
	}
	if !out.OK {
		out.Error = deliveryFailure(e.Code)
	}
	out.Delivery = deliveryStatus(e)
	return out
}

// RetryDelivery requires explicit consent to the current authority-bound review
// digest. No timer, restart scan or incoming duplicate invokes it.

func RetryDelivery(ctx context.Context, root, id, digest string, confirmed bool) Result {
	d, err := openDelivery(root)
	if err != nil {
		return Result{Protocol: 1, Error: deliveryFailure("delivery_storage_unavailable")}
	}
	defer d.close()
	e := d.find(id)
	if e == nil || !confirmed || digest == "" || e.Digest != digest {
		return Result{Protocol: 1, Error: &Failure{Code: "delivery_review_required", Message: "核对原始记录摘要后明确确认补发。"}}
	}
	if e.State == "confirmed" {
		return Result{Protocol: 1, Error: &Failure{Code: "delivery_already_confirmed", Message: "已收到保存回执，无需再次发送。"}, Delivery: deliveryStatus(e)}
	}
	snapshot := *e
	d.close()
	return sendDelivery(ctx, root, snapshot.ID, snapshot.Digest)
}
func RunDeliveryCLI(args []string) int {
	f := flag.NewFlagSet("delivery", flag.ContinueOnError)
	root := f.String("root", "", "original service root")
	action := f.String("action", "list", "list, review or retry")
	id := f.String("id", "", "pending ID")
	digest := f.String("digest", "", "reviewed immutable request digest")
	confirm := f.Bool("confirm", false, "explicitly resend the reviewed original request")
	if f.Parse(args) != nil {
		return 2
	}
	enc := json.NewEncoder(os.Stdout)
	if *action == "retry" {
		out := RetryDelivery(context.Background(), *root, *id, *digest, *confirm)
		_ = enc.Encode(out)
		if !out.OK {
			return 1
		}
		return 0
	}
	d, err := openDelivery(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, deliveryStorageFailure(err).Message)
		return 1
	}
	defer d.close()
	if *action == "review" {
		e := d.find(*id)
		if e == nil {
			return 1
		}
		_ = enc.Encode(e)
		return 0
	}
	if *action != "list" {
		return 2
	}
	out := []*records.AgentDeliveryStatus{}
	for i := range d.ledger.Entries {
		out = append(out, deliveryStatus(&d.ledger.Entries[i]))
	}
	_ = enc.Encode(out)
	return 0
}

func deliveryItem(e *deliveryEntry) records.AgentDeliveryItem {
	p := e.Payload
	out := records.AgentDeliveryItem{Attempts: e.Attempts, Status: *deliveryStatus(e), OperationID: p.Arguments.OperationID, Title: p.Arguments.Title, Origin: p.Arguments.Origin, Scope: p.Arguments.Scope, ConnectionID: p.Connection, Generation: p.Generation, Workspace: p.Workspace, HasGrant: p.Grant != nil, CreatedMS: e.CreatedMS, UpdatedMS: e.UpdatedMS}
	if p.Grant != nil {
		out.Agent = p.Grant.Agent
		out.Scope = p.Grant.Scope
		out.LibraryID = p.Grant.LibraryID
	}
	return out
}

// ListDeliveries reads only this App-owned root. Opening it never submits or
// probes Agent history/configuration. Missing storage is a known empty list;
// corrupt/inaccessible/busy storage is an error, never a fabricated zero.
func ListDeliveries(root string) (records.AgentDeliveryPage, error) {
	page := records.AgentDeliveryPage{Items: []records.AgentDeliveryItem{}, Limit: deliveryLimit}
	if !filepath.IsAbs(root) {
		return page, errors.New("delivery_storage_unavailable")
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return page, errors.New("delivery_storage_unavailable")
	}
	if _, err := os.Lstat(filepath.Join(root, "agent-outbox")); errors.Is(err, os.ErrNotExist) {
		return page, nil
	}
	d, err := openDelivery(root)
	if err != nil {
		return page, err
	}
	defer d.close()
	for i := range d.ledger.Entries {
		page.Items = append(page.Items, deliveryAssignmentItem(d, &d.ledger.Entries[i]))
	}
	sort.Slice(page.Items, func(i, j int) bool {
		if page.Items[i].CreatedMS == page.Items[j].CreatedMS {
			return page.Items[i].Status.ID < page.Items[j].Status.ID
		}
		return page.Items[i].CreatedMS > page.Items[j].CreatedMS
	})
	return page, nil
}
func ReviewDelivery(root, id string) (records.AgentDeliveryReview, error) {
	d, err := openDelivery(root)
	if err != nil {
		return records.AgentDeliveryReview{}, err
	}
	defer d.close()
	e := d.find(id)
	if e == nil {
		return records.AgentDeliveryReview{}, errors.New("delivery_not_found")
	}
	return records.AgentDeliveryReview{Item: deliveryAssignmentItem(d, e), Arguments: e.Payload.Arguments}, nil
}

func workspacePhysical(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", errors.New("workspace_mismatch")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("workspace_mismatch")
	}
	return fmt.Sprintf("%d:%d", uint64(st.Dev), st.Ino), nil
}

func deliveryStorageFailure(err error) *Failure {
	var access *records.AccessError
	if errors.As(err, &access) && access.Code == "delivery_format_unsupported" {
		return &Failure{Code: access.Code, Message: "本机待发记录需要更新接入组件；请重新加载 Agent 的 Memlane 连接。原文件保留，本次未发送。"}
	}
	return deliveryFailure("delivery_storage_unavailable")
}
