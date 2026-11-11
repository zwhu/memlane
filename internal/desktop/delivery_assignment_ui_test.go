package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agentbridge"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"github.com/zwhu/memlane/internal/store"
)

func e05PreviewFixture() *records.DeliveryAssignmentPreview {
	project := records.Scope{ID: uuid.NewString(), Kind: "project", Name: "来源项目", Path: "/synthetic/source-repository"}
	wrong := records.Scope{ID: uuid.NewString(), Kind: "project", Name: "另一个项目", Path: "/synthetic/other-repository"}
	id := strings.Repeat("a", 64)
	return &records.DeliveryAssignmentPreview{LibraryID: uuid.NewString(), EntryID: id, RequestDigest: strings.Repeat("b", 64), ReviewDigest: strings.Repeat("c", 64), Workspace: "/synthetic/source-repository/.worktrees/review", WorkspaceDigest: strings.Repeat("d", 64), Item: records.AgentDeliveryItem{OperationID: uuid.NewString(), ConnectionID: uuid.NewString(), Origin: "raw", Title: "原始提交标题", Workspace: "/synthetic/source-repository/.worktrees/review", Status: records.AgentDeliveryStatus{ID: id, LocalRetained: true}}, Arguments: records.AgentArguments{Text: "真实来源字段的夹具原文开头。\n" + strings.Repeat("逐行核对原始提交，不改为知识。\n", 12) + "完整原文尾部。", Origin: "raw"}, Projects: []records.Scope{project, wrong}, MatchingProject: &project, SelectedProject: wrong.ID, Code: "workspace_mismatch"}
}
func e05PanelFixture(a *App, v *records.DeliveryAssignmentPreview) *DeliveryAssignmentPanel {
	a.page = "待归属提交"
	a.visualFixture = true
	p := &DeliveryAssignmentPanel{EntryID: v.EntryID, Root: a.library.Root, SelectedProject: v.SelectedProject, Choice: assignmentProjectChoice(v.Projects, v.SelectedProject), Preview: v}
	a.deliveryAssignment = DeliveryAssignmentUI{Root: a.library.Root, Selected: v.EntryID, Loaded: true, Items: []records.AgentDeliveryItem{v.Item}, Panels: map[string]*DeliveryAssignmentPanel{v.EntryID: p}}
	return p
}
func TestE05AssignmentMismatchFramesAndOriginalType(t *testing.T) {
	for _, width := range []int{1200, 800} {
		a := New("test", nil)
		v := e05PreviewFixture()
		p := e05PanelFixture(a, v)
		a.selectedID = "保留知识选择"
		a.draft = "保留知识草稿"
		a.query = "保留全局查询"
		tt := ui.NewTester(a.View, width, 640)
		if canConfirmAssignment(p) || !tt.HasText("该来源对应已登记的项目。可打开匹配项目，或重新核对来源。") || !tt.HasText("原始记录 · 来源 Agent 未记录") || !tt.HasText(v.Arguments.Text) {
			t.Fatal("mismatch lost source or enabled wrong scope")
		}
		node, ok := tt.Find("确认项目归属")
		if !ok || node.Y+node.H > 640 || node.X+node.W > float32(width) {
			t.Fatal("fixed footer unavailable", node)
		}
		writeScreenshot(t, tt, fmt.Sprintf("e05-mismatch-%d.png", width))
		match, visible := tt.Find("打开匹配的项目")
		if !visible || match.Y < 110 || match.Y+match.H > 550 {
			t.Fatal("mismatch action below first visible body", match)
		}
		if err := tt.Click("打开匹配的项目"); err != nil {
			t.Fatal(err)
		}
		if a.page != "项目" || a.scope != v.MatchingProject.ID || a.deliveryAssignment.Selected != v.EntryID || p.SelectedProject != v.SelectedProject || a.draft != "保留知识草稿" || a.query != "保留全局查询" {
			t.Fatal("open match selected/assigned or lost draft")
		}
		tt.Frame()
		back, ok := tt.Find("返回归属核对")
		if !ok || back.X < 212 || back.X+back.W > float32(width) || back.Y+back.H > 640 {
			t.Fatal("matched project lost reachable return", back)
		}
		a.navigate("待归属提交", "")
		p.Scroll.Y = p.Scroll.MaxY
		tt.Frame()
		writeScreenshot(t, tt, fmt.Sprintf("e05-original-bottom-%d.png", width))
		if err := tt.Click("复制原始提交全文"); err != nil {
			t.Fatal(err)
		}
		if tt.Clipboard() != v.Arguments.Text {
			t.Fatal("copy truncated original")
		}
	}
}
func TestE05AssignmentUnknownAndChangedPathNeverConfirm(t *testing.T) {
	for _, code := range []string{"workspace_changed", "workspace_unavailable", "workspace_unverified", "project_unregistered", "scope_ambiguous"} {
		a := New("test", nil)
		v := e05PreviewFixture()
		v.SelectedProject = v.MatchingProject.ID
		v.Code = code
		v.CanConfirm = false
		p := e05PanelFixture(a, v)
		if canConfirmAssignment(p) {
			t.Fatal("unverified proof enabled confirmation", code)
		}
	}
	a := New("test", nil)
	v := e05PreviewFixture()
	p := e05PanelFixture(a, v)
	a.deliveryAssignment.Loaded = false
	a.deliveryAssignment.Error = "待归属列表尚未核验"
	tt := ui.NewTester(a.View, 800, 640)
	if tt.HasText("本次 0 条") || !tt.HasText("未核验") {
		t.Fatal("unread/failed list faked zero")
	}
	v.Code = "ready"
	v.SelectedProject = v.MatchingProject.ID
	p.SelectedProject = v.MatchingProject.ID
	v.CanConfirm = true
	if !canConfirmAssignment(p) {
		t.Fatal("real ready proof rejected")
	}
	p.Loading = true
	if canConfirmAssignment(p) {
		t.Fatal("old ready proof accepted while revalidating")
	}
}
func TestE05AssignmentReceiptIsIndependentAndHistoricallyBound(t *testing.T) {
	v := e05PreviewFixture()
	v.SelectedProject = v.MatchingProject.ID
	q := records.DeliveryAssignmentIntent{OperationID: uuid.NewString(), LibraryID: v.LibraryID, EntryID: v.EntryID, RequestDigest: v.RequestDigest, ReviewDigest: v.ReviewDigest, Project: v.SelectedProject, WorkspaceDigest: v.WorkspaceDigest}
	r := records.DeliveryAssignmentReceipt{OperationID: q.OperationID, LibraryID: q.LibraryID, EntryID: q.EntryID, RequestDigest: q.RequestDigest, ReviewDigest: q.ReviewDigest, Project: q.Project, WorkspaceDigest: q.WorkspaceDigest, Memory: uuid.NewString(), Revision: 1, OriginalOperationID: v.Item.OperationID, OriginalConnectionID: v.Item.ConnectionID, Source: "user_assignment", State: "saved", CreatedMS: 1}
	if !validAssignmentReceipt(r, q, *v) {
		t.Fatal("actual independent receipt rejected")
	}
	r.Source = "agent"
	if validAssignmentReceipt(r, q, *v) {
		t.Fatal("Agent save receipt promoted to user assignment")
	}
	r.Source = "user_assignment"
	// SQL completion can be recovered after the source directory changes; this
	// is a historical receipt, never a current workspace authorization proof.
	v.WorkspaceDigest = ""
	v.CanConfirm = false
	v.Assignment = &r
	v.Code = "assignment_saved"
	if !validAssignmentReceipt(r, q, *v) {
		t.Fatal("historical receipt required new source proof")
	}
	other := q
	other.OperationID = uuid.NewString()
	if validAssignmentReceipt(r, other, *v) {
		t.Fatal("receipt moved to another operation")
	}
}
func e05RegisterProject(t *testing.T, db *store.Store, name string) records.Scope {
	t.Helper()
	path := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", path).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	p, err := db.RegisterProject(context.Background(), records.ScopeDraft{Name: name, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestE05AssignmentRealSpoolSQLUnknownReopenAndOriginalAck(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	project := e05RegisterProject(t, db, "来源仓库")
	wrong := e05RegisterProject(t, db, "另一个仓库")
	root := ""
	requests := []records.DeliveryAssignmentIntent{}
	originalSaves := 0
	a, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		switch r.Op {
		case "agent_delivery_authorize":
			return serviceipc.Response{Code: "scope_required", Error: "synthetic no grant"}
		case "agent_tool":
			originalSaves++
			return serviceipc.Response{Error: "must not rewrite Agent save"}
		case "delivery_assignment_preview":
			q := *r.DeliveryAssignmentPreviewQuery
			source, e := agentbridge.ReadDeliveryAssignmentSource(root, q.EntryID, q.ReviewDigest)
			if e != nil {
				return serviceipc.Response{Error: e.Error()}
			}
			result, e := db.DeliveryAssignmentPreview(ctx, source, q.Project)
			if e != nil {
				return serviceipc.Response{Error: e.Error()}
			}
			return serviceipc.Response{OK: true, Result: result}
		case "delivery_assignment_confirm":
			q := *r.DeliveryAssignmentIntent
			durable, e := agentbridge.ReadDeliveryAssignmentIntent(root, q.EntryID)
			if e != nil || durable == nil || *durable != q {
				return serviceipc.Response{Error: "intent not durable before send"}
			}
			if _, e := agentbridge.ReadDeliveryAssignmentClaim(root, q); e != nil {
				return serviceipc.Response{Error: "claim not durable before send"}
			}
			requests = append(requests, q)
			result, found, e := db.LookupDeliveryAssignment(ctx, q)
			if e == nil && !found {
				source, readErr := agentbridge.ReadDeliveryAssignmentClaim(root, q)
				if readErr != nil {
					return serviceipc.Response{Error: readErr.Error()}
				}
				result, e = db.ConfirmDeliveryAssignment(ctx, source, q)
			}
			if e != nil {
				return serviceipc.Response{Error: e.Error()}
			}
			if len(requests) == 1 {
				return serviceipc.Response{Error: "synthetic committed receipt lost"}
			}
			return serviceipc.Response{OK: true, Result: result}
		}
		return serviceipc.Response{Error: "unexpected operation"}
	})
	root = a.library.Root
	config := agentbridge.Config{Root: root, ConnectionID: uuid.NewString(), Generation: 1, Workspace: project.Path, DurableDelivery: true}
	original := records.AgentArguments{OperationID: uuid.NewString(), Text: "用户待确认的原始提交全文。", Title: "真实零grant提交", Origin: "raw"}
	out := config.Call(ctx, "memory_save", original)
	if out.Delivery == nil {
		t.Fatal(out)
	}
	entry := out.Delivery.ID
	a.openDeliveryAssignment(entry)
	awaitProcessingDispatch(t, updates)
	awaitProcessingDispatch(t, updates)
	p := a.deliveryAssignment.Panels[entry]
	if p.Preview == nil || p.SelectedProject != "" || p.Preview.CanConfirm || p.Preview.Code != "choose_project" {
		t.Fatal("initial preview defaulted project", p.Error)
	}
	a.chooseDeliveryAssignment(p, assignmentProjectChoice(p.Preview.Projects, wrong.ID))
	awaitProcessingDispatch(t, updates)
	if p.Preview == nil || p.Preview.Code != "workspace_mismatch" || canConfirmAssignment(p) || p.Preview.MatchingProject.ID != project.ID {
		t.Fatal("wrong project accepted", p.Error)
	}
	a.chooseDeliveryAssignment(p, assignmentProjectChoice(p.Preview.Projects, project.ID))
	awaitProcessingDispatch(t, updates)
	if !canConfirmAssignment(p) {
		t.Fatal("actual registered repository did not verify", p.Error)
	}
	a.confirmDeliveryAssignment(p)
	awaitProcessingDispatch(t, updates)
	if p.Pending == nil || p.Receipt != nil || p.Error == "" || len(requests) != 1 {
		t.Fatal("unknown receipt lost original request", p.Error)
	}
	first := *p.Pending
	raw, readErr := os.ReadFile(filepath.Join(root, "agent-outbox", "ledger.json"))
	var ledger struct {
		Version int `json:"version"`
	}
	if readErr != nil || json.Unmarshal(raw, &ledger) != nil || ledger.Version != 2 {
		t.Fatal("assignment did not upgrade protected journal format", readErr, ledger.Version)
	}
	journal, err := agentbridge.ReadDeliveryAssignmentIntent(root, entry)
	if err != nil || journal == nil || *journal != first {
		t.Fatal("send preceded durable original intent", err)
	}
	a.retireDeliveryAssignment()
	a.openDeliveryAssignment(entry)
	awaitProcessingDispatch(t, updates)
	awaitProcessingDispatch(t, updates)
	p = a.deliveryAssignment.Panels[entry]
	if p.Pending == nil || *p.Pending != first || p.Receipt == nil || !p.AckPending || len(requests) != 1 {
		t.Fatal("reopen automatically sent or failed read-only SQL receipt recovery", p.Error)
	}
	a.confirmDeliveryAssignment(p)
	awaitProcessingDispatch(t, updates)
	awaitProcessingDispatch(t, updates)
	if p.Receipt == nil || p.AckPending || len(requests) != 2 || !reflect.DeepEqual(requests[0], requests[1]) || originalSaves != 0 {
		t.Fatal("retry changed original request or failed independent ack", p.Error)
	}
	review, err := agentbridge.ReviewDelivery(root, entry)
	if err != nil || review.Item.Assignment == nil || review.Item.Assignment.State != "saved" || review.Item.Status.PersistedConfirmed || review.Item.Attempts != 0 || !reflect.DeepEqual(review.Arguments, original) {
		t.Fatal("assignment forged Agent receipt/payload", review, err)
	}
	var count int
	if err := db.DB.QueryRow("SELECT count(*) FROM delivery_assignments").Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate assignment", err, count)
	}
	saved, err := db.Current(ctx, p.Receipt.Memory)
	if err != nil || saved.Origin != "raw" || saved.Text != original.Text || saved.Dataset != project.ID {
		t.Fatal("saved wrong type/content/project", saved, err)
	}
	// Open result preserves raw navigation, rather than silently switching type.
	a.libraryContext = nil
	a.openAssignedRecord(p)
	if a.recordKind != "raw" || a.scope != project.ID {
		t.Fatal("raw result changed knowledge kind")
	}
}
func TestE05AssignmentPreviewEpochRetiresLateRead(t *testing.T) {
	// A local retained source is real; only its delayed IPC response is a fixture.
	entered, release := make(chan struct{}), make(chan struct{})
	preview := e05PreviewFixture()
	a, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op == "agent_delivery_authorize" {
			return serviceipc.Response{Code: "scope_required", Error: "no grant"}
		}
		if r.Op == "delivery_assignment_preview" {
			close(entered)
			<-release
			return serviceipc.Response{OK: true, Result: *preview}
		}
		return serviceipc.Response{Error: "unexpected"}
	})
	path, _ := filepath.EvalSymlinks(t.TempDir())
	out := (agentbridge.Config{Root: a.library.Root, ConnectionID: uuid.NewString(), Generation: 1, Workspace: path, DurableDelivery: true}).Call(context.Background(), "memory_save", records.AgentArguments{OperationID: uuid.NewString(), Text: "isolated retained text", Origin: "knowledge"})
	if out.Delivery == nil {
		t.Fatal(out)
	}
	a.openDeliveryAssignment(out.Delivery.ID)
	awaitProcessingDispatch(t, updates)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no preview")
	}
	a.libraryEpoch++
	a.retireDeliveryAssignment()
	a.selectedID = "new-library-selection"
	a.draft = "new-library-draft"
	close(release)
	awaitProcessingDispatch(t, updates)
	if a.deliveryAssignment.Selected != "" || len(a.deliveryAssignment.Panels) != 0 || a.selectedID != "new-library-selection" || a.draft != "new-library-draft" {
		t.Fatal("late old-root response crossed library epoch")
	}
}

