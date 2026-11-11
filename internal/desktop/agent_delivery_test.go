package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agentbridge"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestD04NativeLocalReadUnknownAuthorizationAndEpoch(t *testing.T) {
	a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		return serviceipc.Response{Code: "service_unavailable", Error: "unsafe synthetic raw failure"}
	})
	workspace, _ := filepath.EvalSymlinks(t.TempDir())
	c := agentbridge.Config{Root: a.library.Root, ConnectionID: uuid.NewString(), Generation: 1, Workspace: workspace, DurableDelivery: true} // No trusted grant: capture while the socket is genuinely absent.
	root := t.TempDir()
	c.Root = root
	out := c.Call(context.Background(), "memory_save", records.AgentArguments{OperationID: "synthetic-unverified", Text: "synthetic retained original", Origin: "raw", Scope: "original-scope"})
	if out.Delivery == nil {
		t.Fatal(out)
	}
	a.library.Root = root
	a.bindAgentDelivery()
	a.homeActivity.Delivery.Full = true
	a.refreshAgentDeliveries()
	awaitProcessingDispatch(t, dispatch)
	awaitProcessingDispatch(t, dispatch)
	s := &a.homeActivity.Delivery
	if !s.Loaded || s.Review == nil || s.Review.Item.Origin != "raw" || deliveryMayRetry(s.Review.Item) {
		t.Fatal(s)
	}
	for _, width := range []int{1000, 600} {
		tt := ui.NewTester(a.agentDeliveryFull, width, 640)
		if !tt.HasText("授权未核验") || !tt.HasText("原始记录") || !tt.HasText("synthetic retained original") || tt.HasText("重试补发") || tt.HasText("确认补发") {
			t.Fatal("unverified UI offered replay or hid retained payload")
		}
		if button, ok := tt.Find("重新读取暂存"); !ok || button.X+button.W > float32(width) || button.Y+button.H > 640 {
			t.Fatal("pending footer escaped narrow pane", button)
		}
	}
	a.refreshAgentDeliveries()
	a.libraryEpoch++
	a.retireAgentDelivery()
	awaitProcessingDispatch(t, dispatch)
	if s.Loaded || s.Review != nil || len(s.Items) > 0 {
		t.Fatal("late read crossed restore epoch")
	}
}
func TestD04NativeReviewExplicitRetryReceiptAndContext(t *testing.T) {
	connection := uuid.NewString()
	scope := uuid.NewString()
	library := uuid.NewString()
	var saves atomic.Int32
	var valid atomic.Bool
	workspace, _ := filepath.EvalSymlinks(t.TempDir())
	a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op == "agent_delivery_authorize" {
			return serviceipc.Response{OK: true, Result: d04NativePhysicalGrant(records.AgentDeliveryGrant{LibraryID: library, ConnectionID: connection, Generation: 3, Scope: scope, Workspace: workspace, WorkspaceDigest: "daemon-derived"})}
		}
		if r.Op == "agent" {
			saves.Add(1)
			if !valid.Load() {
				return serviceipc.Response{Code: "service_unavailable", Error: "unsafe body must not render"}
			}
			if r.AgentRequest.ExpectedLibraryID != library || r.AgentRequest.Arguments.OperationID != "original-operation" || r.AgentRequest.Arguments.Text != "synthetic exact original" {
				t.Error("retargeted original request")
			}
			memory := uuid.NewSHA1(uuid.NameSpaceOID, []byte(connection+"\x00original-operation")).String()
			return serviceipc.Response{OK: true, Result: records.SaveReceipt{Persisted: true, SavedRevision: 1, Record: records.Version{Memory: memory, Dataset: scope, Revision: 2}}}
		}
		return serviceipc.Response{OK: true}
	})
	out := (agentbridge.Config{Root: a.library.Root, ConnectionID: connection, Generation: 3, Workspace: workspace, DurableDelivery: true}).Call(context.Background(), "memory_save", records.AgentArguments{OperationID: "original-operation", Text: "synthetic exact original", Origin: "knowledge"})
	if out.Delivery == nil || out.Delivery.PersistedConfirmed {
		t.Fatal(out)
	}
	a.selectedID = "preserved-knowledge"
	a.detailScroll.Y = 81
	a.homeActivity.Full = true
	a.bindAgentDelivery()
	a.homeActivity.Delivery.Full = true
	a.refreshAgentDeliveries()
	awaitProcessingDispatch(t, dispatch)
	awaitProcessingDispatch(t, dispatch)
	s := &a.homeActivity.Delivery
	if s.Review == nil || s.Review.Item.Scope != scope || s.Review.Arguments.Scope != "" {
		t.Fatal("default effective scope missing or original request rewritten")
	}
	tt := ui.NewTester(a.agentDeliveryFull, 1000, 640)
	if !tt.HasText("等待补发") || tt.HasText("unsafe body must not render") {
		t.Fatal("pending/error projection")
	}
	if tt.HasText(s.Review.Item.Status.ReviewDigest) || tt.HasText(s.Review.Item.Status.RequestDigest) {
		t.Fatal("technical proof leaked into primary presentation")
	}
	if e := tt.Click("查看投递详情"); e != nil {
		t.Fatal(e)
	}
	if !tt.HasText(s.Review.Item.Status.ReviewDigest) {
		t.Fatal("technical proof unavailable in explicit disclosure")
	}
	if e := tt.Click("收起投递详情"); e != nil {
		t.Fatal(e)
	}
	if e := tt.Click("重试补发"); e != nil {
		t.Fatal(e)
	}
	if saves.Load() != 1 || !s.Confirm {
		t.Fatal("review click sent payload")
	}
	valid.Store(true)
	if e := tt.Click("确认补发"); e != nil {
		t.Fatal(e)
	}
	awaitProcessingDispatch(t, dispatch)
	awaitProcessingDispatch(t, dispatch)
	if saves.Load() != 2 || s.Retrying || s.Review == nil || !s.Review.Item.Status.PersistedConfirmed || a.selectedID != "preserved-knowledge" || a.detailScroll.Y != 81 {
		t.Fatal("receipt/context", s)
	}
	if !strings.Contains(s.Info, "保存 v1") {
		t.Fatal(s.Info)
	}
	reopened, e := agentbridge.ReviewDelivery(a.library.Root, out.Delivery.ID)
	if e != nil || reopened.Item.Status.SavedRevision != 1 || reopened.Arguments.Text != "synthetic exact original" {
		t.Fatal(reopened, e)
	}
}
func TestD04NativeStatesDoNotFabricateSavedOrRetry(t *testing.T) {
	for _, state := range []string{"pending_unverified", "waiting", "sending", "blocked", "confirmed"} {
		i := records.AgentDeliveryItem{HasGrant: state != "pending_unverified", Origin: "knowledge", Status: records.AgentDeliveryStatus{State: state, ReviewDigest: "original-review", LocalRetained: true}}
		want := state == "waiting" || state == "sending"
		if deliveryMayRetry(i) != want {
			t.Fatal(state)
		}
		if state == "sending" && deliveryLabel(i, false) != "上次结果未知" {
			t.Fatal("restart fabricated active sending")
		}
	}
}
func TestD04NativeCancelWaitAndRetireLeaveUnknownPayload(t *testing.T) {
	for _, retire := range []bool{false, true} {
		t.Run(fmt.Sprint(retire), func(t *testing.T) {
			connection := uuid.NewString()
			scope := uuid.NewString()
			workspace, _ := filepath.EvalSymlinks(t.TempDir())
			started := make(chan struct{}, 1)
			release := make(chan struct{})
			defer close(release)
			var calls atomic.Int32
			a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
				if r.Op == "agent_delivery_authorize" {
					return serviceipc.Response{OK: true, Result: d04NativePhysicalGrant(records.AgentDeliveryGrant{LibraryID: "original-library", ConnectionID: connection, Generation: 1, Scope: scope, Workspace: workspace, WorkspaceDigest: "daemon-observed"})}
				}
				if r.Op == "agent" && calls.Add(1) > 1 {
					started <- struct{}{}
					<-release
				}
				return serviceipc.Response{Code: "service_unavailable", Error: "synthetic"}
			})
			out := (agentbridge.Config{Root: a.library.Root, ConnectionID: connection, Generation: 1, Workspace: workspace, DurableDelivery: true}).Call(context.Background(), "memory_save", records.AgentArguments{OperationID: "cancel-original", Text: "synthetic retained content"})
			a.bindAgentDelivery()
			s := &a.homeActivity.Delivery
			s.Full = true
			a.refreshAgentDeliveries()
			awaitProcessingDispatch(t, dispatch)
			awaitProcessingDispatch(t, dispatch)
			s.Confirm = true
			a.beginAgentDeliveryRetry()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("retry not started")
			}
			if retire {
				a.libraryEpoch++
				a.retireAgentDelivery()
			} else {
				tt := ui.NewTester(a.agentDeliveryFull, 600, 640)
				if e := tt.Click("取消本次等待"); e != nil {
					t.Fatal(e)
				}
			}
			awaitProcessingDispatch(t, dispatch)
			if retire {
				if s.Retrying || s.Review != nil || s.Root != "" {
					t.Fatal("late retry receipt crossed retired pane")
				}
			} else {
				awaitProcessingDispatch(t, dispatch)
				if s.Retrying || s.Review == nil || s.Review.Item.Status.PersistedConfirmed || !strings.Contains(s.Info, "结果尚未确认") {
					t.Fatal("cancel claimed unwritten/saved", s)
				}
			}
			review, e := agentbridge.ReviewDelivery(a.library.Root, out.Delivery.ID)
			if e != nil || review.Arguments.OperationID != "cancel-original" || review.Item.Status.PersistedConfirmed || review.Item.Status.State != "waiting" {
				t.Fatal("cancel lost original payload", review, e)
			}
		})
	}
}
func TestD04NativeReadFailureIsUnknownNotEmpty(t *testing.T) {
	a, dispatch := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response { return serviceipc.Response{OK: true} })
	if e := os.Mkdir(filepath.Join(a.library.Root, "agent-outbox"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(a.library.Root, "agent-outbox", "ledger.json"), []byte("corrupt"), 0600); e != nil {
		t.Fatal(e)
	}
	a.bindAgentDelivery()
	s := &a.homeActivity.Delivery
	s.Loaded = true
	s.Full = true
	a.refreshAgentDeliveries()
	awaitProcessingDispatch(t, dispatch)
	tt := ui.NewTester(a.agentDeliveryFull, 600, 640)
	if s.Error == "" || tt.HasText("本机没有待发送记录") {
		t.Fatal("failed local read became known zero")
	}
}