func TestE05AssignmentOldHelperStorageRefusalKeepsOriginalVisible(t *testing.T) {
	a := New("test", nil)
	v := e05PreviewFixture()
	p := e05PanelFixture(a, v)
	p.Review = &records.AgentDeliveryReview{Item: v.Item, Arguments: v.Arguments}
	p.Preview = nil
	p.Error = assignmentReadFailure(&serviceipc.ServiceError{Code: "delivery_storage_unavailable", Message: "synthetic old-helper underlying delivery_corrupt"})
	p.KnownRejected = true
	tt := ui.NewTester(a.View, 800, 640)
	if !tt.HasText(v.Arguments.Text) || !tt.HasText("本机原始提交 · 项目归属尚未核验") || tt.HasText("请重新加载已运行的 Agent 连接；原有本机暂存会保留。") || tt.HasText("原文丢失") || tt.HasText("delivery_corrupt") || canConfirmAssignment(p) {
		t.Fatal("legacy storage refusal was promoted to source loss or success")
	}
	writeScreenshot(t, tt, "e05-helper-refusal-original-800.png")
}

func TestE05AssignmentChoiceLabelsAndReloadTiming(t *testing.T) {
	a := New("test", nil)
	v := e05PreviewFixture()
	p := e05PanelFixture(a, v)
	choices := assignmentChoices(v.Projects)
	if !reflect.DeepEqual(choices, []string{"请选择项目", "来源项目", "另一个项目"}) {
		t.Fatal("project selector exposed internal ordinal or identifier", choices)
	}
	tt := ui.NewTester(a.View, 800, 640)
	if tt.HasText("请重新加载已运行的 Agent 连接；原有本机暂存会保留。") {
		t.Fatal("first preview prompted reload before a journal existed")
	}
	p.Pending = &records.DeliveryAssignmentIntent{OperationID: uuid.NewString()}
	tt.Frame()
	if !tt.HasText("请重新加载已运行的 Agent 连接；原有本机暂存会保留。") {
		t.Fatal("pending/unknown journal hid required connection reload")
	}
	p.Pending = nil
	v.Projects[1].Name = v.Projects[0].Name
	label := assignmentProjectChoice(v.Projects, v.Projects[1].ID)
	if label != v.Projects[1].Name+" · "+v.Projects[1].Path {
		t.Fatal("same-name projects not distinguished by actual path", label)
	}
	a.chooseDeliveryAssignment(p, label)
	if p.SelectedProject != v.Projects[1].ID {
		t.Fatal("selection parsed name or silently chose first same-name project")
	}
}