func d04NativePhysicalGrant(g records.AgentDeliveryGrant) records.AgentDeliveryGrant {
	info, e := os.Stat(g.Workspace)
	if e != nil {
		panic(e)
	}
	st := info.Sys().(*syscall.Stat_t)
	g.WorkspacePhysical = fmt.Sprintf("%d:%d", uint64(st.Dev), st.Ino)
	return g
}
func TestD04NativeConfirmedFactDoesNotRequireThisRPCSuccess(t *testing.T) {
	status := records.AgentDeliveryStatus{ID: "same-original", RequestDigest: "original-content", ReviewDigest: "bound-review", State: "confirmed", LocalRetained: true, PersistedConfirmed: true, SavedRevision: 1}
	out := agentbridge.Result{Protocol: 1, OK: false, Error: &agentbridge.Failure{Code: "service_unavailable"}, Delivery: &status}
	current := records.AgentDeliveryReview{Item: records.AgentDeliveryItem{Status: status}, Arguments: records.AgentArguments{Text: "retained original"}}
	if got := confirmedDeliveryRevision(out, current, nil, status.ID, status.ReviewDigest); got != 1 {
		t.Fatal("late failure erased acknowledgement", got)
	}
	out.Delivery = nil
	if got := confirmedDeliveryRevision(out, current, nil, status.ID, status.ReviewDigest); got != 1 {
		t.Fatal("re-read confirmation depended on failed RPC", got)
	}
	if got := confirmedDeliveryRevision(out, current, nil, status.ID, "other-review"); got != 0 {
		t.Fatal("different request confirmation reused", got)
	}
	a := New("test", nil)
	a.homeActivity.Delivery = AgentDeliveryUI{Review: &current, Info: fmt.Sprintf("已确认保存 v%d；索引状态单独读取。", confirmedDeliveryRevision(out, current, nil, status.ID, status.ReviewDigest))}
	tt := ui.NewTester(a.agentDeliveryDetail, 600, 640)
	if !tt.HasText("已确认保存") || tt.HasText("结果尚未确认") {
		t.Fatal("confirmed title contradicted late failure presentation")
	}
	if out.OK || len(out.Result) != 0 || current.Item.Status.SavedRevision != 1 {
		t.Fatal("constructed successful SaveReceipt or changed saved version")
	}
}
func TestD04NativeLateFailedRPCUsesOtherAttemptConfirmation(t *testing.T) {
	connection, scope := uuid.NewString(), uuid.NewString()
	workspace, _ := filepath.EvalSymlinks(t.TempDir())
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	defer close(release)
	var attempts atomic.Int32
	const operation = "same-original-ui"
	memory := uuid.NewSHA1(uuid.NameSpaceOID, []byte(connection+"\x00"+operation)).String()
	a, dispatch := d04ConcurrentUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op == "agent_delivery_authorize" {
			return serviceipc.Response{OK: true, Result: d04NativePhysicalGrant(records.AgentDeliveryGrant{LibraryID: "same-library", ConnectionID: connection, Generation: 1, Scope: scope, Workspace: workspace, WorkspaceDigest: "same-frozen-identity"})}
		}
		switch attempts.Add(1) {
		case 1:
			return serviceipc.Response{Code: "service_unavailable", Error: "synthetic initial failure"}
		case 2:
			started <- struct{}{}
			<-release
			return serviceipc.Response{Code: "service_unavailable", Error: "synthetic late failure"}
		}
		return serviceipc.Response{OK: true, Result: records.SaveReceipt{Persisted: true, SavedRevision: 1, Record: records.Version{Memory: memory, Dataset: scope, Revision: 2}}}
	})
	out := (agentbridge.Config{Root: a.library.Root, ConnectionID: connection, Generation: 1, Workspace: workspace, DurableDelivery: true}).Call(context.Background(), "memory_save", records.AgentArguments{OperationID: operation, Text: "same retained original"})
	if out.Delivery == nil {
		t.Fatal(out)
	}
	a.bindAgentDelivery()
	s := &a.homeActivity.Delivery
	s.Full = true
	a.refreshAgentDeliveries()
	awaitProcessingDispatch(t, dispatch)
	awaitProcessingDispatch(t, dispatch)
	s.Confirm = true
	a.beginAgentDeliveryRetry()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("UI attempt did not start")
	}
	confirmed := agentbridge.RetryDelivery(context.Background(), a.library.Root, out.Delivery.ID, out.Delivery.ReviewDigest, true)
	if !confirmed.OK || confirmed.Delivery == nil || !confirmed.Delivery.PersistedConfirmed {
		t.Fatal(confirmed)
	}
	release <- struct{}{}
	awaitProcessingDispatch(t, dispatch)
	awaitProcessingDispatch(t, dispatch)
	if s.Review == nil || !s.Review.Item.Status.PersistedConfirmed || s.Review.Item.Status.SavedRevision != 1 || strings.Contains(s.Info, "结果尚未确认") || !strings.Contains(s.Info, "保存 v1") {
		t.Fatal("late failed RPC contradicted confirmed pane", s)
	}
	if attempts.Load() != 3 || s.Review.Arguments.Text != "same retained original" {
		t.Fatal("generated another submission or replaced original")
	}
}
func d04ConcurrentUIFixture(t *testing.T, handle func(serviceipc.Request) serviceipc.Response) (*App, chan func()) {
	t.Helper()
	root := t.TempDir()
	path := serviceipc.SocketPath(root)
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("unix", path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { listener.Close(); os.RemoveAll(filepath.Dir(path)) })
	go func() {
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				var r serviceipc.Request
				if json.NewDecoder(conn).Decode(&r) != nil {
					return
				}
				out := handle(r)
				out.Protocol, out.ID = 1, r.ID
				_ = json.NewEncoder(conn).Encode(out)
			}(conn)
		}
	}()
	a := New("test", nil)
	a.library = serviceipc.Library{Root: root}
	a.libraryContext = context.Background()
	dispatch := make(chan func(), 12)
	a.dispatch = func(fn func()) { dispatch <- fn }
	return a, dispatch
}
